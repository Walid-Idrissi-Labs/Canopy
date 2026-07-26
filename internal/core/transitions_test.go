package core

import (
	"strings"
	"testing"
	"time"
)

func intPtr(v int) *int { return &v }

func finishedRun(state TestState, rev RevisionKey) *TestRun {
	finished := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	run := &TestRun{
		ID:          "run1",
		WorkspaceID: "ws1",
		TestName:    "unit",
		Revision:    rev,
		StartedAt:   finished.Add(-30 * time.Second),
		FinishedAt:  &finished,
		State:       state,
	}
	switch state {
	case TestPassing:
		run.ExitCode = intPtr(0)
	case TestFailing:
		run.ExitCode = intPtr(1)
	}
	return run
}

func TestVisibleTestStateTable(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}
	moved := RevisionKey{HeadSHA: "bbb222"}
	edited := RevisionKey{HeadSHA: "aaa111", DirtyDigest: "dirty1"}
	unknown := RevisionKey{}

	tests := []struct {
		name    string
		run     *TestRun
		current RevisionKey
		want    TestState
	}{
		{"passing for the same revision", finishedRun(TestPassing, rev), rev, TestPassing},
		{"passing after a commit changed", finishedRun(TestPassing, rev), moved, TestStale},
		{"passing after an edit made the worktree dirty", finishedRun(TestPassing, rev), edited, TestStale},
		{"failing for the same revision", finishedRun(TestFailing, rev), rev, TestFailing},
		{"failing after the code changed", finishedRun(TestFailing, rev), moved, TestStale},
		{"cancelled stays cancelled", finishedRun(TestCancelled, rev), rev, TestCancelled},
		{"cancelled after a change stays cancelled", finishedRun(TestCancelled, rev), moved, TestCancelled},
		{"error stays error", finishedRun(TestError, rev), rev, TestError},
		{"error after a change stays error", finishedRun(TestError, rev), moved, TestError},
		{"queued", finishedRun(TestQueued, rev), rev, TestQueued},
		{"running", finishedRun(TestRunning, rev), rev, TestRunning},
		{"running while the worktree moves is still running", finishedRun(TestRunning, rev), moved, TestRunning},
		{"never run", nil, rev, TestUnknown},
		{"current revision unknown", finishedRun(TestPassing, rev), unknown, TestUnknown},
		{"run revision unknown", finishedRun(TestPassing, unknown), rev, TestUnknown},
		{"both revisions unknown", finishedRun(TestPassing, unknown), unknown, TestUnknown},
		{"run recorded as not-configured", finishedRun(TestNotConfigured, rev), rev, TestNotConfigured},
		{"run recorded as stale is not trusted", finishedRun(TestStale, rev), rev, TestUnknown},
		{"unrecognised run state", finishedRun(TestState("weird"), rev), rev, TestUnknown},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VisibleTestState(tc.run, tc.current)
			if got != tc.want {
				t.Errorf("VisibleTestState() = %q, want %q", got, tc.want)
			}
			if !got.Valid() {
				t.Errorf("VisibleTestState() returned %q, which is outside the vocabulary", got)
			}
		})
	}
}

// The core acceptance criterion from corrections section 12: editing a file turns a green result
// stale. Every kind of edit that changes the revision key has to do it, not just a new commit.
func TestEditingTurnsPassingIntoStale(t *testing.T) {
	tested := RevisionKey{HeadSHA: "aaa111"}
	run := finishedRun(TestPassing, tested)

	if got := VisibleTestState(run, tested); got != TestPassing {
		t.Fatalf("precondition failed, run should be passing for its own revision, got %q", got)
	}

	changes := map[string]RevisionKey{
		"a tracked file was edited":           {HeadSHA: "aaa111", DirtyDigest: "unstaged"},
		"content was staged":                  {HeadSHA: "aaa111", DirtyDigest: "staged"},
		"an untracked file was added":         {HeadSHA: "aaa111", DirtyDigest: "untracked"},
		"a commit was made":                   {HeadSHA: "bbb222"},
		"a commit was made and then edited":   {HeadSHA: "bbb222", DirtyDigest: "d"},
		"the branch was reset to another sha": {HeadSHA: "ccc333"},
	}

	for name, after := range changes {
		t.Run(name, func(t *testing.T) {
			if got := VisibleTestState(run, after); got != TestStale {
				t.Errorf("after %s the result should be stale, got %q", name, got)
			}
		})
	}
}

