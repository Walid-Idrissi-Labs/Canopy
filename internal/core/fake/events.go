package fake

import (
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Event delivery is implemented properly here rather than stubbed, because the coalescing rules
// are part of the contract the UI is written against. A fake that delivered every event in order
// through an unbounded buffer would let the UI be developed against behaviour the real store
// cannot provide, and the gap would only show up under load, which is the worst time to find it.
//
// The guarantees, which the real store has to match:
//
//   - sequence numbers are monotonic and never reused
//   - a subscriber may miss intermediate updates, never a final transition
//   - a subscriber that asks for events after sequence N receives everything after N

// subscriber is one consumer's queue. Each has its own, so a slow reader cannot hold up a fast one.
type subscriber struct {
	mu     sync.Mutex
	slots  []slot
	byKey  map[string]int
	out    chan core.Event
	wake   chan struct{}
	quit   chan struct{}
	closed bool
	once   sync.Once
}

// slot is a queued event that may have been superseded by a later one.
//
// Coalescing supersedes in place and appends the replacement at the end, rather than overwriting
// the original position. Overwriting would deliver the newer event at the older one's place in the
// queue, which breaks the promise that sequence numbers arrive in order.
type slot struct {
	event      core.Event
	superseded bool
}

const subscriberBuffer = 64

// Events implements core.SnapshotStore.
//
// The intended call order is to take a Snapshot first and subscribe from its Sequence, so nothing
// can slip through the gap between reading and subscribing.
func (s *Store) Events(afterSequence uint64) <-chan core.Event {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub := &subscriber{
		byKey: map[string]int{},
		out:   make(chan core.Event, subscriberBuffer),
		wake:  make(chan struct{}, 1),
		quit:  make(chan struct{}),
	}

	if s.closed {
		close(sub.out)
		return sub.out
	}

	s.subs[sub] = struct{}{}
	go sub.pump()

	// A caller resuming from an older sequence has no history to replay from here, because this
	// store keeps none. Said out loud rather than silently ignored: the real store needs a bounded
	// history to honour this properly, and until it does, a consumer that falls far behind
	// recovers by taking a fresh snapshot rather than by replaying.
	_ = afterSequence

	return sub.out
}

// publishLocked assigns the next sequence number and hands the event to every subscriber. The
// caller must hold s.mu.
func (s *Store) publishLocked(ev core.Event) {
	if s.closed {
		return
	}
	s.seq++
	ev.Sequence = s.seq
	if ev.At.IsZero() {
		ev.At = s.now()
	}
	for sub := range s.subs {
		sub.enqueue(ev)
	}
}

// Close shuts the store down and closes every subscriber channel.
func (s *Store) Close() {
	s.mu.Lock()
	subs := make([]*subscriber, 0, len(s.subs))
	for sub := range s.subs {
		subs = append(subs, sub)
	}
	s.subs = map[*subscriber]struct{}{}
	s.closed = true
	s.mu.Unlock()

	for _, sub := range subs {
		sub.close()
	}
}

// Sequence returns the current event sequence number.
func (s *Store) Sequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seq
}

func (sub *subscriber) enqueue(ev core.Event) {
	sub.mu.Lock()
	if sub.closed {
		sub.mu.Unlock()
		return
	}

	if key := ev.CoalesceKey(); key != "" {
		if idx, ok := sub.byKey[key]; ok && idx < len(sub.slots) && !sub.slots[idx].superseded {
			sub.slots[idx].superseded = true
		}
		sub.byKey[key] = len(sub.slots)
	}
	sub.slots = append(sub.slots, slot{event: ev})
	sub.mu.Unlock()

	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

// pump drains the queue into the output channel.
//
// Every send races the quit channel. Without that, closing the store while a consumer had stopped
// reading would leave this goroutine blocked forever on a full buffer, and the output channel
// would never close, so the consumer's own shutdown would hang waiting for it.
func (sub *subscriber) pump() {
	defer close(sub.out)
	for {
		for {
			ev, ok := sub.next()
			if !ok {
				break
			}
			select {
			case sub.out <- ev:
			case <-sub.quit:
				return
			}
		}

		select {
		case <-sub.wake:
		case <-sub.quit:
			return
		}
	}
}

// next pops the oldest event that has not been superseded.
func (sub *subscriber) next() (core.Event, bool) {
	sub.mu.Lock()
	defer sub.mu.Unlock()

	for len(sub.slots) > 0 {
		s := sub.slots[0]
		sub.slots = sub.slots[1:]

		// Indices in byKey refer to positions in slots, so they shift when the head is dropped.
		for key, idx := range sub.byKey {
			if idx == 0 {
				delete(sub.byKey, key)
				continue
			}
			sub.byKey[key] = idx - 1
		}

		if s.superseded {
			continue
		}
		return s.event, true
	}
	return core.Event{}, false
}

func (sub *subscriber) close() {
	sub.mu.Lock()
	sub.closed = true
	sub.mu.Unlock()

	sub.once.Do(func() { close(sub.quit) })
}
