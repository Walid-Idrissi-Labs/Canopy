package acpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// fakeEngine runs no model: a test moves its one turn along by hand.
type fakeEngine struct {
	mu        sync.Mutex
	turn      core.Turn
	sent      []string
	cancelled []string
	mode      string
	events    chan core.Event
}

func newFakeEngine() *fakeEngine { return &fakeEngine{events: make(chan core.Event, 16)} }

func (f *fakeEngine) NewSession(context.Context, string) (string, error) { return "s1", nil }

func (f *fakeEngine) Send(_, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	f.turn = core.Turn{ID: "t1", State: core.TurnStreaming}
	return "t1", nil
}

func (f *fakeEngine) Session(string) (core.Session, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return core.Session{Turns: []core.Turn{f.turn}}, true
}

func (f *fakeEngine) Cancel(id string) {
	f.edit(func(t *core.Turn) { t.State = core.TurnInterrupted })
	f.mu.Lock()
	f.cancelled = append(f.cancelled, id)
	f.mu.Unlock()
}

func (f *fakeEngine) SetMode(_, mode string) error {
	if mode == "nonsense" {
		return errors.New("unknown mode")
	}
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
	return nil
}

func (f *fakeEngine) Events(uint64) <-chan core.Event { return f.events }

// edit changes the turn and says so, the way the engine publishes an event.
func (f *fakeEngine) edit(change func(*core.Turn)) {
	f.mu.Lock()
	change(&f.turn)
	f.mu.Unlock()
	f.events <- core.Event{}
}

// client is the editor's end of the pipe.
type client struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan map[string]any
}

func start(t *testing.T, engine Engine) (*Server, *client) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	server := New(engine, outW)
	done := make(chan struct{})
	go func() {
		_ = server.Serve(context.Background(), inR)
		close(done)
	}()
	c := &client{t: t, in: inW, lines: make(chan map[string]any, 64)}
	go func() {
		scanner := bufio.NewScanner(outR)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			var m map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
				t.Errorf("the server wrote something that is not JSON: %q", scanner.Text())
				continue
			}
			c.lines <- m
		}
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		<-done
		_ = outW.Close()
	})
	return server, c
}

func (c *client) send(v any) {
	c.t.Helper()
	line, _ := json.Marshal(v)
	if _, err := c.in.Write(append(line, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) next() map[string]any {
	c.t.Helper()
	select {
	case m := <-c.lines:
		return m
	case <-time.After(5 * time.Second):
		c.t.Fatal("the server said nothing")
		return nil
	}
}

// until reads messages up to the reply to id, returning the updates seen on the way and the reply.
func (c *client) until(id float64) ([]map[string]any, map[string]any) {
	c.t.Helper()
	var updates []map[string]any
	for {
		m := c.next()
		if m["method"] == "session/update" {
			updates = append(updates, m["params"].(map[string]any)["update"].(map[string]any))
			continue
		}
		if got, ok := m["id"].(float64); ok && got == id && m["method"] == nil {
			return updates, m
		}
	}
}

func TestInitializeSaysWhatCanopyOffers(t *testing.T) {
	_, c := start(t, newFakeEngine())
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": 1}})
	_, reply := c.until(1)
	result := reply["result"].(map[string]any)
	if result["protocolVersion"] != float64(1) {
		t.Fatalf("protocol version %v, want 1", result["protocolVersion"])
	}
	prompts := result["agentCapabilities"].(map[string]any)["promptCapabilities"].(map[string]any)
	if prompts["embeddedContext"] != true || prompts["image"] != false {
		t.Fatalf("prompt capabilities %v", prompts)
	}
}

func TestANewSessionOffersCanopysModes(t *testing.T) {
	engine := newFakeEngine()
	_, c := start(t, engine)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{"cwd": "/x"}})
	_, reply := c.until(1)
	result := reply["result"].(map[string]any)
	if result["sessionId"] != "s1" {
		t.Fatalf("session %v", result["sessionId"])
	}
	modes := result["modes"].(map[string]any)
	if modes["currentModeId"] != core.ModeBuild || len(modes["availableModes"].([]any)) != len(core.Modes()) {
		t.Fatalf("modes %v", modes)
	}

	c.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/set_mode",
		"params": map[string]any{"sessionId": "s1", "modeId": "plan"}})
	if _, reply := c.until(2); reply["error"] != nil {
		t.Fatalf("set_mode failed: %v", reply["error"])
	}
	engine.mu.Lock()
	mode := engine.mode
	engine.mu.Unlock()
	if mode != "plan" {
		t.Fatalf("mode %q, want plan", mode)
	}
	c.send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "session/set_mode",
		"params": map[string]any{"sessionId": "s1", "modeId": "nonsense"}})
	if _, reply := c.until(3); reply["error"] == nil {
		t.Fatal("an unknown mode was accepted")
	}
}

