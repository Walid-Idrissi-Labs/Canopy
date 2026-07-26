package core

import (
	"context"
	"fmt"
	"time"
)

// The provider contract. Everything above this cannot tell which vendor answered.
//
// Several rules here come from the current API rather than from a general sense of how these
// things work, and getting one wrong produces a 400 or a crash rather than a degraded answer:
//
//   - `refusal` is a stop reason, not an error. A declined request returns success with possibly
//     empty content, so callers must check StopReason before reading Content.
//   - Sampling parameters are rejected by current models. AgentProfile carries a Temperature
//     field; the provider layer is where it gets dropped rather than sent.
//   - Thinking depth is controlled by effort. A token budget for thinking is rejected.
//   - Everything streams. Non-streaming requests time out at large output sizes, and streaming is
//     what the interface wants anyway.
//
// See DECISIONS.md D-31.

// Effort controls how much the model thinks and how much work it does before answering.
type Effort string

const (
	// EffortDefault leaves the choice to the provider.
	EffortDefault Effort = ""
	EffortLow     Effort = "low"
	EffortMedium  Effort = "medium"
	EffortHigh    Effort = "high"
	EffortXHigh   Effort = "xhigh"
	EffortMax     Effort = "max"
)

// AllEfforts returns every effort level, cheapest first.
func AllEfforts() []Effort {
	return []Effort{EffortLow, EffortMedium, EffortHigh, EffortXHigh, EffortMax}
}

// Valid reports whether e is a known level. The empty default is valid.
func (e Effort) Valid() bool {
	if e == EffortDefault {
		return true
	}
	for _, known := range AllEfforts() {
		if e == known {
			return true
		}
	}
	return false
}

func (e Effort) String() string {
	if e == EffortDefault {
		return "default"
	}
	return string(e)
}

// StopReason is why a turn ended.
type StopReason string

const (
	// StopEndTurn is a normal completion.
	StopEndTurn StopReason = "end-turn"
	// StopToolUse means the model wants a tool run and expects results back.
	StopToolUse StopReason = "tool-use"
	// StopMaxTokens means the output cap was reached, so the answer is cut off mid thought.
	StopMaxTokens StopReason = "max-tokens"
	// StopRefusal means the provider declined the request.
	//
	// This arrives as a successful response, not an error, and the content may be empty or a
	// partial. Reading content without checking for this is the mistake it exists to prevent.
	StopRefusal StopReason = "refusal"
	// StopCancelled means the caller cancelled the turn. Any content is partial.
	StopCancelled StopReason = "cancelled"
	// StopError means the turn failed. Err on the result says why.
	StopError StopReason = "error"
)

// Complete reports whether the turn produced a whole answer.
//
// Only end-turn and tool-use qualify. A truncated, refused, cancelled or failed turn did not, and
// presenting any of them as finished is the chat equivalent of a stale green.
func (s StopReason) Complete() bool {
	return s == StopEndTurn || s == StopToolUse
}

func (s StopReason) String() string { return string(s) }

// Role is who produced a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn of a conversation.
type Message struct {
	Role Role

	// Text is the message body.
	Text string

	// ToolCalls are tool invocations the assistant requested.
	ToolCalls []ToolCall

	// ToolResults answer tool calls from the previous assistant turn. They belong on a user
	// message, and every call must be answered, including failed ones: dropping a result leaves
	// the model waiting for an answer that never comes.
	ToolResults []ToolResult
}

// ToolCall is the model asking for a tool to be run.
type ToolCall struct {
	ID    string
	Name  string
	Input []byte // raw JSON, parsed by the tool
}

// ToolResult answers a ToolCall.
type ToolResult struct {
	CallID  string
	Content string
	// IsError marks a failure. A failed tool still gets a result, so the model can adjust rather
	// than wait.
	IsError bool
}

// ToolDefinition describes a tool to the model.
type ToolDefinition struct {
	Name        string
	Description string
	// InputSchema is JSON Schema. Declared once and used both for the provider call and for local
	// argument validation, so the two cannot drift apart.
	InputSchema []byte
}

