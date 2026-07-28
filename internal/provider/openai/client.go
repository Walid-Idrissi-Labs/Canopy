// Package openai talks to any endpoint speaking the OpenAI chat completions API.
//
// One client covers most of the field: OpenAI itself, Kimi, MiniMax, DeepSeek, Groq, OpenRouter,
// NVIDIA, and local runtimes such as Ollama and LM Studio. The base URL is what distinguishes
// them, which is why it is required rather than defaulted.
//
// Hand rolled rather than built on a vendor SDK, per D-30. The surface needed here is small, and
// pointing an SDK written for one vendor at arbitrary base URLs is the case those SDKs handle
// worst.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// DefaultMaxTokens is the response cap when a request does not set one.
const DefaultMaxTokens = 8192

// Client is an OpenAI compatible provider.
type Client struct {
	baseURL string
	secret  core.Secret
	http    *http.Client
	name    string
}

var _ core.ProviderClient = (*Client)(nil)

// Option customises a client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client. For tests.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithName sets the display name, so usage can be attributed to "kimi" rather than to a URL.
func WithName(name string) Option {
	return func(c *Client) { c.name = name }
}

// New builds a client for an endpoint and credential.
func New(baseURL string, secret core.Secret, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		name:    "openai-compatible",
		http: &http.Client{
			// Generous, because a long reasoning turn legitimately takes minutes. The context is
			// the real cancellation mechanism; this is only a backstop against a wedged connection.
			Timeout: 30 * time.Minute,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Name() string { return c.name }

// Stream sends a request and returns the response as it arrives.
func (c *Client) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	if err := req.Validate(); err != nil {
		return nil, c.fail(core.ErrInvalidRequest, err.Error(), err)
	}
	if c.baseURL == "" {
		return nil, c.fail(core.ErrInvalidRequest,
			"no base URL. This provider is defined by its endpoint, so there is no sensible default", nil)
	}

	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, c.fail(core.ErrInvalidRequest, "could not encode the request", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, c.fail(core.ErrInvalidRequest, "could not build the request", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+c.secret.Reveal())

	// A context of the stream's own, so a watchdog can cancel a request that has gone silent
	// without that being indistinguishable from the caller cancelling it.
	streamCtx, cancelStream := context.WithCancel(ctx)
	httpReq = httpReq.WithContext(streamCtx)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		cancelStream()
		return nil, c.classifyTransport(err)
	}
	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		cancelStream()
		return nil, c.classifyStatus(resp)
	}

	return newStream(ctx, c, resp, newStallGuard(cancelStream, StallTimeout)), nil
}

// chatRequest is the wire shape.
type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []chatMessage `json:"messages"`
	MaxTokens  int           `json:"max_tokens,omitempty"`
	Stream     bool          `json:"stream"`
	StreamOpts *streamOpts   `json:"stream_options,omitempty"`
	Tools      []chatTool    `json:"tools,omitempty"`
}

// streamOpts asks for usage on the final chunk.
//
// Without it most implementations report nothing at all for a streamed request, and a turn with no
// usage cannot be costed or budgeted. Providers that do not recognise the field ignore it.
type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatToolSpec `json:"function"`
}

type chatToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func (c *Client) buildRequest(req core.Request) chatRequest {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}

	out := chatRequest{
		Model:      req.Model,
		MaxTokens:  maxTokens,
		Stream:     true,
		StreamOpts: &streamOpts{IncludeUsage: true},
	}

	if req.System != "" {
		out.Messages = append(out.Messages, chatMessage{Role: "system", Content: req.System})
	}

	for _, msg := range req.Messages {
		// Tool results are their own messages in this API, not blocks inside a user turn, which is
		// the main structural difference from Anthropic.
		for _, result := range msg.ToolResults {
			content := result.Content
			if result.IsError {
				// There is no error flag on a tool message here, so the failure has to be stated in
				// the content or the model reads it as a successful result.
				content = "error: " + content
			}
			out.Messages = append(out.Messages, chatMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: result.CallID,
			})
		}

		if msg.Text == "" && len(msg.ToolCalls) == 0 {
			continue
		}

		wire := chatMessage{Role: string(msg.Role), Content: msg.Text}
		for _, call := range msg.ToolCalls {
			wire.ToolCalls = append(wire.ToolCalls, chatToolCall{
				ID:   call.ID,
				Type: "function",
				Function: chatToolFunction{
					Name:      call.Name,
					Arguments: string(call.Input),
				},
			})
		}
		out.Messages = append(out.Messages, wire)
	}

	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, chatTool{
			Type: "function",
			Function: chatToolSpec{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  json.RawMessage(tool.InputSchema),
			},
		})
	}

	// Effort is deliberately not sent. There is no field for it that every implementation in this
	// family agrees on, and several reject unknown fields outright, so sending one would break
	// exactly the providers this client exists to reach. Recorded rather than silently dropped, so
	// the gap is a decision rather than an oversight.

	// Sampling parameters are deliberately not sent either, for consistency with the Anthropic
	// client. AgentProfile carries a Temperature field and this is the layer that drops it.

	return out
}

