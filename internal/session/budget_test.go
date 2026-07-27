package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
)

// costing returns a turn that will be billed at roughly the given number of dollars.
//
// The token count is what is chosen rather than the price, because the engine costs a turn from the
// pricing table rather than trusting whatever the provider claimed. Setting CostUSD on the stream
// event would be overwritten, and a test that did so would be asserting against a number the
// engine never uses. Opus output is $25 per million tokens, so a dollar is forty thousand of them.
func costing(dollars float64) []core.StreamEvent {
	return []core.StreamEvent{
		{Kind: core.EventText, Text: "done"},
		{Kind: core.EventDone, StopReason: core.StopEndTurn,
			Usage: core.Usage{OutputTokens: int(dollars * 40000)}},
	}
}

// The acceptance criterion, and the whole difference between a guardrail and a receipt: the cap
// stops the next request rather than reporting the overspend after it.
func TestACapPausesBeforeTheNextRequestRatherThanAfter(t *testing.T) {
	client := &scriptedClient{name: "claude", events: costing(0.60)}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	if err := e.SetBudget(session.ID, 1.00); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}

	first, err := e.Send(session.ID, "one")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, session.ID, first)

	// 0.60 of 1.00 spent, so there is budget left and the second turn runs.
	second, err := e.Send(session.ID, "two")
	if err != nil {
		t.Fatalf("the second send was refused with budget remaining: %v", err)
	}
	waitForTurn(t, e, session.ID, second)

	// 1.20 of 1.00 now. The third is refused before it goes anywhere.
	_, err = e.Send(session.ID, "three")
	if err == nil {
		t.Fatal("a session past its cap sent another request")
	}
	if !errors.Is(err, ErrPaused) {
		t.Errorf("the refusal is %v, want something a caller can recognise as a pause", err)
	}
	if !strings.Contains(err.Error(), "Raise the cap") {
		t.Errorf("the refusal does not say how to carry on: %v", err)
	}

	budget := e.Budget(session.ID)
	if !budget.Paused {
		t.Error("the session is over its cap and not marked paused")
	}
	if session, _ := e.Session(session.ID); len(session.Turns) != 2 {
		t.Errorf("%d turns, want the two that ran: the refused one was registered anyway",
			len(session.Turns))
	}
}

// Paused, not cancelled. The budget must not destroy the work whose value it was protecting.
func TestRaisingTheCapResumesFromWhereItStopped(t *testing.T) {
	client := &scriptedClient{name: "claude", events: costing(0.60)}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	if err := e.SetBudget(session.ID, 0.50); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}

	first, _ := e.Send(session.ID, "one")
	waitForTurn(t, e, session.ID, first)

	if _, err := e.Send(session.ID, "two"); err == nil {
		t.Fatal("the cap did not hold")
	}

	if err := e.SetBudget(session.ID, 5.00); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if budget := e.Budget(session.ID); budget.Paused {
		t.Error("raising the cap left the session paused")
	}

	resumed, err := e.Send(session.ID, "two")
	if err != nil {
		t.Fatalf("the session did not resume: %v", err)
	}
	waitForTurn(t, e, session.ID, resumed)

	// The transcript survived, which is the reason to pause rather than cancel.
	after, _ := e.Session(session.ID)
	if len(after.Turns) != 2 {
		t.Errorf("%d turns after resuming", len(after.Turns))
	}
	if after.Turns[0].Text == "" {
		t.Error("the work done before the pause was lost")
	}
}

