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
	// Modes is the mode a conversation is in and the ones it could be switched to.
	Modes(sessionID string) (current string, usable []core.Mode)
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
	// hangUp ends the connection from this side: a client that stops reading is let go rather than
	// left holding a tool that is waiting to ask it something.
	hangUp func()
	// streams are the goroutines following turns for this client, waited for when it goes.
	streams sync.WaitGroup
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

// Serve reads one client's requests until in ends or ctx does. Requests are handled in the order
// they arrive, so a cancel cannot overtake the prompt it cancels; what takes time, a turn streaming,
// runs on beside the reading, so a cancel or a permission answer can arrive while it does. When
// the client goes, its conversations carry on without it.
func (h *Hub) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	h.watchOnce.Do(func() { go h.watch(h.engine.Events(0)) })
	c := &conn{hub: h, out: out, done: make(chan struct{}), pending: map[int64]chan json.RawMessage{}}
	var closeOnce sync.Once
	c.hangUp = func() {
		closeOnce.Do(func() {
			if closer, ok := in.(io.Closer); ok {
				_ = closer.Close()
			}
		})
	}
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.hangUp()
		case <-stop:
		}
	}()
	defer func() {
		close(stop)
		h.mu.Lock()
		c.gone = true
		for id, owner := range h.owners {
			if owner == c {
				delete(h.owners, id)
			}
		}
		h.mu.Unlock()
		close(c.done)
		c.streams.Wait()
	}()
	// Lines are read on their own goroutine, so the server can stop when ctx does even while a read
	// is blocked: closing an inherited stdin does not interrupt a read already waiting on it.
	type read struct {
		line []byte
		err  error
	}
	lines := make(chan read)
	go func() {
		reader := bufio.NewReader(in)
		for {
			line, err := reader.ReadBytes('\n')
			select {
			case lines <- read{line, err}:
			case <-stop:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		var next read
		select {
		case next = <-lines:
		case <-ctx.Done():
			return nil
		}
		if len(strings.TrimSpace(string(next.line))) > 0 {
			var m message
			if json.Unmarshal(next.line, &m) != nil {
				c.reply(json.RawMessage("null"), nil, &rpcError{Code: -32700, Message: "not a JSON-RPC message"})
			} else {
				c.handle(ctx, m)
			}
		}
		if next.err != nil {
			if errors.Is(next.err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return next.err
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
		c.reply(m.ID, map[string]any{"sessionId": id, "modes": h.modes(id)}, nil)
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
	case "_canopy/session":
		// Canopy's own interface, attached, draws a conversation from the whole record rather than
		// from the updates an editor is sent; this is that record, as the engine holds it now, from
		// the turn the client asks for on, so a streaming turn does not resend all the ones before
		// it. Picture bytes are left out and tool output is bounded, as it is for editors: the
		// screen draws neither whole, and a conversation of pictures would otherwise outgrow a line.
		var page struct {
			From int `json:"from"`
		}
		_ = json.Unmarshal(m.Params, &page)
		session, ok := h.engine.Session(p.SessionID)
		if !ok {
			c.reply(m.ID, nil, &rpcError{Code: -32602, Message: "there is no conversation " + p.SessionID})
			return
		}
		total := len(session.Turns)
		from := page.From
		if from < 0 || from > total {
			from = 0
		}
		turns := make([]core.Turn, 0, total-from)
		for _, turn := range session.Turns[from:] {
			turns = append(turns, slimTurn(turn))
		}
		session.Turns = turns
		c.reply(m.ID, map[string]any{"session": session, "modes": h.modes(p.SessionID), "from": from,
			"turnCount": total}, nil)
	default:
		if len(m.ID) > 0 {
			c.reply(m.ID, nil, &rpcError{Code: -32601, Message: "Canopy does not offer " + m.Method})
		}
	}
}

// modes is the mode state a session reports: the modes it can be switched to, and the one it is in.
func (h *Hub) modes(sessionID string) map[string]any {
	current, usable := h.engine.Modes(sessionID)
	available := []any{}
	for _, mode := range usable {
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
	c.reply(id, map[string]any{"modes": h.modes(sessionID)}, nil)
	// Taken only now, so a question that has been waiting for a client arrives after the history it
	// belongs to rather than in the middle of it.
	h.own(sessionID, c)
	if running != "" {
		c.streams.Add(1)
		go func() {
			defer c.streams.Done()
			c.stream(ctx, sessionID, running)
		}()
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

// prompt starts one turn and streams it to the client as it happens, replying when it ends.
func (c *conn) prompt(ctx context.Context, m message) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			URI      string `json:"uri"`
			Name     string `json:"name"`
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
		case "resource_link":
			// A reference without its contents: the model is told where it is and can read it.
			fmt.Fprintf(&text, "\n\n<context uri=%q name=%q />", block.URI, block.Name)
		}
	}
	// Whoever prompts a conversation is there to answer its questions.
	c.hub.own(p.SessionID, c)
	turnID, err := c.hub.engine.Send(p.SessionID, strings.TrimSpace(text.String()))
	if err != nil {
		c.reply(m.ID, nil, &rpcError{Code: -32603, Message: err.Error()})
		return
	}
	c.streams.Add(1)
	go func() {
		defer c.streams.Done()
		turn, ended := c.stream(ctx, p.SessionID, turnID)
		switch {
		case !ended:
		case turn.State == core.TurnFailed:
			// A failure is an error, with the reason, rather than an empty reply that looks finished.
			reason := turn.Error
			if reason == "" {
				reason = "the turn failed"
			}
			c.reply(m.ID, nil, &rpcError{Code: -32603, Message: reason})
		default:
			c.reply(m.ID, map[string]any{"stopReason": stopReason(turn)}, nil)
		}
	}()
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

// ask puts one question to this client, reporting whether it answered. A client that leaves, or
// whose conversation is taken by another client, has not answered, and the question moves on.
func (c *conn) ask(ctx context.Context, req permission.Request, decision permission.Decision) (allowed, answered bool) {
	h := c.hub
	id := c.ids.Add(1)
	answer := make(chan json.RawMessage, 1)
	c.mu.Lock()
	c.pending[id] = answer
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()
	title := req.Tool
	if req.Command != "" {
		title += ": " + req.Command
	}
	callID := req.CallID
	if callID == "" {
		callID = fmt.Sprintf("ask-%d", id)
	}
	c.send(message{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(id)), Method: "session/request_permission"},
		map[string]any{
			"sessionId": req.SessionID,
			"toolCall": map[string]any{"toolCallId": callID, "title": title,
				"kind": toolKind(req.Tool), "status": "pending", "rawInput": json.RawMessage(validJSON([]byte(req.Arguments)))},
			"options": []any{
				map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"},
				map[string]any{"optionId": "reject", "name": "Reject: " + decision.Reason, "kind": "reject_once"},
			},
			// The question whole, for a Canopy client, which draws it as the engine asked it.
			"_meta": map[string]any{"canopy": map[string]any{"request": req, "decision": decision}},
		})
	for {
		h.mu.Lock()
		attached, still := h.attached, h.owners[req.SessionID] == c
		h.mu.Unlock()
		if !still {
			// Withdrawn, so a client still showing it does not take a later answer as the one used.
			c.send(message{JSONRPC: "2.0", Method: "$/cancel_request"}, map[string]any{"requestId": id})
			return false, false
		}
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
			return false, false
		case <-c.done:
			return false, false
		case <-attached:
			// Some conversation changed hands; if it was this one, the loop above lets it go.
		}
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
	if d, ok := c.out.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = d.SetWriteDeadline(time.Now().Add(writeTimeout))
	}
	if _, err := c.out.Write(append(line, '\n')); err != nil && c.hangUp != nil {
		c.hangUp()
	}
}

// writeTimeout is how long a client that has stopped reading is waited for before it is let go.
var writeTimeout = 30 * time.Second

// stopReason is how a turn ended, in the protocol's words.
func stopReason(turn core.Turn) string {
	switch turn.State {
	case core.TurnInterrupted:
		return "cancelled"
	case core.TurnRefused:
		return "refusal"
	case core.TurnTruncated:
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
	case name == "run_command" || name == "shell":
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

// slimTurn is a turn as an attached interface is sent it: pictures without their bytes, and tool
// output bounded as it is for an editor.
func slimTurn(turn core.Turn) core.Turn {
	turn.Request = slimMessage(turn.Request)
	if turn.ToolResults != nil {
		results := make([]core.ToolResult, len(turn.ToolResults))
		for i, result := range turn.ToolResults {
			result.Content = bounded(result.Content)
			results[i] = result
		}
		turn.ToolResults = results
	}
	if turn.Steps != nil {
		steps := make([]core.Message, len(turn.Steps))
		for i, step := range turn.Steps {
			steps[i] = slimMessage(step)
		}
		turn.Steps = steps
	}
	return turn
}

func slimMessage(m core.Message) core.Message {
	if m.Images != nil {
		images := make([]core.Image, len(m.Images))
		for i, image := range m.Images {
			images[i] = core.Image{MediaType: image.MediaType}
		}
		m.Images = images
	}
	if m.ToolResults != nil {
		results := make([]core.ToolResult, len(m.ToolResults))
		for i, result := range m.ToolResults {
			result.Content = bounded(result.Content)
			results[i] = result
		}
		m.ToolResults = results
	}
	return m
}
