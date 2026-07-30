package codex

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
// Reading happens on whichever goroutine calls Next, which is the arrangement every other provider
// adapter here has, and there is no background reader. What that buys is that answering a request
// from the app server, queueing an event and noticing the end of the turn all happen in one order
// with no lock between them. What it costs is that a server which stops talking mid-turn is only
// noticed when the caller next asks, and the caller's context is what bounds that.
type stream struct {
	ctx     context.Context
	session *session

	threadID string
	turnID   string

	// notices are things Canopy has to say about the turn, held until the caller starts reading so
	// they arrive in front of the reply rather than behind it.
	notices []string

	pending []core.StreamEvent
	current core.StreamEvent

	// emitted is how much of each message item has already gone out as a delta.
	//
	// The app server sends an item's text twice, once in pieces as `item/agentMessage/delta` and
	// once whole on `item/completed`, and a client that read both would print every reply twice.
	// Counting rather than ignoring the second, because a build that sends no deltas at all would
	// otherwise stream nothing: what is emitted on completion is the part that was never sent.
	emitted map[string]int

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

		m, err := s.session.conn.read()
		if err != nil {
			s.stopped(err)
			continue
		}
		s.handle(m)
	}
}

// stopped ends the turn when the app server stopped talking.
//
// Cancellation is checked before the read error, for the reason the other adapters check it first:
// stopping a turn breaks the read, so looking at the error first would report every turn somebody
// interrupted as a turn that broke, and those two lead a reader somewhere different.
func (s *stream) stopped(err error) {
	if s.ctx.Err() != nil {
		s.finish(core.StopCancelled, nil)
		return
	}
	if said := lastLine(s.session.child.stderr.String()); said != "" {
		s.err = fmt.Errorf("the Codex app server stopped during the turn: %s", said)
	} else {
		s.err = fmt.Errorf("the Codex app server stopped during the turn: %w", err)
	}
	s.finish(core.StopError, s.err)
}

// handle deals with one frame from the app server.
func (s *stream) handle(m message) {
	if m.ID != nil && m.Method != "" {
		s.serve(*m.ID, m.Method, m.Params)
		return
	}
	if m.Method == "" {
		// A response to a request nobody is waiting for any more. Ignored: the turn's own request
		// was answered before streaming began.
		return
	}

	switch m.Method {
	case notifyAgentMessage:
		s.messageDelta(m.Params)
	case notifyReasoningSummary, notifyReasoningText:
		s.thinkingDelta(m.Params)
	case notifyItemStarted:
		s.reportItem(m.Params)
	case notifyItemCompleted:
		s.completeItem(m.Params)
	case notifyTokenUsage:
		s.readUsage(m.Params)
	case notifyError:
		s.readError(m.Params)
	case notifyTurnCompleted:
		s.endTurn(m.Params)

	default:
		// A notification this build has never heard of. Ignored on purpose: this protocol grows by
		// adding notification variants, and a client that failed on an unknown one would break every
		// time the vendor released something.
	}
}

// serve answers a request the app server sent to Canopy.
//
// Two of them are approvals and Canopy declines both. Everything else is answered as a method that
// is not there, which is the truthful answer rather than a stub that pretends: Canopy asked for
// none of the capabilities behind the others, so a server calling one is calling something that was
// never offered.
func (s *stream) serve(id int64, method string, params json.RawMessage) {
	switch method {
	case requestApproveCommand, requestApproveFileChange:
		s.declineApproval(id, method, params)
	default:
		_ = s.session.conn.replyError(id, methodNotFound, fmt.Sprintf(
			"canopy did not advertise the capability behind %s, so it has nothing to answer with",
			method))
	}
}

// declineApproval is the sharp end of Q-23, and Canopy declines.
//
// The app server is asking Canopy to vouch for one of its own tool calls. There were three ways to
// answer and only one of them is honest today.
//
// Approving would mean Canopy standing in as the user's approver for a call it did not make, cannot
// describe in its own vocabulary, and has no trust level for. The conversation view shows Canopy's
// permission mode; a screen reading "plan" while an approval Canopy issued edits files is exactly
// the failure Q-23 says no task in this phase may ship. Forwarding it to Canopy's own approval path
// is the right long-term answer and is not available: A4's gate is built around Canopy's tool
// definitions and per-agent trust levels, and a vendor tool call carries neither.
//
// So it is declined, every time, and each refusal is said out loud rather than swallowed. Declining
// rather than cancelling, which is the protocol's other refusal: cancel stops the whole turn, and
// Canopy is refusing to vouch for one call rather than objecting to the work. Nothing here should be
// read as making a delegated turn gated. The app server's own auto-approved and sandboxed calls
// never reach this method at all, so what declining buys is only that Canopy never grants a
// permission it cannot account for. LIMITATIONS.md says both halves of that in one paragraph.
func (s *stream) declineApproval(id int64, method string, params json.RawMessage) {
	var request approvalParams
	_ = json.Unmarshal(params, &request)

	if err := s.session.conn.reply(id, approvalResponse{Decision: decisionDecline}); err != nil {
		s.notices = append(s.notices, fmt.Sprintf(
			"Codex asked Canopy to approve something and the refusal could not be sent: %v", err))
		return
	}

	s.pending = append(s.pending, core.StreamEvent{Kind: core.EventNotice, Text: fmt.Sprintf(
		"Codex asked permission to %s and Canopy declined. A delegated turn runs Codex's tools under "+
			"Codex's own permissions and sandbox, so Canopy has no approval of yours to give on this "+
			"route. Run it in Codex itself, or use a credential Canopy runs the tools for",
		describeApproval(method, request))})
}

