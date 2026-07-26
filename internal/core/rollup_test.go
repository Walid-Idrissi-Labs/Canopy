package core

import (
	"strings"
	"testing"
)

func passingTest(name string, required bool, rev RevisionKey) TestSnapshot {
	return TestSnapshot{Name: name, Required: required, Latest: finishedRun(TestPassing, rev)}
}

func failingTest(name string, required bool, rev RevisionKey) TestSnapshot {
	return TestSnapshot{Name: name, Required: required, Latest: finishedRun(TestFailing, rev)}
}

func healthyService(name string, required bool) ServiceSnapshot {
	return ServiceSnapshot{
		Name:     name,
		Required: required,
		Instance: &ServiceInstance{InstanceID: "inst-" + name},
		Health: &ServiceHealth{
			ServiceName:  name,
			State:        ServiceHealthy,
			ProcessAlive: ObservationTrue,
			Ready:        ObservationTrue,
			Probe:        ProbeHTTP,
			InstanceID:   "inst-" + name,
		},
	}
}

func unhealthyService(name string, required bool) ServiceSnapshot {
	return ServiceSnapshot{
		Name:     name,
		Required: required,
		Instance: &ServiceInstance{InstanceID: "inst-" + name},
		Health: &ServiceHealth{
			ServiceName:   name,
			State:         ServiceUnhealthy,
			ProcessAlive:  ObservationTrue,
			Ready:         ObservationFalse,
			Probe:         ProbeHTTP,
			InstanceID:    "inst-" + name,
			FailureReason: "connection refused",
		},
	}
}

func workspace(rev RevisionKey, tests []TestSnapshot, services []ServiceSnapshot) WorkspaceSnapshot {
	return WorkspaceSnapshot{
		ID:        "ws1",
		Name:      "feat-login",
		Branch:    "feat/login",
		Ownership: OwnershipExternalReadOnly,
		Revision:  rev,
		Tests:     tests,
		Services:  services,
	}
}

func TestRollUpGreenRequiresEverything(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}
	stale := RevisionKey{HeadSHA: "bbb222"}

	tests := []struct {
		name      string
		workspace WorkspaceSnapshot
		wantGreen bool
	}{
		{
			"required test passing and required service healthy",
			workspace(rev, []TestSnapshot{passingTest("unit", true, rev)}, []ServiceSnapshot{healthyService("web", true)}),
			true,
		},
		{
			"required test passing, no services configured",
			workspace(rev, []TestSnapshot{passingTest("unit", true, rev)}, nil),
			true,
		},
		{
			"required service healthy, no tests configured",
			workspace(rev, nil, []ServiceSnapshot{healthyService("web", true)}),
			true,
		},
		{
			"a required test is failing",
			workspace(rev, []TestSnapshot{passingTest("unit", true, rev), failingTest("e2e", true, rev)}, nil),
			false,
		},
		{
			"a required test is stale",
			workspace(stale, []TestSnapshot{passingTest("unit", true, rev)}, nil),
			false,
		},
		{
			"a required test has never run",
			workspace(rev, []TestSnapshot{{Name: "unit", Required: true}}, nil),
			false,
		},
		{
			"a required service is unhealthy",
			workspace(rev, []TestSnapshot{passingTest("unit", true, rev)}, []ServiceSnapshot{unhealthyService("web", true)}),
			false,
		},
		{
			"a required service has never been probed",
			workspace(rev, nil, []ServiceSnapshot{{Name: "web", Required: true}}),
			false,
		},
		{
			"the revision is unknown",
			workspace(RevisionKey{}, []TestSnapshot{passingTest("unit", true, rev)}, nil),
			false,
		},
		{
			"nothing is configured at all",
			workspace(rev, nil, nil),
			false,
		},
		{
			"everything configured is optional",
			workspace(rev, []TestSnapshot{passingTest("unit", false, rev)}, []ServiceSnapshot{healthyService("web", false)}),
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RollUp(tc.workspace)
			if got.Green != tc.wantGreen {
				t.Errorf("Green = %v, want %v (reason: %s)", got.Green, tc.wantGreen, got.Reason)
			}
			if strings.TrimSpace(got.Reason) == "" {
				t.Error("a roll-up with no reason cannot explain itself")
			}
		})
	}
}

// Optional evidence exists so a user can say "watch this but do not block on it". It must never
// withhold green, and it must never be silently dropped either.
func TestOptionalEvidenceDoesNotBlockGreen(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}
	w := workspace(rev,
		[]TestSnapshot{passingTest("unit", true, rev), failingTest("lint", false, rev)},
		[]ServiceSnapshot{healthyService("web", true), unhealthyService("worker", false)},
	)

	got := RollUp(w)
	if !got.Green {
		t.Errorf("optional failures should not block green, reason: %s", got.Reason)
	}

	// The failure mode section 3.4 warns about: a green icon hiding evidence that is absent. The
	// optional problems have to survive into the output somewhere.
	if got.Caveat == "" {
		t.Error("a failing optional test and an unhealthy optional service produced no caveat")
	}
	if !strings.Contains(got.Caveat, "lint") {
		t.Errorf("caveat does not mention the failing optional test: %q", got.Caveat)
	}
	if !strings.Contains(got.Caveat, "worker") {
		t.Errorf("caveat does not mention the unhealthy optional service: %q", got.Caveat)
	}
	if got.Tests != TestFailing {
		t.Errorf("the tests column should show the optional failure, got %q", got.Tests)
	}
	if got.Services != ServiceUnhealthy {
		t.Errorf("the services column should show the optional problem, got %q", got.Services)
	}
}

