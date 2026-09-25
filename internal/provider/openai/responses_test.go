package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A turn over the Responses API: text and a reasoning summary stream in, a function call arrives
// whole, usage separates cached input, and the reply's items, the encrypted reasoning among them,
// ride back on the next request exactly as they came, under the conversation's prompt cache key.
func TestTheResponsesTransportKeepsReasoning(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(v any) {
			b, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(w, "event: x\ndata: %s\n\n", b)
		}
		send(map[string]any{"type": "response.reasoning_summary_text.delta", "delta": "check the file"})
		send(map[string]any{"type": "response.output_text.delta", "delta": "Reading it."})
		send(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed",
			"output": []any{
				map[string]any{"type": "reasoning", "id": "rs_1", "encrypted_content": "ENC-abc", "summary": []any{}},
				map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Reading it."}}},
				map[string]any{"type": "function_call", "call_id": "call_1", "name": "read_file", "arguments": `{"path":"a.go"}`},
			},
			"usage": map[string]any{"input_tokens": 1000, "output_tokens": 50, "input_tokens_details": map[string]any{"cached_tokens": 800}},
		}})
	}))
	defer server.Close()

	client := New(server.URL+"/v1", core.NewSecret("sk-test"), WithResponses())
	ctx := core.WithSession(context.Background(), "session-7")
	first := core.Request{Model: "gpt-5", System: "be brief", Messages: []core.Message{{Role: core.RoleUser, Text: "read a.go"}},
		Tools: []core.ToolDefinition{{Name: "read_file", Description: "reads", InputSchema: []byte(`{"type":"object"}`)}}}
	stream, err := client.Stream(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	var text, thinking string
	var call *core.ToolCall
	var done core.StreamEvent
	for stream.Next() {
		switch e := stream.Event(); e.Kind {
		case core.EventText:
			text += e.Text
		case core.EventThinking:
			thinking += e.Text
		case core.EventToolCall:
			call = e.ToolCall
		case core.EventDone:
			done = e
		}
	}
	_ = stream.Close()
	if text != "Reading it." || thinking != "check the file" || call == nil || call.ID != "call_1" || string(call.Input) != `{"path":"a.go"}` {
		t.Fatalf("text %q thinking %q call %+v", text, thinking, call)
	}
	if done.StopReason != core.StopToolUse || done.Usage.CacheReadTokens != 800 || done.Usage.InputTokens != 200 || done.Native == nil {
		t.Fatalf("done = %+v", done)
	}

	second := first
	second.Messages = append(second.Messages,
		core.Message{Role: core.RoleAssistant, Text: text, ToolCalls: []core.ToolCall{*call}, Native: done.Native},
		core.Message{Role: core.RoleUser, ToolResults: []core.ToolResult{{CallID: "call_1", Content: "package a"}}})
	stream, err = client.Stream(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	for stream.Next() {
	}
	_ = stream.Close()

	mu.Lock()
	defer mu.Unlock()
	body := bodies[1]
	raw, _ := json.Marshal(body["input"])
	input := string(raw)
	if strings.Contains(input, `"rs_1"`) {
		t.Errorf("a replayed item kept its server id, which nothing stored on the server resolves:\n%s", input)
	}
	tools, _ := json.Marshal(body["tools"])
	reasoning, _ := json.Marshal(body["reasoning"])
	if !strings.Contains(string(tools), `"strict":false`) || !strings.Contains(string(reasoning), `"summary":"auto"`) {
		t.Errorf("tools %s reasoning %s", tools, reasoning)
	}
	for _, want := range []string{`"encrypted_content":"ENC-abc"`, `"type":"function_call_output"`, `"call_id":"call_1"`, `"output":"package a"`} {
		if !strings.Contains(input, want) {
			t.Errorf("the second request's input lacks %s:\n%s", want, input)
		}
	}
	calls := 0
	for _, item := range body["input"].([]any) {
		if m, ok := item.(map[string]any); ok && m["type"] == "function_call" {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("the call was sent twice, once natively and once rebuilt:\n%s", input)
	}
	if body["prompt_cache_key"] != "session-7" || body["store"] != false || body["instructions"] != "be brief" {
		t.Errorf("request settings: key %v store %v instructions %v", body["prompt_cache_key"], body["store"], body["instructions"])
	}
}

// sseOnce serves one scripted event and nothing else.
func sseOnce(t *testing.T, event map[string]any) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		b, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	}))
	t.Cleanup(server.Close)
	return New(server.URL, core.NewSecret("sk"), WithResponses())
}

func finalEvent(t *testing.T, c *Client) core.StreamEvent {
	t.Helper()
	stream, err := c.Stream(context.Background(), core.Request{Model: "gpt-5", Messages: []core.Message{{Role: core.RoleUser, Text: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	var last core.StreamEvent
	for stream.Next() {
		last = stream.Event()
	}
	return last
}

// A response left incomplete says why: the length cap and the content filter have their own stops,
// and any other reason is an error that names it rather than a refusal.
func TestIncompleteResponsesAreMappedByReason(t *testing.T) {
	for reason, want := range map[string]core.StopReason{
		"max_output_tokens": core.StopMaxTokens, "content_filter": core.StopRefusal, "something_new": core.StopError,
	} {
		done := finalEvent(t, sseOnce(t, map[string]any{"type": "response.incomplete", "response": map[string]any{
			"status": "incomplete", "incomplete_details": map[string]any{"reason": reason}}}))
		if done.StopReason != want || (want == core.StopError && !strings.Contains(fmt.Sprint(done.Err), reason)) {
			t.Errorf("%s: stop %s err %v", reason, done.StopReason, done.Err)
		}
	}
}

// An error inside the stream is classified by its code, so a conversation too long for the window
// is compacted and a rate limit is retried.
func TestStreamErrorsAreClassified(t *testing.T) {
	for code, want := range map[string]core.ProviderErrorKind{
		"context_length_exceeded": core.ErrContextLength, "rate_limit_exceeded": core.ErrRateLimited, "whatever": core.ErrUnknown,
	} {
		done := finalEvent(t, sseOnce(t, map[string]any{"type": "error", "code": code, "message": "no"}))
		var perr *core.ProviderError
		if !errors.As(done.Err, &perr) || perr.Kind != want {
			t.Errorf("%s: %v", code, done.Err)
		}
	}
}

func TestResponsesEffort(t *testing.T) {
	for in, want := range map[core.Effort]string{"": "", core.EffortLow: "low", core.EffortXHigh: "high", core.EffortMax: "high"} {
		if got := responsesEffort(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
