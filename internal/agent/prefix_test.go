package agent_test

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

	"github.com/Walid-Idrissi-Labs/Canopy/internal/agent"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
)

// fakeMessages is a Messages API that answers from a script and records every request body.
type fakeMessages struct {
	mu      sync.Mutex
	bodies  [][]byte
	replies []string
}

func (f *fakeMessages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	reply := f.replies[len(f.bodies)-1]
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, reply)
}

func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		var probe struct{ Type string }
		_ = json.Unmarshal([]byte(e), &probe)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", probe.Type, e)
	}
	return b.String()
}

const start = `{"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`

// thinkThenRead thinks, with a signature, and asks to read a file whose input keys are deliberately
// out of alphabetical order: a rebuild that re-serialised the input would sort them.
var thinkThenRead = sse(start,
	`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Read it first."}}`,
	`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG-ONE"}}`,
	`{"type":"content_block_stop","index":0}`,
	`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
	`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Looking."}}`,
	`{"type":"content_block_stop","index":1}`,
	`{"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_1","name":"read_file","input":{}}}`,
	`{"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"zeta\": 1, \"path\": \"a.go\"}"}}`,
	`{"type":"content_block_stop","index":2}`,
	`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":20}}`,
	`{"type":"message_stop"}`)

func answer(text string) string {
	return sse(start,
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"SIG-`+text+`"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"`+text+`"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`)
}

type readTool struct{}

func (readTool) Name() string            { return "read_file" }
func (readTool) Description() string     { return "read a file" }
func (readTool) Kind() core.ToolKind     { return core.ToolRead }
func (readTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (readTool) Run(context.Context, json.RawMessage) (core.ToolResult, error) {
	return core.ToolResult{Content: "package a"}, nil
}

func messagesOf(t *testing.T, body []byte) []json.RawMessage {
	t.Helper()
	var req struct{ Messages []json.RawMessage }
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("decoding a request: %v", err)
	}
	return req.Messages
}

// Current models bind each thinking block to the exact conversation before it and reject or drop it
// if anything there changed. So every request after the first must repeat the previous request's
// messages byte for byte and only append, across tool steps and across a new user turn, and the
// thinking must go back with its signature.
func TestTheConversationIsOnlyEverAppendedTo(t *testing.T) {
	fake := &fakeMessages{replies: []string{thinkThenRead, answer("done"), answer("again")}}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := anthropic.New(core.NewSecret("sk-test"), anthropic.WithBaseURL(srv.URL))
	tools := core.NewToolRegistry()
	if err := tools.Register(readTool{}); err != nil {
		t.Fatal(err)
	}
	loop := &agent.Loop{Client: client, Tools: tools, Trust: core.TrustStandard}

	first := core.Message{Role: core.RoleUser, Text: "read a.go"}
	outcome, err := loop.Run(context.Background(),
		core.Request{Model: "claude-opus-5", Messages: []core.Message{first}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	session := core.Session{ID: "s", Turns: []core.Turn{
		{ID: "1", State: core.TurnComplete, Request: first, Steps: outcome.Messages[1:]},
		{ID: "2", State: core.TurnPending, Request: core.Message{Role: core.RoleUser, Text: "and again"}},
	}}
	if _, err := loop.Run(context.Background(),
		core.Request{Model: "claude-opus-5", Messages: session.History()}, nil); err != nil {
		t.Fatal(err)
	}

	if len(fake.bodies) != 3 {
		t.Fatalf("%d requests, want 3", len(fake.bodies))
	}
	for i := 1; i < len(fake.bodies); i++ {
		before, after := messagesOf(t, fake.bodies[i-1]), messagesOf(t, fake.bodies[i])
		if len(after) <= len(before) {
			t.Fatalf("request %d has %d messages after %d", i+1, len(after), len(before))
		}
		for j := range before {
			if string(stripCache(before[j])) != string(stripCache(after[j])) {
				t.Fatalf("request %d rewrote message %d:\nwas %s\nnow %s", i+1, j, before[j], after[j])
			}
		}
	}
	last := string(fake.bodies[2])
	// The tool input keeps the order it was written in; a rebuild would have sorted "path" first.
	for _, want := range []string{"SIG-ONE", "SIG-done", `{"zeta":1,"path":"a.go"}`} {
		if !strings.Contains(strings.ReplaceAll(last, " ", ""), want) {
			t.Errorf("the third request lost %s:\n%s", want, last)
		}
	}
}

// stripCache removes cache_control markers, which legitimately move between requests.
func stripCache(raw json.RawMessage) string {
	var v any
	_ = json.Unmarshal(raw, &v)
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			delete(t, "cache_control")
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(v)
	out, _ := json.Marshal(v)
	return string(out)
}
