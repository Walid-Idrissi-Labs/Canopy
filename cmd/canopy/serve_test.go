//go:build unix

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/acpserver"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// servedEngine runs no model: a prompt asks one question and its turn says what the answer was.
type servedEngine struct {
	hub    *acpserver.Hub
	mu     sync.Mutex
	turns  []core.Turn
	events chan core.Event
}

func (e *servedEngine) NewSession(context.Context, string) (string, error) { return "session-7", nil }
func (e *servedEngine) LoadSession(_ context.Context, id, _ string) error {
	if id != "session-7" {
		return errors.New("no such conversation")
	}
	return nil
}
func (e *servedEngine) ListSessions() []acpserver.Summary {
	return []acpserver.Summary{{ID: "session-7", Title: "fix \x1b[31mthe parser", Mode: "build", Running: true}}
}
func (e *servedEngine) Session(string) (core.Session, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return core.Session{ID: "session-7", Turns: append([]core.Turn(nil), e.turns...)}, true
}
func (e *servedEngine) Cancel(string)                      {}
func (e *servedEngine) SetMode(string, string) error       { return nil }
func (e *servedEngine) Events(uint64) <-chan core.Event    { return e.events }
func (e *servedEngine) Modes(string) (string, []core.Mode) { return core.ModeBuild, core.Modes() }
func (e *servedEngine) Send(id, text string) (string, error) {
	e.mu.Lock()
	e.turns = append(e.turns, core.Turn{ID: "t1", State: core.TurnStreaming, Request: core.Message{Text: text}})
	e.mu.Unlock()
	go func() {
		allowed := e.hub.Approve(context.Background(), permission.Request{SessionID: id, Tool: "shell",
			Command: "go test ./..."}, permission.Decision{Reason: "runs a command"})
		e.mu.Lock()
		e.turns[0].Text = "denied"
		if allowed {
			e.turns[0].Text = "ran the tests"
		}
		e.turns[0].State = core.TurnComplete
		e.mu.Unlock()
		e.events <- core.Event{}
	}()
	return "t1", nil
}

// syncBuffer is an output a test can read while it is being written.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) waitFor(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(b.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("never saw %q in:\n%s", want, b.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The attach client end to end over a real unix socket: a conversation is started, a prompt asks a
// question, y answers it, and the turn's reply is shown.
func TestAttachStartsPromptsAndAnswers(t *testing.T) {
	engine := &servedEngine{events: make(chan core.Event, 8)}
	engine.hub = acpserver.NewHub(engine)
	dir := t.TempDir()
	path := filepath.Join(dir, "s.sock")
	listener, err := listenServe(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the socket is %v, %v; want 0600", info.Mode(), err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { _ = engine.hub.Serve(context.Background(), conn, conn) }()
		}
	}()
	if _, err := listenServe(path); err == nil {
		t.Fatal("a second server started beside the first")
	}

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stdinR, stdinW := io.Pipe()
	var out, errOut syncBuffer
	done := make(chan int, 1)
	go func() { done <- attachTo(conn, dir, "new", stdinR, &out, &errOut, make(chan os.Signal)) }()
	out.waitFor(t, "conversation 7.")
	_, _ = io.WriteString(stdinW, "run the tests\n")
	out.waitFor(t, "Canopy asks to run shell: go test ./... (runs a command). Allow? [y/N]")
	_, _ = io.WriteString(stdinW, "y\n")
	out.waitFor(t, "ran the tests")
	_ = stdinW.Close()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("exit %d: %s", code, errOut.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attach did not leave at the end of its input")
	}
	if !strings.Contains(out.String(), "left conversation 7 running; canopy attach 7 picks it up") {
		t.Fatalf("leaving did not say the agent keeps going:\n%s", out.String())
	}

	// The list, with what the terminal is sent kept free of escape sequences.
	conn, err = net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var listed syncBuffer
	if code := attachTo(conn, dir, "", strings.NewReader(""), &listed, &errOut, make(chan os.Signal)); code != exitOK {
		t.Fatalf("list exit %d", code)
	}
	if got := listed.String(); !strings.Contains(got, "7      build     working") || strings.Contains(got, "\x1b") {
		t.Fatalf("list %q", got)
	}
}

