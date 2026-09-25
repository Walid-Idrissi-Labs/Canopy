// Package acpserver is Canopy speaking the Agent Client Protocol as the agent, so an editor that
// drives agents over ACP, Zed among them, can run Canopy's own loop with its keys, modes, sandbox and
// verification behind it. The editor starts `canopy acp` and talks to it over stdin and stdout, one
// JSON-RPC message per line.
package acpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// protocolVersion is the ACP version this server speaks.
const protocolVersion = 1

// Engine is what the server needs of Canopy's engine.
type Engine interface {
	// NewSession starts an agent working in dir and returns its conversation.
	NewSession(ctx context.Context, dir string) (string, error)
	// LoadSession makes a saved conversation live again, if it is not already.
	LoadSession(ctx context.Context, sessionID, dir string) error
	// ListSessions is every conversation live in the engine now.
	ListSessions() []Summary
	// Send starts a turn and returns its id.
	Send(sessionID, text string) (string, error)
	Session(sessionID string) (core.Session, bool)
	Cancel(sessionID string)
	// SetMode switches a conversation to one of Canopy's modes, by name.
	SetMode(sessionID, mode string) error
	// Events tells the server something changed, so it looks again. It is subscribed to once.
	Events(afterSequence uint64) <-chan core.Event
}

// Summary is one conversation in a list.
type Summary struct {
	ID, Title, Dir, Mode string
	Running              bool
}

// Hub serves one engine to any number of clients, one connection each. A conversation belongs to
// the client that started or last loaded it, and that client is asked its questions; a question
// that arrives while no client holds the conversation waits for one to load it. That is what lets
// an agent keep working after its client has gone: nothing it does is tied to the connection.
type Hub struct {
	engine    Engine
	watchOnce sync.Once

	mu sync.Mutex
	// changed is closed, and replaced, whenever the engine reports anything, waking every stream.
	changed chan struct{}
	// attached is closed, and replaced, whenever a conversation is taken by a client.
	attached chan struct{}
	owners   map[string]*conn
	waiting  map[string]int

	// Waiting, when set, is told of a question that has no client to ask.
	Waiting func(sessionID string)
}

// NewHub serves engine.
func NewHub(engine Engine) *Hub {
	return &Hub{engine: engine, changed: make(chan struct{}), attached: make(chan struct{}),
		owners: map[string]*conn{}, waiting: map[string]int{}}
}

// conn is one client.
type conn struct {
	hub    *Hub
	out    io.Writer
	writeM sync.Mutex
	ids    atomic.Int64
	done   chan struct{}
	// gone is set, under the hub's lock, once the client has left, so a request still being handled
	// cannot hand it a conversation afterwards.
	gone bool

	mu      sync.Mutex
	pending map[int64]chan json.RawMessage
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// watch turns the engine's one event stream into a wake-up for every stream running now.
func (h *Hub) watch(events <-chan core.Event) {
	for range events {
		h.mu.Lock()
		close(h.changed)
		h.changed = make(chan struct{})
		h.mu.Unlock()
	}
}

func (h *Hub) nextChange() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.changed
}

// own gives a conversation to a client, waking any question waiting for one.
func (h *Hub) own(sessionID string, c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.gone {
		return
	}
	h.owners[sessionID] = c
	close(h.attached)
	h.attached = make(chan struct{})
}