// Request is one turn's worth of input.
type Request struct {
	Model  string
	System string

	Messages []Message
	Tools    []ToolDefinition

	// MaxTokens caps the response. Zero means the provider default.
	//
	// It bounds thinking and answer together, so a value sized for the answer alone truncates once
	// thinking is on. Current models think by default.
	MaxTokens int

	// Effort controls thinking depth and how much work happens before answering.
	Effort Effort

	// DisableThinking turns thinking off. Some providers reject this above a certain effort, which
	// the provider reports as a validation error rather than silently ignoring.
	DisableThinking bool
}

// Validate checks a request before it reaches a provider.
func (r Request) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("a model is required")
	}
	if !r.Effort.Valid() {
		return fmt.Errorf("unknown effort %q", r.Effort)
	}
	if r.MaxTokens < 0 {
		return fmt.Errorf("max tokens cannot be negative")
	}
	if len(r.Messages) == 0 {
		return fmt.Errorf("at least one message is required")
	}
	if r.Messages[0].Role != RoleUser {
		return fmt.Errorf("the first message must be from the user, got %q", r.Messages[0].Role)
	}
	return nil
}

// Usage is what a turn consumed.
type Usage struct {
	InputTokens  int
	OutputTokens int

	// CacheReadTokens and CacheWriteTokens are reported separately because they are priced
	// differently, and because a cache read count stuck at zero is the signal that caching has
	// silently stopped working.
	CacheReadTokens  int
	CacheWriteTokens int

	// CostUSD is computed from a dated pricing table. Zero means the price was not known, which is
	// different from free, and the interface has to say so rather than showing $0.00.
	CostUSD float64
	// CostKnown distinguishes "free" from "we could not price this".
	CostKnown bool
}

// IsZero reports whether a turn consumed nothing, which is what an unfilled Usage looks like.
func (u Usage) IsZero() bool { return u == Usage{} }

// Add accumulates usage across the turns of a multi step task.
//
// Deliberately has no identity element. `Usage{}` cannot serve as one, because an empty running
// total and a turn nobody could price look identical, and treating the first as the second would
// silently mark every accumulation unpriced. Sum is the way to fold a list.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + other.InputTokens,
		OutputTokens:     u.OutputTokens + other.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + other.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + other.CacheWriteTokens,
		CostUSD:          u.CostUSD + other.CostUSD,
		// Unknown anywhere makes the total unknown. A partial sum presented as complete would be a
		// wrong number on screen, which is worse than an absent one.
		CostKnown: u.CostKnown && other.CostKnown,
	}
}

// Sum totals the usage of several turns.
//
// This exists rather than a fold from `Usage{}` because Add has no identity element. Starting a
// running total at the zero value would carry CostKnown false into every sum, so a session of
// perfectly priced turns would report its cost as unknown. Nothing to add is unknown; one thing to
// add is that thing.
func Sum(turns ...Usage) Usage {
	if len(turns) == 0 {
		return Usage{}
	}
	total := turns[0]
	for _, turn := range turns[1:] {
		total = total.Add(turn)
	}
	return total
}

