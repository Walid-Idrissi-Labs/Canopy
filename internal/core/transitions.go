package core

import "fmt"

// This file decides what the user is told about a test. It is pure: given a run and the current
// revision, the answer is always the same, which is what makes the truth contract testable
// instead of merely asserted.
//
// The recorded state of a run and the state shown to a user are deliberately different things.
// A run that exited zero is recorded as passing forever, because that is what happened. What the
// user sees depends on whether that evidence still describes the code in front of them. Keeping
// the two apart is the whole freshness model in one sentence.

// TestVerdict is a visible state together with the reason for it.
//
// The reason exists because the product's central promise is that the dashboard can always
// explain what evidence produced a result. A status nobody can account for is a status nobody
// should trust, so every path through ExplainTestState fills this in.
type TestVerdict struct {
	State  TestState
	Reason string
}

// ExplainTestState returns the visible state of a test run against the current revision, and why.
//
// run may be nil, meaning the test is configured but has never been executed.
func ExplainTestState(run *TestRun, current RevisionKey) TestVerdict {
	// Order matters below, and this case is first for a reason. If the current revision cannot be
	// computed, we do not know what code is in the worktree, so we cannot claim any result
	// describes it. That is a different failure from "the code changed": stale means re-run me,
	// unknown means I cannot even tell what this is.
	if !current.Known() {
		return TestVerdict{
			State:  TestUnknown,
			Reason: "the worktree revision could not be determined, so no result can be tied to it",
		}
	}

	if run == nil {
		return TestVerdict{
			State:  TestUnknown,
			Reason: "this test is configured but has never been run",
		}
	}

	if !run.Revision.Known() {
		return TestVerdict{
			State:  TestUnknown,
			Reason: "this run did not record which revision it tested",
		}
	}

	switch run.State {
	case TestQueued, TestRunning:
		// A run in flight is reported as in flight, even if the worktree has already moved on
		// underneath it. Its recorded revision is the one it started against, so the moment it
		// finishes it will resolve to stale on its own. Pre-empting that here would mean showing
		// stale for a run that has not produced anything to be stale yet.
		return TestVerdict{
			State:  run.State,
			Reason: "a run is in progress",
		}

	case TestPassing, TestFailing:
		// These two are results, so they are bound to the code they describe. Once the worktree
		// moves, the result stops describing it, whichever way it went.
		if !run.Revision.Equal(current) {
			return TestVerdict{
				State: TestStale,
				Reason: fmt.Sprintf("the worktree changed since this ran, it tested %s and the worktree is now %s",
					run.Revision.Short(), current.Short()),
			}
		}
		if run.State == TestPassing {
			return TestVerdict{
				State:  TestPassing,
				Reason: fmt.Sprintf("passed for the current revision %s", current.Short()),
			}
		}
		return TestVerdict{
			State:  TestFailing,
			Reason: fmt.Sprintf("failed for the current revision %s", current.Short()),
		}

	case TestCancelled:
		// A cancelled run is not a result, so there is nothing for a later edit to invalidate. It
		// stays cancelled rather than becoming stale, which is both more informative and equally
		// non-green.
		return TestVerdict{
			State:  TestCancelled,
			Reason: "this run was cancelled before it finished",
		}

	case TestError:
		// Same reasoning as cancelled. The command never produced a verdict about the code, so
		// there is no verdict to go out of date. Reporting stale here would imply a result exists.
		reason := "this run could not complete"
		if run.ErrorMessage != "" {
			reason = run.ErrorMessage
		}
		return TestVerdict{State: TestError, Reason: reason}

	case TestNotConfigured:
		return TestVerdict{
			State:  TestNotConfigured,
			Reason: "no test is configured",
		}

	case TestStale:
		// Staleness is derived here, never stored on a run. A run arriving already marked stale
		// means somebody wrote a state this package is supposed to compute, so the honest answer
		// is that we do not know what it means.
		return TestVerdict{
			State:  TestUnknown,
			Reason: "this run was recorded as stale, but staleness is derived and never stored",
		}

	default:
		return TestVerdict{
			State:  TestUnknown,
			Reason: fmt.Sprintf("this run has an unrecognised state %q", run.State),
		}
	}
}

// VisibleTestState returns what the user should be shown for a test run against the current
// revision.
func VisibleTestState(run *TestRun, current RevisionKey) TestState {
	return ExplainTestState(run, current).State
}

// VisibleState returns what the user should be shown for this configured test.
func (t TestSnapshot) VisibleState(current RevisionKey) TestState {
	return VisibleTestState(t.Latest, current)
}

// Explain returns the visible state of this configured test together with the reason.
func (t TestSnapshot) Explain(current RevisionKey) TestVerdict {
	return ExplainTestState(t.Latest, current)
}

// ExplainServiceState returns the visible state of a service and why.
//
// Unlike tests, service health is not revision bound. A dev server is either answering right now
// or it is not, and that has nothing to do with which commit is checked out. What matters instead
// is that the observation describes the service we think it does.
func ExplainServiceState(s ServiceSnapshot) ServiceVerdict {
	if s.Health == nil {
		return ServiceVerdict{
			State:  ServiceUnknown,
			Reason: "this service has never been probed",
		}
	}

	// An observation belongs to the process instance it was taken against. If the running
	// instance has changed since, the observation describes a different process, and a probe that
	// succeeded against a server which has since been replaced says nothing about the one running
	// now. This is how a leftover server from a deleted worktree ends up reported as healthy.
	if s.Instance != nil && s.Health.InstanceID != "" && s.Health.InstanceID != s.Instance.InstanceID {
		return ServiceVerdict{
			State:  ServiceUnknown,
			Reason: "the last probe describes a different process than the one running now",
		}
	}

	if !s.Health.State.Valid() {
		return ServiceVerdict{
			State:  ServiceUnknown,
			Reason: fmt.Sprintf("unrecognised service state %q", s.Health.State),
		}
	}

	// Liveness is not readiness. A service with no readiness probe configured has not been shown
	// to work, only shown to exist, so it must not be reported as healthy on liveness alone.
	if s.Health.State == ServiceHealthy && !s.Health.Ready.IsTrue() {
		return ServiceVerdict{
			State:  ServiceUnknown,
			Reason: "reported healthy without a successful readiness probe, which is not evidence of readiness",
		}
	}

	reason := s.Health.FailureReason
	if reason == "" {
		reason = fmt.Sprintf("last probe reported %s", s.Health.State)
	}
	return ServiceVerdict{State: s.Health.State, Reason: reason}
}

// ServiceVerdict is a visible service state together with the reason for it.
type ServiceVerdict struct {
	State  ServiceState
	Reason string
}

// VisibleState returns what the user should be shown for this configured service.
func (s ServiceSnapshot) VisibleState() ServiceState {
	return ExplainServiceState(s).State
}

// Explain returns the visible state of this configured service together with the reason.
func (s ServiceSnapshot) Explain() ServiceVerdict {
	return ExplainServiceState(s)
}
