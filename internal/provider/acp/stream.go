package acp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// stream is one delegated turn.
//
// Reading happens on whichever goroutine calls Next, which is the same arrangement the other provider
// adapters have, and there is no background reader. What that buys is that answering a request from
// the bridge, queueing an event and noticing the end of the turn all happen in one order with no lock
// between them. What it costs is that a bridge which stops talking mid-turn is only noticed when the
// caller next asks, and the caller's context is what bounds that.
type stream struct {
	ctx   context.Context
	conn  *conn
	child *process

	sessionID string
	promptID  int64

	// notices are things Canopy has to say about the turn, held until the caller starts reading so
	// they arrive in front of the reply rather than behind it.
	notices []string

	pending []core.StreamEvent
	current core.StreamEvent

	usage core.Usage

	err      error
	finished bool

	done      chan struct{}
	closeOnce sync.Once
}

var _ core.Stream = (*stream)(nil)

func (s *stream) Next() bool {
	for {
		if len(s.notices) > 0 {
			s.current = core.StreamEvent{Kind: core.EventNotice, Text: s.notices[0]}
			s.notices = s.notices[1:]
			return true
		}
		if len(s.pending) > 0 {
			s.current = s.pending[0]
			s.pending = s.pending[1:]
			return true
		}
		if s.finished {
			return false
		}

		message, err := s.conn.read()
		if err != nil {
			s.stopped(err)
			continue
		}
		s.handle(message)
	}
}

// stopped ends the turn when the bridge stopped talking.
//
// Cancellation is checked before the read error, for the reason the Anthropic adapter checks it
// first: stopping a turn breaks the read, so looking at the error first would report every turn
// somebody interrupted as a turn that broke. A stopped turn and a failed one lead the reader
// somewhere different.
func (s *stream) stopped(err error) {
	if s.ctx.Err() != nil {
		s.finish(core.StopCancelled, nil)
		return
	}
	if said := strings.TrimSpace(s.child.stderr.String()); said != "" {
		s.err = fmt.Errorf("the Claude Code bridge stopped during the turn: %s", said)
	} else {
		s.err = fmt.Errorf("the Claude Code bridge stopped during the turn: %w", err)
	}
	s.finish(core.StopError, s.err)
}

// handle deals with one frame from the bridge.
func (s *stream) handle(m message) {
	switch {
	case m.ID != nil && m.Method != "":
		s.serve(*m.ID, m.Method, m.Params)

	case m.ID != nil && *m.ID == s.promptID:
		s.endTurn(m)

	case m.Method == methodSessionUpdate:
		s.update(m.Params)

		// Anything else is a response to a request nobody is waiting for any more, or a notification
		// this build does not use. Both are ignored rather than reported: an ACP agent is allowed to
		// send notifications a client has never heard of, and treating an unknown one as a fault
		// would make every extension to the protocol a Canopy bug.
	}
}

// serve answers a request the bridge sent to Canopy.
//
// Everything except a permission request gets "method not found", and that is the true answer rather
// than a shrug. Canopy advertised no filesystem capability and no terminal capability in the
// handshake, so a bridge asking for one is asking for something that was never offered, and a stub
// that answered would be Canopy quietly acquiring a role it declined.
func (s *stream) serve(id int64, method string, params json.RawMessage) {
	if method == methodRequestPermission {
		s.requestPermission(id, params)
		return
	}
	_ = s.conn.replyError(id, methodNotFound, fmt.Sprintf(
		"canopy did not advertise the capability behind %s, so it has nothing to answer with", method))
}

