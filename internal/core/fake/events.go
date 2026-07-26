package fake

import (
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Event delivery is the shared broker's, not a stub of it.
//
// The coalescing rules are part of the contract the UI is written against, so a fake that delivered
// every event in order through an unbounded buffer would let the UI be developed against behaviour
// no real store can provide, and the gap would only show up under load. Sharing the implementation
// rather than reimplementing it is how the two stay identical.

// Events implements core.SnapshotStore.
func (s *Store) Events(afterSequence uint64) <-chan core.Event {
	return s.events.Subscribe(afterSequence)
}

// publishLocked assigns the next sequence number and hands the event to every subscriber. The
// caller must hold s.mu.
//
// Safe to call under the lock: the broker has its own, and it never calls back into the store.
func (s *Store) publishLocked(ev core.Event) {
	s.events.Publish(ev)
}

// Close shuts the store down and closes every subscriber channel.
func (s *Store) Close() { s.events.Close() }

// Sequence returns the current event sequence number.
func (s *Store) Sequence() uint64 { return s.events.Sequence() }
