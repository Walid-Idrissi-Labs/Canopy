package store

import (
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The behavioural guarantees are exercised end to end through the fake store, which is where they
// are visible as product behaviour. What is tested here is the broker's own surface: the parts a
// second store built on it would get wrong.

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

func TestPublishReturnsTheSequenceItAssigned(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	first := b.Publish(core.Event{Kind: core.EventTrustChanged})
	second := b.Publish(core.Event{Kind: core.EventConfigChanged})

	if first != 1 || second != 2 {
		t.Errorf("sequences = %d, %d, want 1, 2", first, second)
	}
	// A store that kept its own counter alongside this one would have two chances to disagree, so
	// the return value is the only source.
	if b.Sequence() != second {
		t.Errorf("Sequence() = %d, want %d", b.Sequence(), second)
	}
}

func TestTurnEventsFromTwoSessionsBothArrive(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	events := b.Subscribe(0)
	for i := 0; i < 5; i++ {
		b.Publish(core.Event{Kind: core.EventTurnUpdated, SessionID: "s1", TurnID: "t1"})
		b.Publish(core.Event{Kind: core.EventTurnUpdated, SessionID: "s2", TurnID: "t1"})
	}
	b.Publish(core.Event{
		Kind: core.EventTurnUpdated, SessionID: "s1", TurnID: "t1", Final: true,
	})

	got := drain(t, events, 100*time.Millisecond)

	var sessions = map[string]int{}
	var finals int
	for _, ev := range got {
		sessions[ev.SessionID]++
		if ev.Final {
			finals++
		}
	}
	// Two agents streaming at once must not collapse into each other, which is what the per turn
	// coalescing key is for.
	if sessions["s1"] == 0 || sessions["s2"] == 0 {
		t.Errorf("one session's events were swallowed entirely: %v", sessions)
	}
	if finals != 1 {
		t.Errorf("%d final events, want exactly 1: the last word about a turn may never be "+
			"coalesced away", finals)
	}
}

func TestSubscribingAfterCloseYieldsAClosedChannel(t *testing.T) {
	b := NewBroker()
	b.Close()

	events := b.Subscribe(0)
	select {
	case _, ok := <-events:
		if ok {
			t.Error("a broker that is shut down has nothing to deliver")
		}
	case <-time.After(time.Second):
		t.Error("subscribing to a closed broker returned a channel that never closes, so a " +
			"consumer waiting on it would hang forever")
	}
}

func TestPublishAfterCloseIsIgnored(t *testing.T) {
	b := NewBroker()
	b.Close()

	if seq := b.Publish(core.Event{Kind: core.EventTrustChanged}); seq != 0 {
		t.Errorf("a closed broker assigned sequence %d, want 0", seq)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	b := NewBroker()
	events := b.Subscribe(0)

	b.Close()
	b.Close() // A second shutdown must not panic on an already closed channel.

	if _, ok := <-events; ok {
		t.Error("the subscriber channel should be closed")
	}
}

func TestEventsCarryTheBrokersClock(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	at := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	b.SetClock(func() time.Time { return at })

	events := b.Subscribe(0)
	b.Publish(core.Event{Kind: core.EventTrustChanged})

	got := drain(t, events, 100*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("%d events, want 1", len(got))
	}
	// So a store and its events agree on the time. Two clocks would put an event before the state
	// it describes.
	if !got[0].At.Equal(at) {
		t.Errorf("event time = %v, want the broker's clock at %v", got[0].At, at)
	}
}
