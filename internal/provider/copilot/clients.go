package copilot

import (
	"errors"
	"sync"
)

// Clients is every conversation this program has open with the vendor, and the only way to start one.
//
// Why a pool rather than a constructor anybody may call. A client on this route is a resident CLI
// process on the machine and a session GitHub believes is open, and neither of them ends on its own.
// The first build of this package exported New, and of the four paths that called it only one, the
// main conversation, was ever closed: every aside, every compaction and `canopy ask` built a client,
// used it for one turn and dropped it, leaving a process and a session behind for the life of the
// program. A comment asking callers to remember is what produced that, so remembering is not what
// this fixes. New is unexported and every client comes from here, which means there is no way to
// obtain one that this type is not already holding, and no new call path can be written that leaks.
//
// Two kinds come out of it and the difference is who ends them. A conversation's client outlives its
// turns, because the vendor's session is the conversation, and it ends when the pool does or when it
// is evicted. A one-shot ends itself when its single turn does, which is what an aside, a compaction
// and `canopy ask` each want: one question, one answer, nothing to remember.
//
// Considered and rejected: tracking one-shots and closing them only at shutdown. That fixes the leak
// and keeps the unbounded growth, since a long-running Canopy asks a great many side questions, and
// each one would sit on a process until the program ended.
type Clients struct {
	// open is how a session with the vendor is started. A field for the reason Opener is one: it is
	// what lets everything here be driven without a subscription, a network or a CLI binary.
	open Opener

	// limit is how many conversations may hold a live session at once. See heldLimit.
	limit int

	mu     sync.Mutex
	closed bool

	// held is the conversations whose sessions outlive a turn, by whatever key the caller chose.
	held map[string]*entry

	// loose is the one-shots that have been handed out and have not ended yet.
	//
	// Held so that one abandoned by a caller who never streamed, or who never closed the stream it
	// was given, still ends when the program does rather than never. It is not a second cache: a
	// one-shot removes itself from here the moment its turn is over.
	loose map[*Client]struct{}

	// asked counts requests, so eviction can pick the least recently asked-for conversation without
	// consulting a clock. A counter rather than a timestamp because a test that has to sleep for two
	// entries to differ is a slow test that is occasionally wrong.
	asked uint64
}

// entry is one held client and when it was last asked for.
type entry struct {
	client *Client
	asked  uint64
}

// heldLimit is how many conversations may hold a live Copilot session at once.
//
// The number is about the machine rather than about the vendor: each one of these is a resident CLI
// process, and a runtime that lets somebody accumulate them one abandoned conversation at a time is
// the unbounded cache this replaced. Eight is chosen as one more than the largest ordinary
// arrangement, which is a conversation that dispatched a full fleet of six agents and is still being
// talked to, so nothing anybody does deliberately evicts anything.
//
// Why a bound rather than an idle timer, which is the obvious alternative and is worse here.
// Evicting costs the vendor's own copy of the conversation, which cannot be handed back: the next
// turn re-seeds the session from Canopy's transcript, which works and is weaker, because roles
// collapse into a labelled record. So the axis that matters is how many are held at once, which is
// what the machine actually pays for, and not how long since somebody last typed, which would end
// the conversation of the person who went to lunch and keep eight that nobody will return to.
const heldLimit = 8

// Option adjusts a pool.
type Option func(*Clients)

// WithOpener replaces how a conversation with the vendor is started.
func WithOpener(open Opener) Option {
	return func(c *Clients) { c.open = open }
}

// NewClients builds a pool of Copilot conversations.
//
// Whoever builds one owns closing it. In this build that is internal/session's Vendors for the
// interface and `canopy ask` for the one-shot at a terminal, and both of them close it.
func NewClients(options ...Option) *Clients {
	clients := &Clients{
		open:  Open,
		limit: heldLimit,
		held:  map[string]*entry{},
		loose: map[*Client]struct{}{},
	}
	for _, option := range options {
		option(clients)
	}
	return clients
}

// ErrShuttingDown means the pool has been closed and no new session will be started on it.
//
// Its own error rather than a string, so a caller that wants to tell "Canopy is going away" apart
// from "the vendor refused" can. Nothing in this build needs to yet; the alternative was a caller
// matching on a sentence.
var ErrShuttingDown = errors.New(
	"this Canopy is shutting down, so no new conversation is being started")

