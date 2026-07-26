package fake

import (
	"context"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func rollUpOf(t *testing.T, s *Store, workspaceID string) core.Rollup {
	t.Helper()
	w, ok := s.Snapshot().Workspace(workspaceID)
	if !ok {
		t.Fatalf("workspace %q not found", workspaceID)
	}
	return core.RollUp(w)
}

// waitForEvent reads until it sees an event matching the predicate, or gives up.
func waitForEvent(t *testing.T, events <-chan core.Event, want func(core.Event) bool) core.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event channel closed before the expected event arrived")
			}
			if want(ev) {
				return ev
			}
		case <-deadline:
			t.Fatal("timed out waiting for the expected event")
		}
	}
}

// The four scripted worktrees are the target demo, so they have to actually be those four states
// and not merely be named after them.
func TestTheFourScriptedWorkspaces(t *testing.T) {
	s := New()
	defer s.Close()

	tests := []struct {
		id        string
		wantGreen bool
		wantTests core.TestState
	}{
		{"ws-feat-login", true, core.TestPassing},
		{"ws-fix-cache", false, core.TestFailing},
		{"ws-refactor-api", true, core.TestPassing},
		{"ws-spike-search", false, core.TestNotConfigured},
	}

	snap := s.Snapshot()
	if len(snap.Workspaces) != 4 {
		t.Fatalf("got %d workspaces, want 4", len(snap.Workspaces))
	}

	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			got := rollUpOf(t, s, tc.id)
			if got.Green != tc.wantGreen {
				t.Errorf("Green = %v, want %v (%s)", got.Green, tc.wantGreen, got.Reason)
			}
			if got.Tests != tc.wantTests {
				t.Errorf("tests = %q, want %q", got.Tests, tc.wantTests)
			}
		})
	}
}

// The P1-05 acceptance criterion and the first demo: a revision change turns a passing result
// stale, without anything being re-run and without a restart.
func TestRevisionChangeTurnsPassingIntoStale(t *testing.T) {
	s := New()
	defer s.Close()

	const id = "ws-refactor-api"

	before := rollUpOf(t, s, id)
	if before.Tests != core.TestPassing || !before.Green {
		t.Fatalf("precondition failed, want a green passing workspace, got %q green=%v",
			before.Tests, before.Green)
	}

	snap := s.Snapshot()
	events := s.Events(snap.Sequence)

	if err := s.Touch(id); err != nil {
		t.Fatalf("Touch: %v", err)
	}

	ev := waitForEvent(t, events, func(e core.Event) bool {
		return e.Kind == core.EventRevisionChanged && e.WorkspaceID == id
	})
	if ev.Sequence <= snap.Sequence {
		t.Errorf("event sequence %d is not after the snapshot sequence %d", ev.Sequence, snap.Sequence)
	}

	after := rollUpOf(t, s, id)
	if after.Tests != core.TestStale {
		t.Errorf("tests = %q, want stale", after.Tests)
	}
	if after.Green {
		t.Error("a stale result must not be green")
	}
	if after.Reason == before.Reason {
		t.Error("the reason should have changed along with the state")
	}
}

// Staleness clears by re-running, and only by re-running.
func TestRerunningClearsStale(t *testing.T) {
	s := New()
	defer s.Close()

	const id = "ws-refactor-api"
	if err := s.Touch(id); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if got := rollUpOf(t, s, id); got.Tests != core.TestStale {
		t.Fatalf("precondition failed, want stale, got %q", got.Tests)
	}

	if _, err := s.Start(context.Background(), id, "unit"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := rollUpOf(t, s, id)
	if got.Tests != core.TestPassing {
		t.Errorf("tests = %q, want passing after a rerun", got.Tests)
	}
	if !got.Green {
		t.Errorf("workspace should be green again: %s", got.Reason)
	}
}

// Editing while a run is in flight is the case most likely to produce a false green: the run
// started against the old code, so its result cannot describe the new code, however it turns out.
func TestEditingDuringARunLeavesTheResultStale(t *testing.T) {
	s := New()
	defer s.Close()

	const id = "ws-refactor-api"
	runID, err := s.BeginRun(context.Background(), id, "unit")
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}

	if got := rollUpOf(t, s, id); got.Tests != core.TestRunning {
		t.Fatalf("want running while in flight, got %q", got.Tests)
	}

	if err := s.Touch(id); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	if err := s.CompleteRun(runID, core.TestPassing); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}

	got := rollUpOf(t, s, id)
	if got.Tests != core.TestStale {
		t.Errorf("tests = %q, want stale, the run tested code that no longer exists", got.Tests)
	}
	if got.Green {
		t.Error("a run that passed against superseded code must not be green")
	}
}