// One agent hitting its own cap must not stop the others, and the total across all of them is a
// separate question somebody may also want to answer.
func TestTheOverallCapIsSeparateFromEachAgentsOwn(t *testing.T) {
	client := &scriptedClient{name: "claude", events: costing(0.40)}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	one := e.Create("claude", "claude-opus-5")
	two := e.Create("claude", "claude-opus-5")

	if err := e.SetBudget(one.ID, 0.30); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if err := e.SetOverallBudget(2.00); err != nil {
		t.Fatalf("SetOverallBudget: %v", err)
	}

	first, _ := e.Send(one.ID, "one")
	waitForTurn(t, e, one.ID, first)

	if _, err := e.Send(one.ID, "again"); err == nil {
		t.Error("the capped agent kept going")
	}
	second, err := e.Send(two.ID, "two")
	if err != nil {
		t.Fatalf("one agent's cap stopped another: %v", err)
	}
	waitForTurn(t, e, two.ID, second)

	overall := e.OverallBudget()
	if overall.Spent < 0.79 || overall.Spent > 0.81 {
		t.Errorf("the overall spend is $%.2f, want both turns counted", overall.Spent)
	}
}

func TestTheOverallCapStopsEveryAgent(t *testing.T) {
	client := &scriptedClient{name: "claude", events: costing(0.60)}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	one := e.Create("claude", "claude-opus-5")
	two := e.Create("claude", "claude-opus-5")
	if err := e.SetOverallBudget(0.50); err != nil {
		t.Fatalf("SetOverallBudget: %v", err)
	}

	first, _ := e.Send(one.ID, "one")
	waitForTurn(t, e, one.ID, first)

	if _, err := e.Send(two.ID, "two"); err == nil {
		t.Error("an agent that had spent nothing kept going past the overall cap")
	} else if !strings.Contains(err.Error(), "every agent") {
		t.Errorf("the refusal does not say the cap is shared: %v", err)
	}
}

// A cap that has silently not been counting half the requests reads as reassurance, which is the
// worst thing a number can do.
func TestAnUncostedRequestIsCountedRatherThanTreatedAsFree(t *testing.T) {
	client := &scriptedClient{name: "gateway", events: []core.StreamEvent{
		{Kind: core.EventText, Text: "done"},
		{Kind: core.EventDone, StopReason: core.StopEndTurn,
			Usage: core.Usage{InputTokens: 1000, OutputTokens: 500}},
	}}
	// A gateway with no rate set, which is the ordinary case for anything OpenAI compatible.
	e := New(fixedResolver{client: client, id: pricing.ModelID{
		Provider: core.ProviderOpenAICompatible, Host: "gateway.example.com", Model: "some-model",
	}})
	defer e.Close()

	session := e.Create("gateway", "some-model")
	if err := e.SetBudget(session.ID, 1.00); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}

	turnID, _ := e.Send(session.ID, "one")
	waitForTurn(t, e, session.ID, turnID)

	budget := e.Budget(session.ID)
	if budget.Unpriced != 1 {
		t.Errorf("%d uncosted requests recorded, want 1", budget.Unpriced)
	}
	if budget.Reliable() {
		t.Error("a budget with uncosted requests reports itself as reliable")
	}
	if !strings.Contains(budget.Status(), "floor") {
		t.Errorf("the status reads %q and does not say the number is incomplete", budget.Status())
	}
}

func TestNoCapIsTheDefaultAndSaysSo(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	budget := e.Budget(session.ID)

	if budget.Capped() {
		t.Error("a session started with a cap nobody set")
	}
	if !strings.Contains(budget.Status(), "no cap set") {
		t.Errorf("the status reads %q", budget.Status())
	}
	if err := e.SetBudget(session.ID, -1); err == nil {
		t.Error("a negative cap was accepted")
	}
}

func TestRemovingACapLetsAPausedAgentGoAgain(t *testing.T) {
	client := &scriptedClient{name: "claude", events: costing(0.60)}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	if err := e.SetBudget(session.ID, 0.10); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	turnID, _ := e.Send(session.ID, "one")
	waitForTurn(t, e, session.ID, turnID)

	if _, err := e.Send(session.ID, "two"); err == nil {
		t.Fatal("the cap did not hold")
	}
	if err := e.SetBudget(session.ID, 0); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}
	if _, err := e.Send(session.ID, "two"); err != nil {
		t.Errorf("removing the cap did not resume the session: %v", err)
	}
}
