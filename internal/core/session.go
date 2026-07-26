package core

import (
	"fmt"
	"time"
)

// A conversation, and the state of the agent having it.
//
// `Message` and `Role` already exist on the provider contract, and this deliberately reuses them
// rather than defining a parallel pair. Building a provider request from a session's history is
// then a copy rather than a translation, and a translation between two shapes that mean the same
// thing is where a tool result quietly loses its error flag.

// TurnState is where a turn is in its life.
//
// A closed vocabulary, like every other state in this package, because "is this finished" gets
// asked from a dozen places and each one inventing its own answer is how a partial reply ends up
// presented as a complete one.
type TurnState string

const (
	// TurnPending means the request has been made and nothing has come back yet.
	TurnPending TurnState = "pending"
	// TurnStreaming means the reply is arriving. Anything read now is partial.
	TurnStreaming TurnState = "streaming"
	// TurnAwaitingTools means the model asked for tools and is waiting for results.
	//
	// Distinct from streaming because nothing is arriving, and distinct from complete because the
	// turn is not over. A turn parked here is waiting on Canopy, not on the provider.
	TurnAwaitingTools TurnState = "awaiting-tools"
	// TurnComplete means the model finished and the reply is whole.
	TurnComplete TurnState = "complete"
	// TurnInterrupted means the user stopped it. Whatever text exists is partial and must be
	// labelled as such wherever it is shown.
	TurnInterrupted TurnState = "interrupted"
	// TurnRefused means the provider declined. This arrives as a successful response, so it is a
	// state rather than an error, and the content may be empty.
	TurnRefused TurnState = "refused"
	// TurnTruncated means the output limit was reached, so the reply stops mid thought.
	//
	// Kept apart from complete because the text looks finished and is not. The chat equivalent of
	// a stale green: plausible, and wrong in the direction that costs you.
	TurnTruncated TurnState = "truncated"
	// TurnFailed means the turn did not produce an answer. Error says why.
	TurnFailed TurnState = "failed"
)

// AllTurnStates returns every valid turn state.
func AllTurnStates() []TurnState {
	return []TurnState{
		TurnPending, TurnStreaming, TurnAwaitingTools,
		TurnComplete, TurnInterrupted, TurnRefused, TurnTruncated, TurnFailed,
	}
}

// Valid reports whether s is a known turn state.
func (s TurnState) Valid() bool {
	for _, known := range AllTurnStates() {
		if s == known {
			return true
		}
	}
	return false
}

// Terminal reports whether a turn in this state will change again on its own.
//
// A terminal turn is what makes an event final, and a final event is the one that may never be
// coalesced away. Get this wrong in the permissive direction and the last thing a user hears about
// a turn is that it was streaming.
func (s TurnState) Terminal() bool {
	switch s {
	case TurnComplete, TurnInterrupted, TurnRefused, TurnTruncated, TurnFailed:
		return true
	default:
		return false
	}
}

// Whole reports whether the reply is the entire reply.
//
// Only one state qualifies. Interrupted, refused, truncated and failed all leave text that reads
// as an answer and is not one, so every renderer asks this rather than checking for the absence of
// an error.
func (s TurnState) Whole() bool { return s == TurnComplete }

func (s TurnState) String() string { return string(s) }

// FromStopReason maps how a provider ended a turn onto how the session records it.
//
// The mapping is not the identity, which is the point of having it. A refusal and a truncation both
// arrive as successful responses, and both produce text a caller could mistake for a finished
// answer.
func TurnStateFromStopReason(reason StopReason) TurnState {
	switch reason {
	case StopEndTurn:
		return TurnComplete
	case StopToolUse:
		return TurnAwaitingTools
	case StopRefusal:
		return TurnRefused
	case StopMaxTokens:
		return TurnTruncated
	case StopCancelled:
		return TurnInterrupted
	default:
		return TurnFailed
	}
}

// AgentState is what an agent is doing, as one word for a list.
//
// Exists separately from TurnState because an agent outlives any one turn, and because the agents
// view in A5 shows several at once and needs a single word per row that means the same thing for
// every one of them.
type AgentState string

