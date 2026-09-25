package acpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
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
	// history is finished turns ahead of the current one.
	history []core.Turn
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{events: make(chan core.Event, 16), turn: core.Turn{ID: "t0", State: core.TurnComplete}}
}

func (f *fakeEngine) NewSession(context.Context, string) (string, error) { return "s1", nil }

func (f *fakeEngine) LoadSession(_ context.Context, id, _ string) error {
	if id != "s1" {
		return errors.New("no such conversation")
	}
	return nil
}

func (f *fakeEngine) ListSessions() []Summary {
	f.mu.Lock()
	defer f.mu.Unlock()
	return []Summary{{ID: "s1", Title: "the bug", Dir: "/x", Mode: "plan", Running: !f.turn.State.Terminal()}}
}

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
	return core.Session{Turns: append(append([]core.Turn(nil), f.history...), f.turn)}, true
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

func (f *fakeEngine) Modes(string) (string, []core.Mode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	current := f.mode
	if current == "" {
		current = core.ModeBuild
	}
	// Runway and cruise need what this engine does not have, as they do under canopy acp.
	var usable []core.Mode
	for _, mode := range core.Modes() {
		if mode.Name != core.ModeRunway && mode.Name != core.ModeCruise {
			usable = append(usable, mode)
		}
	}
	return current, usable
}

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
	done  chan struct{}
}

func start(t *testing.T, engine Engine) (*Hub, *client) {
	t.Helper()
	hub := NewHub(engine)
	return hub, connect(t, hub)
}

// connect is one more client of the same hub.
func connect(t *testing.T, hub *Hub) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() {
		_ = hub.Serve(context.Background(), inR, outW)
		close(done)
	}()
	c := &client{t: t, in: inW, lines: make(chan map[string]any, 64), done: done}
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
		c.leave()
		_ = outW.Close()
	})
	return c
}

// leave hangs up, and returns once the hub has noticed.
func (c *client) leave() {
	_ = c.in.Close()
	<-c.done
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
	// Only the modes this conversation could be switched to are offered.
	if modes["currentModeId"] != core.ModeBuild || len(modes["availableModes"].([]any)) != 3 {
		t.Fatalf("modes %v", modes)
	}
	for _, offered := range modes["availableModes"].([]any) {
		if id := offered.(map[string]any)["id"]; id == core.ModeRunway || id == core.ModeCruise {
			t.Fatalf("%v is offered where it cannot be used", id)
		}
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
		{"cancelled naming allow", map[string]any{"outcome": map[string]any{"outcome": "cancelled", "optionId": "allow"}}, false},
		{"an option never offered", map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow_always"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hub, c := start(t, newFakeEngine())
			c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{}})
			_, _ = c.until(1)
			got := make(chan bool, 1)
			go func() {
				got <- hub.Approve(context.Background(), permission.Request{SessionID: "s1", Tool: "shell",
					CallID: "call-9", Command: "rm -rf build"}, permission.Decision{Reason: "runs a command"})
			}()
			ask := c.next()
			if ask["method"] != "session/request_permission" {
				t.Fatalf("asked %v", ask)
			}
			params := ask["params"].(map[string]any)
			// On the call it is about, so an editor draws the question on that call's card.
			if params["sessionId"] != "s1" || params["toolCall"].(map[string]any)["toolCallId"] != "call-9" ||
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
	hub, c := start(t, newFakeEngine())
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{}})
	_, _ = c.until(1)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan bool, 1)
	go func() {
		got <- hub.Approve(ctx, permission.Request{SessionID: "s1", Tool: "shell"}, permission.Decision{})
	}()
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
	hub.mu.Lock()
	owner := hub.owners["s1"]
	hub.mu.Unlock()
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if len(owner.pending) != 0 {
		t.Fatalf("the cancelled question is still waiting for an answer: %v", owner.pending)
	}
}