// Anything but y refuses.
func TestAttachRefusesUnlessToldYes(t *testing.T) {
	engine := &servedEngine{events: make(chan core.Event, 8)}
	engine.hub = acpserver.NewHub(engine)
	client, server := net.Pipe()
	go func() { _ = engine.hub.Serve(context.Background(), server, server) }()
	stdinR, stdinW := io.Pipe()
	var out, errOut syncBuffer
	go func() { attachTo(client, t.TempDir(), "7", stdinR, &out, &errOut, make(chan os.Signal)) }()
	out.waitFor(t, "conversation 7.")
	_, _ = io.WriteString(stdinW, "go\n")
	out.waitFor(t, "Allow? [y/N]")
	_, _ = io.WriteString(stdinW, "sure\n")
	out.waitFor(t, "denied")
	_ = stdinW.Close()
}

func TestTheServeSocketDirectoryIsPrivate(t *testing.T) {
	// A test's own temporary directory is too deep for a socket, which is its own check below.
	base, err := os.MkdirTemp("/tmp", "cs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	t.Setenv("XDG_RUNTIME_DIR", base)
	path, err := serveSocket("/some/project")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("socket directory %v, %v", info.Mode(), err)
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()+strings.Repeat("/deeper", 10))
	if _, err := serveSocket("/some/project"); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("a path too long for a socket was not refused: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", base)
	// Loosened by someone else, it is refused rather than used.
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := serveSocket("/some/project"); err == nil {
		t.Fatal("a directory others can open was used")
	}
}

// Picking up a conversation that has a question waiting shows the question, after the history,
// and the answer typed reaches the tool that asked.
func TestAttachAnswersAQuestionThatWasWaiting(t *testing.T) {
	engine := &servedEngine{events: make(chan core.Event, 8)}
	engine.hub = acpserver.NewHub(engine)
	for i := range 200 {
		engine.turns = append(engine.turns, core.Turn{ID: fmt.Sprintf("old-%d", i), State: core.TurnComplete,
			Request: core.Message{Text: "ask"}, Text: "answer"})
	}
	engine.turns = append(engine.turns, core.Turn{ID: "t1", State: core.TurnStreaming,
		Text: "working \x1b]52;c;cHduZWQ=\x07on it"})
	answer := make(chan bool, 1)
	go func() {
		answer <- engine.hub.Approve(context.Background(), permission.Request{SessionID: "session-7", Tool: "shell",
			Command: "make"}, permission.Decision{Reason: "runs a command"})
	}()
	client, server := net.Pipe()
	go func() { _ = engine.hub.Serve(context.Background(), server, server) }()
	stdinR, stdinW := io.Pipe()
	var out, errOut syncBuffer
	go func() { attachTo(client, t.TempDir(), "7", stdinR, &out, &errOut, make(chan os.Signal)) }()
	out.waitFor(t, "Allow? [y/N]")
	_, _ = io.WriteString(stdinW, "y\n")
	select {
	case ok := <-answer:
		if !ok {
			t.Fatal("y did not allow")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("the waiting question was never answered:\n%s", out.String())
	}
	// What an agent streams is shown with its terminal controls taken out.
	out.waitFor(t, "working ]52;c;cHduZWQ=on it")
	if strings.Contains(out.String(), "\x1b") || strings.Contains(out.String(), "\x07") {
		t.Fatal("a terminal control from the agent's text reached the terminal")
	}
	_ = stdinW.Close()
}

// attach finds the server from a directory inside the project, and through a link to it.
func TestAttachFindsTheServerFromInsideTheProject(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "cs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	t.Setenv("XDG_RUNTIME_DIR", base)
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "internal", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := serveSocket(project)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := listenServe(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	engine := &servedEngine{events: make(chan core.Event, 8)}
	engine.hub = acpserver.NewHub(engine)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { _ = engine.hub.Serve(context.Background(), conn, conn) }()
		}
	}()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(project, link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(project, "internal", "deep"), link} {
		t.Chdir(dir)
		var out, errOut syncBuffer
		if code := runAttach(nil, strings.NewReader(""), &out, &errOut); code != exitOK {
			t.Fatalf("from %s: exit %d: %s", dir, code, errOut.String())
		}
		if !strings.Contains(out.String(), "working") {
			t.Fatalf("from %s the list was %q", dir, out.String())
		}
	}
}