// Serve reads one client's requests until in ends. Each request runs on its own goroutine, so a
// cancel or a permission answer can arrive while a prompt streams. When the client goes, its
// conversations carry on without it.
func (h *Hub) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	h.watchOnce.Do(func() { go h.watch(h.engine.Events(0)) })
	c := &conn{hub: h, out: out, done: make(chan struct{}), pending: map[int64]chan json.RawMessage{}}
	var wg sync.WaitGroup
	defer func() {
		h.mu.Lock()
		c.gone = true
		for id, owner := range h.owners {
			if owner == c {
				delete(h.owners, id)
			}
		}
		h.mu.Unlock()
		close(c.done)
		wg.Wait()
	}()
	reader := bufio.NewReader(in)
	for {
		line, err := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var m message
			if json.Unmarshal(line, &m) != nil {
				c.reply(nil, nil, &rpcError{Code: -32700, Message: "not a JSON-RPC message"})
			} else {
				wg.Add(1)
				go func() {
					defer wg.Done()
					c.handle(ctx, m)
				}()
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (c *conn) handle(ctx context.Context, m message) {
	h := c.hub
	// An answer to a question this server asked the client.
	if m.Method == "" && len(m.ID) > 0 {
		var id int64
		if json.Unmarshal(m.ID, &id) == nil {
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- m.Result
			}
		}
		return
	}
	var p struct {
		SessionID string `json:"sessionId"`
		Cwd       string `json:"cwd"`
		ModeID    string `json:"modeId"`
	}
	_ = json.Unmarshal(m.Params, &p)
	switch m.Method {
	case "initialize":
		c.reply(m.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"agentCapabilities": map[string]any{
				"loadSession":         true,
				"promptCapabilities":  map[string]any{"image": false, "audio": false, "embeddedContext": true},
				"sessionCapabilities": map[string]any{"list": map[string]any{}},
			},
			"authMethods": []any{},
		}, nil)
	case "authenticate":
		// Canopy's credentials are its own named keys, set up with `canopy keys`; there is nothing
		// for the client to sign in to.
		c.reply(m.ID, map[string]any{}, nil)
	case "session/new":
		id, err := h.engine.NewSession(ctx, p.Cwd)
		if err != nil {
			c.reply(m.ID, nil, &rpcError{Code: -32603, Message: err.Error()})
			return
		}
		h.own(id, c)
		c.reply(m.ID, map[string]any{"sessionId": id, "modes": modes(core.ModeBuild)}, nil)
	case "session/load":
		c.load(ctx, m.ID, p.SessionID, p.Cwd)
	case "session/list":
		c.list(m.ID)
	case "session/set_mode":
		if err := h.engine.SetMode(p.SessionID, p.ModeID); err != nil {
			c.reply(m.ID, nil, &rpcError{Code: -32602, Message: err.Error()})
			return
		}
		c.reply(m.ID, map[string]any{}, nil)
	case "session/prompt":
		c.prompt(ctx, m)
	case "session/cancel":
		h.engine.Cancel(p.SessionID)
	default:
		if len(m.ID) > 0 {
			c.reply(m.ID, nil, &rpcError{Code: -32601, Message: "Canopy does not offer " + m.Method})
		}
	}
}

// modes is the mode state a session reports, offering Canopy's five.
func modes(current string) map[string]any {
	var available []any
	for _, mode := range core.Modes() {
		available = append(available, map[string]any{"id": mode.Name, "name": mode.Name, "description": mode.Description})
	}
	return map[string]any{"currentModeId": current, "availableModes": available}
}

// load takes a conversation, live or saved, replays what it holds so far and, if a turn is still
// running, streams the rest of it.
func (c *conn) load(ctx context.Context, id json.RawMessage, sessionID, dir string) {
	h := c.hub
	if err := h.engine.LoadSession(ctx, sessionID, dir); err != nil {
		c.reply(id, nil, &rpcError{Code: -32603, Message: err.Error()})
		return
	}
	session, ok := h.engine.Session(sessionID)
	if !ok {
		c.reply(id, nil, &rpcError{Code: -32603, Message: "there is no conversation " + sessionID})
		return
	}
	h.own(sessionID, c)
	var running string
	for _, turn := range session.Turns {
		if !turn.State.Terminal() {
			running = turn.ID
			continue
		}
		if turn.Request.Text != "" {
			c.update(sessionID, map[string]any{"sessionUpdate": "user_message_chunk",
				"content": map[string]any{"type": "text", "text": turn.Request.Text}})
		}
		c.replay(sessionID, turn)
	}
	mode := core.ModeBuild
	for _, s := range h.engine.ListSessions() {
		if s.ID == sessionID && s.Mode != "" {
			mode = s.Mode
		}
	}
	c.reply(id, map[string]any{"modes": modes(mode)}, nil)
	if running != "" {
		go c.stream(ctx, sessionID, running)
	}
}

