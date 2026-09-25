package session

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The inventory is taken from what a turn sends, so it matches the wire: the system prompt and
// instructions together are the request's system, its tools are the request's tools, and its
// history is the next request's messages less the new one.
func TestTheInventoryMatchesTheWire(t *testing.T) {
	// The reply carries signed thinking in its native form, as Anthropic's does, so the inventory
	// has replayed reasoning to find.
	events := reply("an answer of some length")
	events[len(events)-1].Native = &core.Native{Provider: "anthropic", Data: []byte(`{"role":"assistant",` +
		`"content":[{"type":"thinking","thinking":"` + strings.Repeat("considering ", 400) + `","signature":"s"},` +
		`{"type":"text","text":"an answer of some length"}]}`)}
	client := &scriptedClient{name: "claude", events: events}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)
	registry := core.NewToolRegistry()
	registry.MustRegister(&kindTool{name: "reader", kind: core.ToolRead})
	e.WithTools(registry, core.TrustStandard, nil)
	e.WithInstructions("<instructions source=\"AGENTS.md\">\n" + string(make([]byte, 4000)) + "\n</instructions>")
	created := e.Create("claude", "claude-opus-5")
	first, err := e.Send(created.ID, "first question")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, e, created.ID, first)

	inv := e.Inventory(created.ID)
	second, err := e.Send(created.ID, "second")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, e, created.ID, second)
	client.mu.Lock()
	system, tools, history := client.system, client.tools, client.history
	client.mu.Unlock()

	if inv.System+inv.Instructions != core.TakeInventory(system, "", nil, nil).System &&
		inv.System+inv.Instructions+1 != core.TakeInventory(system, "", nil, nil).System {
		t.Errorf("system %d + instructions %d does not add up to the %d sent", inv.System, inv.Instructions,
			core.TakeInventory(system, "", nil, nil).System)
	}
	if inv.Instructions < 1000 {
		t.Errorf("the instructions measure %d tokens; about 1000 were sent", inv.Instructions)
	}
	if inv.ToolCount != len(tools) || inv.ToolCount == 0 {
		t.Errorf("%d tools in the inventory, %d sent", inv.ToolCount, len(tools))
	}
	if inv.Messages != len(history)-1 {
		t.Errorf("%d messages in the inventory, %d sent before the new one", inv.Messages, len(history)-1)
	}
	// About 4800 bytes of thinking, about 1200 tokens; the signature and framing add nothing.
	if inv.Reasoning < 1100 || inv.Reasoning > 1300 {
		t.Errorf("the replayed thinking measures %d tokens; about 1200 were sent", inv.Reasoning)
	}
	if inv.Asked == 0 || inv.Answered == 0 || inv.Total() <= inv.System {
		t.Errorf("the conversation is missing from the inventory: %+v", inv)
	}
}
