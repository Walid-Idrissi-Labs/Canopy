package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/acpserver"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// attachedEngine connects a remote engine to a hub serving engine, over a pipe.
func attachedEngine(t *testing.T, engine *servedEngine) *remoteEngine {
	t.Helper()
	engine.hub = acpserver.NewHub(engine)
	client, server := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = engine.hub.Serve(ctx, server, server)
	}()
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		<-done
	})
	rpc := newRPCClient(client)
	if err := rpc.call("initialize", map[string]any{"protocolVersion": 1}, nil); err != nil {
		t.Fatal(err)
	}
	return newRemoteEngine(rpc, t.TempDir())
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Attached, the interface's engine sends a prompt to the server, sees the question the server's
// engine asked exactly as it was asked, answers it, and draws the conversation as the server holds
// it, updated as it changes.
func TestAnAttachedInterfaceRunsAConversationOnTheServer(t *testing.T) {
	served := &servedEngine{events: make(chan core.Event, 8)}
	e := attachedEngine(t, served)
	events := e.Events(0)
	id, err := e.open("7")
	if err != nil || id != "session-7" {
		t.Fatalf("opened %q: %v", id, err)
	}
	turn, err := e.Send(id, "run the tests")
	if err != nil || turn != "t1" {
		t.Fatalf("sent %q: %v", turn, err)
	}
	eventually(t, "the question arrives", func() bool { return len(e.PendingAll()) == 1 })
	asked, ok := e.Pending(id)
	if !ok || asked.Request.Tool != "shell" || asked.Request.Command != "go test ./..." ||
		asked.Decision.Reason != "runs a command" {
		t.Fatalf("the question arrived as %+v", asked)
	}
	if !e.Answer(id, true, false) {
		t.Fatal("the answer was not sent")
	}
	eventually(t, "the turn ends as the server's engine ended it", func() bool {
		s, _ := e.Session(id)
		return len(s.Turns) == 1 && s.Turns[0].Text == "ran the tests" && s.Turns[0].State == core.TurnComplete
	})
	select {
	case <-events:
	default:
		t.Fatal("the screens were never told anything changed")
	}
	if _, ok := e.Pending(id); ok {
		t.Fatal("an answered question is still pending")
	}
	if _, err := e.Compact(context.Background(), id); !errors.Is(err, errAttached) {
		t.Fatalf("a feature the server does not offer answered %v", err)
	}
}

// A prompt the server refuses is refused here, with the server's reason.
func TestAnAttachedPromptTheServerRefusesSaysWhy(t *testing.T) {
	served := &servedEngine{events: make(chan core.Event, 8), refuse: errors.New("over its spending cap")}
	e := attachedEngine(t, served)
	if _, err := e.open("7"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Send("session-7", "hello"); err == nil || !strings.Contains(err.Error(), "over its spending cap") {
		t.Fatalf("a refused prompt answered %v", err)
	}
}

// The interface itself draws an attached conversation: its question, and after the answer, the reply.
func TestTheInterfaceDrawsAnAttachedConversation(t *testing.T) {
	served := &servedEngine{events: make(chan core.Event, 8)}
	e := attachedEngine(t, served)
	id, err := e.open("7")
	if err != nil {
		t.Fatal(err)
	}
	store := fake.New()
	defer store.Close()
	keyStore, _ := routelessCredentials(t)
	var app tea.Model = tui.NewAppConfigured(store, signInAware{keyStore}, e, "project", "myclaude", tui.AppOptions{Session: id})
	app, _ = app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	events := e.Events(0)
	if _, err := e.Send(id, "run the tests"); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the question arrives", func() bool { return len(e.PendingAll()) == 1 })
	app, _ = app.Update(chat.EventMsg{Event: <-events})
	if view := app.(tui.App).View().Content; !strings.Contains(view, "go test ./...") {
		t.Fatalf("the question is not drawn:\n%s", view)
	}
}

// scriptedServer answers the attached engine's requests with handle, and lets a test send it
// messages unasked; closing it hangs up.
type scriptedServer struct {
	conn net.Conn
	mu   sync.Mutex
	asks []map[string]json.RawMessage
}

func scripted(t *testing.T, handle func(method string, params json.RawMessage) any) (*remoteEngine, *scriptedServer) {
	t.Helper()
	client, server := net.Pipe()
	s := &scriptedServer{conn: server}
	go func() {
		scanner := bufio.NewScanner(server)
		scanner.Buffer(make([]byte, 1<<20), 16<<20)
		for scanner.Scan() {
			var m map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &m) != nil || m["method"] == nil || m["id"] == nil {
				continue
			}
			s.mu.Lock()
			s.asks = append(s.asks, m)
			s.mu.Unlock()
			var method string
			_ = json.Unmarshal(m["method"], &method)
			s.send(map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": handle(method, m["params"])})
		}
	}()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return newRemoteEngine(newRPCClient(client), t.TempDir()), s
}

func (s *scriptedServer) send(v any) {
	line, _ := json.Marshal(v)
	_, _ = s.conn.Write(append(line, '\n'))
}

// froms are the turns each _canopy/session asked from.
func (s *scriptedServer) froms() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []int
	for _, m := range s.asks {
		var p struct {
			From int `json:"from"`
		}
		_ = json.Unmarshal(m["params"], &p)
		out = append(out, p.From)
	}
	return out
}