// Returning the worktree to its earlier content does legitimately restore the result, because the
// revision key is content addressed and the evidence really does describe that content again.
// What must not happen is a near miss counting as a match.
func TestReturningToTheTestedContentRestoresTheResult(t *testing.T) {
	tested := RevisionKey{HeadSHA: "aaa111", DirtyDigest: "digest-A"}
	run := finishedRun(TestPassing, tested)

	if got := VisibleTestState(run, RevisionKey{HeadSHA: "aaa111", DirtyDigest: "digest-B"}); got != TestStale {
		t.Errorf("different content should be stale, got %q", got)
	}
	if got := VisibleTestState(run, tested); got != TestPassing {
		t.Errorf("identical content should be passing again, got %q", got)
	}
	if got := VisibleTestState(run, RevisionKey{HeadSHA: "aaa111"}); got != TestStale {
		t.Errorf("same commit but clean is different content, should be stale, got %q", got)
	}
}

// From corrections section 12: a cancelled, timed out, unknown or unconfigured run is never green.
// This walks every state and every revision relationship rather than spot checking, because
// "never" is the kind of claim that quietly stops being true.
func TestNothingButAMatchingPassIsEverGreen(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}
	moved := RevisionKey{HeadSHA: "bbb222"}
	unknown := RevisionKey{}

	for _, runState := range AllTestStates() {
		for _, current := range []RevisionKey{rev, moved, unknown} {
			for _, runRev := range []RevisionKey{rev, moved, unknown} {
				run := finishedRun(runState, runRev)
				got := VisibleTestState(run, current)
				if !got.IsGreen() {
					continue
				}
				// The only combination allowed to be green.
				if runState == TestPassing && runRev.Equal(current) {
					continue
				}
				t.Errorf("run state %q with run revision %v against current %v was green",
					runState, runRev, current)
			}
		}
	}
}

// A run with no evidence at all must never be green, whatever the revision situation is.
func TestNilRunIsNeverGreen(t *testing.T) {
	for _, current := range []RevisionKey{{HeadSHA: "aaa"}, {}} {
		if VisibleTestState(nil, current).IsGreen() {
			t.Errorf("a test that never ran was green against current %v", current)
		}
	}
}

// From corrections section 3.2: a failed parser does not turn a successful exit code into a
// failure. v0.1 ships no parsers, so this asserts the shape holds when they arrive: the counts are
// metadata and the exit code is the verdict.
func TestParserMetadataDoesNotOverrideExitCode(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}
	run := finishedRun(TestPassing, rev)
	run.Parser = "jest"
	run.ErrorMessage = "parser failed to read the json report"
	run.PassedCount = nil
	run.FailedCount = intPtr(3)

	if got := VisibleTestState(run, rev); got != TestPassing {
		t.Errorf("exit code zero should still be passing despite parser trouble, got %q", got)
	}
}

// An unknown revision is not the same as a changed one, and the distinction has to survive into
// what the user is told. Stale means re-run me. Unknown means I cannot tell what this code is.
func TestUnknownRevisionIsDistinctFromStale(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}
	run := finishedRun(TestPassing, rev)

	stale := ExplainTestState(run, RevisionKey{HeadSHA: "bbb222"})
	unknown := ExplainTestState(run, RevisionKey{})

	if stale.State != TestStale {
		t.Errorf("changed revision should be stale, got %q", stale.State)
	}
	if unknown.State != TestUnknown {
		t.Errorf("uncomputable revision should be unknown, got %q", unknown.State)
	}
	if stale.Reason == unknown.Reason {
		t.Error("stale and unknown should not be explained the same way")
	}
}

// The dashboard must never show a result it cannot account for, so every path has to produce a
// reason, including the ones nobody expects to hit.
func TestEveryVerdictHasAReason(t *testing.T) {
	revisions := []RevisionKey{{HeadSHA: "aaa111"}, {HeadSHA: "bbb222"}, {}}
	for _, runState := range append(AllTestStates(), TestState("nonsense")) {
		for _, current := range revisions {
			for _, runRev := range revisions {
				v := ExplainTestState(finishedRun(runState, runRev), current)
				if strings.TrimSpace(v.Reason) == "" {
					t.Errorf("no reason given for run state %q, run revision %v, current %v",
						runState, runRev, current)
				}
			}
		}
	}
	if strings.TrimSpace(ExplainTestState(nil, RevisionKey{HeadSHA: "a"}).Reason) == "" {
		t.Error("no reason given for a test that never ran")
	}
}

