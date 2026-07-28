// Package agent runs the turn: the model asks for a tool, permission is checked, the tool runs, the
// result goes back, and round again until the model stops.
//
// Two bounds are not optional and both exist for the same reason. A confused model will call the
// same tool with the same arguments forever, and without a step limit that is an infinite loop, and
// without a token budget it is an infinite loop that spends real money. Neither is a hypothetical:
// it is the normal failure mode of a model that has misread a tool result.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// DefaultMaxSteps is how many times round the loop a turn may go.
//
// A step is one model call plus the tools it asked for. Generous, because real work genuinely takes
// twenty or thirty steps, and low enough that a model stuck in a circle is stopped within a minute
// or two rather than overnight.
const DefaultMaxSteps = 50

// Approver is asked when the permission model says a call needs a person.
//
// An interface because who answers differs: the chat screen puts a prompt on screen, a headless run
// has a policy, and a test says yes or no immediately. The loop does not care which, and must never
// assume there is a terminal.
type Approver interface {
	// Approve returns whether the call may proceed. Returning false is a refusal, not an error: the
	// model is told no and carries on with something else.
	Approve(ctx context.Context, req permission.Request, decision permission.Decision) bool
}

// ApproverFunc adapts a function to Approver.
type ApproverFunc func(context.Context, permission.Request, permission.Decision) bool

func (f ApproverFunc) Approve(
	ctx context.Context, req permission.Request, d permission.Decision,
) bool {
	return f(ctx, req, d)
}

// DenyAll refuses everything that needs asking.
//
// The correct default when nobody is there to ask. An unattended run that approved by default would
// be an unattended run with broad trust, whatever the profile said.
var DenyAll = ApproverFunc(func(context.Context, permission.Request, permission.Decision) bool {
	return false
})

// Observer receives everything as it happens.
//
// The loop tells rather than returns, because a turn takes minutes and a caller that only learned
// the outcome at the end could not draw any of it. Every method may be called from the loop's own
// goroutine and must not block for long.
type Observer interface {
	// Text and Thinking are chunks of the reply as they arrive.
	Text(chunk string)
	Thinking(chunk string)

	// ToolRequested fires when the model asks for a tool, before permission is decided. The
	// interface shows it immediately, because the gap between asking and being approved is exactly
	// when a user wants to see what is being proposed.
	ToolRequested(call core.ToolCall)

	// ToolFinished fires once, whatever happened: allowed and ran, denied, or failed.
	ToolFinished(call core.ToolCall, result core.ToolResult)

	// StepFinished fires after each model call with what it consumed, so a running total is
	// available before the turn ends.
	StepFinished(usage core.Usage)
}

// Outcome is how a turn ended.
type Outcome struct {
	// Stop is why it stopped, in the provider's vocabulary, so a caller maps it the same way it
	// maps a turn with no tools in it.
	Stop core.StopReason

	// Usage is everything the whole turn consumed, across every step.
	Usage core.Usage

	// Steps is how many model calls it took.
	Steps int

	// Messages is the conversation as it now stands, including every tool call and result. The
	// caller stores this so the next turn continues from it rather than from the request alone.
	Messages []core.Message

	// LimitHit says which bound stopped the turn, empty when the model stopped on its own.
	//
	// Its own field rather than folded into Stop, because "the model finished" and "we stopped it
	// because it was going in circles" are different things to tell a user and the second needs
	// saying plainly.
	LimitHit string
}

// Loop runs a turn to completion.
type Loop struct {
	Client core.ProviderClient
	Tools  *core.ToolRegistry

	// Trust is how much this agent may do, and it is fixed for the whole turn.
	//
	// Use TrustNow instead of reading it. A turn can outlast the decision that started it: somebody
	// watching a reply arrive and deciding halfway through that they want it planning rather than
	// building expects that to take hold on the next tool call, not on the next message. A level
	// captured at the top of Run cannot do that, and the version of this that was a plain field
	// meant switching mode mid reply looked like it worked and changed nothing.
	Trust core.TrustLevel

	// LiveTrust, when set, is asked before every tool call and wins over Trust.
	//
	// A function rather than a channel or a mutex-guarded field, so the loop holds no state that has
	// to be kept in step with the engine's, and so a caller that has no notion of changing its mind
	// can leave it nil and get the fixed level.
	LiveTrust func() core.TrustLevel

	Grants   *permission.Grants
	Trail    *permission.Trail
	Approver Approver

	AgentID   string
	SessionID string

	// MaxSteps defaults to DefaultMaxSteps.
	MaxSteps int
	// MaxTokens bounds the whole turn. Zero means no token bound, which is only appropriate when
	// something above is enforcing one.
	MaxTokens int
}

// TrustNow is how much this agent may do at this moment.
//
// Asked per tool call rather than once per turn, so that tightening the level while a reply is
// arriving takes hold on the next thing the model tries rather than on the next thing the user
// says. Loosening mid turn works the same way, which is the less interesting direction.
//
// A tightened level will start refusing a model that is halfway through a sequence of edits, and
// that is the correct outcome rather than a rough edge: the refusal goes back as a tool result the
// model can read and react to, so it stops and says what it was doing instead of failing the turn.
func (l *Loop) TrustNow() core.TrustLevel {
	if l.LiveTrust != nil {
		if level := l.LiveTrust(); level != "" {
			return level
		}
	}
	return l.Trust
}