// replay sends a finished turn as the updates it would have streamed.
func (c *conn) replay(sessionID string, turn core.Turn) {
	if turn.Thinking != "" {
		c.update(sessionID, map[string]any{"sessionUpdate": "agent_thought_chunk",
			"content": map[string]any{"type": "text", "text": turn.Thinking}})
	}
	if turn.Text != "" {
		c.update(sessionID, map[string]any{"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{"type": "text", "text": turn.Text}})
	}
	for _, call := range turn.ToolCalls {
		c.update(sessionID, toolCallUpdate(call))
	}
	for _, result := range turn.ToolResults {
		c.update(sessionID, toolResultUpdate(result))
	}
}

// list says what conversations the engine is running, and which are waiting on a person.
func (c *conn) list(id json.RawMessage) {
	h := c.hub
	sessions := []any{}
	for _, s := range h.engine.ListSessions() {
		h.mu.Lock()
		waiting := h.waiting[s.ID] > 0
		_, held := h.owners[s.ID]
		h.mu.Unlock()
		sessions = append(sessions, map[string]any{"sessionId": s.ID, "cwd": s.Dir, "title": s.Title,
			"_meta": map[string]any{"canopy": map[string]any{"mode": s.Mode, "running": s.Running,
				"waiting": waiting, "attached": held}}})
	}
	c.reply(id, map[string]any{"sessions": sessions}, nil)
}

// prompt runs one turn and streams it to the client as it happens.
func (c *conn) prompt(ctx context.Context, m message) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Resource struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"resource"`
		} `json:"prompt"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		c.reply(m.ID, nil, &rpcError{Code: -32602, Message: "the prompt could not be read"})
		return
	}
	var text strings.Builder
	for _, block := range p.Prompt {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "resource":
			// Context the client attached, a file it has open for one, given to the model as such.
			fmt.Fprintf(&text, "\n\n<context uri=%q>\n%s\n</context>", block.Resource.URI, block.Resource.Text)
		}
	}
	// Whoever prompts a conversation is there to answer its questions.
	c.hub.own(p.SessionID, c)
	turnID, err := c.hub.engine.Send(p.SessionID, strings.TrimSpace(text.String()))
	if err != nil {
		c.reply(m.ID, nil, &rpcError{Code: -32603, Message: err.Error()})
		return
	}
	turn, ended := c.stream(ctx, p.SessionID, turnID)
	if ended {
		c.reply(m.ID, map[string]any{"stopReason": stopReason(turn)}, nil)
	}
}

// stream sends what a turn adds, as it adds it, until the turn ends, reporting whether it did. A
// client that goes stops the stream, not the turn.
func (c *conn) stream(ctx context.Context, sessionID, turnID string) (core.Turn, bool) {
	var text, thinking, calls, results int
	for {
		// Taken before looking, so a change made while this pass runs still wakes the next one.
		changed := c.hub.nextChange()
		var turn core.Turn
		if session, ok := c.hub.engine.Session(sessionID); ok {
			for _, t := range session.Turns {
				if t.ID == turnID {
					turn = t
				}
			}
		}
		if len(turn.Thinking) > thinking {
			c.update(sessionID, map[string]any{"sessionUpdate": "agent_thought_chunk",
				"content": map[string]any{"type": "text", "text": turn.Thinking[thinking:]}})
			thinking = len(turn.Thinking)
		}
		if len(turn.Text) > text {
			c.update(sessionID, map[string]any{"sessionUpdate": "agent_message_chunk",
				"content": map[string]any{"type": "text", "text": turn.Text[text:]}})
			text = len(turn.Text)
		}
		for ; calls < len(turn.ToolCalls); calls++ {
			c.update(sessionID, toolCallUpdate(turn.ToolCalls[calls]))
		}
		for ; results < len(turn.ToolResults); results++ {
			c.update(sessionID, toolResultUpdate(turn.ToolResults[results]))
		}
		if turn.State.Terminal() {
			return turn, true
		}
		select {
		case <-ctx.Done():
			return turn, false
		case <-c.done:
			return turn, false
		case <-changed:
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func toolCallUpdate(call core.ToolCall) map[string]any {
	return map[string]any{"sessionUpdate": "tool_call", "toolCallId": call.ID, "title": call.Name,
		"kind": toolKind(call.Name), "status": "in_progress", "rawInput": json.RawMessage(validJSON(call.Input))}
}

func toolResultUpdate(result core.ToolResult) map[string]any {
	status := "completed"
	if result.IsError {
		status = "failed"
	}
	return map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": result.CallID, "status": status,
		"content": []any{map[string]any{"type": "content",
			"content": map[string]any{"type": "text", "text": bounded(result.Content)}}}}
}

// Approve asks the client holding the conversation whether a call may run, which is where a person
// answers the questions Canopy's permission layer asks. It blocks the tool that asked, never a
// stream. With no client holding the conversation, the question waits for one to load it; a client
// that goes without answering hands the question to the next.
func (h *Hub) Approve(ctx context.Context, req permission.Request, decision permission.Decision) bool {
	told := false
	for {
		h.mu.Lock()
		c := h.owners[req.SessionID]
		attached := h.attached
		if c == nil {
			h.waiting[req.SessionID]++
		}
		h.mu.Unlock()
		if c == nil {
			if h.Waiting != nil && !told {
				told = true
				h.Waiting(req.SessionID)
			}
			select {
			case <-attached:
			case <-ctx.Done():
			}
			h.mu.Lock()
			if h.waiting[req.SessionID]--; h.waiting[req.SessionID] <= 0 {
				delete(h.waiting, req.SessionID)
			}
			h.mu.Unlock()
			if ctx.Err() != nil {
				return false
			}
			continue
		}
		if allowed, answered := c.ask(ctx, req, decision); answered || ctx.Err() != nil {
			return allowed
		}
	}
}

// ask puts one question to this client, reporting whether it answered.
func (c *conn) ask(ctx context.Context, req permission.Request, decision permission.Decision) (allowed, answered bool) {
	id := c.ids.Add(1)
	answer := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.pending[id] = answer
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}
	title := req.Tool
	if req.Command != "" {
		title += ": " + req.Command
	}
	c.send(message{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(id)), Method: "session/request_permission"},
		map[string]any{
			"sessionId": req.SessionID,
			"toolCall": map[string]any{"toolCallId": fmt.Sprintf("ask-%d", id), "title": title,
				"kind": toolKind(req.Tool), "status": "pending", "rawInput": json.RawMessage(validJSON([]byte(req.Arguments)))},
			"options": []any{
				map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
				map[string]any{"optionId": "reject", "name": "Reject: " + decision.Reason, "kind": "reject_once"},
			},
		})
	select {
	case raw := <-answer:
		var result struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		return json.Unmarshal(raw, &result) == nil && result.Outcome.Outcome == "selected" &&
			result.Outcome.OptionID == "allow", true
	case <-ctx.Done():
		forget()
		return false, false
	case <-c.done:
		forget()
		return false, false
	}
}

