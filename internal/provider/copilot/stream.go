package copilot

import (
	"context"
	"errors"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// stream turns the vendor's callback into core's pull iterator.
//
// The adaptation is a channel and this reads from it, which sounds like nothing and hides the one
// decision that matters: where a step ends. The vendor's session does not end when a tool is
// requested, it waits, so there are two ways for a step to be over and they mean different things.
// Idle means the agent has finished and is waiting for a person. A tool request means the agent has
// finished this step and is waiting for Canopy.
//
// A step ends at the first tool request rather than at all of them, and that is deliberate. The
// vendor may ask for several at once, and draining "however many happen to have arrived by now" is a
// rule whose answer depends on scheduling, which makes a test that passes on one machine and fails
// on another. One per step is the same answer every time; the calls the agent asked for and did not
// get in this step are still pending on its side and come out on the next one, at the cost of a
// round trip and no correctness at all.
type stream struct {
	ctx    context.Context
	client *Client
	agent  Agent

	current core.StreamEvent
	usage   core.Usage

	// pending is the done event, held back until after the event that caused it has been delivered.
	pending *core.StreamEvent

	finished bool
	closed   bool
	err      error
}

var _ core.Stream = (*stream)(nil)

func newStream(ctx context.Context, client *Client, agent Agent) *stream {
	return &stream{ctx: ctx, client: client, agent: agent}
}

// Next advances to the next event.
//
// The done event is delivered through here like any other, because that is what core.Stream promises
// and what agent.Loop reads: it returns at the done event rather than reading past it, so a stream
// that ended without producing one hangs the turn.
func (s *stream) Next() bool {
	if s.finished {
		return false
	}
	if s.pending != nil {
		s.current, s.pending = *s.pending, nil
		s.finished = true
		return true
	}

	for {
		select {
		case <-s.ctx.Done():
			// The vendor is told, not just abandoned. A session left running after Canopy stopped
			// reading would go on spending somebody's allowance on an answer nobody will see, and
			// the session has to stay usable afterwards, which is what Abort is for and what killing
			// the runtime would not be.
			//
			// A context of its own, because the turn's is already cancelled and abort would be
			// refused before it was sent.
			_ = s.agent.Abort(context.WithoutCancel(s.ctx))
			s.finish(core.StopCancelled, nil)
			return true

		case event, open := <-s.agent.Events():
			if !open {
				s.err = errors.New(
					"the Copilot runtime stopped talking before the turn ended, so the reply above is " +
						"whatever had arrived")
				s.finish(core.StopError, s.err)
				return true
			}

			switch event.Kind {
			case EventText:
				if event.Text == "" {
					continue
				}
				s.current = core.StreamEvent{Kind: core.EventText, Text: event.Text}
				return true

			case EventThinking:
				if event.Text == "" {
					continue
				}
				s.current = core.StreamEvent{Kind: core.EventThinking, Text: event.Text}
				return true

			case EventUsage:
				// Accumulated rather than emitted. Usage belongs on the done event, and the vendor
				// reports it per model call, so a step that made more than one has to add them up.
				s.usage = s.usage.Add(event.Usage)
				continue

			case EventToolCall:
				if event.Call == nil {
					continue
				}
				call := *event.Call
				s.current = core.StreamEvent{Kind: core.EventToolCall, ToolCall: &call}
				s.hold(core.StopToolUse, nil)
				return true

			case EventIdle:
				s.finish(core.StopEndTurn, nil)
				return true

			case EventFailed:
				s.err = event.Err
				s.finish(core.StopError, event.Err)
				return true
			}
		}
	}
}

// finish makes the done event the current one.
func (s *stream) finish(reason core.StopReason, err error) {
	s.current = core.StreamEvent{Kind: core.EventDone, StopReason: reason, Usage: s.usage, Err: err}
	s.finished = true
}

// hold queues the done event behind the one already being delivered.
func (s *stream) hold(reason core.StopReason, err error) {
	done := core.StreamEvent{Kind: core.EventDone, StopReason: reason, Usage: s.usage, Err: err}
	s.pending = &done
}

func (s *stream) Event() core.StreamEvent { return s.current }

// Err reports the failure that ended the stream.
//
// Nil for a cancelled turn, because cancelling is something the user did rather than something that
// went wrong, and every layer above reads a non-nil error here as a fault worth showing.
func (s *stream) Err() error { return s.err }

// Close releases the turn without ending the conversation.
//
// The session outlives the stream on purpose: this route's whole design is one session per
// conversation, and a Close that disconnected would make it one session per turn and lose the
// history it exists to keep. What ends the conversation is Client.Close.
func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.client.release()
	return nil
}
