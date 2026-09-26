package main

import (
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// Always on a dispatch question is kept, and the next dispatch in that conversation is not asked.
func TestAlwaysStartingAgentsIsHonoured(t *testing.T) {
	engine := session.New(nil)
	t.Cleanup(engine.Close)
	confirm := dispatchConfirm(engine, "s1")
	done := make(chan bool, 1)
	go func() { done <- confirm(session.Confirmation{}) }()
	for {
		if waiting := engine.PendingAll(); len(waiting) == 1 {
			if got := waiting[0].Decision.Scope; got != (permission.Scope{Tool: "spawn_agents"}) || got.String() == "" {
				t.Fatalf("always would cover %+v (%q), not starting agents alone", got, got.String())
			}
			break
		}
		time.Sleep(time.Millisecond)
	}
	engine.Answer("s1", true, true)
	if !<-done {
		t.Fatal("the approval was lost")
	}
	// And nothing else: the same conversation's commands are still asked about.
	command := permission.Request{SessionID: "s1", AgentID: "s1", Tool: "run_command", Kind: core.ToolExecute, Command: "rm -rf build"}
	if engine.Granted("s1", command, permission.Scope{Tool: "run_command"}) {
		t.Fatal("always on starting agents approved a shell command too")
	}
	answered := make(chan bool, 1)
	go func() { answered <- confirm(session.Confirmation{}) }()
	select {
	case ok := <-answered:
		if !ok {
			t.Fatal("a dispatch after always was refused")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a dispatch after always was asked again")
	}
}