// Run drives a turn until the model stops or a bound is reached.
func (l *Loop) Run(ctx context.Context, req core.Request, obs Observer) (Outcome, error) {
	if l.Client == nil {
		return Outcome{}, errors.New("a loop needs a provider client")
	}
	if obs == nil {
		obs = noopObserver{}
	}
	approver := l.Approver
	if approver == nil {
		approver = DenyAll
	}
	maxSteps := l.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}

	messages := append([]core.Message(nil), req.Messages...)
	if l.Tools != nil {
		req.Tools = l.Tools.Definitions()
	}

	outcome := Outcome{Messages: messages}

	for step := 1; ; step++ {
		if step > maxSteps {
			outcome.Stop = core.StopError
			outcome.LimitHit = fmt.Sprintf(
				"stopped after %d steps, which usually means the model was going in circles", maxSteps)
			return outcome, nil
		}
		if l.MaxTokens > 0 && outcome.Usage.TotalTokens() >= l.MaxTokens {
			outcome.Stop = core.StopError
			outcome.LimitHit = fmt.Sprintf(
				"stopped after %d tokens, which is this turn's budget", outcome.Usage.TotalTokens())
			return outcome, nil
		}

		req.Messages = outcome.Messages
		reply, err := l.step(ctx, req, obs)
		if err != nil {
			return outcome, err
		}

		outcome.Steps = step
		outcome.Usage = accumulate(outcome.Usage, reply.usage)
		obs.StepFinished(reply.usage)

		// The assistant's message goes in whatever happened, because it is what the model said and
		// the next request has to contain it. A turn that dropped it would ask the model to
		// continue from a conversation it does not recognise.
		if reply.text != "" || len(reply.calls) > 0 {
			outcome.Messages = append(outcome.Messages, core.Message{
				Role:      core.RoleAssistant,
				Text:      reply.text,
				ToolCalls: reply.calls,
			})
		}

		if reply.stop != core.StopToolUse || len(reply.calls) == 0 {
			outcome.Stop = reply.stop
			return outcome, nil
		}

		results := make([]core.ToolResult, 0, len(reply.calls))
		for _, call := range reply.calls {
			result := l.invoke(ctx, call, approver, obs)
			results = append(results, result)

			// Cancellation is checked between calls rather than only around the model. A turn that
			// was stopped should not run the remaining three tools it had queued up, and the tools
			// themselves take a context but a fast one will have finished before it fires.
			if ctx.Err() != nil {
				outcome.Messages = append(outcome.Messages,
					core.Message{Role: core.RoleUser, ToolResults: results})
				outcome.Stop = core.StopCancelled
				return outcome, nil
			}
		}

		outcome.Messages = append(outcome.Messages,
			core.Message{Role: core.RoleUser, ToolResults: results})
	}
}

// reply is one model call's worth of output.
type reply struct {
	text  string
	calls []core.ToolCall
	usage core.Usage
	stop  core.StopReason
}

// step performs one model call and collects what came back.
func (l *Loop) step(ctx context.Context, req core.Request, obs Observer) (reply, error) {
	stream, err := l.Client.Stream(ctx, req)
	if err != nil {
		return reply{}, err
	}
	defer func() { _ = stream.Close() }()

	var out reply
	var text []byte

	for stream.Next() {
		event := stream.Event()
		switch event.Kind {
		case core.EventText:
			text = append(text, event.Text...)
			obs.Text(event.Text)
		case core.EventThinking:
			obs.Thinking(event.Text)
		case core.EventToolCall:
			out.calls = append(out.calls, *event.ToolCall)
			obs.ToolRequested(*event.ToolCall)
		case core.EventDone:
			// Returned at rather than read past. The done event is by contract the last thing a
			// stream has to say, so continuing to read after it means depending on the stream also
			// reporting that it is finished, promptly. A stream that simply stops producing events
			// would hang the turn here rather than ending it, which is a much worse failure than
			// leaving an event unread.
			out.usage = event.Usage
			out.stop = event.StopReason
			out.text = string(text)
			return out, event.Err
		}
	}
	if err := stream.Err(); err != nil {
		return out, err
	}

	out.text = string(text)
	if out.stop == "" {
		// A stream that ended without saying how is a bug in a provider adapter, not a finished
		// turn. Reported rather than treated as complete, because the alternative is presenting an
		// answer nobody can vouch for.
		return out, errors.New("the provider stopped without saying how the turn ended")
	}
	return out, nil
}

