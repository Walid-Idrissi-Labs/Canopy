package session

import (
	"context"
	"sync"
	"testing"
	"time"

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
	deadline := time.Now().Add(5 * time.Second)
	for {
		hooks.mu.Lock()
		n := len(hooks.ended)
		var got core.Turn
		if n > 0 {
			got = hooks.ended[0]
		}
		hooks.mu.Unlock()
		if n == 1 {
			if got.ID != turnID || got.State != core.TurnComplete || got.Text != "Hello" {
				t.Fatalf("told of %+v", got)
			}
			return
		}
		if n > 1 || time.Now().After(deadline) {
			t.Fatalf("told of %d turns", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