// A prompt streams the turn as it happens: thinking, text, each call and its result, and the reply
// comes only once the turn has ended.
func TestAPromptStreamsTheTurnAndEndsWithIt(t *testing.T) {
	engine := newFakeEngine()
	_, c := start(t, engine)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "session/prompt", "params": map[string]any{
		"sessionId": "s1", "prompt": []any{
			map[string]any{"type": "text", "text": "fix the bug"},
			map[string]any{"type": "resource", "resource": map[string]any{"uri": "file:///a.go", "text": "package a"}},
		}}})
	// Wait until the turn exists before changing it.
	for {
		engine.mu.Lock()
		n := len(engine.sent)
		engine.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Each change is seen before the next is made, so the stream passes over the turn more than once
	// and a chunk it sent already would show up twice.
	var updates []map[string]any
	seen := func(kind string) {
		t.Helper()
		for {
			m := c.next()
			if m["method"] != "session/update" {
				t.Fatalf("unexpected %v", m)
			}
			u := m["params"].(map[string]any)["update"].(map[string]any)
			updates = append(updates, u)
			if u["sessionUpdate"] == kind {
				return
			}
		}
	}
	engine.edit(func(t *core.Turn) { t.Thinking = "hmm"; t.Text = "Looking" })
	seen("agent_message_chunk")
	engine.edit(func(t *core.Turn) {
		t.Text = "Looking at it."
		t.ToolCalls = []core.ToolCall{{ID: "c1", Name: "read_file", Input: []byte(`{"path":"a.go"}`)}}
	})
	seen("tool_call")
	engine.edit(func(t *core.Turn) {
		t.ToolResults = []core.ToolResult{{CallID: "c1", Content: "package a"}}
		t.State = core.TurnComplete
	})
	rest, reply := c.until(7)
	updates = append(updates, rest...)

	if got := reply["result"].(map[string]any)["stopReason"]; got != "end_turn" {
		t.Fatalf("stop reason %v, want end_turn", got)
	}
	engine.mu.Lock()
	sent := engine.sent[0]
	engine.mu.Unlock()
	if !strings.HasPrefix(sent, "fix the bug") || !strings.Contains(sent, `<context uri="file:///a.go">`) ||
		!strings.Contains(sent, "package a") {
		t.Fatalf("the model was sent %q", sent)
	}
	var text, thought string
	var call, result map[string]any
	calls := 0
	for _, u := range updates {
		switch u["sessionUpdate"] {
		case "agent_message_chunk":
			text += u["content"].(map[string]any)["text"].(string)
		case "agent_thought_chunk":
			thought += u["content"].(map[string]any)["text"].(string)
		case "tool_call":
			call = u
			calls++
		case "tool_call_update":
			result = u
		}
	}
	if text != "Looking at it." || thought != "hmm" {
		t.Fatalf("text %q thought %q: each chunk should be sent once, in order", text, thought)
	}
	if calls != 1 || call["toolCallId"] != "c1" || call["kind"] != "read" ||
		call["rawInput"].(map[string]any)["path"] != "a.go" {
		t.Fatalf("tool call %v", call)
	}
	if result == nil || result["toolCallId"] != "c1" || result["status"] != "completed" {
		t.Fatalf("tool result %v", result)
	}
}

