package core

import "time"

// WorkspaceOwnership says how much authority Canopy has over a worktree.
//
// The distinction is a safety boundary rather than bookkeeping. Canopy observes worktrees that
// other tools created, and it must never delete, reset or clean something it did not make.
type WorkspaceOwnership string

const (
	// OwnershipPrimary is the user's original checkout. Canopy never removes it, under any
	// future feature.
	OwnershipPrimary WorkspaceOwnership = "primary"
	// OwnershipManaged is a worktree Canopy created for an agent, and may remove again.
	OwnershipManaged WorkspaceOwnership = "managed"
	// OwnershipAdopted is a worktree that started external and was explicitly handed to Canopy.
	OwnershipAdopted WorkspaceOwnership = "adopted"
	// OwnershipExternalReadOnly is a worktree Canopy discovered and watches, but never modifies.
	// Somebody else made it, so Canopy does not get to remove it.
	OwnershipExternalReadOnly WorkspaceOwnership = "external-read-only"
)

// AllWorkspaceOwnerships returns every valid ownership value.
func AllWorkspaceOwnerships() []WorkspaceOwnership {
	return []WorkspaceOwnership{
		OwnershipPrimary,
		OwnershipManaged,
		OwnershipAdopted,
		OwnershipExternalReadOnly,
	}
}

// Valid reports whether o is a known ownership value.
func (o WorkspaceOwnership) Valid() bool {
	for _, known := range AllWorkspaceOwnerships() {
		if o == known {
			return true
		}
	}
	return false
}

// DiscoveredNotCreated reports whether this worktree came from outside Canopy.
//
// Discovery may only ever produce these two. A worktree Canopy did not make is one it does not
// get to unmake, and keeping that as a property of the ownership value rather than a rule someone
// remembers is what makes it checkable.
func (o WorkspaceOwnership) DiscoveredNotCreated() bool {
	return o == OwnershipPrimary || o == OwnershipExternalReadOnly
}

// AllowsLifecycleOperations reports whether Canopy may remove, prune or reset this worktree.
//
// True only for worktrees Canopy created for an agent, or ones explicitly handed to it. The
// primary checkout is never removable under any feature, and neither is a worktree somebody else
// created and Canopy merely watches.
//
// Every destructive git path asks this question through this method, so a reviewer has one place
// to check rather than several to find.
func (o WorkspaceOwnership) AllowsLifecycleOperations() bool {
	return o == OwnershipManaged || o == OwnershipAdopted
}

func (o WorkspaceOwnership) String() string { return string(o) }

// DirtyState summarises uncommitted work in a worktree.
type DirtyState struct {
	Staged    int
	Unstaged  int
	Untracked int
}

// IsDirty reports whether the worktree has any uncommitted change at all.
func (d DirtyState) IsDirty() bool {
	return d.Staged > 0 || d.Unstaged > 0 || d.Untracked > 0
}

// Total returns the number of changed paths across all three categories.
func (d DirtyState) Total() int {
	return d.Staged + d.Unstaged + d.Untracked
}

// TestSnapshot is one configured test and the most recent run of it.
//
// It holds evidence, not conclusions. The state a user sees is derived from Latest together with
// the workspace's current revision, by VisibleTestState. Keeping those apart is what lets the
// dashboard explain why something is stale instead of just asserting that it is.
type TestSnapshot struct {
	Name string

	// Required decides whether this test can block a green roll-up.
	Required bool

	// Latest is the most recent run, or nil if this test has never been run.
	Latest *TestRun
}

// ServiceSnapshot is one configured service, the instance backing it, and its latest health
// observation.
type ServiceSnapshot struct {
	Name string

	// Required decides whether this service can block a green roll-up.
	Required bool

	// Managed is always false. Canopy observes services rather than starting them, see D-06.
	Managed bool

	// Instance is the running service this snapshot describes, or nil if none was found.
	Instance *ServiceInstance

	// Health is the latest observation, or nil if the service has never been probed.
	Health *ServiceHealth
}

// WorkspaceSnapshot is everything known about one worktree at one moment.
//
// It is immutable once published. Consumers read it and never write to it, which is what lets the
// UI hold one without locking and lets the store replace it wholesale.
type WorkspaceSnapshot struct {
	ID   string
	Name string
	Path string

	// Branch is empty when the worktree has a detached HEAD.
	Branch   string
	Detached bool

	Ownership WorkspaceOwnership

	// Revision is the current content of the worktree. An unknown revision means evidence about
	// this workspace cannot be trusted.
	Revision RevisionKey

	// RevisionError explains why Revision is unknown, in words a user can act on. Empty when the
	// revision is known. This is what turns an unknown state from a shrug into an explanation,
	// for example naming the untracked file that exceeded the fingerprint limit.
	RevisionError string

	Dirty DirtyState

	// LastActivity is the most recent filesystem or git activity seen in this worktree.
	LastActivity time.Time

	Tests    []TestSnapshot
	Services []ServiceSnapshot
}

// Test returns the snapshot for a named test.
func (w WorkspaceSnapshot) Test(name string) (TestSnapshot, bool) {
	for _, t := range w.Tests {
		if t.Name == name {
			return t, true
		}
	}
	return TestSnapshot{}, false
}

// Service returns the snapshot for a named service.
func (w WorkspaceSnapshot) Service(name string) (ServiceSnapshot, bool) {
	for _, s := range w.Services {
		if s.Name == name {
			return s, true
		}
	}
	return ServiceSnapshot{}, false
}