// invoke checks permission, runs the tool, and records the whole thing.
//
// Every path through this produces exactly one audit entry and exactly one result. That is the
// property the audit trail depends on: a call that produced no entry is a call nobody can find
// afterwards, and a call that produced no result leaves the model waiting for an answer that is
// never coming, which it responds to by asking again.
func (l *Loop) invoke(
	ctx context.Context, call core.ToolCall, approver Approver, obs Observer,
) core.ToolResult {
	started := time.Now()

	entry := permission.Entry{
		At:        started,
		AgentID:   l.AgentID,
		SessionID: l.SessionID,
		Tool:      call.Name,
		Arguments: string(call.Input),
	}

	finish := func(result core.ToolResult) core.ToolResult {
		entry.Result = result.Content
		entry.Failed = result.IsError
		entry.Duration = time.Since(started)
		// The same measurement the audit trail records, so the number on screen and the number in
		// the trail can never disagree about the same call.
		result.Duration = entry.Duration
		if l.Trail != nil {
			l.Trail.Record(entry)
		}
		obs.ToolFinished(call, result)
		return result
	}

	tool, ok := l.tool(call.Name)
	if !ok {
		entry.Outcome = permission.Deny
		entry.Reason = "no such tool"
		// Named, so the model can correct itself. "Unknown tool" without the name leaves it
		// guessing which of the three it just asked for was wrong.
		return finish(core.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("there is no tool called %q available to this agent", call.Name),
			IsError: true,
		})
	}
	entry.Kind = tool.Kind()

	if err := core.ValidateToolInput(tool.Schema(), call.Input); err != nil {
		entry.Outcome = permission.Deny
		entry.Reason = "invalid arguments"
		return finish(core.ToolResult{
			CallID: call.ID, Content: err.Error(), IsError: true,
		})
	}

	req := permission.Request{
		AgentID:   l.AgentID,
		SessionID: l.SessionID,
		Tool:      call.Name,
		Kind:      tool.Kind(),
		Paths:     pathsIn(call.Input),
		Command:   commandIn(call.Input),
	}

	decision := permission.Decide(req, l.TrustNow(), l.Grants)
	entry.Outcome = decision.Outcome
	entry.Reason = decision.Reason

	switch decision.Outcome {
	case permission.Deny:
		// Returned to the model rather than ending the turn. A refused call is information: the
		// model can try something within its remit, which is usually what it should do.
		return finish(core.ToolResult{
			CallID: call.ID, Content: "refused: " + decision.Reason, IsError: true,
		})

	case permission.Ask:
		if !approver.Approve(ctx, req, decision) {
			entry.Outcome = permission.Deny
			entry.Reason = "not approved"
			return finish(core.ToolResult{
				CallID:  call.ID,
				Content: "the user did not approve this. Try a different approach, or ask them why.",
				IsError: true,
			})
		}
		entry.Outcome = permission.Allow
	}

	result, err := tool.Run(ctx, call.Input)
	if err != nil {
		// A Go error from a tool means it could not run at all, which is different from it running
		// and failing. Both go back to the model; only this one leaves Ran false in the trail.
		return finish(core.ToolResult{
			CallID:  call.ID,
			Content: fmt.Sprintf("%s could not run: %v", call.Name, err),
			IsError: true,
		})
	}

	if result.Refused {
		entry.Outcome = permission.Deny
		entry.Reason = result.Content
		result.IsError = true
		result.CallID = call.ID
		return finish(result)
	}

	entry.Ran = true
	result.CallID = call.ID
	return finish(result)
}

func (l *Loop) tool(name string) (core.Tool, bool) {
	if l.Tools == nil {
		return nil, false
	}
	return l.Tools.Get(name)
}

// pathsIn pulls the workspace paths out of a tool's arguments.
//
// By convention rather than by declaration: a tool that takes a path calls it `path`. A convention
// rather than something enforced by the schema because the permission layer has to work for tools
// it has never seen, and one that missed a differently named field would silently drop the path
// scoping and approve too broadly. The names are checked in a test against every registered tool.
func pathsIn(input json.RawMessage) []string {
	var args map[string]any
	if err := json.Unmarshal(input, &args); err != nil {
		return nil
	}

	var out []string
	for _, key := range []string{"path", "file", "directory", "dir"} {
		if value, ok := args[key].(string); ok && value != "" {
			out = append(out, value)
		}
	}
	return out
}

// commandIn pulls the shell command out of a tool's arguments, by the same convention.
func commandIn(input json.RawMessage) string {
	var args map[string]any
	if err := json.Unmarshal(input, &args); err != nil {
		return ""
	}
	if value, ok := args["command"].(string); ok {
		return value
	}
	return ""
}

// accumulate adds a step's usage to a running total.
//
// Not core.Usage.Add, because that has no identity element and the total starts empty. The first
// step's usage becomes the total; everything after adds to it.
func accumulate(total, step core.Usage) core.Usage {
	if total.IsZero() {
		return step
	}
	if step.IsZero() {
		return total
	}
	return total.Add(step)
}

// noopObserver is used when a caller does not want the running commentary.
type noopObserver struct{}

func (noopObserver) Text(string)                                 {}
func (noopObserver) Thinking(string)                             {}
func (noopObserver) ToolRequested(core.ToolCall)                 {}
func (noopObserver) ToolFinished(core.ToolCall, core.ToolResult) {}
func (noopObserver) StepFinished(core.Usage)                     {}