func TestUnknownRevisionIsNeverGreen(t *testing.T) {
	s := New()
	defer s.Close()

	const id = "ws-feat-login"
	const reason = "untracked file testdata/dump.sql is above the 25MB fingerprint limit"

	if err := s.SetRevisionUnknown(id, reason); err != nil {
		t.Fatalf("SetRevisionUnknown: %v", err)
	}

	got := rollUpOf(t, s, id)
	if got.Green {
		t.Error("an unknown revision must never be green")
	}
	if got.Reason != reason {
		t.Errorf("reason = %q, want the revision error to be surfaced", got.Reason)
	}

	if _, err := s.Current(context.Background(), id); err == nil {
		t.Error("Current should return an error when the revision is unknown")
	}
}

func TestCurrentReturnsTheZeroKeyOnFailure(t *testing.T) {
	s := New()
	defer s.Close()

	if err := s.SetRevisionUnknown("ws-feat-login", "boom"); err != nil {
		t.Fatalf("SetRevisionUnknown: %v", err)
	}

	// The contract says the zero key on failure, never a partly filled one that would report
	// itself as known.
	key, err := s.Current(context.Background(), "ws-feat-login")
	if err == nil {
		t.Fatal("want an error")
	}
	if key.Known() {
		t.Errorf("Current returned a key that reports itself as known: %v", key)
	}
}

func TestRunLifecycle(t *testing.T) {
	s := New()
	defer s.Close()

	const id = "ws-feat-login"
	runID, err := s.BeginRun(context.Background(), id, "unit")
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	if got := rollUpOf(t, s, id); got.Tests != core.TestRunning {
		t.Errorf("tests = %q, want running", got.Tests)
	}
	if got := rollUpOf(t, s, id); got.Green {
		t.Error("a workspace with a run in flight has no current result, so it is not green")
	}

	if err := s.CompleteRun(runID, core.TestFailing); err != nil {
		t.Fatalf("CompleteRun: %v", err)
	}
	if got := rollUpOf(t, s, id); got.Tests != core.TestFailing {
		t.Errorf("tests = %q, want failing", got.Tests)
	}
}

func TestCompleteRunRejectsNonTerminalStates(t *testing.T) {
	s := New()
	defer s.Close()

	runID, err := s.BeginRun(context.Background(), "ws-feat-login", "unit")
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	if err := s.CompleteRun(runID, core.TestRunning); err == nil {
		t.Error("running is not a terminal state and should be rejected")
	}
	if err := s.CompleteRun(runID, core.TestStale); err == nil {
		t.Error("stale is derived, never a recorded outcome, and should be rejected")
	}
}

