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
	// Send starts a turn and returns its id.
	Send(sessionID, text string) (string, error)
	Session(sessionID string) (core.Session, bool)
	Cancel(sessionID string)
	// SetMode switches a conversation to one of Canopy's modes, by name.
	SetMode(sessionID, mode string) error
	// Events tells the server something changed, so it looks again. It is subscribed to once.
	Events(afterSequence uint64) <-chan core.Event
}

// Server serves one editor.
type Server struct {
	engine Engine
	out    io.Writer
	writeM sync.Mutex

	ids     atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan json.RawMessage
	// changed is closed, and replaced, whenever the engine reports anything, waking every stream.
	changed chan struct{}
}

// New serves engine to the editor on the other end of out.
func New(engine Engine, out io.Writer) *Server {
	return &Server{engine: engine, out: out, pending: map[int64]chan json.RawMessage{}, changed: make(chan struct{})}
}

// watch turns the engine's one event stream into a wake-up for every prompt streaming now.
func (s *Server) watch(events <-chan core.Event) {
	for range events {
		s.mu.Lock()
		close(s.changed)
		s.changed = make(chan struct{})
		s.mu.Unlock()
	}
}

func (s *Server) nextChange() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changed
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

// Serve reads requests until in ends. Each prompt runs on its own goroutine, so a cancel or a
// permission answer can arrive while it streams.
func (s *Server) Serve(ctx context.Context, in io.Reader) error {
	go s.watch(s.engine.Events(0))
	reader := bufio.NewReader(in)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		line, err := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var m message
			if json.Unmarshal(line, &m) != nil {
				s.reply(nil, nil, &rpcError{Code: -32700, Message: "not a JSON-RPC message"})
			} else {
				wg.Add(1)
				go func() {
					defer wg.Done()
					s.handle(ctx, m)
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

func (s *Server) handle(ctx context.Context, m message) {
	// An answer to a question this server asked the editor.
	if m.Method == "" && len(m.ID) > 0 {
		var id int64
		if json.Unmarshal(m.ID, &id) == nil {
			s.mu.Lock()
			ch := s.pending[id]
			delete(s.pending, id)
			s.mu.Unlock()
			if ch != nil {
				ch <- m.Result
			}
		}
		return
	}
	switch m.Method {
	case "initialize":
		s.reply(m.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"agentCapabilities": map[string]any{
				"loadSession":        false,
				"promptCapabilities": map[string]any{"image": false, "audio": false, "embeddedContext": true},
			},
			"authMethods": []any{},
		}, nil)
	case "authenticate":
		// Canopy's credentials are its own named keys, set up with `canopy keys`; there is nothing
		// for the editor to sign in to.
		s.reply(m.ID, map[string]any{}, nil)
	case "session/new":
		var p struct {
			Cwd string `json:"cwd"`
		}
		_ = json.Unmarshal(m.Params, &p)
		id, err := s.engine.NewSession(ctx, p.Cwd)
		if err != nil {
			s.reply(m.ID, nil, &rpcError{Code: -32603, Message: err.Error()})
			return
		}
		var modes []any
		for _, mode := range core.Modes() {
			modes = append(modes, map[string]any{"id": mode.Name, "name": mode.Name, "description": mode.Description})
		}
		s.reply(m.ID, map[string]any{"sessionId": id,
			"modes": map[string]any{"currentModeId": core.ModeBuild, "availableModes": modes}}, nil)
	case "session/set_mode":
		var p struct {
			SessionID string `json:"sessionId"`
			ModeID    string `json:"modeId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		if err := s.engine.SetMode(p.SessionID, p.ModeID); err != nil {
			s.reply(m.ID, nil, &rpcError{Code: -32602, Message: err.Error()})
			return
		}
		s.reply(m.ID, map[string]any{}, nil)
	case "session/prompt":
		s.prompt(ctx, m)
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		s.engine.Cancel(p.SessionID)
	default:
		if len(m.ID) > 0 {
			s.reply(m.ID, nil, &rpcError{Code: -32601, Message: "Canopy does not offer " + m.Method})
		}
	}
}

// prompt runs one turn and streams it to the editor as it happens.
func (s *Server) prompt(ctx context.Context, m message) {
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
		s.reply(m.ID, nil, &rpcError{Code: -32602, Message: "the prompt could not be read"})
		return
	}
	var text strings.Builder
	for _, block := range p.Prompt {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "resource":
			// Context the editor attached, a file it has open for one, given to the model as such.
			fmt.Fprintf(&text, "\n\n<context uri=%q>\n%s\n</context>", block.Resource.URI, block.Resource.Text)
		}
	}
	turnID, err := s.engine.Send(p.SessionID, strings.TrimSpace(text.String()))
	if err != nil {
		s.reply(m.ID, nil, &rpcError{Code: -32603, Message: err.Error()})
		return
	}
	turn := s.stream(ctx, p.SessionID, turnID)
	s.reply(m.ID, map[string]any{"stopReason": stopReason(turn)}, nil)
}

// stream sends what a turn adds, as it adds it, until the turn ends.
func (s *Server) stream(ctx context.Context, sessionID, turnID string) core.Turn {
	var text, thinking, calls, results int
	for {
		// Taken before looking, so a change made while this pass runs still wakes the next one.
		changed := s.nextChange()
		var turn core.Turn
		if session, ok := s.engine.Session(sessionID); ok {
			for _, t := range session.Turns {
				if t.ID == turnID {
					turn = t
				}
			}
		}
		if len(turn.Thinking) > thinking {
			s.update(sessionID, map[string]any{"sessionUpdate": "agent_thought_chunk",
				"content": map[string]any{"type": "text", "text": turn.Thinking[thinking:]}})
			thinking = len(turn.Thinking)
		}
		if len(turn.Text) > text {
			s.update(sessionID, map[string]any{"sessionUpdate": "agent_message_chunk",
				"content": map[string]any{"type": "text", "text": turn.Text[text:]}})
			text = len(turn.Text)
		}
		for ; calls < len(turn.ToolCalls); calls++ {
			call := turn.ToolCalls[calls]
			s.update(sessionID, map[string]any{"sessionUpdate": "tool_call", "toolCallId": call.ID,
				"title": call.Name, "kind": toolKind(call.Name), "status": "in_progress",
				"rawInput": json.RawMessage(validJSON(call.Input))})
		}
		for ; results < len(turn.ToolResults); results++ {
			result := turn.ToolResults[results]
			status := "completed"
			if result.IsError {
				status = "failed"
			}
			s.update(sessionID, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": result.CallID,
				"status": status, "content": []any{map[string]any{"type": "content",
					"content": map[string]any{"type": "text", "text": bounded(result.Content)}}}})
		}
		if turn.State.Terminal() {
			return turn
		}
		select {
		case <-ctx.Done():
			return turn
		case <-changed:
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// Approve asks the editor whether a call may run, which is where a person using the editor answers
// the questions Canopy's permission layer asks. It blocks the tool that asked, never the stream.
func (s *Server) Approve(ctx context.Context, req permission.Request, decision permission.Decision) bool {
	id := s.ids.Add(1)
	answer := make(chan json.RawMessage, 1)
	s.mu.Lock()
	s.pending[id] = answer
	s.mu.Unlock()
	title := req.Tool
	if req.Command != "" {
		title += ": " + req.Command
	}
	s.send(message{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(id)), Method: "session/request_permission"},
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
			result.Outcome.OptionID == "allow"
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return false
	}
}

func (s *Server) update(sessionID string, update map[string]any) {
	s.send(message{JSONRPC: "2.0", Method: "session/update"}, map[string]any{"sessionId": sessionID, "update": update})
}

func (s *Server) reply(id json.RawMessage, result any, rpcErr *rpcError) {
	m := message{JSONRPC: "2.0", ID: id, Error: rpcErr}
	if rpcErr == nil {
		raw, _ := json.Marshal(result)
		m.Result = raw
	}
	s.send(m, nil)
}

func (s *Server) send(m message, params any) {
	if params != nil {
		raw, _ := json.Marshal(params)
		m.Params = raw
	}
	line, err := json.Marshal(m)
	if err != nil {
		return
	}
	s.writeM.Lock()
	defer s.writeM.Unlock()
	_, _ = s.out.Write(append(line, '\n'))
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
