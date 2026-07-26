package core

import (
	"fmt"
	"time"
)

// EventKind names what changed.
//
// Events are notifications, not truth. They say "something about this workspace moved", and the
// consumer re-reads the snapshot to find out what it is now. Keeping the payload thin is
// deliberate: if an event carried its own copy of the state, that copy could disagree with the
// snapshot, and then two sources would each claim to be authoritative. There would be no way to
// tell which one was lying.
type EventKind string

const (
	// EventWorkspacesChanged means the set of workspaces changed, because one was discovered or
	// removed outside Canopy.
	EventWorkspacesChanged EventKind = "workspaces-changed"
	// EventWorkspaceUpdated means a workspace's git details changed.
	EventWorkspaceUpdated EventKind = "workspace-updated"
	// EventRevisionChanged means a worktree's content changed, which is what turns green results
	// stale.
	EventRevisionChanged EventKind = "revision-changed"
	// EventTestRunUpdated means a test run started, progressed, or finished.
	EventTestRunUpdated EventKind = "test-run-updated"
	// EventServiceHealthUpdated means a service was probed.
	EventServiceHealthUpdated EventKind = "service-health-updated"
	// EventLogAppended means new lines are available in a log buffer.
	EventLogAppended EventKind = "log-appended"
	// EventConfigChanged means the configuration was loaded, reloaded, or failed validation.
	EventConfigChanged EventKind = "config-changed"
	// EventTrustChanged means the trust decision for this project changed.
	EventTrustChanged EventKind = "trust-changed"
	// EventSessionsChanged means a session was created or removed.
	EventSessionsChanged EventKind = "sessions-changed"
	// EventSessionUpdated means something about a session other than its in flight turn changed,
	// such as its title or which credential it uses.
	EventSessionUpdated EventKind = "session-updated"
	// EventTurnUpdated means a turn started, streamed, or finished.
	//
	// This is the highest volume event in the system by a wide margin: one per token, times
	// however many agents are running. It is also the case the coalescing rules were designed for,
	// and the reason they hold is that events carry no payload. A reader who sees one notification
	// where three were sent takes a snapshot and finds every token that arrived, because the turn's
	// text grows in the snapshot rather than travelling in the event.
	EventTurnUpdated EventKind = "turn-updated"
)

// AllEventKinds returns every valid event kind.
func AllEventKinds() []EventKind {
	return []EventKind{
		EventWorkspacesChanged,
		EventWorkspaceUpdated,
		EventRevisionChanged,
		EventTestRunUpdated,
		EventServiceHealthUpdated,
		EventLogAppended,
		EventConfigChanged,
		EventTrustChanged,
		EventSessionsChanged,
		EventSessionUpdated,
		EventTurnUpdated,
	}
}

// Valid reports whether k is a known event kind.
func (k EventKind) Valid() bool {
	for _, known := range AllEventKinds() {
		if k == known {
			return true
		}
	}
	return false
}

func (k EventKind) String() string { return string(k) }

// Event is one sequenced notification that something changed.
type Event struct {
	// Sequence is monotonically increasing across the whole store, never reused and never
	// reordered. A consumer that has seen sequence N can ask for everything after N and be sure
	// it missed nothing in between.
	Sequence uint64

	At   time.Time
	Kind EventKind

	// The subject of the event. Which fields are set depends on Kind, and empty means not
	// applicable.
	WorkspaceID string
	TestName    string
	ServiceName string
	RunID       string
	BufferID    string
	SessionID   string
	TurnID      string

	// Final marks a transition that will not be followed by another for this subject, such as a
	// test run reaching passing, failing, cancelled or error.
	//
	// A final event may never be coalesced or dropped, at any load. If it were, the last thing a
	// user ever heard about a run would be "running", and a suite that finished ten minutes ago
	// would still be spinning on screen. Dropping intermediate updates loses nothing, because the
	// snapshot is authoritative. Dropping the last one loses the answer.
	Final bool
}

// CoalesceKey returns the key under which this event may replace an earlier undelivered event, or
// an empty string if it must never be coalesced.
//
// Coalescing exists so that a burst of updates about the same thing does not queue up behind a
// slow consumer. It is safe precisely because events carry no payload: replacing three "health
// changed" notifications with one loses nothing, since the reader looks at the snapshot either
// way and the snapshot already holds the newest value.
//
// The one thing coalescing must never touch is a final transition, which is why Final short
// circuits this method rather than being one case among many.
func (e Event) CoalesceKey() string {
	if e.Final {
		return ""
	}
	switch e.Kind {
	case EventWorkspacesChanged:
		return "workspaces"
	case EventWorkspaceUpdated:
		return fmt.Sprintf("workspace|%s", e.WorkspaceID)
	case EventRevisionChanged:
		return fmt.Sprintf("revision|%s", e.WorkspaceID)
	case EventTestRunUpdated:
		return fmt.Sprintf("test-run|%s|%s", e.WorkspaceID, e.RunID)
	case EventServiceHealthUpdated:
		return fmt.Sprintf("service-health|%s|%s", e.WorkspaceID, e.ServiceName)
	case EventLogAppended:
		return fmt.Sprintf("log|%s", e.BufferID)
	case EventConfigChanged:
		return "config"
	case EventTrustChanged:
		return "trust"
	case EventSessionsChanged:
		return "sessions"
	case EventSessionUpdated:
		return fmt.Sprintf("session|%s", e.SessionID)
	case EventTurnUpdated:
		// Keyed by turn rather than by session, so two agents streaming at once never collapse into
		// each other. Coalescing a burst of tokens from one turn loses nothing; coalescing across
		// turns would lose the fact that a different turn moved at all.
		return fmt.Sprintf("turn|%s|%s", e.SessionID, e.TurnID)
	default:
		// An unrecognised kind is never coalesced. Being conservative here means an unknown
		// event is delivered redundantly rather than silently discarded.
		return ""
	}
}

// Coalescable reports whether this event may be replaced by a later one with the same key.
func (e Event) Coalescable() bool {
	return e.CoalesceKey() != ""
}