func TestCancelledRunIsNeverGreen(t *testing.T) {
	s := New()
	defer s.Close()

	const id = "ws-feat-login"
	runID, err := s.BeginRun(context.Background(), id, "unit")
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	if err := s.Cancel(context.Background(), runID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got := rollUpOf(t, s, id)
	if got.Tests != core.TestCancelled {
		t.Errorf("tests = %q, want cancelled", got.Tests)
	}
	if got.Green {
		t.Error("a cancelled run must never be green")
	}
	if err := s.Cancel(context.Background(), runID); err == nil {
		t.Error("cancelling an already finished run should fail")
	}
}

// Nothing runs without trust. The check belongs at the point of execution, since configuration can
// change between being approved and being run.
func TestNothingRunsWithoutTrust(t *testing.T) {
	s := New()
	defer s.Close()

	for _, state := range core.AllTrustStates() {
		s.SetTrust(state)
		_, err := s.Start(context.Background(), "ws-feat-login", "unit")
		if state == core.TrustGranted {
			if err != nil {
				t.Errorf("trust %q should allow execution, got %v", state, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("trust %q allowed a command to run", state)
		}
	}
}

func TestUnknownWorkspaceAndTestAreRejected(t *testing.T) {
	s := New()
	defer s.Close()

	if _, err := s.Start(context.Background(), "nope", "unit"); err == nil {
		t.Error("an unknown workspace should be rejected")
	}
	if _, err := s.Start(context.Background(), "ws-feat-login", "nope"); err == nil {
		t.Error("an unknown test should be rejected")
	}
	if _, err := s.Current(context.Background(), "nope"); err == nil {
		t.Error("Current on an unknown workspace should be rejected")
	}
}

// A snapshot that shares a backing array with the store is not immutable, whatever the doc
// comment says. This is the test that keeps it honest.
func TestSnapshotIsIsolatedFromTheStore(t *testing.T) {
	s := New()
	defer s.Close()

	snap := s.Snapshot()
	original := snap.Workspaces[0].Tests[0].Name

	snap.Workspaces[0].Name = "vandalised"
	snap.Workspaces[0].Tests[0].Name = "vandalised"

	fresh := s.Snapshot()
	if fresh.Workspaces[0].Name == "vandalised" {
		t.Error("writing to a snapshot changed the store")
	}
	if fresh.Workspaces[0].Tests[0].Name != original {
		t.Error("writing to a snapshot's test slice changed the store")
	}
}

func TestSnapshotSequenceTracksEvents(t *testing.T) {
	s := New()
	defer s.Close()

	before := s.Snapshot().Sequence
	if err := s.Touch("ws-feat-login"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	after := s.Snapshot().Sequence

	if after <= before {
		t.Errorf("sequence did not advance: %d then %d", before, after)
	}
}

func TestSequenceNumbersAreMonotonic(t *testing.T) {
	s := New()
	defer s.Close()

	events := s.Events(s.Snapshot().Sequence)
	for i := 0; i < 20; i++ {
		if err := s.Touch("ws-feat-login"); err != nil {
			t.Fatalf("Touch: %v", err)
		}
	}

	var last uint64
	deadline := time.After(2 * time.Second)
	seen := 0
	for seen < 1 {
		select {
		case ev := <-events:
			if ev.Sequence <= last {
				t.Fatalf("sequence went backwards: %d after %d", ev.Sequence, last)
			}
			last = ev.Sequence
			seen++
		case <-deadline:
			t.Fatal("no events arrived")
		}
	}
}

func TestRemovedWorkspaceDisappears(t *testing.T) {
	s := New()
	defer s.Close()

	if err := s.RemoveWorkspace("ws-fix-cache"); err != nil {
		t.Fatalf("RemoveWorkspace: %v", err)
	}

	snap := s.Snapshot()
	if len(snap.Workspaces) != 3 {
		t.Errorf("got %d workspaces, want 3", len(snap.Workspaces))
	}
	if _, ok := snap.Workspace("ws-fix-cache"); ok {
		t.Error("the removed workspace is still present")
	}
}

func TestServiceHealthAffectsTheRollUp(t *testing.T) {
	s := New()
	defer s.Close()

	const id = "ws-feat-login"
	if got := rollUpOf(t, s, id); !got.Green {
		t.Fatalf("precondition failed, want green, got %s", got.Reason)
	}

	err := s.SetServiceHealth(id, "web", core.ServiceHealth{
		State:               core.ServiceUnhealthy,
		ProcessAlive:        core.ObservationTrue,
		Ready:               core.ObservationFalse,
		Probe:               core.ProbeHTTP,
		InstanceID:          "ws-feat-login-web-1",
		FailureReason:       "connection refused",
		ConsecutiveFailures: 2,
	})
	if err != nil {
		t.Fatalf("SetServiceHealth: %v", err)
	}

	got := rollUpOf(t, s, id)
	if got.Green {
		t.Error("an unhealthy required service must block green")
	}
	if got.Services != core.ServiceUnhealthy {
		t.Errorf("services = %q, want unhealthy", got.Services)
	}
	if got.Tests != core.TestPassing {
		t.Errorf("the tests column should be unaffected, got %q", got.Tests)
	}
}

func TestEventsChannelClosesOnStoreClose(t *testing.T) {
	s := New()
	events := s.Events(0)
	s.Close()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the events channel was not closed when the store shut down")
		}
	}
}

func TestSubscribingAfterCloseReturnsAClosedChannel(t *testing.T) {
	s := New()
	s.Close()

	events := s.Events(0)
	select {
	case _, ok := <-events:
		if ok {
			t.Error("want a closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("subscribing after close blocked")
	}
}
