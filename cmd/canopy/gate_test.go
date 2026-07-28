package main

import (
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The green gate reads the roll-up once the tests have stopped moving, so what counts as stopped is
// the whole of whether it works.
//
// It asked the wrong question. A verdict answers "what does this evidence say about the code", and a
// run that has been queued has not recorded a revision yet, so the honest verdict on it is unknown.
// Unknown is not running, so every test read as finished the instant it was started: the gate never
// waited, judged a suite that had not run, and reverted every turn runway was given.

// here is a workspace whose revision is known, which is the ordinary case with the poller going.
func here(tests ...core.TestSnapshot) core.WorkspaceSnapshot {
	return core.WorkspaceSnapshot{
		ID: "w1", Name: "main", Revision: core.RevisionKey{HeadSHA: "aaa111"}, Tests: tests,
	}
}

func ranAs(name string, state core.TestState, revision string) core.TestSnapshot {
	latest := &core.TestRun{
		ID: "run-1", WorkspaceID: "w1", TestName: name, State: state, StartedAt: time.Now(),
	}
	if revision != "" {
		latest.Revision = core.RevisionKey{HeadSHA: revision}
	}
	return core.TestSnapshot{Name: name, Required: true, Latest: latest}
}

// The bug, exactly. A queued run carries no revision, because the runner records one only when the
// run finishes, so this is the state every test is in for the first instant after it is started.
func TestAQueuedRunWithNoRevisionYetIsNotSettled(t *testing.T) {
	if settled(here(ranAs("unit", core.TestQueued, ""))) {
		t.Error("a queued test read as finished, so the gate would judge a suite that had not run")
	}
	if settled(here(ranAs("unit", core.TestRunning, ""))) {
		t.Error("a running test read as finished")
	}
}

// One still going holds the whole check, or the verdict is whatever happened to fail first.
func TestOneRunningTestHoldsTheWholeCheck(t *testing.T) {
	snapshot := here(
		ranAs("build", core.TestPassing, "aaa111"),
		ranAs("unit", core.TestRunning, "aaa111"),
		ranAs("vet", core.TestPassing, "aaa111"),
	)
	if settled(snapshot) {
		t.Error("the check settled while one test was still running")
	}
}

// And the gate has to be able to finish, or runway would hang on every turn instead.
func TestFinishedTestsSettle(t *testing.T) {
	snapshot := here(
		ranAs("build", core.TestPassing, "aaa111"),
		ranAs("unit", core.TestFailing, "aaa111"),
	)
	if !settled(snapshot) {
		t.Error("a suite that has finished did not settle")
	}
}

// A test configured and never run is not something to wait for. Nothing is in flight for it, and the
// roll-up reports it as unknown, which is the true thing to say about a test with no result. Waiting
// would hang the gate for the full timeout on any project with a test that has not been run yet.
func TestATestThatHasNeverRunDoesNotHoldTheCheck(t *testing.T) {
	snapshot := here(core.TestSnapshot{Name: "lint", Required: true})
	if !settled(snapshot) {
		t.Error("a test that has never run held the check open")
	}
}
