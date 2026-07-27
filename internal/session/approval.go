package session

import (
	"context"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// Asking a person, from a goroutine that is not the interface.
//
// The tool loop runs in the background and blocks when a call needs approval. The interface is an
// event loop that must never block. Those two facts are what this file reconciles: the loop parks a
// question in the snapshot and waits on a channel, the interface notices the question the same way
// it notices everything else, and the answer comes back through the channel.
//
// The alternative, having the loop call into the interface, would mean the interface's own update
// loop running inside a provider goroutine, which is the shape of every deadlock a TUI ever has.

// Prompt is a tool call waiting for a person to say yes or no.
type Prompt struct {
	SessionID string
	Request   permission.Request
	Decision  permission.Decision

	// answer carries the reply back to the waiting tool loop. Buffered, so answering never blocks
	// the interface even if the loop has already given up and gone.
	answer chan answer
}

// Scope is what approving would cover, in words the user will recognise.
func (p Prompt) Scope() permission.Scope { return p.Decision.Scope }

// answer is what a person decided, and how widely.
type answer struct {
	approved bool
	// remember widens the approval to cover later calls in this session. Without it the approval
	// covers this call and nothing else, which is the right default and the wrong thing to force on
	// somebody watching an agent make thirty edits.
	remember bool
}

// Approve implements agent.Approver by asking whoever is watching.
//
// Blocks until answered or the turn is cancelled. That is correct: the tool has not run and the
// model is waiting for its result either way, so there is nothing useful to do in the meantime.
func (e *Engine) Approve(
	ctx context.Context, req permission.Request, decision permission.Decision,
) bool {
	prompt := &Prompt{
		SessionID: req.SessionID,
		Request:   req,
		Decision:  decision,
		answer:    make(chan answer, 1),
	}

	e.mu.Lock()
	if e.pending == nil {
		e.pending = map[string]*Prompt{}
	}
	// One question at a time per session, which is guaranteed by there being one turn at a time.
	// Recorded rather than assumed: a second prompt arriving would replace the first and the first
	// would wait forever.
	if existing, waiting := e.pending[req.SessionID]; waiting {
		e.mu.Unlock()
		// Refuse rather than replace. Somebody is already being asked something, and silently
		// dropping their question to ask a different one is worse than declining the second.
		_ = existing
		return false
	}
	e.pending[req.SessionID] = prompt
	e.mu.Unlock()

	e.events.Publish(core.Event{Kind: core.EventTurnUpdated, SessionID: req.SessionID})

	defer func() {
		e.mu.Lock()
		delete(e.pending, req.SessionID)
		e.mu.Unlock()
		e.events.Publish(core.Event{Kind: core.EventTurnUpdated, SessionID: req.SessionID})
	}()

	select {
	case reply := <-prompt.answer:
		if reply.approved && reply.remember {
			// Widening happens here rather than in the loop, because the loop grants the decision's
			// scope on every approval and this is the difference between "yes" and "yes, and stop
			// asking". grantsFor creates the set on first use, so there is nothing to guard against:
			// an earlier version checked whether the map existed and silently skipped the grant on
			// the first approval of a session, which is every approval that matters.
			e.grantsFor(req.SessionID).Grant(decision.Scope)
		}
		return reply.approved

	case <-ctx.Done():
		// The turn was cancelled while somebody was being asked. Refusing is the only safe reading:
		// they never answered, and a cancelled turn should not leave a command running behind it.
		return false
	}
}

// Pending returns the question a session is waiting on, if any.
func (e *Engine) Pending(sessionID string) (Prompt, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	prompt, ok := e.pending[sessionID]
	if !ok {
		return Prompt{}, false
	}
	// A copy without the channel, so a caller cannot answer by writing to it directly and bypass
	// the bookkeeping in Answer.
	return Prompt{
		SessionID: prompt.SessionID,
		Request:   prompt.Request,
		Decision:  prompt.Decision,
	}, true
}

// Answer replies to a pending question.
//
// Reports whether there was anything to answer, so an interface that sends a stale answer, because
// the turn was cancelled between drawing the prompt and the keystroke, can tell rather than
// silently doing nothing.
func (e *Engine) Answer(sessionID string, approved, remember bool) bool {
	e.mu.Lock()
	prompt, ok := e.pending[sessionID]
	e.mu.Unlock()

	if !ok {
		return false
	}
	select {
	case prompt.answer <- answer{approved: approved, remember: remember}:
		return true
	default:
		// Already answered. Buffered by one, so this only happens on a double keystroke, and
		// ignoring the second is right: the first answer is the one they meant.
		return false
	}
}

// AwaitingApproval reports whether a session is waiting on a person.
//
// The question the agents view is really asking, and the reason AgentAwaitingPermission is its own
// state rather than a flavour of working: an agent parked here looks busy and is not, and several of
// them parked here is a queue the user cannot see unless it is named.
func (e *Engine) AwaitingApproval(sessionID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, waiting := e.pending[sessionID]
	return waiting
}
