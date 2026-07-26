package core

import "context"

// The interfaces below are the seam between the verification engine and everything that consumes
// it. They came from the original truth contract and survive the move to an agent runtime intact,
// because whether a result still describes the code in front of you is the same question whoever
// produced the code.
//
// The agent side of the contract, meaning providers, sessions, tools and permissions, is added in
// A1 through A4 alongside the packages that implement it. Interfaces are written when the thing
// they describe is about to exist, not before.

// WorkspaceSource discovers the worktrees Canopy should watch.
//
// Discovery only reads. Creating and removing worktrees is a separate concern (A5-03) so that the
// code path which finds worktrees can never be the one that destroys them.
type WorkspaceSource interface {
	// Discover returns every worktree currently known, including the primary checkout.
	//
	// It returns the full set rather than a delta, so a worktree removed outside Canopy simply
	// stops appearing. Callers replace their view wholesale instead of reconciling, which is what
	// makes an externally deleted worktree disappear safely rather than lingering as a row nobody
	// can explain.
	Discover(ctx context.Context) ([]WorkspaceSnapshot, error)
}

// RevisionTracker computes the current content identity of a worktree.
//
// This is the source of all freshness. Everything the product claims about a result still being
// true reduces to comparing what this returns now against what it returned when the result was
// captured.
type RevisionTracker interface {
	// Current returns the revision key for a workspace as of now.
	//
	// On failure it returns the zero RevisionKey, for which Known reports false, together with an
	// error explaining why. An implementation must never return a partially filled key that
	// would report itself as known, because a half computed revision compares unequal to
	// everything and would present as a permanent, unexplained stale.
	//
	// The error text is shown to the user in WorkspaceSnapshot.RevisionError, so it should name
	// the cause concretely, for example the untracked file that exceeded the fingerprint limit,
	// rather than saying that something went wrong.
	Current(ctx context.Context, workspaceID string) (RevisionKey, error)
}

// TestRunner executes configured test commands.
//
// An implementation must refuse to run anything unless the project's trust state allows
// execution. Trust is checked at the point of execution rather than at the point of
// configuration, because configuration can change between the two.
type TestRunner interface {
	// Start begins a run and returns its run ID.
	//
	// The revision is captured when the run starts, not when it finishes, because the result
	// belongs to the code that was present when the command began reading it.
	//
	// Start returns an error only when the run could not be started at all. A command that starts
	// and then fails is a successful Start followed by a run that reaches TestFailing. A command
	// that could not start, such as a missing binary, produces TestError rather than TestFailing,
	// and the two must not be conflated: one says the code is broken, the other says we do not
	// know.
	Start(ctx context.Context, workspaceID, testName string) (runID string, err error)

	// Cancel stops a running test.
	//
	// It terminates the whole process group, not just the immediate child, since test runners
	// routinely spawn workers that would otherwise survive and keep holding ports and file
	// handles. A cancelled run reaches TestCancelled and is never green.
	Cancel(ctx context.Context, runID string) error
}

// HealthChecker probes a service the user started.
//
// Canopy observes services rather than starting them. Managed services are deferred past A9,
// see D-06.
type HealthChecker interface {
	// Check performs one probe and returns what it observed.
	//
	// On failure it returns a ServiceHealth with State ServiceUnknown and FailureReason filled
	// in, alongside the error. It must not return a zero ServiceHealth, because a caller that
	// ignored the error would then be holding a value that claims nothing rather than one that
	// says the check failed.
	//
	// The returned health carries the InstanceID it describes. A result whose instance no longer
	// matches the running service is evidence about a different process, and treating it
	// otherwise is how a stale server from a deleted worktree ends up reported as healthy.
	Check(ctx context.Context, workspaceID, serviceName string) (ServiceHealth, error)
}

// SnapshotStore is the authoritative read model.
//
// The snapshot owns product truth. The event stream only says that something moved. That split is
// the reason a consumer can recover from any amount of dropped, delayed or coalesced
// notification: at worst it re-reads and is correct again.
type SnapshotStore interface {
	// Snapshot returns a complete, internally consistent view of everything known right now.
	//
	// The result is immutable and safe to hold. Callers must not mutate it.
	Snapshot() ProjectSnapshot

	// Events returns a channel of notifications for everything that happened after the given
	// sequence number.
	//
	// The intended usage is to take a Snapshot, read its Sequence, and subscribe from there.
	// Doing it in that order means no update can slip through the gap between reading and
	// subscribing, which is the bug this signature exists to make impossible.
	//
	// The channel is closed when the store shuts down. A consumer that falls behind may see
	// coalesced events, but never a dropped final transition, and never a sequence number out of
	// order.
	Events(afterSequence uint64) <-chan Event
}
