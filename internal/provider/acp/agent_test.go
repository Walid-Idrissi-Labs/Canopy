package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A fake ACP agent, in process, so that not one test in this package needs Claude Code installed or
// a subscription to bill.
//
// It is a real JSON-RPC peer over a real pair of pipes rather than a mock of the client's own
// methods, which is the only version worth having: the thing most likely to be wrong about a
// protocol adapter is what it puts on the wire, and a mock that is handed Go structs never reads a
// byte of that. Every test below asserts against the frames that actually crossed.
//
// The one thing it deliberately does not do is check that Canopy's messages are valid ACP against the
// published schema. That is what the live test is for, and it is skipped by default for the reason
// internal/session/live_test.go gives: a scripted peer is written from the same understanding as the
// code it exercises, so if that understanding is wrong they are wrong together.

// agent is a scripted ACP agent on the other end of a client's pipes.
type agent struct {
	t *testing.T

	// protocolVersion is what the handshake answers with. Overridden to prove the mismatch path.
	protocolVersion int

	// configOptions is what session/new advertises.
	configOptions []configOption

	// script runs once the prompt arrives, and is where a test says what the turn does.
	script func(a *agent)

	// authRequired makes session/new refuse the way a logged-out bridge does.
	authRequired bool

	in  *bufio.Reader
	out io.Writer

	mu       sync.Mutex
	received []message
	stopped  bool

	sessionID string
	promptID  int64
	done      chan struct{}
}

// launch wires an agent to a client and returns the option that makes the client use it.
//
// Two pipes and a goroutine, which is what a subprocess is minus the subprocess. The returned
// process carries a stop function so Close has something real to close, and stopping it closes the
// agent's side, which is exactly how a killed bridge looks to the client.
func (a *agent) launch(c *Client) {
	c.launch = func(ctx context.Context) (*process, error) {
		toAgent, fromClient := io.Pipe()
		toClient, fromAgent := io.Pipe()

		a.in = bufio.NewReader(toAgent)
		a.out = fromAgent
		a.done = make(chan struct{})
		if a.protocolVersion == 0 {
			a.protocolVersion = protocolVersion
		}

		go a.serve()

		return &process{
			stdin:  fromClient,
			stdout: toClient,
			stderr: &bounded{limit: 1024},
			stop: func() {
				a.mu.Lock()
				a.stopped = true
				a.mu.Unlock()
				_ = fromClient.Close()
				_ = fromAgent.Close()
			},
		}, nil
	}
}

func (a *agent) serve() {
	defer close(a.done)
	for {
		line, err := a.in.ReadBytes('\n')
		if err != nil {
			return
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			return
		}

		a.mu.Lock()
		a.received = append(a.received, m)
		a.mu.Unlock()

		if !a.handle(m) {
			return
		}
	}
}

// handle answers one client message, reporting whether the agent should keep listening.
func (a *agent) handle(m message) bool {
	switch m.Method {
	case methodInitialize:
		a.respond(*m.ID, initializeResult{
			ProtocolVersion: a.protocolVersion,
			AgentInfo:       &implementation{Name: "fake-bridge", Version: "0.0.1"},
		})

	case methodSessionNew:
		if a.authRequired {
			a.fail(*m.ID, authRequiredCode, "Not logged in. Please run /login")
			return true
		}
		a.sessionID = "session-1"
		a.respond(*m.ID, newSessionResult{SessionID: a.sessionID, ConfigOptions: a.configOptions})

	case methodSetConfigOption:
		a.respond(*m.ID, map[string]any{"configOptions": a.configOptions})

	case methodSessionPrompt:
		a.promptID = *m.ID
		if a.script != nil {
			a.script(a)
		}

	case methodSessionCancel:
		a.end(stopCancelled, nil)

	default:
		if m.ID != nil && m.Method != "" {
			a.fail(*m.ID, methodNotFound, "no such method")
		}
	}
	return true
}

func (a *agent) respond(id int64, result any) {
	a.write(message{JSONRPC: "2.0", ID: &id, Result: encode(result)})
}

func (a *agent) fail(id int64, code int, text string) {
	a.write(message{JSONRPC: "2.0", ID: &id, Error: &rpcError{Code: code, Message: text}})
}

// say sends a session/update notification carrying one variant.
func (a *agent) say(update map[string]any) {
	a.write(message{JSONRPC: "2.0", Method: methodSessionUpdate, Params: encode(sessionNotification{
		SessionID: a.sessionID,
		Update:    encode(update),
	})})
}

func (a *agent) text(chunk string) {
	a.say(map[string]any{
		"sessionUpdate": updateAgentMessageChunk,
		"content":       map[string]any{"type": "text", "text": chunk},
	})
}

func (a *agent) thought(chunk string) {
	a.say(map[string]any{
		"sessionUpdate": updateAgentThoughtChunk,
		"content":       map[string]any{"type": "text", "text": chunk},
	})
}