func TestAnUnknownMethodIsAnError(t *testing.T) {
	_, c := start(t, newFakeEngine())
	c.send(map[string]any{"jsonrpc": "2.0", "id": 4, "method": "session/fork", "params": map[string]any{}})
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

// waitSent waits until the engine has been sent a prompt.
func waitSent(engine *fakeEngine) {
	for {
		engine.mu.Lock()
		n := len(engine.sent)
		engine.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The point of serve: a client that goes leaves its agent working, and one that comes back later
// sees what it did, streams the rest, and answers the question it left waiting.
func TestAConversationOutlivesItsClient(t *testing.T) {
	engine := newFakeEngine()
	engine.history = []core.Turn{{ID: "t-old", State: core.TurnComplete,
		Request: core.Message{Text: "first ask"}, Text: "first answer",
		ToolCalls:   []core.ToolCall{{ID: "c0", Name: "grep", Input: []byte(`{}`)}},
		ToolResults: []core.ToolResult{{CallID: "c0", Content: "3 hits"}}}}
	var told []string
	var toldMu sync.Mutex
	hub, first := start(t, engine)
	hub.Waiting = func(id string) {
		toldMu.Lock()
		told = append(told, id)
		toldMu.Unlock()
	}
	first.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{}})
	_, _ = first.until(1)
	first.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/prompt", "params": map[string]any{
		"sessionId": "s1", "prompt": []any{map[string]any{"type": "text", "text": "go"}}}})
	waitSent(engine)
	engine.edit(func(t *core.Turn) { t.Text = "Working" })
	first.leave()

	engine.mu.Lock()
	cancelled := len(engine.cancelled)
	engine.mu.Unlock()
	if cancelled != 0 {
		t.Fatal("the client leaving cancelled its agent's turn")
	}

	// A question with nobody to ask waits, and says so.
	answer := make(chan bool, 1)
	go func() {
		answer <- hub.Approve(context.Background(), permission.Request{SessionID: "s1", Tool: "shell",
			Command: "make"}, permission.Decision{Reason: "runs a command"})
	}()
	for {
		toldMu.Lock()
		n := len(told)
		toldMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	second := connect(t, hub)
	second.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/list"})
	_, reply := second.until(1)
	listed := reply["result"].(map[string]any)["sessions"].([]any)[0].(map[string]any)
	meta := listed["_meta"].(map[string]any)["canopy"].(map[string]any)
	if listed["sessionId"] != "s1" || meta["waiting"] != true || meta["attached"] != false || meta["running"] != true {
		t.Fatalf("listed %v", listed)
	}
	engine.edit(func(t *core.Turn) { t.Text = "Working on it" })
	engine.mu.Lock()
	engine.mode = "plan"
	engine.mu.Unlock()

	second.send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/load",
		"params": map[string]any{"sessionId": "s1", "cwd": "/x", "mcpServers": []any{}}})
	// History comes before the reply to the load; the running turn and the waiting question after.
	var replayed []map[string]any
	var ask map[string]any
	var text string
	loaded := false
	for !loaded || ask == nil {
		m := second.next()
		switch {
		case m["method"] == "session/update" && loaded:
			u := m["params"].(map[string]any)["update"].(map[string]any)
			if u["sessionUpdate"] == "agent_message_chunk" {
				text += u["content"].(map[string]any)["text"].(string)
			}
		case m["method"] == "session/update":
			replayed = append(replayed, m["params"].(map[string]any)["update"].(map[string]any))
		case m["method"] == "session/request_permission":
			if !loaded {
				t.Fatal("the waiting question arrived before the history it belongs to")
			}
			ask = m
		case m["id"] == float64(2):
			if m["error"] != nil {
				t.Fatalf("load failed: %v", m)
			}
			if m["result"].(map[string]any)["modes"].(map[string]any)["currentModeId"] != "plan" {
				t.Fatalf("the loaded conversation's mode was not reported: %v", m)
			}
			loaded = true
		}
	}
	if len(replayed) < 4 || replayed[0]["sessionUpdate"] != "user_message_chunk" ||
		replayed[1]["content"].(map[string]any)["text"] != "first answer" ||
		replayed[2]["sessionUpdate"] != "tool_call" || replayed[3]["status"] != "completed" {
		t.Fatalf("the finished turn was not replayed: %v", replayed)
	}
	second.send(map[string]any{"jsonrpc": "2.0", "id": ask["id"],
		"result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow"}}})
	select {
	case ok := <-answer:
		if !ok {
			t.Fatal("the answer from the returning client was lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting question never reached the returning client")
	}

	// The turn still running streams on to the client that loaded it.
	engine.edit(func(t *core.Turn) { t.Text = "Working on it. Done."; t.State = core.TurnComplete })
	for !strings.HasSuffix(text, "Done.") {
		m := second.next()
		if m["method"] == "session/update" {
			u := m["params"].(map[string]any)["update"].(map[string]any)
			if u["sessionUpdate"] == "agent_message_chunk" {
				text += u["content"].(map[string]any)["text"].(string)
			}
		}
	}
	if text != "Working on it. Done." {
		t.Fatalf("the running turn streamed %q", text)
	}
	toldMu.Lock()
	defer toldMu.Unlock()
	if len(told) != 1 {
		t.Fatalf("the waiting question was announced %d times", len(told))
	}
}

