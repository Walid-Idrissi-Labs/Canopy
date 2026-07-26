package core

import "time"

// TestState is the state of a test as shown to the user.
//
// The vocabulary is closed. These nine values are exactly the ones the truth contract defines,
// and AllTestStates is asserted against them in the tests. Adding or removing one is a change to
// the shared contract, not an implementation detail, because the roll-up rules, the display
// wording and the acceptance criteria are all written in terms of this list.
type TestState string

const (
	// TestNotConfigured means no test was ever configured for this workspace. It is emphatically
	// not the same as passing, and the two must never be rendered alike.
	TestNotConfigured TestState = "not-configured"
	// TestQueued means a run has been requested but has not started.
	TestQueued TestState = "queued"
	// TestRunning means a run is in flight.
	TestRunning TestState = "running"
	// TestPassing means the command exited zero for the revision recorded on the run, and that
	// revision is still the current one.
	TestPassing TestState = "passing"
	// TestFailing means the command ran to completion and exited non zero.
	TestFailing TestState = "failing"
	// TestStale means a run completed but the worktree has changed since. The result may still be
	// accurate, but Canopy cannot claim so. It reads as "ask me again", not as "broken".
	TestStale TestState = "stale"
	// TestCancelled means a run was stopped by the user. Never green.
	TestCancelled TestState = "cancelled"
	// TestError means the command could not run or could not finish, as opposed to running and
	// failing. A missing binary and a timeout land here.
	TestError TestState = "error"
	// TestUnknown means the evidence cannot be trusted, typically because the revision could not
	// be computed. Never green.
	TestUnknown TestState = "unknown"
)

// AllTestStates returns every valid test state, in the order the truth contract lists them.
func AllTestStates() []TestState {
	return []TestState{
		TestNotConfigured,
		TestQueued,
		TestRunning,
		TestPassing,
		TestFailing,
		TestStale,
		TestCancelled,
		TestError,
		TestUnknown,
	}
}

// Valid reports whether s is part of the closed vocabulary.
func (s TestState) Valid() bool {
	for _, known := range AllTestStates() {
		if s == known {
			return true
		}
	}
	return false
}

// IsGreen reports whether this state may contribute to a green roll-up.
//
// Exactly one state qualifies. Everything else, including stale and not-configured, does not.
func (s TestState) IsGreen() bool {
	return s == TestPassing
}

// IsTerminal reports whether a run in this state has finished and will not change on its own.
//
// This matters to the event store: a terminal transition is the last thing anyone will hear about
// a run, so it may never be coalesced away.
func (s TestState) IsTerminal() bool {
	switch s {
	case TestPassing, TestFailing, TestCancelled, TestError:
		return true
	default:
		return false
	}
}

// IsPending reports whether a run is expected to produce a result shortly.
func (s TestState) IsPending() bool {
	return s == TestQueued || s == TestRunning
}

func (s TestState) String() string { return string(s) }

// TestRun is the record of a single execution of a configured test command.
//
// The revision is captured when the run starts rather than when it finishes, because the result
// belongs to the code that was present when the command began reading it. A run that starts,
// takes four minutes, and passes tells you the code from four minutes ago was good.
type TestRun struct {
	ID          string
	WorkspaceID string
	TestName    string

	// Revision is the worktree content this run was executed against.
	Revision RevisionKey

	// CommandDisplay is the fully resolved command, safe to show to a user. It never contains
	// values marked secret.
	CommandDisplay string

	StartedAt  time.Time
	FinishedAt *time.Time

	// ExitCode is nil until the process finishes. It is the only source of pass and fail truth,
	// since no framework parsers ship.
	ExitCode *int

	// State is the recorded outcome of the run itself. It is not necessarily what the user sees:
	// a run recorded as passing displays as stale once the worktree moves on. Use
	// VisibleTestState to get the displayed value.
	State TestState

	// PassedCount and FailedCount are optional metadata and stay nil, since no framework parsers
	// ship. They exist so adding parsers later is not a contract change.
	PassedCount *int
	FailedCount *int

	// OutputBufferID points at the bounded log buffer holding this run's output.
	OutputBufferID string

	// Parser names the parser that produced the counts, and is currently always empty.
	Parser string

	// ErrorMessage explains a TestError, for example that the binary was not found or that the
	// run exceeded its timeout. It is empty for an ordinary pass or fail.
	ErrorMessage string
}

// Duration returns how long the run took, and whether it has finished.
func (r *TestRun) Duration() (time.Duration, bool) {
	if r == nil || r.FinishedAt == nil {
		return 0, false
	}
	return r.FinishedAt.Sub(r.StartedAt), true
}

// Age returns how long ago the run finished, relative to now, and whether it has finished.
func (r *TestRun) Age(now time.Time) (time.Duration, bool) {
	if r == nil || r.FinishedAt == nil {
		return 0, false
	}
	return now.Sub(*r.FinishedAt), true
}