const (
	// AgentIdle means the agent is waiting for the user.
	AgentIdle AgentState = "idle"
	// AgentWorking means a turn is in flight, whether thinking, streaming or running tools.
	AgentWorking AgentState = "working"
	// AgentAwaitingPermission means the agent asked to do something and is waiting on an answer.
	//
	// Its own state rather than a flavour of working, because it is the one that needs a person.
	// An agent parked here looks busy and is not, and several of them parked here is a queue the
	// user cannot see unless it is named.
	AgentAwaitingPermission AgentState = "awaiting-permission"
	// AgentFailed means the last turn did not produce an answer.
	AgentFailed AgentState = "failed"
	// AgentStopped means the user stopped it.
	AgentStopped AgentState = "stopped"
)

// AllAgentStates returns every valid agent state.
func AllAgentStates() []AgentState {
	return []AgentState{AgentIdle, AgentWorking, AgentAwaitingPermission, AgentFailed, AgentStopped}
}

// Valid reports whether s is a known agent state.
func (s AgentState) Valid() bool {
	for _, known := range AllAgentStates() {
		if s == known {
			return true
		}
	}
	return false
}

// NeedsAttention reports whether this agent is waiting on a person.
//
// The question the agents view is really asking. With eight agents running, the useful sort is not
// by name or by age but by which ones have stopped and cannot start again without you.
func (s AgentState) NeedsAttention() bool {
	return s == AgentAwaitingPermission || s == AgentFailed
}

func (s AgentState) String() string { return string(s) }

// Turn is one exchange: what was asked, and what came back.
type Turn struct {
	// ID is unique within its session.
	ID string

	State TurnState

	// Request is what was sent, as the provider contract's own message shape.
	Request Message

	// Text is the reply so far. It grows while streaming, which is what lets a coalesced event
	// carry no payload: the reader takes a snapshot and finds everything that has arrived.
	Text string

	// Thinking is visible reasoning, kept apart from Text so it can be folded away without
	// touching the answer, and so it never ends up in what gets copied out as the reply.
	Thinking string

	// ToolCalls are what the model asked to run, and ToolResults what came back.
	ToolCalls   []ToolCall
	ToolResults []ToolResult

	// Usage is what the turn consumed. Meaningful only once the turn is terminal; before that it
	// is whatever the provider has reported so far, which is usually nothing.
	Usage Usage

	// Provider is the name of the credential that answered, which is not necessarily the one that
	// was asked. A fallback chain records the difference here.
	Provider string
	Model    string

	// Error explains a failed turn in words a user can act on.
	Error string

	StartedAt time.Time
	// EndedAt is zero until the turn is terminal.
	EndedAt time.Time
}

// Duration is how long the turn took, or how long it has been running.
func (t Turn) Duration(now time.Time) time.Duration {
	if t.EndedAt.IsZero() {
		return now.Sub(t.StartedAt)
	}
	return t.EndedAt.Sub(t.StartedAt)
}

// Validate checks that a turn is coherent.
func (t Turn) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("a turn needs an ID")
	}
	if !t.State.Valid() {
		return fmt.Errorf("turn %s has unknown state %q", t.ID, t.State)
	}
	// A terminal turn with no end time reports a duration that grows forever, so a finished turn
	// would keep counting up on screen as though it were still running.
	if t.State.Terminal() && t.EndedAt.IsZero() {
		return fmt.Errorf("turn %s is %s but has no end time", t.ID, t.State)
	}
	if t.State == TurnFailed && t.Error == "" {
		return fmt.Errorf("turn %s failed without saying why", t.ID)
	}
	return nil
}

// Compaction records that part of a conversation has been replaced by a summary in what gets sent.
//
// It is a record, not a deletion. The turns it covers are still in Session.Turns, still on screen
// and still searchable; this only changes what History builds for the next request. That split is
// what makes the promise keepable: nothing is destroyed, only left out of the prompt.
type Compaction struct {
	// Summary stands in for the turns before Through.
	Summary string
	// Through is how many turns from the start of the conversation it replaces.
	Through int
	At      time.Time

	// TokensBefore and TokensAfter are estimates of the conversation either side of the compaction,
	// so the transcript can say what compacting actually bought rather than only that it happened.
	//
	// Both estimated, and measured the same way. Using the provider's reported count for one of them
	// would be comparing the size of a whole request, system prompt and tool schemas included,
	// against the size of a list of messages, which made a compaction look like it had made things
	// larger.
	TokensBefore int
	TokensAfter  int
}

