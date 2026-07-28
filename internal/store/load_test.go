package store

// Delivery under load, which is where a promise about events is either true or decorative.
//
// Two properties from opposite sides of the same mechanism: nothing that matters is dropped when
// publishers outrun the reader, and nothing arrives out of the order it was numbered in when several
// publish at once. Both are written against several goroutines on purpose. The single publisher case
// was already covered and was already passing, which is exactly why it caught neither.

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func TestNoFinalTransitionIsDroppedWhenPublishersOutrunTheSubscriber(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	events := b.Subscribe(0)

	// Nothing is read until every publisher has finished, so the subscriber's buffer is long since
	// full and everything after that is queued rather than delivered. That is the state a terminal
	// is in while several agents stream at it faster than it can redraw.
	const publishers, turns = 8, 50
	var wg sync.WaitGroup
	for p := range publishers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session := fmt.Sprintf("s%d", p)
			for turn := range turns {
				id := fmt.Sprintf("t%d", turn)
				// Tokens and then the answer, which is the shape of a real turn and the reason the
				// finals have something to be crowded out by.
				for range 5 {
					b.Publish(core.Event{
						Kind: core.EventTurnUpdated, SessionID: session, TurnID: id,
					})
				}
				b.Publish(core.Event{
					Kind: core.EventTurnUpdated, SessionID: session, TurnID: id, Final: true,
				})
			}
		}()
	}
	wg.Wait()

	finals := map[string]int{}
	for _, ev := range drain(t, events, 500*time.Millisecond) {
		if ev.Final {
			finals[ev.SessionID+"|"+ev.TurnID]++
		}
	}

	if len(finals) != publishers*turns {
		t.Errorf("%d turns reported how they ended, want %d: the last word about a turn is the one "+
			"thing coalescing may never take", len(finals), publishers*turns)
	}
	for turn, count := range finals {
		if count != 1 {
			t.Errorf("turn %s ended %d times", turn, count)
		}
	}
}

func TestSequencesArriveInTheOrderTheyWereAssigned(t *testing.T) {
	b := NewBroker()
	defer b.Close()

	events := b.Subscribe(0)

	// Every event is final, so nothing coalesces and the order is the only thing under test.
	const publishers, each = 8, 40
	var wg sync.WaitGroup
	for p := range publishers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				b.Publish(core.Event{
					Kind:      core.EventTurnUpdated,
					SessionID: fmt.Sprintf("s%d", p),
					TurnID:    fmt.Sprintf("t%d", i),
					Final:     true,
				})
			}
		}()
	}
	wg.Wait()

	got := drain(t, events, 500*time.Millisecond)
	if len(got) != publishers*each {
		t.Fatalf("%d events arrived, want %d", len(got), publishers*each)
	}

	var last uint64
	for _, ev := range got {
		if ev.Sequence <= last {
			t.Fatalf("sequence %d arrived after %d: a consumer told that sequence numbers are never "+
				"reordered cannot be handed an order that only holds while one goroutine publishes",
				ev.Sequence, last)
		}
		last = ev.Sequence
	}
}
