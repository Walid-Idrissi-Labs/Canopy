package core

import "testing"

// A final transition is the last thing anyone hears about a run. Coalescing it away would leave a
// finished suite displaying as running until something unrelated happened to nudge the UI, which
// is a state the dashboard would be asserting without evidence.
func TestFinalEventsAreNeverCoalescable(t *testing.T) {
	for _, kind := range AllEventKinds() {
		e := Event{
			Kind:        kind,
			WorkspaceID: "ws1",
			ServiceName: "web",
			RunID:       "run1",
			BufferID:    "buf1",
			Final:       true,
		}
		if e.Coalescable() {
			t.Errorf("final %q event is coalescable, key %q", kind, e.CoalesceKey())
		}
	}
}

func TestNonFinalEventsAreCoalescable(t *testing.T) {
	// Every known kind should have somewhere to coalesce to when it is not final. An event with
	// no key is delivered every time, which is correct for final transitions and wasteful for
	// everything else.
	for _, kind := range AllEventKinds() {
		e := Event{
			Kind:        kind,
			WorkspaceID: "ws1",
			ServiceName: "web",
			RunID:       "run1",
			BufferID:    "buf1",
		}
		if !e.Coalescable() {
			t.Errorf("non-final %q event has no coalesce key", kind)
		}
	}
}

func TestUnknownEventKindIsNeverCoalesced(t *testing.T) {
	// Being conservative about an unrecognised kind means it gets delivered redundantly rather
	// than dropped silently. Redundant delivery is a performance problem, a silent drop is a
	// correctness one.
	e := Event{Kind: EventKind("something-new"), WorkspaceID: "ws1"}
	if e.Coalescable() {
		t.Error("an unrecognised event kind should not be coalesced")
	}
}

// Coalescing replaces an older undelivered event with a newer one under the same key. If two
// different subjects shared a key, an update about one would silently discard the update about
// the other.
func TestCoalesceKeysSeparateDifferentSubjects(t *testing.T) {
	tests := []struct {
		name string
		a, b Event
	}{
		{
			"health of different services in one workspace",
			Event{Kind: EventServiceHealthUpdated, WorkspaceID: "ws1", ServiceName: "web"},
			Event{Kind: EventServiceHealthUpdated, WorkspaceID: "ws1", ServiceName: "api"},
		},
		{
			"health of the same service in different workspaces",
			Event{Kind: EventServiceHealthUpdated, WorkspaceID: "ws1", ServiceName: "web"},
			Event{Kind: EventServiceHealthUpdated, WorkspaceID: "ws2", ServiceName: "web"},
		},
		{
			"revisions of different workspaces",
			Event{Kind: EventRevisionChanged, WorkspaceID: "ws1"},
			Event{Kind: EventRevisionChanged, WorkspaceID: "ws2"},
		},
		{
			"different runs",
			Event{Kind: EventTestRunUpdated, WorkspaceID: "ws1", RunID: "run1"},
			Event{Kind: EventTestRunUpdated, WorkspaceID: "ws1", RunID: "run2"},
		},
		{
			"different log buffers",
			Event{Kind: EventLogAppended, BufferID: "buf1"},
			Event{Kind: EventLogAppended, BufferID: "buf2"},
		},
		{
			"different kinds about the same workspace",
			Event{Kind: EventRevisionChanged, WorkspaceID: "ws1"},
			Event{Kind: EventWorkspaceUpdated, WorkspaceID: "ws1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.a.CoalesceKey() == tc.b.CoalesceKey() {
				t.Errorf("both events share coalesce key %q, so one would discard the other",
					tc.a.CoalesceKey())
			}
		})
	}
}

func TestCoalesceKeysMatchForTheSameSubject(t *testing.T) {
	a := Event{Sequence: 1, Kind: EventServiceHealthUpdated, WorkspaceID: "ws1", ServiceName: "web"}
	b := Event{Sequence: 9, Kind: EventServiceHealthUpdated, WorkspaceID: "ws1", ServiceName: "web"}
	if a.CoalesceKey() != b.CoalesceKey() {
		t.Errorf("two updates about the same service should coalesce: %q vs %q",
			a.CoalesceKey(), b.CoalesceKey())
	}
}