// Corrections section 3.4 says a single green icon must not hide which evidence is absent, so the
// columns stay independently addressable rather than collapsing into the one indicator.
func TestTestsAndServicesRemainSeparatelyAddressable(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}

	w := workspace(rev,
		[]TestSnapshot{passingTest("unit", true, rev)},
		[]ServiceSnapshot{unhealthyService("web", true)},
	)
	got := RollUp(w)

	if got.Tests != TestPassing {
		t.Errorf("tests column = %q, want passing, it should not be dragged down by the service", got.Tests)
	}
	if got.Services != ServiceUnhealthy {
		t.Errorf("services column = %q, want unhealthy", got.Services)
	}
	if got.Green {
		t.Error("an unhealthy required service must block green even when the tests pass")
	}
	if !strings.Contains(got.Reason, "web") {
		t.Errorf("the reason should name the service that blocked green, got %q", got.Reason)
	}
}

// One broken test among several working ones must not be averaged away.
func TestAggregateColumnShowsTheWorstState(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}

	tests := []struct {
		name  string
		tests []TestSnapshot
		want  TestState
	}{
		{
			"one failure among passes",
			[]TestSnapshot{passingTest("a", true, rev), passingTest("b", true, rev), failingTest("c", true, rev)},
			TestFailing,
		},
		{
			"failing outranks stale",
			[]TestSnapshot{failingTest("a", true, rev), passingTest("b", true, RevisionKey{HeadSHA: "old"})},
			TestFailing,
		},
		{
			"stale outranks running",
			[]TestSnapshot{
				{Name: "a", Required: true, Latest: finishedRun(TestRunning, rev)},
				passingTest("b", true, RevisionKey{HeadSHA: "old"}),
			},
			TestStale,
		},
		{
			"unknown outranks stale",
			[]TestSnapshot{
				{Name: "a", Required: true},
				passingTest("b", true, RevisionKey{HeadSHA: "old"}),
			},
			TestUnknown,
		},
		{
			"all passing",
			[]TestSnapshot{passingTest("a", true, rev), passingTest("b", true, rev)},
			TestPassing,
		},
		{
			"nothing configured",
			nil,
			TestNotConfigured,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RollUp(workspace(rev, tc.tests, nil))
			if got.Tests != tc.want {
				t.Errorf("tests column = %q, want %q", got.Tests, tc.want)
			}
		})
	}
}

// The fourth workspace in the target demo. An unconfigured worktree must be visibly different
// from a verified one, and must never be mistaken for it.
func TestUnconfiguredWorkspaceIsNeverGreen(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}
	got := RollUp(workspace(rev, nil, nil))

	if got.Green {
		t.Error("a workspace with nothing configured must never be green")
	}
	if got.Tests != TestNotConfigured {
		t.Errorf("tests column = %q, want not-configured", got.Tests)
	}
	if got.Services != ServiceNotConfigured {
		t.Errorf("services column = %q, want not-configured", got.Services)
	}
	if !strings.Contains(got.Reason, "nothing is configured") {
		t.Errorf("the reason should say nothing is configured, got %q", got.Reason)
	}
}

// An unknown revision explains every blocker at once, so the reason should say that rather than
// listing each test separately with the same underlying cause.
func TestUnknownRevisionExplainsItselfOnce(t *testing.T) {
	w := workspace(RevisionKey{}, []TestSnapshot{
		passingTest("unit", true, RevisionKey{HeadSHA: "aaa"}),
		passingTest("e2e", true, RevisionKey{HeadSHA: "aaa"}),
	}, nil)
	w.RevisionError = "untracked file testdata/dump.sql is 512MB, above the 25MB fingerprint limit"

	got := RollUp(w)
	if got.Green {
		t.Error("an unknown revision must never be green")
	}
	if got.Reason != w.RevisionError {
		t.Errorf("the reason should be the revision error, got %q", got.Reason)
	}
}

func TestRollUpCounts(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}
	w := workspace(rev,
		[]TestSnapshot{passingTest("a", true, rev), failingTest("b", true, rev), passingTest("c", false, rev)},
		[]ServiceSnapshot{healthyService("web", true), unhealthyService("worker", true)},
	)

	got := RollUp(w)
	if got.TestsTotal != 3 || got.TestsPassing != 2 {
		t.Errorf("tests %d/%d passing, want 2/3", got.TestsPassing, got.TestsTotal)
	}
	if got.ServicesTotal != 2 || got.ServicesUp != 1 {
		t.Errorf("services %d/%d up, want 1/2", got.ServicesUp, got.ServicesTotal)
	}
	if got.RequiredTests != 2 {
		t.Errorf("required tests = %d, want 2", got.RequiredTests)
	}
}

// Every blocking problem should appear in the reason, not just the first one found. Someone
// fixing a workspace wants the whole list.
func TestReasonListsEveryBlocker(t *testing.T) {
	rev := RevisionKey{HeadSHA: "aaa111"}
	w := workspace(rev,
		[]TestSnapshot{failingTest("unit", true, rev), failingTest("e2e", true, rev)},
		[]ServiceSnapshot{unhealthyService("web", true)},
	)

	got := RollUp(w)
	for _, want := range []string{"unit", "e2e", "web"} {
		if !strings.Contains(got.Reason, want) {
			t.Errorf("reason %q does not mention %q", got.Reason, want)
		}
	}
}
