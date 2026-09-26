package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

type fakeStanding struct {
	snapshot core.WorkspaceSnapshot
	known    bool
	head     core.RevisionKey
	headOK   bool
}

func (f fakeStanding) Snapshot(string) (core.WorkspaceSnapshot, bool) { return f.snapshot, f.known }
func (f fakeStanding) Head(context.Context, string) (core.RevisionKey, bool) {
	return f.head, f.headOK
}

// An agent's standing is judged at the revision its workspace is at now, not the one the last poll
// saw: a green run of the revision before its final edits is stale, not green.
func TestAnAgentsStandingIsJudgedAtItsRevisionNow(t *testing.T) {
	tested := core.RevisionKey{HeadSHA: "aaaaaaaaaa"}
	finished := time.Now()
	passed := core.TestRun{TestName: "unit", Revision: tested, State: core.TestPassing, FinishedAt: &finished}
	snapshot := core.WorkspaceSnapshot{Name: "worker", Revision: tested,
		Tests: []core.TestSnapshot{{Name: "unit", Required: true, Latest: &passed}}}

	green := standingOf(fakeStanding{snapshot: snapshot, known: true, head: tested, headOK: true})("worker")
	if !strings.HasPrefix(green, "green at aaaaaaa") {
		t.Fatalf("tested and unchanged reads %q", green)
	}
	moved := standingOf(fakeStanding{snapshot: snapshot, known: true,
		head: core.RevisionKey{HeadSHA: "bbbbbbbbbb"}, headOK: true})("worker")
	if strings.HasPrefix(moved, "green") || !strings.Contains(moved, "bbbbbbb") {
		t.Fatalf("edited since the green run reads %q", moved)
	}
	if got := standingOf(fakeStanding{snapshot: snapshot, known: true})("worker"); !strings.Contains(got, "could not be read") {
		t.Fatalf("an unreadable revision reads %q", got)
	}
	if got := standingOf(fakeStanding{})("nobody"); got != "no verification recorded for it" {
		t.Fatalf("an unknown agent reads %q", got)
	}
}
