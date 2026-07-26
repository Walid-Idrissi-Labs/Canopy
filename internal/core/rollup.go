package core

import "fmt"

// This file answers the one question the dashboard exists to answer: is this worktree verified
// for the code currently in it?
//
// The answer is deliberately hard to get to yes. Green requires every required test to be passing
// for the current revision and every required service to be healthy, with nothing unknown, stale
// or never run. Anything else is not green, and the reason says which piece of evidence was
// missing.

// Rollup is the workspace level verification summary.
//
// Green and the two aggregate columns are separate values on purpose. Corrections section 3.4 is
// explicit that a single green icon must not hide which evidence is absent, so the columns always
// report the worst state across everything configured, while Green reports only on the evidence
// the user marked required.
type Rollup struct {
	// Green is the verification indicator. True only when every required piece of evidence is
	// current and passing.
	Green bool

	// Reason explains the verdict in words a user can act on. It is filled in whether or not
	// Green is true.
	Reason string

	// Caveat names non-blocking problems that exist even though Green is true, for example an
	// optional test that is failing. It is empty when there is nothing to add.
	//
	// This field is the guard against the failure mode section 3.4 warns about. Without it, a
	// user who marked a test optional months ago sees a green row and never learns that it has
	// been broken since.
	Caveat string

	// Tests aggregates every configured test, required or not.
	Tests TestState

	// Services aggregates every configured service, required or not.
	Services ServiceState

	TestsPassing   int
	TestsTotal     int
	ServicesUp     int
	ServicesTotal  int
	RequiredTests  int
	RequiredUpSvcs int
}

// testSeverity ranks test states by how far they are from a trustworthy pass, worst first.
//
// The aggregate column shows the highest ranked state across the configured tests, so one broken
// test is never averaged away by four working ones. The ordering is a product decision rather
// than an arbitrary one: a current failure is the most actionable thing a user can see, and
// not-configured is the least, because it is not a problem at all.
func testSeverity(s TestState) int {
	switch s {
	case TestFailing:
		return 70
	case TestError:
		return 60
	case TestUnknown:
		return 50
	case TestStale:
		return 40
	case TestRunning:
		return 30
	case TestQueued:
		return 20
	case TestPassing:
		return 10
	case TestNotConfigured:
		return 0
	default:
		// An unrecognised state outranks everything. If a state exists that this function has
		// never heard of, the safe reading is that something is wrong, not that it is fine.
		return 100
	}
}

// serviceSeverity ranks service states by distance from healthy, worst first.
func serviceSeverity(s ServiceState) int {
	switch s {
	case ServiceCrashed:
		return 70
	case ServiceUnhealthy:
		return 60
	case ServiceUnknown:
		return 50
	case ServiceStopped:
		return 40
	case ServiceStopping:
		return 30
	case ServiceStarting:
		return 20
	case ServiceHealthy:
		return 10
	case ServiceNotConfigured:
		return 0
	default:
		return 100
	}
}

// RollUp computes the verification summary for a workspace.
func RollUp(w WorkspaceSnapshot) Rollup {
	r := Rollup{
		Tests:         TestNotConfigured,
		Services:      ServiceNotConfigured,
		TestsTotal:    len(w.Tests),
		ServicesTotal: len(w.Services),
	}

	// Blockers are required evidence that is not currently passing or healthy. Caveats are the
	// same problem on optional evidence: worth saying, never enough to withhold green.
	var blockers []string
	var caveats []string

	for _, test := range w.Tests {
		verdict := test.Explain(w.Revision)

		if testSeverity(verdict.State) > testSeverity(r.Tests) {
			r.Tests = verdict.State
		}
		if verdict.State.IsGreen() {
			r.TestsPassing++
		}
		if !test.Required {
			if !verdict.State.IsGreen() {
				caveats = append(caveats, fmt.Sprintf("optional test %q is %s", test.Name, verdict.State))
			}
			continue
		}

		r.RequiredTests++
		if !verdict.State.IsGreen() {
			blockers = append(blockers, fmt.Sprintf("test %q is %s (%s)", test.Name, verdict.State, verdict.Reason))
		}
	}

	for _, service := range w.Services {
		verdict := service.Explain()

		if serviceSeverity(verdict.State) > serviceSeverity(r.Services) {
			r.Services = verdict.State
		}
		if verdict.State.IsGreen() {
			r.ServicesUp++
		}
		if !service.Required {
			if !verdict.State.IsGreen() {
				caveats = append(caveats, fmt.Sprintf("optional service %q is %s", service.Name, verdict.State))
			}
			continue
		}

		r.RequiredUpSvcs++
		if !verdict.State.IsGreen() {
			blockers = append(blockers, fmt.Sprintf("service %q is %s (%s)", service.Name, verdict.State, verdict.Reason))
		}
	}

	r.Caveat = joinReasons(caveats)

	// A revision we could not compute means we do not know what code this is, so nothing can be
	// verified for it. Checked before the blocker list because it explains every blocker at once,
	// and repeating "unknown revision" per test would bury the actual cause.
	if !w.Revision.Known() {
		r.Green = false
		r.Reason = "the worktree revision could not be determined"
		if w.RevisionError != "" {
			r.Reason = w.RevisionError
		}
		return r
	}

	// Nothing configured is not the same as nothing wrong. A workspace with no required evidence
	// has never been verified, and showing it green would be the product's central lie: an
	// unconfigured worktree looking exactly like a tested one.
	if r.RequiredTests == 0 && r.RequiredUpSvcs == 0 {
		r.Green = false
		switch {
		case r.TestsTotal == 0 && r.ServicesTotal == 0:
			r.Reason = "nothing is configured for this workspace, so there is no evidence to show"
		default:
			r.Reason = "nothing is marked required, so there is no evidence that has to hold"
		}
		return r
	}

	if len(blockers) > 0 {
		r.Green = false
		r.Reason = joinReasons(blockers)
		return r
	}

	r.Green = true
	r.Reason = fmt.Sprintf("all required evidence is current and passing for revision %s", w.Revision.Short())
	return r
}

// joinReasons combines several explanations into one line, keeping them all rather than picking a
// winner. A user fixing a workspace wants the whole list, not the first item repeatedly.
func joinReasons(reasons []string) string {
	switch len(reasons) {
	case 0:
		return ""
	case 1:
		return reasons[0]
	}
	out := reasons[0]
	for _, reason := range reasons[1:] {
		out += "; " + reason
	}
	return out
}