// Session is one conversation.
type Session struct {
	ID string

	// Title is what the session is called in a list. Empty until something names it.
	Title string

	// WorkspaceID is where the conversation is rooted. Empty means the primary checkout, which is
	// the common case: a session is not tied to a worktree unless someone put it in one.
	WorkspaceID string

	// KeyName is the credential this session uses, by the name the user gave it.
	KeyName string
	Model   string

	Turns []Turn

	// Compactions are every summarisation this session has been through, oldest first.
	//
	// A list rather than one value, because a long session compacts more than once and each one is
	// a thing that happened to the conversation. Keeping only the latest would hide that an agent
	// had been through three rounds of forgetting, which is exactly what somebody debugging a
	// confused agent needs to see.
	Compactions []Compaction

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Compacted returns the compaction currently in effect, if any.
func (s Session) Compacted() (Compaction, bool) {
	if len(s.Compactions) == 0 {
		return Compaction{}, false
	}
	return s.Compactions[len(s.Compactions)-1], true
}

// Usage totals everything the session has consumed.
//
// Uses Sum rather than folding from a zero value, because Usage has no identity element: an empty
// running total and a turn nobody could price are the same value, so folding from zero would report
// a fully priced session as unpriced.
func (s Session) Usage() Usage {
	usages := make([]Usage, 0, len(s.Turns))
	for _, turn := range s.Turns {
		usages = append(usages, turn.Usage)
	}
	return Sum(usages...)
}

// Active returns the turn in flight, if there is one.
func (s Session) Active() (Turn, bool) {
	if len(s.Turns) == 0 {
		return Turn{}, false
	}
	last := s.Turns[len(s.Turns)-1]
	if last.State.Terminal() {
		return Turn{}, false
	}
	return last, true
}

// AgentState summarises the session as one word.
func (s Session) AgentState() AgentState {
	last, running := s.Active()
	if running {
		return AgentWorking
	}
	if len(s.Turns) == 0 {
		return AgentIdle
	}
	last = s.Turns[len(s.Turns)-1]
	switch last.State {
	case TurnFailed:
		return AgentFailed
	case TurnInterrupted:
		return AgentStopped
	default:
		return AgentIdle
	}
}

// History renders the session as provider messages, ready to send.
//
// This is why Turn holds a `Message` rather than a bare string. Reconstructing the conversation is
// a copy, and a copy cannot lose a tool result's error flag on the way.
func (s Session) History() []Message {
	turns := s.Turns
	messages := make([]Message, 0, len(turns)*2)

	// A compaction replaces the turns it covers with its summary. Sent as a user message rather
	// than an assistant one, because it is Canopy speaking about the conversation and not the model
	// recalling it, and a model that reads its own summary as something it said will defend it.
	if compaction, ok := s.Compacted(); ok && compaction.Through <= len(turns) {
		messages = append(messages, Message{
			Role: RoleUser,
			Text: "Summary of the earlier part of this conversation:\n\n" + compaction.Summary,
		})
		turns = turns[compaction.Through:]
	}

	for _, turn := range turns {
		messages = append(messages, turn.Request)

		// A turn that produced nothing is left out entirely. An empty assistant message is rejected
		// by the API, and a turn that failed before the model said anything has nothing to
		// contribute to the context anyway.
		if turn.Text == "" && len(turn.ToolCalls) == 0 {
			continue
		}
		messages = append(messages, Message{
			Role:      RoleAssistant,
			Text:      turn.Text,
			ToolCalls: turn.ToolCalls,
		})
		if len(turn.ToolResults) > 0 {
			messages = append(messages, Message{Role: RoleUser, ToolResults: turn.ToolResults})
		}
	}
	return messages
}

// Validate checks that a session is coherent.
func (s Session) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("a session needs an ID")
	}
	seen := map[string]bool{}
	for i, turn := range s.Turns {
		if err := turn.Validate(); err != nil {
			return fmt.Errorf("session %s: %w", s.ID, err)
		}
		if seen[turn.ID] {
			return fmt.Errorf("session %s has two turns with ID %q", s.ID, turn.ID)
		}
		seen[turn.ID] = true

		// Only the last turn may be in flight. Anything earlier still streaming means a turn was
		// abandoned without being closed out, and it would show as running forever.
		if i < len(s.Turns)-1 && !turn.State.Terminal() {
			return fmt.Errorf("session %s: turn %s is %s but is not the last turn",
				s.ID, turn.ID, turn.State)
		}
	}
	return nil
}
