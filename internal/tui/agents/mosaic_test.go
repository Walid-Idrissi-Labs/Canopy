package agents_test

// The mosaic's promises: every agent visible or declared off screen, every pane answering for its
// own agent, and no line ever wider than the terminal.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/agents"
)

// emberLit and emberOut are the two drawings a pane's bottom border can wear, copied from the
// brand rather than imported so a change to the fire has to be noticed here.
const (
	emberLit = "▄█▀█▀█▄"
	emberOut = " ▄▄▀▄▄ "
)

func eight() *fakeEngine {
	names := []string{"alpha", "bravo", "carol", "delta", "echo", "fox", "golf", "hotel"}
	statuses := make([]session.AgentStatus, 0, len(names))
	for _, name := range names {
		statuses = append(statuses, status(name, core.AgentIdle, "working on "+name))
	}
	return engine(statuses...)
}

func mosaic(e *fakeEngine, width, height int) agents.Model {
	m := agents.New(e)
	m.SetSize(width, height)
	return key(m, "v")
}

// This is the screen the product is named for: several agents working away, all of them visible
// at once.
func TestTheMosaicShowsEveryAgentAtOnce(t *testing.T) {
	m := mosaic(eight(), 200, 40)

	view := plain(m.Body())
	for _, name := range []string{"alpha", "bravo", "carol", "delta", "echo", "fox", "golf", "hotel"} {
		if !strings.Contains(view, name) {
			t.Errorf("the mosaic does not show %q:\n%s", name, view)
		}
	}
	for i, line := range strings.Split(m.Body(), "\n") {
		if got := len([]rune(plain(line))); got > 200 {
			t.Errorf("line %d is %d columns wide", i, got)
		}
	}
}

// What does not fit is said to be off screen, never silently absent.
func TestAnAgentBeyondTheGridIsDeclaredOffScreen(t *testing.T) {
	e := eight()
	e.statuses = append(e.statuses, status("india", core.AgentAwaitingPermission, "the ninth"))
	e.sessions["s-india"] = conversation("a reply from india")

	m := mosaic(e, 200, 40)
	view := plain(m.Body())
	if !strings.Contains(view, "off screen") {
		t.Errorf("a ninth agent is hidden without a word:\n%s", view)
	}

	// And paging reaches it.
	m = key(m, "]")
	if !strings.Contains(plain(m.Body()), "india") {
		t.Errorf("paging does not reach the ninth agent:\n%s", plain(m.Body()))
	}
}

// The digit drawn in a pane's border jumps to that pane, and the digit of the pane you are on
// opens it, so any visible agent is two presses from a conversation.
func TestDigitsJumpAndThenOpen(t *testing.T) {
	m := mosaic(eight(), 200, 40)

	m = key(m, "3")
	selected, _ := m.Selected()
	if selected.Agent.Name != "carol" {
		t.Fatalf("digit 3 selected %q, want the third pane", selected.Agent.Name)
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	if cmd == nil {
		t.Fatal("the digit of the selected pane should ask to open it")
	}
	msg, ok := cmd().(agents.SwitchMsg)
	if !ok || msg.AgentName != "carol" {
		t.Errorf("opening gave %+v, want carol's conversation", cmd())
	}
}

// Eight panes are eight fires, and each one answers for its own agent, never for the cursor's.
func TestEachPaneWearsItsOwnFire(t *testing.T) {
	m := mosaic(engine(
		status("busy", core.AgentWorking, "digging"),
		status("resting", core.AgentIdle, "done"),
	), 100, 20)

	view := plain(m.Body())
	if !strings.Contains(view, emberLit) {
		t.Errorf("the working agent's fire is not lit:\n%s", view)
	}
	if !strings.Contains(view, emberOut) {
		t.Errorf("the idle agent's fire is not out:\n%s", view)
	}
}

// The hero layout is one agent large and the rest in the corner of your eye.
func TestTheHeroLayoutShowsTheRestOnDeck(t *testing.T) {
	m := agents.New(engine(
		status("hero", core.AgentWorking, "the one being read"),
		status("second", core.AgentIdle, ""),
		status("third", core.AgentIdle, ""),
	))
	m.SetSize(120, 30)
	m = key(m, "v")
	m = key(m, "v")

	if m.Mode() != agents.ModeHero {
		t.Fatalf("mode = %v, want hero", m.Mode())
	}
	view := plain(m.Body())
	for _, name := range []string{"hero", "second", "third"} {
		if !strings.Contains(view, name) {
			t.Errorf("the hero layout does not show %q:\n%s", name, view)
		}
	}
	// The conversation being read belongs to the hero, full width up top.
	if !strings.Contains(view, "a reply from hero") {
		t.Errorf("the hero's conversation is not on screen:\n%s", view)
	}
}

// An animation running behind another screen, or with nothing working, would be waking the
// program for frames nobody can see.
func TestTheFiresOnlyDanceWhileWatchedAndWorking(t *testing.T) {
	e := engine(
		status("busy", core.AgentWorking, "digging"),
		status("resting", core.AgentIdle, "done"),
	)
	m := model(e)
	m = key(m, "v")

	// Not visible yet, so a refresh schedules nothing.
	m, cmd := m.Update(struct{}{})
	if cmd != nil {
		t.Error("the fires are dancing behind another screen")
	}

	if cmd = m.SetVisible(true); cmd == nil {
		t.Error("visible, in a pane layout, with an agent working: the fires should dance")
	}
	// And only one ticker, however many refreshes arrive.
	if _, again := m.Update(struct{}{}); again != nil {
		t.Error("a second refresh started a second ticker")
	}

	// With nobody working there is nothing to animate.
	idle := model(engine(status("resting", core.AgentIdle, "done")))
	idle = key(idle, "v")
	if cmd = idle.SetVisible(true); cmd != nil {
		t.Error("the fires are dancing with no agent working")
	}
}