func TestCancelEndsThePromptAsCancelled(t *testing.T) {
	engine := newFakeEngine()
	_, c := start(t, engine)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/prompt", "params": map[string]any{
		"sessionId": "s1", "prompt": []any{map[string]any{"type": "text", "text": "go"}}}})
	for {
		engine.mu.Lock()
		n := len(engine.sent)
		engine.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.send(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": "s1"}})
	_, reply := c.until(1)
	if got := reply["result"].(map[string]any)["stopReason"]; got != "cancelled" {
		t.Fatalf("stop reason %v, want cancelled", got)
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if len(engine.cancelled) != 1 || engine.cancelled[0] != "s1" {
		t.Fatalf("cancelled %v", engine.cancelled)
	}
}

// A question the permission layer asks goes to the editor, and only an explicit allow lets the
// call run: a rejection, a cancelled question or an answer that makes no sense all refuse it.
func TestApprovalIsTheEditorsAnswer(t *testing.T) {
	cases := []struct {
		name   string
		answer map[string]any
		want   bool
	}{
		{"allowed", map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow"}}, true},
		{"rejected", map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "reject"}}, false},
		{"cancelled", map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, false},
		{"garbled", map[string]any{"outcome": "allow"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, c := start(t, newFakeEngine())
			got := make(chan bool, 1)
			go func() {
				got <- server.Approve(context.Background(), permission.Request{SessionID: "s1", Tool: "shell",
					Command: "rm -rf build"}, permission.Decision{Reason: "runs a command"})
			}()
			ask := c.next()
			if ask["method"] != "session/request_permission" {
				t.Fatalf("asked %v", ask)
			}
			params := ask["params"].(map[string]any)
			if params["sessionId"] != "s1" ||
				!strings.Contains(params["toolCall"].(map[string]any)["title"].(string), "rm -rf build") {
				t.Fatalf("the question does not say what would run: %v", params)
			}
			c.send(map[string]any{"jsonrpc": "2.0", "id": ask["id"], "result": tc.answer})
			select {
			case allowed := <-got:
				if allowed != tc.want {
					t.Fatalf("allowed %v, want %v", allowed, tc.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the answer never reached the tool")
			}
		})
	}
}

func TestAnUnansweredQuestionRefusesWhenTheCallIsCancelled(t *testing.T) {
	server, c := start(t, newFakeEngine())
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan bool, 1)
	go func() { got <- server.Approve(ctx, permission.Request{Tool: "shell"}, permission.Decision{}) }()
	_ = c.next()
	cancel()
	select {
	case allowed := <-got:
		if allowed {
			t.Fatal("a cancelled question was taken as yes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled question kept the tool waiting")
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if len(server.pending) != 0 {
		t.Fatalf("the cancelled question is still waiting for an answer: %v", server.pending)
	}
}

func TestAnUnknownMethodIsAnError(t *testing.T) {
	_, c := start(t, newFakeEngine())
	c.send(map[string]any{"jsonrpc": "2.0", "id": 4, "method": "session/load", "params": map[string]any{}})
	_, reply := c.until(4)
	if reply["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("reply %v", reply)
	}
	// A notification it does not know gets no reply at all, as JSON-RPC says.
	c.send(map[string]any{"jsonrpc": "2.0", "method": "something/else"})
	c.send(map[string]any{"jsonrpc": "2.0", "id": 5, "method": "authenticate"})
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case m := <-c.lines:
			if m["id"] != float64(5) || m["error"] != nil {
				t.Fatalf("unexpected %v", m)
			}
		case <-deadline:
			return
		}
	}
}
