package session

import (
	"context"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// Always, on a question asked outside the tool loop, is recorded as its scope and found again, and
// covers nothing else.
func TestAnAlwaysOutsideTheLoopIsHonouredAndNarrow(t *testing.T) {
	e := New(fixedResolver{})
	defer e.Close()
	spawn := permission.Scope{Tool: "spawn_agents"}
	req := permission.Request{SessionID: "s1", Tool: "spawn_agents", Kind: core.ToolExecute, Command: "start 3"}
	if e.Granted("s1", req, spawn) {
		t.Fatal("granted before anybody answered")
	}
	done := make(chan bool, 1)
	go func() {
		done <- e.Approve(context.Background(), req, permission.Decision{Outcome: permission.Ask, Scope: spawn})
	}()
	for !e.Answer("s1", true, true) {
		time.Sleep(time.Millisecond)
	}
	if !<-done {
		t.Fatal("the approval was lost")
	}
	later := req
	later.Command = "start 5, a different question"
	if !e.Granted("s1", later, spawn) {
		t.Fatal("always did not cover the next dispatch")
	}
	shell := permission.Request{SessionID: "s1", Tool: "run_command", Kind: core.ToolExecute, Command: "rm -rf x"}
	if e.Granted("s1", shell, permission.Scope{Tool: "run_command", Command: "rm -rf x"}) {
		t.Fatal("an always for dispatch covered a shell command")
	}
	if e.Granted("s2", req, spawn) {
		t.Fatal("an always in one conversation covered another")
	}
}
