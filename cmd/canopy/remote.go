package main

// The interface attached to `canopy serve`: the same screens as `canopy`, drawing conversations the
// server runs. What they need of an engine is answered over the socket where the protocol has a way
// to ask it, and refused, in words, where it does not; nothing here pretends a feature works that
// the server cannot do for a client.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// errAttached is what a feature the server does not offer its clients answers with.
var errAttached = errors.New("not while attached to canopy serve; run it in the terminal where canopy serve runs")

// remoteEngine is canopy serve, seen from the interface.
type remoteEngine struct {
	client *rpcClient
	dir    string
	trail  *permission.Trail

	mu       sync.Mutex
	sessions map[string]core.Session
	order    []string
	modes    map[string]remoteModes
	// questions are what the server has asked and nobody has answered yet, oldest first.
	questions []remoteQuestion
	sequence  uint64
	watchers  []chan core.Event
	closed    bool
	// refreshing are the conversations being fetched now, and again those changed meanwhile, so a
	// turn streaming a hundred updates is fetched as often as a fetch takes rather than a hundred
	// times at once.
	refreshing map[string]bool
	again      map[string]bool
}

type remoteModes struct {
	Current   string `json:"currentModeId"`
	Available []struct {
		ID string `json:"id"`
	} `json:"availableModes"`
}

type remoteQuestion struct {
	id       json.RawMessage
	request  permission.Request
	decision permission.Decision
}

var _ tui.Engine = (*remoteEngine)(nil)

// newRemoteEngine follows what the server sends until it closes the connection.
func newRemoteEngine(client *rpcClient, dir string) *remoteEngine {
	e := &remoteEngine{client: client, dir: dir, trail: permission.NewTrail(),
		sessions: map[string]core.Session{}, modes: map[string]remoteModes{},
		refreshing: map[string]bool{}, again: map[string]bool{}}
	go e.follow()
	return e
}

// follow turns what the server sends unasked into questions and fresh copies of conversations.
func (e *remoteEngine) follow() {
	for {
		select {
		case m := <-e.client.incoming:
			e.received(m)
		case <-e.client.closed:
			e.mu.Lock()
			e.closed = true
			for _, w := range e.watchers {
				close(w)
			}
			e.watchers = nil
			e.mu.Unlock()
			return
		}
	}
}

func (e *remoteEngine) received(m map[string]json.RawMessage) {
	var method string
	_ = json.Unmarshal(m["method"], &method)
	switch method {
	case "session/request_permission":
		var params struct {
			SessionID string `json:"sessionId"`
			Meta      struct {
				Canopy struct {
					Request  permission.Request  `json:"request"`
					Decision permission.Decision `json:"decision"`
				} `json:"canopy"`
			} `json:"_meta"`
		}
		if json.Unmarshal(m["params"], &params) != nil {
			return
		}
		request := params.Meta.Canopy.Request
		if request.SessionID == "" {
			request.SessionID = params.SessionID
		}
		e.mu.Lock()
		e.questions = append(e.questions, remoteQuestion{id: m["id"], request: request, decision: params.Meta.Canopy.Decision})
		e.mu.Unlock()
		e.publish(request.SessionID)
	case "$/cancel_request":
		withdrawn, ok := withdrawnQuestion(m)
		if !ok {
			return
		}
		e.mu.Lock()
		for i, q := range e.questions {
			var id json.Number
			_ = json.Unmarshal(q.id, &id)
			if id.String() == withdrawn {
				e.questions = append(e.questions[:i], e.questions[i+1:]...)
				break
			}
		}
		e.mu.Unlock()
		e.publish("")
	case "session/update":
		var params struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal(m["params"], &params) == nil && params.SessionID != "" {
			// Looked at again whole rather than rebuilt from the update, so what is drawn is what the
			// engine holds, not a reconstruction of it.
			e.refreshSoon(params.SessionID)
		}
	}
}

// refreshSoon fetches a conversation off the reading goroutine, once more after the fetch in flight
// if it changed again meanwhile.
func (e *remoteEngine) refreshSoon(sessionID string) {
	e.mu.Lock()
	if e.refreshing[sessionID] {
		e.again[sessionID] = true
		e.mu.Unlock()
		return
	}
	e.refreshing[sessionID] = true
	e.mu.Unlock()
	go func() {
		for {
			e.refresh(sessionID)
			e.mu.Lock()
			if !e.again[sessionID] {
				delete(e.refreshing, sessionID)
				e.mu.Unlock()
				return
			}
			delete(e.again, sessionID)
			e.mu.Unlock()
		}
	}()
}

