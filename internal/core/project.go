package core

import "time"

// ConfigState is what Canopy knows about the project's configuration file.
//
// Missing and invalid are kept apart because they need different words in the UI and lead the
// user to different next actions. Neither may be quietly treated as "nothing configured, all
// good".
type ConfigState string

const (
	// ConfigMissing means no configuration file was found. Canopy can still show worktrees, git
	// state and revisions, but has nothing to run.
	ConfigMissing ConfigState = "missing"
	// ConfigInvalid means a file exists but failed validation. Nothing runs, and ConfigError says
	// why.
	ConfigInvalid ConfigState = "invalid"
	// ConfigLoaded means a valid configuration is in effect.
	ConfigLoaded ConfigState = "loaded"
)

// AllConfigStates returns every valid configuration state.
func AllConfigStates() []ConfigState {
	return []ConfigState{ConfigMissing, ConfigInvalid, ConfigLoaded}
}

// Valid reports whether s is a known configuration state.
func (s ConfigState) Valid() bool {
	for _, known := range AllConfigStates() {
		if s == known {
			return true
		}
	}
	return false
}

func (s ConfigState) String() string { return string(s) }

// TrustState is whether the user has approved running this repository's configured commands.
//
// A configuration file that lives in a repository can run anything as the user, so approval is
// required before the first execution and is invalidated whenever an executable field changes.
type TrustState string

const (
	// TrustNotRequired means there is nothing executable to approve, so no prompt is owed.
	TrustNotRequired TrustState = "not-required"
	// TrustPending means commands exist but have never been approved. Nothing runs.
	TrustPending TrustState = "pending"
	// TrustGranted means the current configuration was reviewed and approved.
	TrustGranted TrustState = "granted"
	// TrustStale means a previous approval exists but an executable field has changed since, so
	// approval no longer covers what would run. Nothing runs until it is re-approved.
	TrustStale TrustState = "stale"
	// TrustDenied means the user looked at the commands and said no.
	TrustDenied TrustState = "denied"
)

// AllTrustStates returns every valid trust state.
func AllTrustStates() []TrustState {
	return []TrustState{TrustNotRequired, TrustPending, TrustGranted, TrustStale, TrustDenied}
}

// Valid reports whether s is a known trust state.
func (s TrustState) Valid() bool {
	for _, known := range AllTrustStates() {
		if s == known {
			return true
		}
	}
	return false
}

// AllowsExecution reports whether Canopy may run this project's configured commands.
//
// Only an explicit grant qualifies. Pending, stale, denied and not-required all mean no. The
// method exists so that every execution path asks the same question in the same words.
func (s TrustState) AllowsExecution() bool {
	return s == TrustGranted
}

func (s TrustState) String() string { return string(s) }

// ProjectSnapshot is the complete, self consistent view of everything Canopy knows.
//
// It is the authoritative read model. A consumer can throw away all of its local state, take a
// fresh snapshot, and be correct again, which is what makes recovery from a dropped or reordered
// event stream possible rather than merely hoped for.
type ProjectSnapshot struct {
	// Sequence is the event sequence number this snapshot reflects. Resuming from
	// Events(Sequence) yields exactly the updates that happened after it, with no gap and no
	// replay.
	Sequence uint64

	TakenAt time.Time

	// RepoRoot is the primary checkout's path.
	RepoRoot string

	ConfigState ConfigState

	// ConfigError explains an invalid configuration, naming the field and the reason.
	ConfigError string

	TrustState TrustState

	Workspaces []WorkspaceSnapshot
}

// Workspace returns the snapshot for a workspace ID.
func (p ProjectSnapshot) Workspace(id string) (WorkspaceSnapshot, bool) {
	for _, w := range p.Workspaces {
		if w.ID == id {
			return w, true
		}
	}
	return WorkspaceSnapshot{}, false
}

// CanExecute reports whether the project is in a state where configured commands may run at all.
//
// It requires both a valid configuration and an explicit trust grant. Callers should use this
// rather than checking the two fields separately, so the answer cannot drift between call sites.
func (p ProjectSnapshot) CanExecute() bool {
	return p.ConfigState == ConfigLoaded && p.TrustState.AllowsExecution()
}
