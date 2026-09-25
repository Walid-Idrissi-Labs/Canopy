package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
)

// fakeMessages is an in-process Messages API: it records every request body and answers each
// with the next scripted reply, as server-sent events.
type fakeMessages struct {
	mu      sync.Mutex
	bodies  [][]byte
	replies [][]string
}

func (f *fakeMessages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	var events []string
	if len(f.replies) > 0 {
		events, f.replies = f.replies[0], f.replies[1:]
	}
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var head struct{ Type string }
		_ = json.Unmarshal([]byte(e), &head)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, e)
	}
}

// sse builds one scripted reply: a message start, the given blocks, and the stop.
func sse(stop string, blocks ...[]string) []string {
	out := []string{`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant",` +
		`"model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`}
	for i, block := range blocks {
		out = append(out, fmt.Sprintf(`{"type":"content_block_start","index":%d,"content_block":%s}`, i, block[0]))
		for _, delta := range block[1:] {
			out = append(out, fmt.Sprintf(`{"type":"content_block_delta","index":%d,"delta":%s}`, i, delta))
		}
		out = append(out, fmt.Sprintf(`{"type":"content_block_stop","index":%d}`, i))
	}
	return append(out,
		fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":5}}`, stop),
		`{"type":"message_stop"}`)
}

func thinking(text, signature string) []string {
	return []string{`{"type":"thinking","thinking":"","signature":""}`,
		fmt.Sprintf(`{"type":"thinking_delta","thinking":%q}`, text),
		fmt.Sprintf(`{"type":"signature_delta","signature":%q}`, signature)}
}

func text(s string) []string {
	return []string{`{"type":"text","text":""}`, fmt.Sprintf(`{"type":"text_delta","text":%q}`, s)}
}

func toolUse(id, name, input string) []string {
	return []string{fmt.Sprintf(`{"type":"tool_use","id":%q,"name":%q,"input":{}}`, id, name),
		fmt.Sprintf(`{"type":"input_json_delta","partial_json":%q}`, input)}
}

// cacheControl matches a cache_control marker, which moves to the newest block on every request by
// design and is not part of what the cache matches on. Nothing else is normalised: the rest of each
// request is compared byte for byte.
var cacheControl = regexp.MustCompile(`,?"cache_control":\{[^{}]*\}`)

// The prompt cache pays only when every request begins with the one before it, byte for byte. Over
// a working conversation, tool calls with signed thinking, a mode switch and a second question,
// each request's system and tools are the same as the first's, and its messages begin with the
// previous request's messages exactly, thinking blocks and their signatures included.
func TestEveryRequestExtendsThePreviousOneExactly(t *testing.T) {
	fake := &fakeMessages{replies: [][]string{
		sse("tool_use", thinking("look first", "sig-1"), text("Reading."), toolUse("toolu_1", "reader", `{"path":"a"}`)),
		sse("tool_use", thinking("now write", "sig-2"), toolUse("toolu_2", "writer", `{"path":"a"}`)),
		sse("end_turn", text("Done.")),
		sse("end_turn", thinking("second", "sig-3"), text("Also done.")),
	}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	client := anthropic.New(core.NewSecret("test-key"), anthropic.WithBaseURL(server.URL))
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)
	registry := core.NewToolRegistry()
	registry.MustRegister(&kindTool{name: "reader", kind: core.ToolRead})
	registry.MustRegister(&kindTool{name: "writer", kind: core.ToolWrite})
	e.WithTools(registry, core.TrustBroad, nil)
	e.WithInstructions("<instructions source=\"AGENTS.md\">\nuse tabs\n</instructions>")

	created := e.Create("claude", "claude-opus-5")
	first, err := e.Send(created.ID, "fix it")
	if err != nil {
		t.Fatal(err)
	}
	if turn := waitForTurn(t, e, created.ID, first); turn.State != core.TurnComplete {
		t.Fatalf("the first turn ended %s: %s", turn.State, turn.Error)
	}
	plan, _ := core.ModeByName("plan")
	if err := e.SetMode(created.ID, plan); err != nil {
		t.Fatal(err)
	}
	second, err := e.Send(created.ID, "and the other one")
	if err != nil {
		t.Fatal(err)
	}
	if turn := waitForTurn(t, e, created.ID, second); turn.State != core.TurnComplete {
		t.Fatalf("the second turn ended %s: %s", turn.State, turn.Error)
	}

	fake.mu.Lock()
	bodies := fake.bodies
	fake.mu.Unlock()
	if len(bodies) != 4 {
		t.Fatalf("%d requests, want 4", len(bodies))
	}
	type request struct {
		System   json.RawMessage   `json:"system"`
		Tools    json.RawMessage   `json:"tools"`
		Messages []json.RawMessage `json:"messages"`
	}
	canonical := func(raw json.RawMessage) string {
		return cacheControl.ReplaceAllString(string(raw), "")
	}
	var previous request
	for i, body := range bodies {
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if i > 0 {
			if canonical(req.System) != canonical(previous.System) {
				t.Errorf("request %d changed the system prompt", i)
			}
			if canonical(req.Tools) != canonical(previous.Tools) {
				t.Errorf("request %d changed the tools", i)
			}
			if len(req.Messages) <= len(previous.Messages) {
				t.Fatalf("request %d has %d messages after %d", i, len(req.Messages), len(previous.Messages))
			}
			for j, m := range previous.Messages {
				if canonical(req.Messages[j]) != canonical(m) {
					t.Errorf("request %d rewrote message %d:\nwas %s\nnow %s", i, j, canonical(m),
						canonical(req.Messages[j]))
				}
			}
		}
		previous = req
	}
	last := bodies[len(bodies)-1]
	for _, signature := range []string{"sig-1", "sig-2"} {
		if !bytes.Contains(last, []byte(signature)) {
			t.Errorf("the thinking signed %s was not replayed", signature)
		}
	}
	if !strings.Contains(string(last), "use tabs") {
		t.Error("the instructions did not reach the system prompt")
	}
}