// refresh fetches a conversation as the server holds it now, and says it changed.
func (e *remoteEngine) refresh(sessionID string) bool {
	var reply struct {
		Session core.Session `json:"session"`
		Modes   remoteModes  `json:"modes"`
	}
	if err := e.client.call("_canopy/session", map[string]any{"sessionId": sessionID}, &reply); err != nil {
		return false
	}
	e.mu.Lock()
	if _, known := e.sessions[sessionID]; !known {
		e.order = append(e.order, sessionID)
	}
	e.sessions[sessionID] = reply.Session
	e.modes[sessionID] = reply.Modes
	e.mu.Unlock()
	e.publish(sessionID)
	return true
}

// publish tells every screen watching that something changed.
func (e *remoteEngine) publish(sessionID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	e.sequence++
	event := core.Event{Sequence: e.sequence, At: time.Now(), Kind: core.EventSessionUpdated, SessionID: sessionID}
	for _, w := range e.watchers {
		select {
		case w <- event:
		default:
			// A screen that is behind catches up from the next one; each says only "look again".
		}
	}
}

// open loads a conversation the server holds, or starts one, and returns its id.
func (e *remoteEngine) open(target string) (string, error) {
	if target == "new" || target == "" {
		var created struct {
			SessionID string `json:"sessionId"`
		}
		if err := e.client.call("session/new", map[string]any{"cwd": e.dir, "mcpServers": []any{}}, &created); err != nil {
			return "", err
		}
		e.refresh(created.SessionID)
		return created.SessionID, nil
	}
	id := session.SessionID(target)
	if err := e.client.call("session/load", map[string]any{"sessionId": id, "cwd": e.dir, "mcpServers": []any{}}, nil); err != nil {
		return "", err
	}
	if !e.refresh(id) {
		return "", errors.New("canopy serve did not send the conversation")
	}
	return id, nil
}

func (e *remoteEngine) Session(id string) (core.Session, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.sessions[id]
	return s, ok
}

func (e *remoteEngine) Sessions() []core.Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]core.Session, 0, len(e.order))
	for _, id := range e.order {
		out = append(out, e.sessions[id])
	}
	return out
}

// Send starts a turn on the server. The server registers the turn before it reads the next request,
// so the conversation fetched right after already holds it, or the prompt's refusal has arrived.
func (e *remoteEngine) Send(sessionID, prompt string) (string, error) {
	before, _ := e.Session(sessionID)
	reply := e.client.start("session/prompt", map[string]any{"sessionId": sessionID,
		"prompt": []any{map[string]any{"type": "text", "text": prompt}}})
	e.refresh(sessionID)
	after, _ := e.Session(sessionID)
	if len(after.Turns) > len(before.Turns) {
		return after.Turns[len(after.Turns)-1].ID, nil
	}
	select {
	case r := <-reply:
		if r.Error != nil {
			return "", errors.New(r.Error.Message)
		}
	default:
	}
	return "", errors.New("canopy serve did not start the turn")
}

func (e *remoteEngine) Cancel(sessionID string) {
	e.client.write(map[string]any{"jsonrpc": "2.0", "method": "session/cancel",
		"params": map[string]any{"sessionId": sessionID}})
}

func (e *remoteEngine) Events(uint64) <-chan core.Event {
	w := make(chan core.Event, 64)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		close(w)
		return w
	}
	e.watchers = append(e.watchers, w)
	return w
}

// question is the oldest one for a conversation.
func (e *remoteEngine) question(sessionID string) (remoteQuestion, int, bool) {
	for i, q := range e.questions {
		if q.request.SessionID == sessionID {
			return q, i, true
		}
	}
	return remoteQuestion{}, 0, false
}

func (e *remoteEngine) Pending(sessionID string) (session.Prompt, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	q, _, ok := e.question(sessionID)
	if !ok {
		return session.Prompt{}, false
	}
	return session.Prompt{SessionID: sessionID, Request: q.request, Decision: q.decision}, true
}

func (e *remoteEngine) PendingAll() []session.Waiting {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []session.Waiting
	for _, q := range e.questions {
		out = append(out, session.Waiting{SessionID: q.request.SessionID, Agent: session.Code(q.request.SessionID),
			Request: q.request, Decision: q.decision})
	}
	return out
}

// Answer replies to the oldest question for a conversation. Always cannot be carried over the
// protocol, so it is answered as this once; LIMITATIONS says so.
func (e *remoteEngine) Answer(sessionID string, approved, _ bool) bool {
	e.mu.Lock()
	q, i, ok := e.question(sessionID)
	if ok {
		e.questions = append(e.questions[:i], e.questions[i+1:]...)
	}
	e.mu.Unlock()
	if !ok {
		return false
	}
	option := "reject"
	if approved {
		option = "allow"
	}
	e.client.write(map[string]any{"jsonrpc": "2.0", "id": q.id,
		"result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": option}}})
	e.publish(sessionID)
	return true
}