// requestPermission is the sharp end of Q-23, and Canopy declines.
//
// The bridge is asking Canopy to approve one of Claude Code's tool calls. There were three ways to
// answer and only one of them is honest today.
//
// Approving would mean Canopy standing in as the user's approver for a call it did not make, cannot
// describe in its own vocabulary, and has no trust level for. The conversation view shows Canopy's
// permission mode; a screen reading "plan" while an approval Canopy issued edits files is exactly the
// failure Q-23 says no task in this phase may ship. Forwarding it to Canopy's own approval path is
// the right long-term answer and is not available yet: A4's gate is built around Canopy's tool
// definitions and per-agent trust levels, and a vendor tool call carries neither.
//
// So it is declined, every time, and each refusal is said out loud rather than swallowed. Declining
// does not make a delegated turn safe and nothing here should be read as claiming it does: Claude
// Code's own auto-approved tools never reach this method at all, and Canopy neither sees nor gates
// those. What declining does buy is that Canopy never grants a permission it cannot account for.
// LIMITATIONS.md says both halves of that in the same paragraph.
func (s *stream) requestPermission(id int64, params json.RawMessage) {
	var request permissionParams
	if err := json.Unmarshal(params, &request); err != nil {
		_ = s.conn.reply(id, permissionResult{Outcome: permissionOutcome{Outcome: "cancelled"}})
		return
	}

	what := strings.TrimSpace(request.ToolCall.Title)
	if what == "" {
		what = "a tool call"
	}

	if option, ok := refusal(request.Options); ok {
		_ = s.conn.reply(id, permissionResult{
			Outcome: permissionOutcome{Outcome: "selected", OptionID: option},
		})
		s.pending = append(s.pending, core.StreamEvent{Kind: core.EventNotice, Text: fmt.Sprintf(
			"Claude Code asked permission to run %s and Canopy declined. A delegated turn runs Claude "+
				"Code's tools under Claude Code's permissions, so Canopy has no approval of yours to "+
				"give on this route. Run it in Claude Code itself, or use a credential Canopy runs the "+
				"tools for", what)})
		return
	}

	// No way to say no was offered, so the turn stops instead. Cancelling is a heavier answer than
	// declining one call, and it is the right one: the alternative is picking from a list whose every
	// entry is a yes.
	_ = s.conn.reply(id, permissionResult{Outcome: permissionOutcome{Outcome: "cancelled"}})
	s.pending = append(s.pending, core.StreamEvent{Kind: core.EventNotice, Text: fmt.Sprintf(
		"Claude Code asked permission to run %s and offered no way to decline, so Canopy stopped the "+
			"turn rather than approving something on your behalf", what)})
}

// refusal picks the option that says no, preferring the one that says no only this time.
//
// By kind rather than by name, because the names are the agent's own words and are shown to people,
// while the kinds are the protocol's and are meant to be acted on. Matching on a label would be a
// permission decision made by string comparison against somebody else's copy.
func refusal(options []permissionOption) (string, bool) {
	for _, want := range []string{"reject_once", "reject_always"} {
		for _, option := range options {
			if option.Kind == want {
				return option.OptionID, true
			}
		}
	}
	return "", false
}

// update turns one session/update notification into whatever core events it implies, often none.
func (s *stream) update(params json.RawMessage) {
	var notification sessionNotification
	if err := json.Unmarshal(params, &notification); err != nil {
		return
	}

	var kind updateKind
	if err := json.Unmarshal(notification.Update, &kind); err != nil {
		return
	}

	switch kind.SessionUpdate {
	case updateAgentMessageChunk:
		if text := chunkText(notification.Update); text != "" {
			s.pending = append(s.pending, core.StreamEvent{Kind: core.EventText, Text: text})
		}

	case updateAgentThoughtChunk:
		if text := chunkText(notification.Update); text != "" {
			s.pending = append(s.pending, core.StreamEvent{Kind: core.EventThinking, Text: text})
		}

	case updateToolCall:
		s.reportToolCall(notification.Update)

	case updateUserMessageChunk, updateToolCallUpdate, updateUsage:
		// Deliberately nothing, and each for its own reason. A user message chunk is Canopy's own
		// prompt read back. A tool call update is progress on a call already reported once, and one
		// notice per status change would bury the reply. A usage update carries how full the context
		// window is, which core.Usage has no field for: putting it in InputTokens would be a
		// cumulative occupancy figure presented as a per-turn count.

	default:
		// An update this build has never heard of. Ignored on purpose: ACP grows by adding
		// notification variants, and a client that failed on an unknown one would break every time
		// the protocol moved.
	}
}

func chunkText(raw json.RawMessage) string {
	var chunk contentChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return ""
	}
	if chunk.Content.Type != "text" {
		// Images and embedded resources can appear in an agent's own output. There is no core event
		// that carries one, and a caption invented here would be Canopy narrating something it cannot
		// see.
		return ""
	}
	return chunk.Content.Text
}

// reportToolCall says what the delegated agent is doing, as a notice and never as a tool call.
//
// This is the other half of Q-23 and it is load bearing rather than cosmetic. core.EventToolCall is
// a request for the caller to run something: internal/agent/loop.go collects those and invokes them
// through Canopy's permission gate. A delegated tool call has already been run, or is being run, by
// Claude Code. Emitting it as a tool call would make Canopy run it a second time, against a tool
// definition it does not have, having gated a call somebody else already performed.
//
// So it is a notice. The distinction the user is entitled to is between "Canopy is about to do this,
// under your rules" and "Claude Code did this, under its own", and the two must not arrive on the
// same channel.
func (s *stream) reportToolCall(raw json.RawMessage) {
	var call toolCallUpdate
	if err := json.Unmarshal(raw, &call); err != nil {
		return
	}
	what := strings.TrimSpace(call.Title)
	if what == "" {
		what = strings.TrimSpace(call.Kind)
	}
	if what == "" {
		return
	}
	s.pending = append(s.pending, core.StreamEvent{
		Kind: core.EventNotice,
		Text: "Claude Code is running its own tool: " + what,
	})
}