// TotalTokens is every token the turn touched.
func (u Usage) TotalTokens() int {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

// StreamEventKind is what a stream event carries.
type StreamEventKind string

const (
	// EventText is a chunk of the reply.
	EventText StreamEventKind = "text"
	// EventThinking is a chunk of visible reasoning, when the provider returns any.
	EventThinking StreamEventKind = "thinking"
	// EventToolCall is a completed tool invocation request.
	EventToolCall StreamEventKind = "tool-call"
	// EventNotice is something the reader needs to know about the turn itself rather than an answer
	// to what they asked. A fallback to a different provider is the first of these.
	//
	// Separate from text on purpose. Merged into the reply it would read as the model saying it,
	// and the whole reason for the event is that it comes from Canopy rather than from the model.
	EventNotice StreamEventKind = "notice"
	// EventDone is the final event. It carries the stop reason and usage, and always arrives, even
	// on failure.
	EventDone StreamEventKind = "done"
)

// StreamEvent is one piece of a streaming response.
type StreamEvent struct {
	Kind StreamEventKind

	// Text is the chunk, for text and thinking events.
	Text string

	// ToolCall is set on tool call events.
	ToolCall *ToolCall

	// StopReason, Usage and Err are set on the done event.
	StopReason StopReason
	Usage      Usage
	Err        error
}

// Stream is a response in progress.
//
// Shaped like the standard Go iterator so callers cannot forget to check for an error: Next
// returns false both at the end and on failure, and Err distinguishes them.
type Stream interface {
	// Next advances to the next event, returning false when the stream ends or fails.
	Next() bool
	// Event returns the current event.
	Event() StreamEvent
	// Err returns the failure that ended the stream, or nil if it finished normally.
	Err() error
	// Close releases the stream. Safe to call more than once, and required even on a stream that
	// ended on its own.
	Close() error
}

// ProviderClient talks to one model API.
//
// Named for what it is rather than taking the obvious name, because Provider is already the vendor
// enum on KeyRef. Two things called Provider in one package would be a coin flip at every call
// site.
type ProviderClient interface {
	// Name identifies the provider for display and for attributing usage.
	Name() string

	// Stream sends a request and returns the response as it arrives.
	//
	// Cancelling ctx stops the stream and releases the connection. A cancelled stream still emits
	// a done event carrying StopCancelled and whatever usage was incurred, because work that was
	// paid for has to be accounted for even when it was thrown away.
	Stream(ctx context.Context, req Request) (Stream, error)
}

// ProviderError is a failure from a model API, classified so callers can act on it.
//
// The classification exists because the actions differ completely: a rate limit means wait, a bad
// key means fix the credential, an overload means try elsewhere. "Something went wrong" is the
// agent equivalent of a status nobody can act on.
type ProviderError struct {
	Kind     ProviderErrorKind
	Provider string
	Message  string
	// RetryAfter is how long to wait, when the provider said. Zero means it did not.
	RetryAfter time.Duration
	// StatusCode is the HTTP status, or zero for failures that never reached the server.
	StatusCode int
	Err        error
}

// ProviderErrorKind is the class of a provider failure.
type ProviderErrorKind string

const (
	// ErrAuthentication means the credential was rejected. Never retry, never fall back: a wrong
	// key is a thing to fix, and routing around it hides the problem while billing elsewhere.
	ErrAuthentication ProviderErrorKind = "authentication"
	// ErrRateLimited means slow down. Retryable, and a reasonable trigger for a fallback.
	ErrRateLimited ProviderErrorKind = "rate-limited"
	// ErrOverloaded means the provider is busy. Retryable and a good fallback trigger.
	ErrOverloaded ProviderErrorKind = "overloaded"
	// ErrContextLength means the conversation is too long. Compact rather than retry.
	ErrContextLength ProviderErrorKind = "context-length"
	// ErrInvalidRequest means the request was malformed. Never retryable.
	ErrInvalidRequest ProviderErrorKind = "invalid-request"
	// ErrNetwork means the request never got an answer. Retryable.
	ErrNetwork ProviderErrorKind = "network"
	// ErrCancelled means the caller stopped it.
	ErrCancelled ProviderErrorKind = "cancelled"
	// ErrUnknown is anything unrecognised. Not retried, because retrying something we do not
	// understand is how a loop starts.
	ErrUnknown ProviderErrorKind = "unknown"
)

// Retryable reports whether trying again could plausibly work.
func (k ProviderErrorKind) Retryable() bool {
	switch k {
	case ErrRateLimited, ErrOverloaded, ErrNetwork:
		return true
	default:
		return false
	}
}

// AllowsFallback reports whether this failure should be retried on a different credential.
//
// Deliberately narrower than Retryable. A network failure is worth retrying but says nothing about
// the credential, and an authentication failure must never fall through: silently billing a
// different key because the first one was wrong is dishonest, and it hides the actual problem.
func (k ProviderErrorKind) AllowsFallback() bool {
	return k == ErrRateLimited || k == ErrOverloaded
}

func (e *ProviderError) Error() string {
	if e.Provider != "" {
		return fmt.Sprintf("%s: %s: %s", e.Provider, e.Kind, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *ProviderError) Unwrap() error { return e.Err }

// Retryable reports whether this failure could plausibly succeed on retry.
func (e *ProviderError) Retryable() bool { return e.Kind.Retryable() }

// AllowsFallback reports whether this failure justifies trying a different credential.
func (e *ProviderError) AllowsFallback() bool { return e.Kind.AllowsFallback() }