// Only a real directory, the user's own, that nobody else can open will do.
func TestOnlyAPrivateDirectoryHoldsTheSocket(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(dir, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(private)
	if err != nil {
		t.Fatal(err)
	}
	if !privateDir(info, os.Getuid()) {
		t.Fatal("the user's own 0700 directory was refused")
	}
	if privateDir(info, os.Getuid()+1) {
		t.Fatal("a directory owned by someone else was accepted")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || privateDir(info, os.Getuid()) {
		t.Fatal("a link to a private directory was accepted in place of one")
	}
}

// Two servers started at the same moment both find no socket yet; the lock is what settles it.
func TestTheLockKeepsASecondServerOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if listener, err := listenServe(path); err == nil {
		_ = listener.Close()
		t.Fatal("a server started while another held the lock")
	}
	_ = lock.Close()
	listener, err := listenServe(path)
	if err != nil {
		t.Fatalf("the lock was not given up: %v", err)
	}
	_ = listener.Close()
}

// A question shown to one attach and then taken by another client is withdrawn from the first,
// which says so rather than taking the next line typed as its answer.
func TestAttachLetsGoOfAQuestionTakenElsewhere(t *testing.T) {
	engine := &servedEngine{events: make(chan core.Event, 8)}
	engine.hub = acpserver.NewHub(engine)
	client, server := net.Pipe()
	go func() { _ = engine.hub.Serve(context.Background(), server, server) }()
	stdinR, stdinW := io.Pipe()
	defer func() { _ = stdinW.Close() }()
	var out, errOut syncBuffer
	go func() { attachTo(client, t.TempDir(), "new", stdinR, &out, &errOut, make(chan os.Signal)) }()
	out.waitFor(t, "conversation 7.")
	_, _ = io.WriteString(stdinW, "go\n")
	out.waitFor(t, "Allow? [y/N]")

	other, otherServer := net.Pipe()
	go func() { _ = engine.hub.Serve(context.Background(), otherServer, otherServer) }()
	go func() { _, _ = io.Copy(io.Discard, other) }()
	_, _ = other.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"session/load","params":{"sessionId":"session-7"}}` + "\n"))
	out.waitFor(t, "that question is now with another client")
}

// attach looks for a server up to the repository's top and no further: a repository nested in
// another is not served by the outer one's server.
func TestAttachStopsAtTheRepositoryTop(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "cs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	t.Setenv("XDG_RUNTIME_DIR", base)
	outer := t.TempDir()
	inner := filepath.Join(outer, "vendor", "inner")
	for _, dir := range []string{filepath.Join(outer, ".git"), filepath.Join(inner, ".git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path, err := serveSocket(outer)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := listenServe(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	t.Chdir(inner)
	var out, errOut syncBuffer
	if code := runAttach(nil, strings.NewReader(""), &out, &errOut); code == exitOK {
		t.Fatal("attach inside a nested repository reached the outer repository's server")
	}
	if !strings.Contains(errOut.String(), "not running") {
		t.Fatalf("errOut %q", errOut.String())
	}
}

// The directory holding the sockets is checked as itself: a link to a private directory is not one.
func TestALinkedSocketDirectoryIsRefused(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "cs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	private := filepath.Join(base, "elsewhere")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, filepath.Join(base, fmt.Sprintf("canopy-%d", os.Getuid()))); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", base)
	if _, err := serveSocket("/some/project"); err == nil {
		t.Fatal("a link in place of the socket directory was used")
	}
}