func (c *conn) update(sessionID string, update map[string]any) {
	c.send(message{JSONRPC: "2.0", Method: "session/update"}, map[string]any{"sessionId": sessionID, "update": update})
}

func (c *conn) reply(id json.RawMessage, result any, rpcErr *rpcError) {
	m := message{JSONRPC: "2.0", ID: id, Error: rpcErr}
	if rpcErr == nil {
		raw, _ := json.Marshal(result)
		m.Result = raw
	}
	c.send(m, nil)
}

func (c *conn) send(m message, params any) {
	if params != nil {
		raw, _ := json.Marshal(params)
		m.Params = raw
	}
	line, err := json.Marshal(m)
	if err != nil {
		return
	}
	c.writeM.Lock()
	defer c.writeM.Unlock()
	_, _ = c.out.Write(append(line, '\n'))
}

// stopReason is how a turn ended, in the protocol's words.
func stopReason(turn core.Turn) string {
	switch turn.State {
	case core.TurnInterrupted:
		return "cancelled"
	case core.TurnRefused:
		return "refusal"
	}
	if strings.Contains(turn.Error, "tokens") {
		return "max_tokens"
	}
	return "end_turn"
}

// toolKind sorts Canopy's tools into the kinds an editor draws differently.
func toolKind(name string) string {
	switch {
	case strings.HasPrefix(name, "read") || name == "repo_map" || name == "read_output":
		return "read"
	case strings.HasPrefix(name, "edit") || strings.HasPrefix(name, "write"):
		return "edit"
	case name == "grep" || name == "glob" || strings.HasPrefix(name, "find_"):
		return "search"
	case name == "shell":
		return "execute"
	case name == "fetch_url":
		return "fetch"
	}
	return "other"
}

func validJSON(raw []byte) []byte {
	if json.Valid(raw) {
		return raw
	}
	quoted, _ := json.Marshal(string(raw))
	return quoted
}

// bounded keeps a tool result the editor is sent to a sensible size.
func bounded(s string) string {
	const limit = 8000
	if len(s) > limit {
		return s[:limit] + "\n... (cut off)"
	}
	return s
}