func TestTestSnapshotVisibleState(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}

	neverRun := TestSnapshot{Name: "unit", Required: true}
	if got := neverRun.VisibleState(rev); got != TestUnknown {
		t.Errorf("a configured test that never ran = %q, want unknown", got)
	}

	passed := TestSnapshot{Name: "unit", Required: true, Latest: finishedRun(TestPassing, rev)}
	if got := passed.VisibleState(rev); got != TestPassing {
		t.Errorf("a passing test = %q, want passing", got)
	}
	if got := passed.VisibleState(RevisionKey{HeadSHA: "bbb222"}); got != TestStale {
		t.Errorf("a passing test after a change = %q, want stale", got)
	}
}

func TestServiceVisibleState(t *testing.T) {
	instance := &ServiceInstance{
		WorkspaceID: "ws1",
		ServiceName: "web",
		InstanceID:  "inst-1",
		PID:         4242,
		Port:        4100,
	}
	healthy := &ServiceHealth{
		WorkspaceID:  "ws1",
		ServiceName:  "web",
		State:        ServiceHealthy,
		ProcessAlive: ObservationTrue,
		Ready:        ObservationTrue,
		Probe:        ProbeHTTP,
		InstanceID:   "inst-1",
	}

	tests := []struct {
		name string
		snap ServiceSnapshot
		want ServiceState
	}{
		{
			"never probed",
			ServiceSnapshot{Name: "web", Required: true},
			ServiceUnknown,
		},
		{
			"healthy with a successful readiness probe",
			ServiceSnapshot{Name: "web", Required: true, Instance: instance, Health: healthy},
			ServiceHealthy,
		},
		{
			"running but failing its probe",
			ServiceSnapshot{Name: "web", Required: true, Instance: instance, Health: &ServiceHealth{
				State:        ServiceUnhealthy,
				ProcessAlive: ObservationTrue,
				Ready:        ObservationFalse,
				Probe:        ProbeHTTP,
				InstanceID:   "inst-1",
			}},
			ServiceUnhealthy,
		},
		{
			"probe describes a process that is no longer the running one",
			ServiceSnapshot{Name: "web", Required: true, Instance: &ServiceInstance{InstanceID: "inst-2"}, Health: healthy},
			ServiceUnknown,
		},
		{
			"claims healthy without ever passing a readiness probe",
			ServiceSnapshot{Name: "web", Required: true, Instance: instance, Health: &ServiceHealth{
				State:        ServiceHealthy,
				ProcessAlive: ObservationTrue,
				Ready:        ObservationUnknown,
				Probe:        ProbeNone,
				InstanceID:   "inst-1",
			}},
			ServiceUnknown,
		},
		{
			"unrecognised state",
			ServiceSnapshot{Name: "web", Health: &ServiceHealth{State: ServiceState("fine")}},
			ServiceUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.snap.VisibleState()
			if got != tc.want {
				t.Errorf("VisibleState() = %q, want %q", got, tc.want)
			}
			if v := tc.snap.Explain(); strings.TrimSpace(v.Reason) == "" {
				t.Error("no reason given")
			}
		})
	}
}

// A process being alive says the program exists. It does not say the program works. Reporting
// healthy on liveness alone is one of the two easiest false greens in this product, alongside
// accepting a probe from an unrelated process on the same port.
func TestLivenessAloneIsNotHealth(t *testing.T) {
	snap := ServiceSnapshot{
		Name:     "web",
		Required: true,
		Instance: &ServiceInstance{InstanceID: "inst-1", PID: 999},
		Health: &ServiceHealth{
			State:        ServiceHealthy,
			ProcessAlive: ObservationTrue,
			Ready:        ObservationUnknown,
			Probe:        ProbeProcess,
			InstanceID:   "inst-1",
		},
	}
	if got := snap.VisibleState(); got.IsGreen() {
		t.Errorf("a live process with no readiness evidence reported %q, which is green", got)
	}
}
