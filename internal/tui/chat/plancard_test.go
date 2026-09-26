package chat_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

func planned(engine *fakeEngine) chat.Model {
	plan, _ := core.ModeByName(core.ModePlan)
	engine.mode = plan
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	return m
}

func planSession() core.Session {
	return core.Session{ID: "s1", Turns: []core.Turn{
		turn("t1", "how would you fix the parser", "1. Split the lexer.\n2. Add a test.", core.TurnComplete)}}
}

// Under a finished plan with nothing typed, enter carries it out: build mode, and the approval sent.
func TestEnterCarriesOutAFinishedPlan(t *testing.T) {
	engine := &fakeEngine{session: planSession()}
	m := planned(engine)
	if !strings.Contains(plain(m.Body()), "carries this plan out in build mode") {
		t.Fatalf("no plan card:\n%s", plain(m.Body()))
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if len(engine.sent) != 0 || engine.mode.Name != core.ModePlan {
		t.Fatal("one enter carried the plan out")
	}
	_, _ = m.Update(keyCode(tea.KeyEnter))
	if engine.mode.Name != core.ModeBuild || len(engine.sent) != 1 || !strings.HasPrefix(engine.sent[0], "That plan is approved") {
		t.Fatalf("mode %q, sent %v", engine.mode.Name, engine.sent)
	}
	if engine.sentIn[0] != core.ModeBuild {
		t.Fatalf("the approval was sent in %s mode", engine.sentIn[0])
	}
}

// Typing is changing the plan, not approving it; and there is no card outside plan mode, without a
// plan, or where build is not allowed.
func TestThePlanCardOnlyAppearsWhereItMeansSomething(t *testing.T) {
	engine := &fakeEngine{session: planSession()}
	m := planned(engine)
	m, _ = m.Update(keyText("n"))
	if strings.Contains(plain(m.Body()), "carries this plan out") {
		t.Fatal("the card stayed while a revision was being typed")
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if engine.mode.Name != core.ModePlan || len(engine.sent) != 1 || engine.sent[0] != "n" {
		t.Fatalf("a typed message approved the plan: mode %q, sent %v", engine.mode.Name, engine.sent)
	}

	building := &fakeEngine{session: planSession()}
	m = chat.New(building, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	if strings.Contains(plain(m.Body()), "carries this plan out") {
		t.Fatal("a card outside plan mode")
	}

	readOnly := &fakeEngine{session: planSession(), trust: core.TrustReadOnly}
	m = planned(readOnly)
	m, _ = m.Update(keyCode(tea.KeyEnter))
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if len(readOnly.sent) != 0 || !strings.Contains(m.Error(), "cannot be carried out") {
		t.Fatalf("a read-only agent was sent to build: sent %v, error %q", readOnly.sent, m.Error())
	}
}

// An answer in plan mode that sets out no steps is not a plan; a key between the two enters keeps
// planning; and with another agent's question waiting on enter, no card offers enter for the plan.
func TestThePlanCardNeedsAPlanAndAClearKey(t *testing.T) {
	answer := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		turn("t1", "what is the parser", "It turns text into a tree.", core.TurnComplete)}}}
	m := planned(answer)
	if strings.Contains(plain(m.Body()), "carries this plan out") {
		t.Fatal("a plain answer was offered as a plan")
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	_, _ = m.Update(keyCode(tea.KeyEnter))
	if len(answer.sent) != 0 || answer.mode.Name != core.ModePlan {
		t.Fatal("enter on an answer carried out a plan")
	}

	engine := &fakeEngine{session: planSession()}
	m = planned(engine)
	m, _ = m.Update(keyCode(tea.KeyEnter))
	m, _ = m.Update(keyCode(tea.KeyDown))
	_, _ = m.Update(keyCode(tea.KeyEnter))
	if len(engine.sent) != 0 {
		t.Fatal("a key between the enters did not keep planning")
	}

	visited := &fakeEngine{session: planSession(), waiting: []session.Waiting{{SessionID: "s2", Agent: "other",
		Request: permission.Request{SessionID: "s2", Tool: "run_command", Command: "make"}}}}
	m = planned(visited)
	if strings.Contains(plain(m.Body()), "carries this plan out") {
		t.Fatal("the plan card offered enter while another agent's question waits on it")
	}
}
