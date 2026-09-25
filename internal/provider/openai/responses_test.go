package openai

import (
	"context"
	"encoding/json"
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
