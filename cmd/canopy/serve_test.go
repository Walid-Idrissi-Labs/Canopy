package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
func (e *servedEngine) Cancel(string)                   {}
func (e *servedEngine) SetMode(string, string) error    { return nil }
func (e *servedEngine) Events(uint64) <-chan core.Event { return e.events }
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