// Once turns have finished, only the ones after them are asked for and the finished ones are
// kept; a reply that says the record shrank under them is fetched again whole.
func TestAnAttachedConversationIsFetchedFromItsFirstUnfinishedTurn(t *testing.T) {
	done := core.Turn{ID: "t1", State: core.TurnComplete, Text: "first"}
	var calls int
	e, server := scripted(t, func(_ string, params json.RawMessage) any {
		var p struct {
			From int `json:"from"`
		}
		_ = json.Unmarshal(params, &p)
		calls++
		switch calls {
		case 1:
			return map[string]any{"session": core.Session{ID: "s1", Turns: []core.Turn{done,
				{ID: "t2", State: core.TurnStreaming}}}, "from": 0, "turnCount": 2}
		case 2:
			return map[string]any{"session": core.Session{ID: "s1", Turns: []core.Turn{
				{ID: "t2", State: core.TurnComplete, Text: "second"}}}, "from": p.From, "turnCount": 2}
		case 3:
			// An undo took both turns back.
			return map[string]any{"session": core.Session{ID: "s1"}, "from": p.From, "turnCount": 0}
		default:
			return map[string]any{"session": core.Session{ID: "s1"}, "from": 0, "turnCount": 0}
		}
	})
	e.refresh("s1")
	e.refresh("s1")
	s, _ := e.Session("s1")
	if len(s.Turns) != 2 || s.Turns[0].Text != "first" || s.Turns[1].Text != "second" {
		t.Fatalf("merged into %+v", s.Turns)
	}
	e.refresh("s1")
	s, _ = e.Session("s1")
	if len(s.Turns) != 0 {
		t.Fatalf("after the undo the conversation holds %d turns", len(s.Turns))
	}
	if got := server.froms(); len(got) != 4 || got[0] != 0 || got[1] != 1 || got[2] != 2 || got[3] != 0 {
		t.Fatalf("asked from %v", got)
	}
}

// A question the server takes back is gone here too, and a connection that ends is said to, by
// the events ending.
func TestAWithdrawnQuestionAndAClosedConnection(t *testing.T) {
	e, server := scripted(t, func(string, json.RawMessage) any { return map[string]any{} })
	events := e.Events(0)
	server.send(map[string]any{"jsonrpc": "2.0", "id": 5, "method": "session/request_permission",
		"params": map[string]any{"sessionId": "s1", "_meta": map[string]any{"canopy": map[string]any{
			"request": map[string]any{"Tool": "run_command", "Command": "make"}}}}})
	eventually(t, "the question arrives", func() bool { return len(e.PendingAll()) == 1 })
	if q := e.PendingAll()[0]; q.SessionID != "s1" || q.Request.Command != "make" {
		t.Fatalf("the question arrived as %+v", q)
	}
	server.send(map[string]any{"jsonrpc": "2.0", "method": "$/cancel_request", "params": map[string]any{"requestId": 5}})
	eventually(t, "the question is withdrawn", func() bool { return len(e.PendingAll()) == 0 })
	_ = server.conn.Close()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-events:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("the events never ended after the connection closed")
		}
	}
}
