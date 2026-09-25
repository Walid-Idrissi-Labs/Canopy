package openai

// OpenAI's Responses API, the transport its reasoning models are built for. Over chat completions a
// reasoning model's thinking is discarded between requests, so every step of a turn reasons again
// from nothing and pays for it; here the reasoning comes back encrypted, rides with the
// conversation as the reply's native content, and is sent back unchanged. The request also names
// the conversation as its prompt cache key, so its requests land where their prefix is cached.
//
// Used for OpenAI's own endpoint only, and only when CANOPY_OPENAI_RESPONSES=on, until it has been
// run against the real service: every other endpoint in this family speaks chat completions.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// ResponsesEnvVar turns the Responses transport on for OpenAI's own endpoint.
const ResponsesEnvVar = "CANOPY_OPENAI_RESPONSES"

// responsesProvider owns the native content this transport produces and replays. Its native content
// is a list of output items, which Message.WithoutReasoning does not recognise and leaves whole after
// a compaction: OpenAI's encrypted reasoning is not bound to the conversation before it, so replaying
// it there is harmless, only a little longer.
const responsesProvider = "openai-responses"

// WithResponses uses the Responses API rather than chat completions.
func WithResponses() Option { return func(c *Client) { c.responses = true } }

type responsesRequest struct {
	Model           string            `json:"model"`
	Instructions    string            `json:"instructions,omitempty"`
	Input           []any             `json:"input"`
	Tools           []responsesTool   `json:"tools,omitempty"`
	Stream          bool              `json:"stream"`
	Store           bool              `json:"store"`
	Include         []string          `json:"include,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	PromptCacheKey  string            `json:"prompt_cache_key,omitempty"`
	Reasoning       *reasoningOptions `json:"reasoning,omitempty"`
}

// reasoningOptions asks for an effort and for a summary of the reasoning as it goes: without the
// summary a reasoning model says nothing for minutes, which is indistinguishable from a stalled
// connection, and the summary is what the transcript shows as thinking.
type reasoningOptions struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	// Strict is off: strict schemas need every property required and no others allowed, and
	// Canopy's tools have optional arguments.
	Strict bool `json:"strict"`
}

func (c *Client) buildResponsesRequest(ctx context.Context, req core.Request) responsesRequest {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}
	out := responsesRequest{
		Model: req.Model, Instructions: req.System, Stream: true, MaxOutputTokens: maxTokens,
		// Nothing is kept on OpenAI's side; the reasoning comes back encrypted instead and travels
		// with the conversation, which keeps it as Canopy's own record rather than theirs.
		Store:          false,
		PromptCacheKey: core.SessionFrom(ctx),
	}
	// Reasoning settings only for the models that reason; the others refuse them outright.
	if reasons(req.Model) {
		out.Include = []string{"reasoning.encrypted_content"}
		out.Reasoning = &reasoningOptions{Summary: "auto", Effort: responsesEffort(req.Effort)}
	}
	for _, msg := range req.Messages {
		for _, result := range msg.ToolResults {
			output := result.Content
			if result.IsError {
				output = "error: " + output
			}
			out.Input = append(out.Input, map[string]any{"type": "function_call_output",
				"call_id": result.CallID, "output": output})
		}
		// A reply this transport produced goes back as it came, reasoning included, less the ids the
		// server gave its items: nothing is stored on its side, so an id refers to nothing there and
		// the request is refused. The call ids that tie results to calls are kept.
		if native := msg.NativeFor(responsesProvider); native != nil {
			var items []map[string]json.RawMessage
			if json.Unmarshal(native, &items) == nil {
				for _, item := range items {
					delete(item, "id")
					out.Input = append(out.Input, item)
				}
				continue
			}
		}
		text := msg.Text
		for i := len(msg.Reports) - 1; i >= 0; i-- {
			text = core.ReportText(msg.Reports[i]) + "\n\n" + text
		}
		if msg.Note != "" {
			text = core.ReminderText(msg.Note) + "\n\n" + text
		}
		if text != "" {
			kind := "input_text"
			if msg.Role == core.RoleAssistant {
				kind = "output_text"
			}
			out.Input = append(out.Input, map[string]any{"role": string(msg.Role),
				"content": []any{map[string]any{"type": kind, "text": text}}})
		}
		for _, call := range msg.ToolCalls {
			out.Input = append(out.Input, map[string]any{"type": "function_call", "call_id": call.ID,
				"name": call.Name, "arguments": string(call.Input)})
		}
	}
	for _, tool := range req.Tools {
		out.Tools = append(out.Tools, responsesTool{Type: "function", Name: tool.Name,
			Description: tool.Description, Parameters: json.RawMessage(tool.InputSchema)})
	}
	return out
}

// responsesEffort maps Canopy's effort onto the API's, whose top is high; empty leaves the model's
// own default.
func responsesEffort(e core.Effort) string {
	switch e {
	case "":
		return ""
	case core.EffortMax, core.EffortXHigh:
		return "high"
	}
	return string(e)
}

func (c *Client) streamResponses(ctx context.Context, req core.Request) (core.Stream, error) {
	body, err := json.Marshal(c.buildResponsesRequest(ctx, req))
	if err != nil {
		return nil, c.fail(core.ErrInvalidRequest, "could not encode the request", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, c.fail(core.ErrInvalidRequest, "could not build the request", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+c.secret.Reveal())
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
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	return &responsesStream{client: c, resp: resp, scanner: scanner,
		stalls: newStallGuard(cancelStream, StallTimeout)}, nil
}

// responsesStream turns the Responses event stream into core events.
type responsesStream struct {
	client   *Client
	resp     *http.Response
	scanner  *bufio.Scanner
	stalls   *stallGuard
	current  core.StreamEvent
	pending  []core.StreamEvent
	err      error
	finished bool
	closed   bool
}

func (s *responsesStream) Next() bool {
	for {
		if len(s.pending) > 0 {
			s.current, s.pending = s.pending[0], s.pending[1:]
			return true
		}
		if s.finished {
			return false
		}
		if !s.scanner.Scan() {
			err := s.scanner.Err()
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			if s.stalls.Fired() {
				err = errors.New("the provider stopped sending anything, so the request was abandoned")
			}
			s.finish(core.StopError, s.client.classifyTransport(err))
			continue
		}
		s.stalls.touch()
		line := s.scanner.Text()
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			s.handle(strings.TrimSpace(data))
		}
	}
}

func (s *responsesStream) handle(data string) {
	var event struct {
		Type     string          `json:"type"`
		Delta    string          `json:"delta"`
		Item     json.RawMessage `json:"item"`
		Response struct {
			Status            string            `json:"status"`
			Output            []json.RawMessage `json:"output"`
			IncompleteDetails *struct {
				Reason string `json:"reason"`
			} `json:"incomplete_details"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Usage struct {
				InputTokens        int `json:"input_tokens"`
				OutputTokens       int `json:"output_tokens"`
				InputTokensDetails struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage"`
		} `json:"response"`
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	if json.Unmarshal([]byte(data), &event) != nil {
		return
	}
	switch event.Type {
	case "response.output_text.delta":
		if event.Delta != "" {
			s.pending = append(s.pending, core.StreamEvent{Kind: core.EventText, Text: event.Delta})
		}
	case "response.reasoning_summary_text.delta":
		if event.Delta != "" {
			s.pending = append(s.pending, core.StreamEvent{Kind: core.EventThinking, Text: event.Delta})
		}
	case "response.completed", "response.incomplete":
		var calls int
		for _, raw := range event.Response.Output {
			var item struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}
			if json.Unmarshal(raw, &item) == nil && item.Type == "function_call" {
				calls++
				s.pending = append(s.pending, core.StreamEvent{Kind: core.EventToolCall, ToolCall: &core.ToolCall{
					ID: item.CallID, Name: item.Name, Input: []byte(item.Arguments)}})
			}
		}
		u := event.Response.Usage
		usage := core.Usage{InputTokens: u.InputTokens - u.InputTokensDetails.CachedTokens,
			CacheReadTokens: u.InputTokensDetails.CachedTokens, OutputTokens: u.OutputTokens}
		stop := core.StopEndTurn
		if event.Type == "response.incomplete" {
			reason := ""
			if event.Response.IncompleteDetails != nil {
				reason = event.Response.IncompleteDetails.Reason
			}
			switch reason {
			case "max_output_tokens":
				stop = core.StopMaxTokens
			case "content_filter":
				stop = core.StopRefusal
			default:
				s.finish(core.StopError, s.client.fail(core.ErrUnknown,
					fmt.Sprintf("the response was left incomplete (%s)", reason), nil))
				return
			}
		} else if calls > 0 {
			stop = core.StopToolUse
		}
		var native *core.Native
		if data, err := json.Marshal(event.Response.Output); err == nil && len(event.Response.Output) > 0 {
			native = &core.Native{Provider: responsesProvider, Data: data}
		}
		s.pending = append(s.pending, core.StreamEvent{Kind: core.EventDone, StopReason: stop, Usage: usage, Native: native})
		s.finished = true
	case "response.failed", "error":
		message, code := event.Message, event.Code
		if event.Response.Error != nil {
			message, code = event.Response.Error.Message, event.Response.Error.Code
		}
		s.finish(core.StopError, s.client.fail(streamErrorKind(code),
			s.client.scrub(fmt.Sprintf("the provider failed the response: %s", message)), nil))
	}
}

