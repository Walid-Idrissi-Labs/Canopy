// Package store holds the machinery every authoritative store shares.
//
// The event broker lives here rather than inside any one store because there is now more than one:
// the fake from P1 and the session engine from A3, with persistence to follow. Coalescing is subtle
// enough that two copies would drift, and the way they would drift is a dropped final transition
// under load, which is the one failure the whole design exists to prevent.
package store

import (
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The guarantees, which every store built on this inherits:
//
//   - sequence numbers are monotonic and never reused
//   - a subscriber may miss intermediate updates, never a final transition
//   - a slow subscriber never holds up a fast one

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

// Broker assigns sequence numbers and fans events out to subscribers.
//
// Safe for concurrent use, and deliberately independent of whatever state the store around it
// holds. It never calls back into its owner, so a store may publish while holding its own lock
// without any risk of inverting the two.
type Broker struct {
	mu     sync.Mutex
	seq    uint64
	subs   map[*subscriber]struct{}
	closed bool
	now    func() time.Time
}

// NewBroker builds a broker using the wall clock.
func NewBroker() *Broker {
	return &Broker{subs: map[*subscriber]struct{}{}, now: time.Now}
}

// SetClock replaces the clock. Only useful in tests.
func (b *Broker) SetClock(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = now
}

// Subscribe returns a channel of everything that happens from now on.
//
// The intended call order is to take a snapshot first and subscribe from its sequence, so nothing
// can slip through the gap between reading and subscribing.
func (b *Broker) Subscribe(afterSequence uint64) <-chan core.Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub := &subscriber{
		byKey: map[string]int{},
		out:   make(chan core.Event, subscriberBuffer),
		wake:  make(chan struct{}, 1),
		quit:  make(chan struct{}),
	}

	if b.closed {
		close(sub.out)
		return sub.out
	}

	b.subs[sub] = struct{}{}
	go sub.pump()

	// A caller resuming from an older sequence has no history to replay from here, because nothing
	// keeps one yet. Said out loud rather than silently ignored: honouring this properly needs a
	// bounded history, and until there is one, a consumer that falls far behind recovers by taking a
	// fresh snapshot rather than by replaying.
	_ = afterSequence

	return sub.out
}

// Publish assigns the next sequence number and hands the event to every subscriber.
//
// Returns the sequence it assigned, which is zero if the broker is closed. A store that keeps its
// own copy of the sequence for snapshots uses this return rather than a second counter, since two
// counters is two chances to disagree.
func (b *Broker) Publish(ev core.Event) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return 0
	}
	b.seq++
	ev.Sequence = b.seq
	if ev.At.IsZero() {
		ev.At = b.now()
	}

	// Queued under the same lock that assigned the sequence, and this is load bearing rather than
	// convenient. Assigning the number and then queueing outside the lock lets two publishers
	// interleave: one takes 6, the other takes 7, and whichever the scheduler runs first is the one
	// the subscriber sees first. Sequence numbers that only arrive in order when a single goroutine
	// is publishing are not the guarantee core.Event describes, and with several agents streaming at
	// once a single goroutine is exactly what is not happening.
	//
	// Cheap, because enqueue never blocks. It appends to the subscriber's own queue and pokes its
	// pump without waiting for anybody to read, so a slow consumer still cannot hold a publisher up.
	for sub := range b.subs {
		sub.enqueue(ev)
	}
	return b.seq
}

// Sequence returns the current event sequence number.
func (b *Broker) Sequence() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// Now reads the broker's clock, so a store and its events agree on the time.
func (b *Broker) Now() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.now()
}

// Close shuts the broker down and closes every subscriber channel.
func (b *Broker) Close() {
	b.mu.Lock()
	subs := make([]*subscriber, 0, len(b.subs))
	for sub := range b.subs {
		subs = append(subs, sub)
	}
	b.subs = map[*subscriber]struct{}{}
	b.closed = true
	b.mu.Unlock()

	for _, sub := range subs {
		sub.close()
	}
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
// Every send races the quit channel. Without that, closing the broker while a consumer had stopped
// reading would leave this goroutine blocked forever on a full buffer, and the output channel would
// never close, so the consumer's own shutdown would hang waiting for it.
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