// A client that goes while a question is open to it has not answered it: the question moves to
// the next client rather than being taken as a refusal or lost.
func TestAQuestionMovesOnWhenItsClientLeaves(t *testing.T) {
	hub, first := start(t, newFakeEngine())
	first.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{}})
	_, _ = first.until(1)
	answer := make(chan bool, 1)
	go func() {
		answer <- hub.Approve(context.Background(), permission.Request{SessionID: "s1", Tool: "shell"},
			permission.Decision{})
	}()
	if m := first.next(); m["method"] != "session/request_permission" {
		t.Fatalf("asked %v", m)
	}
	first.leave()
	select {
	case <-answer:
		t.Fatal("a question was settled by its client leaving")
	case <-time.After(100 * time.Millisecond):
	}
	second := connect(t, hub)
	second.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/load",
		"params": map[string]any{"sessionId": "s1"}})
	for {
		m := second.next()
		if m["method"] == "session/request_permission" {
			second.send(map[string]any{"jsonrpc": "2.0", "id": m["id"],
				"result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "reject"}}})
			break
		}
	}
	select {
	case ok := <-answer:
		if ok {
			t.Fatal("a rejection was taken as yes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question never reached the second client")
	}
}

func TestLoadingAnUnknownConversationIsAnError(t *testing.T) {
	_, c := start(t, newFakeEngine())
	c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/load", "params": map[string]any{"sessionId": "nope"}})
	if _, reply := c.until(1); reply["error"] == nil {
		t.Fatalf("reply %v", reply)
	}
}

// A request still being handled when its client leaves must not hand that client a conversation:
// a question sent to it could never be answered, and would be sent again and again.
func TestAClientThatHasLeftIsNeverGivenAConversation(t *testing.T) {
	hub := NewHub(newFakeEngine())
	gone := &conn{hub: hub, done: make(chan struct{}), pending: map[int64]chan json.RawMessage{}, gone: true}
	close(gone.done)
	hub.own("s1", gone)
	hub.mu.Lock()
	owner := hub.owners["s1"]
	hub.mu.Unlock()
	if owner != nil {
		t.Fatal("a client that had left was given the conversation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if hub.Approve(ctx, permission.Request{SessionID: "s1"}, permission.Decision{}) {
		t.Fatal("a question with nobody to answer it was taken as yes")
	}
}

// A conversation taken by another client takes its open question with it, even while the first
// client is still connected: the person who just picked it up is the one who can answer.
func TestAQuestionFollowsItsConversationToANewClient(t *testing.T) {
	hub, first := start(t, newFakeEngine())
	first.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/new", "params": map[string]any{}})
	_, _ = first.until(1)
	answer := make(chan bool, 1)
	go func() {
		answer <- hub.Approve(context.Background(), permission.Request{SessionID: "s1", Tool: "shell"},
			permission.Decision{})
	}()
	asked := first.next()
	if asked["method"] != "session/request_permission" {
		t.Fatalf("asked %v", asked)
	}
	second := connect(t, hub)
	second.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/load", "params": map[string]any{"sessionId": "s1"}})
	// The first client is told the question is no longer its to answer.
	for {
		m := first.next()
		if m["method"] == "$/cancel_request" {
			if m["params"].(map[string]any)["requestId"] != asked["id"] {
				t.Fatalf("withdrew %v, but the question was %v", m["params"], asked["id"])
			}
			break
		}
	}
	for {
		m := second.next()
		if m["method"] == "session/request_permission" {
			second.send(map[string]any{"jsonrpc": "2.0", "id": m["id"],
				"result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "allow"}}})
			break
		}
	}
	select {
	case ok := <-answer:
		if !ok {
			t.Fatal("the new client's allow was not taken")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the question stayed with the client that no longer holds the conversation")
	}
}

// Stopping the server ends every connection, even one whose client is still there.
func TestStoppingEndsAConnectedClient(t *testing.T) {
	hub := NewHub(newFakeEngine())
	inR, _ := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = hub.Serve(ctx, inR, io.Discard)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a connected client kept the server from stopping")
	}
}

// A client that stops reading is let go rather than holding up the tool that wants to ask it.
func TestAClientThatStopsReadingIsLetGo(t *testing.T) {
	defer func(was time.Duration) { writeTimeout = was }(writeTimeout)
	writeTimeout = 50 * time.Millisecond
	hub := NewHub(newFakeEngine())
	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		_ = hub.Serve(context.Background(), server, server)
		close(done)
	}()
	// Start a session, then never read again: the reply to it is never taken off the pipe.
	_, _ = client.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{}}` + "\n"))
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a client that stopped reading was never let go")
	}
	_ = client.Close()
}

// A cancel sent straight after its prompt is not overtaken by it.
func TestACancelRightAfterItsPromptCancelsIt(t *testing.T) {
	engine := newFakeEngine()
	_, c := start(t, engine)
	prompt, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/prompt", "params": map[string]any{
		"sessionId": "s1", "prompt": []any{map[string]any{"type": "text", "text": "go"}}}})
	cancel, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": "s1"}})
	if _, err := c.in.Write([]byte(string(prompt) + "\n" + string(cancel) + "\n")); err != nil {
		t.Fatal(err)
	}
	_, reply := c.until(1)
	if got := reply["result"].(map[string]any)["stopReason"]; got != "cancelled" {
		t.Fatalf("stop reason %v, want cancelled", got)
	}
}

// A failed turn is an error with its reason, not an empty reply that looks finished; a turn cut
// off at the output limit says so.
func TestHowATurnEndedIsReported(t *testing.T) {
	for _, tc := range []struct {
		state core.TurnState
		want  string
	}{{core.TurnFailed, "error: the key was refused"}, {core.TurnTruncated, "max_tokens"}, {core.TurnRefused, "refusal"}} {
		t.Run(string(tc.state), func(t *testing.T) {
			engine := newFakeEngine()
			_, c := start(t, engine)
			c.send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "session/prompt", "params": map[string]any{
				"sessionId": "s1", "prompt": []any{map[string]any{"type": "resource_link", "uri": "file:///a.go", "name": "a.go"}}}})
			waitSent(engine)
			engine.edit(func(t *core.Turn) { t.State, t.Error = tc.state, "the key was refused" })
			_, reply := c.until(1)
			got := ""
			if e, ok := reply["error"].(map[string]any); ok {
				got = "error: " + e["message"].(string)
			} else {
				got = reply["result"].(map[string]any)["stopReason"].(string)
			}
			if got != tc.want {
				t.Fatalf("reported %q, want %q", got, tc.want)
			}
			engine.mu.Lock()
			sent := engine.sent[0]
			engine.mu.Unlock()
			if !strings.Contains(sent, `<context uri="file:///a.go" name="a.go" />`) {
				t.Fatalf("a linked resource did not reach the model: %q", sent)
			}
		})
	}
}

// A line that is not JSON gets an error whose id is null, as JSON-RPC requires.
func TestALineThatIsNotJSONIsAnError(t *testing.T) {
	_, c := start(t, newFakeEngine())
	if _, err := c.in.Write([]byte("not json\n")); err != nil {
		t.Fatal(err)
	}
	m := c.next()
	if id, present := m["id"]; !present || id != nil || m["error"].(map[string]any)["code"] != float64(-32700) {
		t.Fatalf("reply %v", m)
	}
}

// blockingReader is a stdin nobody writes to and that closing does not wake.
type blockingReader struct{ never chan struct{} }

func (b blockingReader) Read([]byte) (int, error) {
	<-b.never
	return 0, io.EOF
}

// A signal stops canopy acp even while its stdin is open and silent.
func TestStoppingDoesNotWaitForARead(t *testing.T) {
	hub := NewHub(newFakeEngine())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = hub.Serve(ctx, blockingReader{never: make(chan struct{})}, io.Discard)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the server waited for a read that was never going to come")
	}
}
