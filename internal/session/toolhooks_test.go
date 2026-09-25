package session

import (
	"context"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

type endedHooks struct {
	mu    sync.Mutex
	ended []core.Turn
}

func (h *endedHooks) Before(context.Context, permission.Request) string { return "" }
func (h *endedHooks) After(context.Context, permission.Request, core.ToolResult) string {
	return ""
}
func (h *endedHooks) TurnEnded(_ string, turn core.Turn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ended = append(h.ended, turn)
}

// Turn-end hooks are told of each turn once it has ended, with how it ended.
func TestTurnEndHooksAreToldOfEveryTurn(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("Hello")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()
	hooks := &endedHooks{}
	e.SetToolHooks(hooks)

	session := e.Create("claude", "claude-opus-5")
	turnID, err := e.Send(session.ID, "hi")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, e, session.ID, turnID)
	// Already told by the time the turn reads as finished, so whoever waits for the finish on their
	// way out finds the hooks started and can wait for them.
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if len(hooks.ended) != 1 {
		t.Fatalf("told of %d turns when the turn read as finished", len(hooks.ended))
	}
	if got := hooks.ended[0]; got.ID != turnID || got.State != core.TurnComplete || got.Text != "Hello" {
		t.Fatalf("told of %+v", got)
	}
}

type refusingHooks struct {
	endedHooks
	asked []permission.Request
}

func (h *refusingHooks) Before(_ context.Context, req permission.Request) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, req)
	return "not today"
}

// The engine's hooks reach every agent's loop: a call is refused by them and never runs.
func TestTheEnginesHooksReachTheLoop(t *testing.T) {
	step := []core.StreamEvent{
		{Kind: core.EventToolCall, ToolCall: &core.ToolCall{ID: "c", Name: "reader", Input: []byte(`{}`)}},
		{Kind: core.EventDone, StopReason: core.StopToolUse},
	}
	client := &scriptedClient{name: "claude", events: step}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)
	reader := &kindTool{name: "reader", kind: core.ToolRead}
	registry := core.NewToolRegistry()
	registry.MustRegister(reader)
	e.WithTools(registry, core.TrustStandard, nil)
	e.SetMaxSteps(2)
	hooks := &refusingHooks{}
	e.SetToolHooks(hooks)

	session := e.Create("claude", "claude-opus-5")
	turnID, err := e.Send(session.ID, "read")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, e, session.ID, turnID)
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	if len(hooks.asked) == 0 || hooks.asked[0].Tool != "reader" || reader.runs.Load() != 0 {
		t.Fatalf("asked %d times, the tool ran %d times", len(hooks.asked), reader.runs.Load())
	}
}