// endTurn handles the answer to session/prompt, which is the last thing the bridge owes.
func (s *stream) endTurn(m message) {
	if m.Error != nil {
		if m.Error.Code == authRequiredCode {
			s.err = fmt.Errorf("%w. %s", ErrNotSignedIn, m.Error.Message)
		} else {
			s.err = fmt.Errorf("the delegated turn failed: %s", m.Error.Message)
		}
		s.finish(core.StopError, s.err)
		return
	}

	var result promptResult
	if err := json.Unmarshal(m.Result, &result); err != nil {
		s.err = fmt.Errorf("the Claude Code bridge ended the turn unreadably: %w", err)
		s.finish(core.StopError, s.err)
		return
	}

	if result.Usage != nil {
		s.usage = core.Usage{
			InputTokens:      result.Usage.InputTokens,
			OutputTokens:     result.Usage.OutputTokens,
			CacheReadTokens:  result.Usage.CachedReadTokens,
			CacheWriteTokens: result.Usage.CachedWriteTokens,
			// Never known on this route, and this is the one field on a delegated turn that must not
			// be filled in later by somebody being helpful. The tokens are real, and the list price
			// of those tokens is not what a subscriber pays: their plan is billed monthly and these
			// tokens are metered against its limits. A dollar figure here would be a number that is
			// arithmetically correct and factually wrong about somebody's bill.
			CostKnown: false,
		}
	}

	switch result.StopReason {
	case stopEndTurn:
		s.finish(core.StopEndTurn, nil)
	case stopMaxTokens:
		s.finish(core.StopMaxTokens, nil)
	case stopMaxTurnRequests:
		// Mapped onto the token cap because core has no third word for "a bound stopped it", and both
		// are an answer cut off rather than an answer refused, which is the property Complete()
		// reports. The notice carries the part the enum loses, so the screen says which bound it was.
		s.pending = append(s.pending, core.StreamEvent{Kind: core.EventNotice,
			Text: "Claude Code stopped after reaching its own limit on requests within one turn, so " +
				"this answer is incomplete"})
		s.finish(core.StopMaxTokens, nil)
	case stopRefusal:
		s.finish(core.StopRefusal, nil)
	case stopCancelled:
		s.finish(core.StopCancelled, nil)
	default:
		// Not guessed at. A stop reason this build has never seen is a protocol that moved, and
		// treating it as a normal ending would present an answer nobody can vouch for as complete.
		s.err = fmt.Errorf(
			"the Claude Code bridge ended the turn with %q, which this build of Canopy does not know "+
				"how to read", result.StopReason)
		s.finish(core.StopError, s.err)
	}
}

// finish queues the terminal event.
//
// Always runs, on every path including failure and cancellation, because the done event is the last
// thing a caller ever hears about a turn and a stream that ends without one is indistinguishable
// from one still in progress.
func (s *stream) finish(reason core.StopReason, err error) {
	s.finished = true
	s.pending = append(s.pending, core.StreamEvent{
		Kind:       core.EventDone,
		StopReason: reason,
		Usage:      s.usage,
		Err:        err,
	})
}

// await reads until the answer to one request arrives, serving whatever else turns up on the way.
//
// Used during setup, before there is a turn to stream. Session updates that arrive here are queued
// rather than dropped, because a bridge is allowed to start talking as soon as it has a session and
// the first thing it says should not be lost to the fact that Canopy was still setting up.
func (s *stream) await(id int64) (json.RawMessage, error) {
	for {
		m, err := s.conn.read()
		if err != nil {
			return nil, err
		}

		if m.ID != nil && *m.ID == id && m.Method == "" {
			if m.Error != nil {
				return nil, m.Error
			}
			return m.Result, nil
		}
		s.handle(m)
	}
}

// watchCancellation asks the bridge to stop when the caller does.
//
// A notification and then nothing else, which is what the protocol asks for: the agent answers the
// original session/prompt with the cancelled stop reason, so the turn ends through the same path a
// finished one does and whatever was already streamed stays on screen. Killing the process here
// would end the turn with an EOF instead, which is a broken connection rather than a decision
// somebody made.
func (s *stream) watchCancellation() {
	go func() {
		select {
		case <-s.ctx.Done():
			_ = s.conn.notify(methodSessionCancel, cancelParams{SessionID: s.sessionID})
		case <-s.done:
		}
	}()
}

func (s *stream) Event() core.StreamEvent { return s.current }

func (s *stream) Err() error { return s.err }

// Close ends the turn and the process behind it.
//
// Required even on a stream that finished on its own, because what is being released here is a child
// process rather than a socket, and a bridge nobody stopped is a Node process and a Claude Agent SDK
// beneath it still running after the conversation moved on.
func (s *stream) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.child.stop()
	})
	return nil
}