// mapFinishReason translates the API's finish reason.
//
// The content filter case is why this is a function. It is this family's refusal, and it arrives on
// a successful response, so mapping it to a normal stop would present a declined request as an
// answered one.
func mapFinishReason(reason string) core.StopReason {
	switch reason {
	case "stop":
		return core.StopEndTurn
	case "length":
		return core.StopMaxTokens
	case "tool_calls", "function_call":
		return core.StopToolUse
	case "content_filter":
		return core.StopRefusal
	case "":
		// A stream that ended without one did not finish. Reporting it as a normal stop would
		// present a truncated answer as complete.
		return core.StopError
	default:
		return core.StopEndTurn
	}
}

func (c *Client) fail(kind core.ProviderErrorKind, message string, err error) *core.ProviderError {
	return &core.ProviderError{
		Kind: kind, Provider: c.Name(), Message: c.scrub(message), Err: err,
	}
}

func (c *Client) classifyTransport(err error) *core.ProviderError {
	switch {
	case err == nil:
		return nil
	case strings.Contains(err.Error(), context.Canceled.Error()):
		return c.fail(core.ErrCancelled, "cancelled", err)
	default:
		return c.fail(core.ErrNetwork, c.scrub(err.Error()), err)
	}
}

// classifyStatus turns a non-200 into an error a caller can act on.
//
// The provider's own message rides along on every branch rather than being replaced by the advice:
// the advice says what to do, the message says what specifically happened, and on the third-party
// endpoints this client serves the message is often the only place the real story is told.
func (c *Client) classifyStatus(resp *http.Response) *core.ProviderError {
	out := &core.ProviderError{Provider: c.Name(), StatusCode: resp.StatusCode}

	// Read once: the body is a stream, and a second branch reading it again would get nothing.
	words := c.scrub(readErrorBody(resp))
	out.RetryAfter = core.ParseRetryAfter(resp.Header.Get("Retry-After"))

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		out.Kind = core.ErrAuthentication
		out.Message = core.WithDetail(
			"the credential was rejected. Check it with `canopy keys test`, or add it again", words)
	case http.StatusNotFound:
		out.Kind = core.ErrInvalidRequest
		out.Message = core.WithDetail("not found. Check the base URL and the model name, since "+
			"this provider is defined by its endpoint", words)
	case http.StatusRequestEntityTooLarge:
		out.Kind = core.ErrContextLength
		out.Message = core.WithDetail(
			"the request is too large. Compact the conversation or shorten the input", words)
	case http.StatusTooManyRequests:
		out.Kind = core.ErrRateLimited
		advice := "rate limited"
		if out.RetryAfter > 0 {
			advice += ", retry in " + out.RetryAfter.String()
		}
		out.Message = core.WithDetail(advice, words)
	case http.StatusBadRequest:
		out.Kind = core.ErrInvalidRequest
		out.Message = words
		if strings.Contains(strings.ToLower(words), "context") ||
			strings.Contains(strings.ToLower(words), "too long") {
			out.Kind = core.ErrContextLength
		}
	default:
		if resp.StatusCode >= 500 {
			out.Kind = core.ErrOverloaded
			out.Message = core.WithDetail(
				fmt.Sprintf("the provider returned %d", resp.StatusCode), words)
		} else {
			out.Kind = core.ErrUnknown
			out.Message = words
		}
	}

	if out.Message == "" {
		out.Message = fmt.Sprintf("the provider returned %d", resp.StatusCode)
	}
	return out
}

// readErrorBody extracts a usable message from an error response.
//
// Bounded, because an endpoint that returns an HTML error page instead of JSON would otherwise put
// a whole document into an error string that later gets rendered in a terminal.
func readErrorBody(resp *http.Response) string {
	const limit = 4096
	buf := make([]byte, limit)
	n, _ := resp.Body.Read(buf)
	if n == 0 {
		return ""
	}
	raw := buf[:n]

	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err == nil {
		if payload.Error.Message != "" {
			return payload.Error.Message
		}
		if payload.Message != "" {
			return payload.Message
		}
	}
	return strings.TrimSpace(string(raw))
}

// scrub removes the credential from text before it leaves this package.
//
// Same reasoning as the Anthropic client: a provider that quotes the rejected key back would
// otherwise put it on screen and into any screenshot of it.
func (c *Client) scrub(text string) string {
	if c.secret.IsZero() {
		return text
	}
	value := c.secret.Reveal()
	if value == "" || !strings.Contains(text, value) {
		return text
	}
	return strings.ReplaceAll(text, value, core.Redacted)
}