func (e *remoteEngine) Mode(sessionID string) core.Mode {
	e.mu.Lock()
	current := e.modes[sessionID].Current
	e.mu.Unlock()
	if mode, ok := core.ModeByName(current); ok {
		return mode
	}
	mode, _ := core.ModeByName(core.ModeBuild)
	return mode
}

func (e *remoteEngine) SetMode(sessionID string, mode core.Mode) error {
	if err := e.client.call("session/set_mode", map[string]any{"sessionId": sessionID, "modeId": mode.Name}, nil); err != nil {
		return err
	}
	e.refresh(sessionID)
	return nil
}

func (e *remoteEngine) ModeUnusable(sessionID string, mode core.Mode) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, available := range e.modes[sessionID].Available {
		if available.ID == mode.Name {
			return nil
		}
	}
	return errors.New(mode.Name + " is not available to this conversation")
}

func (e *remoteEngine) Create(string, string) core.Session {
	id, err := e.open("new")
	if err != nil {
		return core.Session{}
	}
	s, _ := e.Session(id)
	return s
}

func (e *remoteEngine) Trail() *permission.Trail          { return e.trail }
func (e *remoteEngine) Tools() (*core.ToolRegistry, bool) { return nil, false }

// What the protocol has no way to ask for, refused in words or answered as empty.

func (e *remoteEngine) Compact(context.Context, string) (session.CompactionResult, error) {
	return session.CompactionResult{}, errAttached
}
func (e *remoteEngine) Apply(string, session.CompactionResult) error { return errAttached }
func (e *remoteEngine) UseCredential(string, string, string) error   { return errAttached }
func (e *remoteEngine) Undo(context.Context, string, string) error   { return errAttached }
func (e *remoteEngine) UndoPreview(context.Context, string, string) (session.UndoPlan, error) {
	return session.UndoPlan{}, errAttached
}
func (e *remoteEngine) Inventory(string) core.Inventory           { return core.Inventory{} }
func (e *remoteEngine) Budget(string) session.Budget              { return session.Budget{} }
func (e *remoteEngine) SetBudget(string, float64) error           { return errAttached }
func (e *remoteEngine) OverallBudget() session.Budget             { return session.Budget{} }
func (e *remoteEngine) SetOverallBudget(float64) error            { return errAttached }
func (e *remoteEngine) Fork(string, string) (core.Session, error) { return core.Session{}, errAttached }
func (e *remoteEngine) Steer(string, string) error                { return errAttached }
func (e *remoteEngine) Steering(string) []string                  { return nil }
func (e *remoteEngine) ClearSteering(string) []string             { return nil }
func (e *remoteEngine) Retry(string) (string, error)              { return "", errAttached }
func (e *remoteEngine) Grants(string) []permission.Scope          { return nil }
func (e *remoteEngine) Revoke(string, permission.Scope)           {}
func (e *remoteEngine) Asides(string) []session.Aside             { return nil }
func (e *remoteEngine) AgentStatuses() []session.AgentStatus      { return nil }
func (e *remoteEngine) RemoveAgent(string) error                  { return errAttached }
func (e *remoteEngine) RenameCredential(string, string) int       { return 0 }
func (e *remoteEngine) Aside(context.Context, string, string) (string, error) {
	return "", errAttached
}
func (e *remoteEngine) AddAgent(context.Context, session.Agent) (session.Agent, error) {
	return session.Agent{}, errAttached
}
func (e *remoteEngine) Judge(context.Context, string, []core.JudgeCandidate) (string, error) {
	return "", errAttached
}

// attachInterface opens the interface on a conversation canopy serve runs. Leaving it leaves the
// conversation running there.
func attachInterface(conn io.ReadWriter, dir, target string, errOut io.Writer) int {
	client := newRPCClient(conn)
	if err := client.call("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}, nil); err != nil {
		_, _ = fmt.Fprintln(errOut, terminalText(err.Error()))
		return exitFailed
	}
	engine := newRemoteEngine(client, dir)
	sessionID, err := engine.open(target)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, terminalText(err.Error()))
		return exitFailed
	}
	keyStore, err := openKeyStore()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	opened, _ := engine.Session(sessionID)
	monitor := newWorktrees("", nil)
	defer monitor.Close()
	if _, err := tui.RunAppConfigured(monitor, signInAware{keyStore}, engine, filepath.Base(dir), opened.KeyName,
		tui.AppOptions{Session: sessionID}); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	code := session.Code(sessionID)
	_, _ = fmt.Fprintf(errOut, "left conversation %s running in canopy serve; canopy attach %s picks it up\n", code, code)
	return exitOK
}