func (s *responsesStream) finish(reason core.StopReason, err error) {
	s.finished = true
	s.err = err
	s.pending = append(s.pending, core.StreamEvent{Kind: core.EventDone, StopReason: reason, Err: err})
}

func (s *responsesStream) Event() core.StreamEvent { return s.current }
func (s *responsesStream) Err() error              { return s.err }

func (s *responsesStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.stalls.stop()
	return s.resp.Body.Close()
}

// streamErrorKind classifies an error reported inside the stream by its code, so a conversation
// that outgrew its window is compacted and a rate limit is retried rather than both being unknown.
func streamErrorKind(code string) core.ProviderErrorKind {
	switch code {
	case "context_length_exceeded":
		return core.ErrContextLength
	case "rate_limit_exceeded":
		return core.ErrRateLimited
	case "server_error", "server_is_overloaded":
		return core.ErrOverloaded
	case "invalid_api_key", "invalid_authentication":
		return core.ErrAuthentication
	}
	return core.ErrUnknown
}

// reasons reports whether a model is one of OpenAI's reasoning families, which take reasoning
// settings; gpt-4o and gpt-4.1 are refused with them.
func reasons(model string) bool {
	m := strings.ToLower(model)
	if strings.HasPrefix(m, "gpt-5") || strings.Contains(m, "codex") {
		return true
	}
	return len(m) > 1 && m[0] == 'o' && m[1] >= '0' && m[1] <= '9'
}
