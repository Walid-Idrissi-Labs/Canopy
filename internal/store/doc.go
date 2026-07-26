// Package store holds the authoritative state and publishes sequenced events.
//
// The event channel notifies the UI. It does not own product truth. One in-memory store is
// authoritative, readers get immutable snapshots, and events carry monotonically increasing
// sequence numbers so a consumer can throw away everything it knows, ask for a fresh snapshot and
// resume from a known point.
//
// Replaceable updates such as the latest health result may be coalesced. Final state transitions
// may not, at any load. Log buffers are bounded and kept separate from state, with explicit rules
// about what can be dropped.
//
// Filled in by P2-08.
package store
