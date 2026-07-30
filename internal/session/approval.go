package session

import (
	"context"
	"sort"

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

	// installed is when this question joined the queue, counted rather than timed.
	//
	// The pending map is a map, so it has no order, and "the oldest question" is the one thing a
	// person answering several of them needs. A wall clock would do as well until two prompts landed
	// in the same millisecond, which is exactly what a fan out does.
	installed uint64

	// answer carries the reply back to the waiting tool loop. Buffered, so answering never blocks
	// the interface even if the loop has already given up and gone.
	answer chan answer
}

// Waiting is a pending question together with who is asking it.
//
// Its own type rather than Prompt, because a question surfaced on somebody else's screen has to name
// the agent, and Prompt is what the asking conversation already knows. It also carries no channel,
// so nothing holding one of these can answer by writing to it and bypass Answer's bookkeeping.
type Waiting struct {
	SessionID string

	// Agent is what a person calls the thing asking. The session id where no agent record names it,
	// which is honest rather than blank: an unnamed conversation is still identifiable.
	Agent string

	Request  permission.Request
	Decision permission.Decision
}

// Scope is what approving would cover, the same answer Prompt gives about the same question.
func (w Waiting) Scope() permission.Scope { return w.Decision.Scope }

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
	e.promptSeq++
	prompt.installed = e.promptSeq
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
			// This is the only place an approval is ever recorded, and that is the whole of what
			// makes "yes" and "yes, and stop asking" different answers. The loop used to grant the
			// decision's scope on every approval as well, which quietly turned the first into the
			// second: a one-time yes covered every later call of the same shape and the user was
			// never asked again. It does not any more.
			//
			// grantsFor creates the set on first use, so there is nothing to guard against: an
			// earlier version checked whether the map existed and silently skipped the grant on the
			// first approval of a session, which is every approval that matters.
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

// PendingAll returns every question waiting on a person, oldest first, whoever is asking.
//
// The accessor D-47 needs. A prompt raised by any agent may surface on whatever screen somebody is
// on, because walking to a subagent's screen to discover it was stuck is the attention failure that
// decision exists to name, and a screen cannot show what it cannot ask for.
//
// Oldest first, always. Anybody rendering one of these shows the front of the queue and a count of
// the rest, and a queue that reordered itself between two frames would move the answer out from
// under the key somebody was already pressing.
func (e *Engine) PendingAll() []Waiting {
	e.mu.Lock()
	defer e.mu.Unlock()

	queue := make([]*Prompt, 0, len(e.pending))
	for _, prompt := range e.pending {
		queue = append(queue, prompt)
	}
	sort.Slice(queue, func(i, j int) bool { return queue[i].installed < queue[j].installed })

	out := make([]Waiting, 0, len(queue))
	for _, prompt := range queue {
		out = append(out, Waiting{
			SessionID: prompt.SessionID,
			Agent:     e.agentNameLocked(prompt.SessionID),
			Request:   prompt.Request,
			Decision:  prompt.Decision,
		})
	}
	return out
}

// agentNameLocked is what a person calls the agent having this conversation.
//
// Walked in creation order rather than over the map, so the answer cannot change between two calls
// with nothing having happened. The caller already holds the lock, which is why this exists rather
// than AgentFor being reused: AgentFor takes it.
func (e *Engine) agentNameLocked(sessionID string) string {
	for _, name := range e.agentOrder {
		if agent, ok := e.agents[name]; ok && agent.SessionID == sessionID {
			return agent.Name
		}
	}
	// A conversation with no agent record is still one somebody can be asked about, so it is named
	// by the only name it has rather than left blank.
	return sessionID
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