// ask sends a permission request and returns the outcome the client chose.
//
// Synchronous on the agent's own goroutine, which is what a real bridge does too: it cannot proceed
// with the tool call until the client has answered.
func (a *agent) ask(title string, options []permissionOption) permissionOutcome {
	id := int64(9000)
	a.write(message{JSONRPC: "2.0", ID: &id, Method: methodRequestPermission,
		Params: encode(permissionParams{
			SessionID: a.sessionID,
			ToolCall:  toolCallUpdate{ToolCallID: "call-1", Title: title, Kind: "edit"},
			Options:   options,
		})})

	for {
		line, err := a.in.ReadBytes('\n')
		if err != nil {
			return permissionOutcome{}
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			return permissionOutcome{}
		}
		a.mu.Lock()
		a.received = append(a.received, m)
		a.mu.Unlock()

		if m.ID != nil && *m.ID == id {
			var result permissionResult
			_ = json.Unmarshal(m.Result, &result)
			return result.Outcome
		}
		if !a.handle(m) {
			return permissionOutcome{}
		}
	}
}

// end answers the prompt, which is the last thing the agent owes.
func (a *agent) end(reason string, usage *promptUsage) {
	a.respond(a.promptID, promptResult{StopReason: reason, Usage: usage})
}

func (a *agent) write(m message) {
	raw, err := json.Marshal(m)
	if err != nil {
		a.t.Errorf("the fake agent could not encode a message: %v", err)
		return
	}
	if _, err := a.out.Write(append(raw, '\n')); err != nil {
		return
	}
}

// sent returns every frame the client wrote, for tests that assert on the traffic.
func (a *agent) sent() []message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]message(nil), a.received...)
}

func (a *agent) wasStopped() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopped
}

// paramsOf finds the first message with a method and decodes its parameters.
func paramsOf[T any](t *testing.T, sent []message, method string) T {
	t.Helper()
	var out T
	for _, m := range sent {
		if m.Method != method {
			continue
		}
		if err := json.Unmarshal(m.Params, &out); err != nil {
			t.Fatalf("decoding the %s parameters: %v", method, err)
		}
		return out
	}
	t.Fatalf("the client never sent %s", method)
	return out
}

// installed is a discovered installation with nothing real behind it.
func installed() Installation {
	return Installation{
		CLI:     "/usr/local/bin/claude",
		Bridge:  "/usr/local/bin/claude-agent-acp",
		Account: Account{Email: "someone@example.com", Plan: "max", Method: "claude.ai"},
	}
}

// drained is a whole stream, sorted by event kind.
type drained struct {
	text      string
	thinking  string
	notices   []string
	toolCalls []core.ToolCall
	stop      core.StopReason
	usage     core.Usage
	err       error
}

// drain reads a stream to its end, which is what every test here does with one.
//
// It keeps reading after the done event rather than returning at it, so that a stream emitting
// anything afterwards is caught rather than tolerated: done is by contract the last thing.
func drain(t *testing.T, s core.Stream) drained {
	t.Helper()

	var got drained
	var text, thinking strings.Builder
	seenDone := false

	for s.Next() {
		event := s.Event()
		if seenDone {
			t.Errorf("the stream emitted a %s event after done, which must be the last one", event.Kind)
		}
		switch event.Kind {
		case core.EventText:
			text.WriteString(event.Text)
		case core.EventThinking:
			thinking.WriteString(event.Text)
		case core.EventNotice:
			got.notices = append(got.notices, event.Text)
		case core.EventToolCall:
			got.toolCalls = append(got.toolCalls, *event.ToolCall)
		case core.EventDone:
			got.stop = event.StopReason
			got.usage = event.Usage
			got.err = event.Err
			seenDone = true
		}
	}
	if !seenDone {
		t.Fatal("the stream ended without a done event, so nothing knows how the turn finished")
	}

	got.text, got.thinking = text.String(), thinking.String()
	return got
}

// noticed reports whether any notice contains a phrase.
func (d drained) noticed(phrase string) bool {
	for _, notice := range d.notices {
		if strings.Contains(notice, phrase) {
			return true
		}
	}
	return false
}

// ask runs one turn against a scripted agent and returns everything it produced.
func ask(t *testing.T, a *agent, opts ...Option) drained {
	t.Helper()

	a.t = t
	client := New(installed(), append([]Option{WithWorkspace(t.TempDir())}, opts...)...)
	a.launch(client)

	stream, err := client.Stream(context.Background(), turn("hello"))
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()

	return drain(t, stream)
}

// turn is the smallest valid request.
func turn(text string) core.Request {
	return core.Request{
		Model:    "sonnet",
		Messages: []core.Message{{Role: core.RoleUser, Text: text}},
	}
}
