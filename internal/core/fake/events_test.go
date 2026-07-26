package fake

import (
	"context"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// drain reads until the channel goes quiet for the given idle period.
func drain(t *testing.T, events <-chan core.Event, idle time.Duration) []core.Event {
	t.Helper()
	var got []core.Event
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-time.After(idle):
			return got
		}
	}
}

// A consumer that falls behind should see fewer notifications, not a growing backlog. This is the
// behaviour that keeps a slow terminal from turning into unbounded memory, and it is safe only
// because events carry no state of their own.
func TestBurstsOfCoalescableEventsCollapse(t *testing.T) {
	s := New()
	defer s.Close()

	events := s.Events(s.Snapshot().Sequence)

	const burst = 500
	for i := 0; i < burst; i++ {
		if err := s.Touch("ws-feat-login"); err != nil {
			t.Fatalf("Touch: %v", err)
		}
	}

	got := drain(t, events, 200*time.Millisecond)
	if len(got) == 0 {
		t.Fatal("no events arrived at all")
	}
	if len(got) >= burst {
		t.Errorf("got %d events from a burst of %d, they should have coalesced", len(got), burst)
	}

	// Coalescing must not cost correctness. The store still holds the newest revision, which is
	// the point: the reader missed intermediate notifications, not the answer.
	w, ok := s.Snapshot().Workspace("ws-feat-login")
	if !ok {
		t.Fatal("workspace disappeared")
	}
	if core.RollUp(w).Tests != core.TestStale {
		t.Error("the store should still reflect the edits despite dropped notifications")
	}
}

// The one thing coalescing may never touch. If a final transition were dropped, the last thing a
// user ever heard about a run would be that it was running, and a suite that finished ten minutes
// ago would still be spinning on screen.
func TestFinalEventsSurviveABurst(t *testing.T) {
	s := New()
	defer s.Close()

	events := s.Events(s.Snapshot().Sequence)

	const runs = 100
	for i := 0; i < runs; i++ {
		// Interleave a coalescable event with every final one, so the finals have something to be
		// crowded out by.
		if err := s.Touch("ws-feat-login"); err != nil {
			t.Fatalf("Touch: %v", err)
		}
		if _, err := s.Start(context.Background(), "ws-feat-login", "unit"); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}

	got := drain(t, events, 500*time.Millisecond)

	finals := 0
	for _, ev := range got {
		if ev.Final {
			finals++
		}
	}
	if finals != runs {
		t.Errorf("got %d final events, want %d, none may ever be dropped", finals, runs)
	}
}

func TestSequenceNeverGoesBackwards(t *testing.T) {
	s := New()
	defer s.Close()

	events := s.Events(s.Snapshot().Sequence)

	for i := 0; i < 50; i++ {
		if err := s.Touch("ws-feat-login"); err != nil {
			t.Fatalf("Touch: %v", err)
		}
		if err := s.SetServiceHealth("ws-feat-login", "web", core.ServiceHealth{
			State:        core.ServiceHealthy,
			ProcessAlive: core.ObservationTrue,
			Ready:        core.ObservationTrue,
			Probe:        core.ProbeHTTP,
			InstanceID:   "ws-feat-login-web-1",
		}); err != nil {
			t.Fatalf("SetServiceHealth: %v", err)
		}
	}

	var last uint64
	for _, ev := range drain(t, events, 300*time.Millisecond) {
		if ev.Sequence <= last {
			t.Fatalf("sequence went backwards or repeated: %d after %d", ev.Sequence, last)
		}
		last = ev.Sequence
	}
}

// Two subscribers are independent. A reader that stops reading must not stall one that has not.
func TestSlowSubscriberDoesNotBlockAFastOne(t *testing.T) {
	s := New()
	defer s.Close()

	slow := s.Events(0)
	fast := s.Events(0)

	for i := 0; i < 300; i++ {
		if err := s.Touch("ws-feat-login"); err != nil {
			t.Fatalf("Touch: %v", err)
		}
	}

	// slow is deliberately never read from until the end.
	got := drain(t, fast, 300*time.Millisecond)
	if len(got) == 0 {
		t.Error("the fast subscriber received nothing while the slow one sat idle")
	}

	_ = drain(t, slow, 100*time.Millisecond)
}

// Every event about a subject the caller cares about should identify that subject, or a consumer
// cannot tell what to re-read.
func TestEventsIdentifyTheirSubject(t *testing.T) {
	s := New()
	defer s.Close()

	events := s.Events(s.Snapshot().Sequence)

	if err := s.Touch("ws-fix-cache"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	ev := waitForEvent(t, events, func(e core.Event) bool { return e.Kind == core.EventRevisionChanged })

	if ev.WorkspaceID != "ws-fix-cache" {
		t.Errorf("WorkspaceID = %q, want ws-fix-cache", ev.WorkspaceID)
	}
	if ev.At.IsZero() {
		t.Error("events should carry a timestamp")
	}
	if !ev.Kind.Valid() {
		t.Errorf("event kind %q is outside the vocabulary", ev.Kind)
	}
}