// For returns the client holding a conversation, starting one if this is its first turn.
//
// Asking twice with the same key gives back the same client, which is the whole reason this exists:
// the vendor's session is the conversation, it has no API for being handed a history, and a client
// rebuilt per turn would open a session that had never heard the previous message.
//
// The key is the caller's business. internal/session keys on the conversation, the credential and
// the account together, so that a credential signed in again as somebody else starts a new
// conversation with the vendor rather than inheriting the previous person's session.
func (c *Clients) For(key, name string, conversation Conversation) (*Client, error) {
	if key == "" {
		// An empty key would make every caller with nothing to key on share one session, which is
		// the aside-shares-a-conversation bug. Once is what those callers want and this says so.
		return nil, errors.New("a held Copilot conversation needs a key: use Once for a single turn")
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrShuttingDown
	}
	c.asked++
	if existing, ok := c.held[key]; ok {
		existing.asked = c.asked
		c.mu.Unlock()
		return existing.client, nil
	}

	client := c.build(name, conversation, false)
	c.held[key] = &entry{client: client, asked: c.asked}
	evicted := c.evict(key)
	c.mu.Unlock()

	// Outside the lock, deliberately: closing a client deregisters it from this pool, so doing it
	// under the lock would deadlock on the next line of its own Close.
	for _, victim := range evicted {
		_ = victim.Close()
	}
	return client, nil
}

// Once returns a client for a single turn, which ends the session when that turn does.
//
// An aside, a compaction and `canopy ask` are all of this shape: one question about a transcript,
// asked once, by something that has no conversation of its own to keep. Giving them a held client
// would put a summarisation request into the middle of somebody's session, and giving them one that
// nothing closes is what leaked.
func (c *Clients) Once(name string, conversation Conversation) (*Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, ErrShuttingDown
	}
	client := c.build(name, conversation, true)
	c.loose[client] = struct{}{}
	return client, nil
}

// build makes a client bound to this pool. Called with the lock held.
func (c *Clients) build(name string, conversation Conversation, once bool) *Client {
	return &Client{
		name:         name,
		open:         c.open,
		conversation: conversation,
		pool:         c,
		once:         once,
	}
}

// evict brings the number of held conversations back within the limit, and reports the casualties.
//
// Called with the lock held, and it closes nothing: the caller does that after unlocking, because
// Close comes back here to deregister.
//
// A turn in flight is never evicted. Taking a session away mid-reply to satisfy a bound would lose
// somebody's answer to a housekeeping rule, so if every held conversation is busy the limit is
// exceeded rather than enforced, and the next call tries again.
//
// Nor is the conversation that was just asked for, whatever its age. It is the only one in the map
// known to be about to be used, and it is also the only one with no turn on it yet, so a rule that
// looked at busyness alone would hand a caller a client and close it in the same call.
func (c *Clients) evict(keep string) []*Client {
	var evicted []*Client
	for len(c.held) > c.limit {
		key, victim := "", (*entry)(nil)
		for at, held := range c.held {
			if at == keep || held.client.inFlight() {
				continue
			}
			if victim == nil || held.asked < victim.asked {
				key, victim = at, held
			}
		}
		if victim == nil {
			return evicted
		}
		delete(c.held, key)
		evicted = append(evicted, victim.client)
	}
	return evicted
}

// forget drops a client that has closed itself, or been closed by whoever asked for it.
//
// Called from Client.Close with no client lock held, which is what keeps the two locks in one order.
func (c *Clients) forget(client *Client) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.loose, client)
	// A scan rather than a key kept on the client, because the map is bounded by heldLimit and a
	// second copy of the key is a second thing that can be wrong.
	for key, held := range c.held {
		if held.client == client {
			delete(c.held, key)
			return
		}
	}
}

// Live is how many clients this pool is holding open.
//
// Exported because it is the only honest way for anything above to assert that a conversation
// really ended, and a test that reads a private map is a test of a map rather than of a promise.
func (c *Clients) Live() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.held) + len(c.loose)
}

// Close ends every conversation this pool is holding, and refuses to start any more.
//
// Safe to call twice, and safe on a pool that never opened anything: both are what a shutdown path
// does by accident, and neither is worth an error. Failures are joined rather than returned one at a
// time, because refusing to close the rest because one would not close is the worst answer available
// at the moment a program is ending.
func (c *Clients) Close() error {
	c.mu.Lock()
	c.closed = true
	holding := make([]*Client, 0, len(c.held)+len(c.loose))
	for _, held := range c.held {
		holding = append(holding, held.client)
	}
	for client := range c.loose {
		holding = append(holding, client)
	}
	c.held = map[string]*entry{}
	c.loose = map[*Client]struct{}{}
	c.mu.Unlock()

	var failures error
	for _, client := range holding {
		failures = errors.Join(failures, client.Close())
	}
	return failures
}