// describeApproval says what was refused, in the vendor's own words where it gave any.
func describeApproval(method string, request approvalParams) string {
	if command := strings.TrimSpace(request.Command); command != "" {
		return "run `" + command + "`"
	}
	if method == requestApproveFileChange {
		return "change files"
	}
	if reason := strings.TrimSpace(request.Reason); reason != "" {
		return reason
	}
	return "do something"
}

func (s *stream) messageDelta(params json.RawMessage) {
	var delta deltaNotification
	if err := json.Unmarshal(params, &delta); err != nil || delta.Delta == "" {
		return
	}
	s.emitted[delta.ItemID] += len(delta.Delta)
	s.pending = append(s.pending, core.StreamEvent{Kind: core.EventText, Text: delta.Delta})
}

func (s *stream) thinkingDelta(params json.RawMessage) {
	var delta deltaNotification
	if err := json.Unmarshal(params, &delta); err != nil || delta.Delta == "" {
		return
	}
	s.pending = append(s.pending, core.StreamEvent{Kind: core.EventThinking, Text: delta.Delta})
}

// reportItem says what the delegated agent is doing, as a notice and never as a tool call.
//
// This is the other half of Q-23 and it is load bearing rather than cosmetic. core.EventToolCall is
// a request for the caller to run something: internal/agent/loop.go collects those and invokes them
// through Canopy's permission gate. Every tool-shaped item here has already been run, or is being
// run, by the app server inside its own sandbox. Emitting one as a tool call would make Canopy run
// it a second time, against a tool definition it does not have, having gated a call somebody else
// already performed.
//
// So it is a notice. The distinction a user is entitled to is between "Canopy is about to do this,
// under your rules" and "Codex did this, under its own", and the two must not arrive on the same
// channel. There is no branch below that can produce a tool call event, which is the property
// TestNoItemFromTheDelegatedAgentIsEverHandedBackToBeRun holds over every item type the protocol
// has.
func (s *stream) reportItem(params json.RawMessage) {
	var notification itemNotification
	if err := json.Unmarshal(params, &notification); err != nil {
		return
	}
	if what := describeItem(notification.Item); what != "" {
		s.pending = append(s.pending, core.StreamEvent{Kind: core.EventNotice, Text: what})
	}
}

// describeItem names one thing the delegated agent did, or nothing where there is nothing to say.
func describeItem(item threadItem) string {
	switch item.Type {
	case itemUserMessage, itemAgentMessage, itemReasoning:
		// Canopy's own prompt read back, and the reply and its reasoning, all of which arrive as
		// their own events.
		return ""

	case itemCommandExecution:
		if command := strings.TrimSpace(item.Command); command != "" {
			return "Codex is running its own command: " + command
		}
		return "Codex is running a command of its own"

	case itemFileChange:
		if len(item.Changes) == 1 {
			for path := range item.Changes {
				return "Codex is changing a file of its own accord: " + path
			}
		}
		if len(item.Changes) > 1 {
			return fmt.Sprintf("Codex is changing %d files of its own accord", len(item.Changes))
		}
		return "Codex is changing files of its own accord"

	case itemMCPToolCall:
		return fmt.Sprintf("Codex is calling %s on the %s server you configured for it",
			orSomething(item.Tool), orSomething(item.Server))

	case itemDynamicToolCall:
		return "Codex is running its own tool: " + orSomething(item.Tool)

	case itemWebSearch:
		if query := strings.TrimSpace(item.Query); query != "" {
			return "Codex is searching the web for: " + query
		}
		return "Codex is searching the web"

	case itemImageView:
		return "Codex is looking at an image: " + orSomething(item.Path)

	case itemPlan:
		if text := strings.TrimSpace(item.Text); text != "" {
			return "Codex's plan: " + text
		}
		return ""

	case itemContextCompact:
		return "Codex compacted its own context, so it is working from a summary of the earlier part " +
			"of this turn"

	default:
		// An item type this build has never heard of. Named without pretending to understand it,
		// because silence about something the agent did is worse than an unglamorous label, and
		// guessing at its meaning would be worse than both.
		return "Codex did something this build of Canopy does not have a name for: " + item.Type
	}
}

func orSomething(value string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return "something"
}

