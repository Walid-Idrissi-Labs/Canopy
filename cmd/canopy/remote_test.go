package main

import (
	"context"
	"errors"
	"net"
	"strings"
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