// completeItem picks up the part of a reply that never arrived as a delta.
//
// Ordinarily nothing: the text was streamed and this is the same text again, whole. It matters on a
// build or a model that sends no deltas, where without this the reply would be complete on the app
// server's side and empty on Canopy's.
func (s *stream) completeItem(params json.RawMessage) {
	var notification itemNotification
	if err := json.Unmarshal(params, &notification); err != nil {
		return
	}
	if notification.Item.Type != itemAgentMessage {
		return
	}
	already := s.emitted[notification.Item.ID]
	if already >= len(notification.Item.Text) {
		return
	}
	s.pending = append(s.pending, core.StreamEvent{
		Kind: core.EventText,
		Text: notification.Item.Text[already:],
	})
	s.emitted[notification.Item.ID] = len(notification.Item.Text)
}

// readUsage records what the turn cost in tokens, and never what it cost in money.
//
// Last rather than Total, because Total is cumulative across the thread and reporting it would make
// every turn look like it consumed everything before it as well.
func (s *stream) readUsage(params json.RawMessage) {
	var notification tokenUsageParams
	if err := json.Unmarshal(params, &notification); err != nil {
		return
	}
	last := notification.TokenUsage.Last
	s.usage = core.Usage{
		InputTokens:     int(last.InputTokens),
		OutputTokens:    int(last.OutputTokens),
		CacheReadTokens: int(last.CachedInputTokens),
		// Never known on this route, and this is the one field on a delegated turn that must not be
		// filled in later by somebody being helpful. The tokens are real, and the list price of
		// those tokens is not what a subscriber pays: their plan is billed monthly and these tokens
		// are metered against its limits. A dollar figure here would be arithmetically correct and
		// factually wrong about somebody's bill.
		CostKnown: false,
	}
}

// readError takes a failure the app server reported during the turn.
//
// A retryable one is a notice and not the end. The app server retries some failures itself, so
// treating the first report as terminal would have Canopy calling a turn dead that is still
// running, and the turn's own completion is what settles it either way.
func (s *stream) readError(params json.RawMessage) {
	var notification errorNotification
	if err := json.Unmarshal(params, &notification); err != nil {
		return
	}
	message := strings.TrimSpace(notification.Error.Message)
	if message == "" {
		return
	}
	if notification.WillRetry {
		s.pending = append(s.pending, core.StreamEvent{Kind: core.EventNotice,
			Text: "Codex hit a problem and is trying again: " + message})
		return
	}
	s.err = fmt.Errorf("the delegated turn failed: %s", message)
}

// endTurn handles turn/completed, which is the last thing the app server owes.
func (s *stream) endTurn(params json.RawMessage) {
	var notification turnCompletedParams
	if err := json.Unmarshal(params, &notification); err != nil {
		s.err = fmt.Errorf("the Codex app server ended the turn unreadably: %w", err)
		s.finish(core.StopError, s.err)
		return
	}
	if s.turnID != "" && notification.Turn.ID != "" && notification.Turn.ID != s.turnID {
		// Somebody else's turn on a thread this build did not start. Ignored rather than taken for
		// this one, since ending on it would cut a live turn short.
		return
	}

	switch notification.Turn.Status {
	case turnCompleted:
		s.finish(core.StopEndTurn, nil)

	case turnInterrupted:
		s.finish(core.StopCancelled, nil)

	case turnFailed:
		if s.err == nil {
			s.err = fmt.Errorf("the delegated turn failed: %s", failureText(notification.Turn.Error))
		}
		s.finish(core.StopError, s.err)

	case turnInProgress:
		// A progress report rather than an ending, which the status says plainly. Ignored so the
		// stream keeps reading.

	default:
		// Not guessed at. A status this build has never seen is a protocol that moved, and treating
		// it as a normal ending would present an answer nobody can vouch for as complete.
		s.err = fmt.Errorf(
			"the Codex app server ended the turn with status %q, which this build of Canopy does not "+
				"know how to read", notification.Turn.Status)
		s.finish(core.StopError, s.err)
	}
}

func failureText(err *turnError) string {
	if err == nil {
		return "the app server did not say why"
	}
	said := strings.TrimSpace(err.Message)
	if detail := strings.TrimSpace(err.AdditionalDetails); detail != "" {
		said += ": " + detail
	}
	if said == "" {
		return "the app server did not say why"
	}
	return said
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

// watchCancellation asks the app server to stop when the caller does.
//
// A request and then nothing else, which is what the protocol asks for: the app server ends the turn
// with the interrupted status, so it finishes through the same path a completed one does and
// whatever was already streamed stays on screen. Killing the process here would end the turn with an
// EOF instead, which is a broken connection rather than a decision somebody made.
func (s *stream) watchCancellation() {
	go func() {
		select {
		case <-s.ctx.Done():
			_, _ = s.session.conn.send(methodTurnInterrupt, turnInterruptParams{
				ThreadID: s.threadID,
				TurnID:   s.turnID,
			})
		case <-s.done:
		}
	}()
}

func (s *stream) Event() core.StreamEvent { return s.current }

func (s *stream) Err() error { return s.err }

// Close ends the turn and the process behind it.
//
// Required even on a stream that finished on its own, because what is being released is a child
// process rather than a socket. An app server nobody stopped keeps running, and on this route it is
// holding whatever MCP servers the user's own config.toml told it to start.
func (s *stream) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		_ = s.session.Close()
	})
	return nil
}
