package chat_test

// No reflex spends money. D-43's third rule: ctrl+r fired a real request to a real provider on one
// press, and it is the key half the world's fingers press expecting to search their history.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// worthCompacting is a conversation long enough that summarising it would do something.
func worthCompacting() *fakeEngine {
	turns := make([]core.Turn, 16)
	for i := range turns {
		turns[i] = turn("t", strings.Repeat("question ", 40), strings.Repeat("answer ", 40),
			core.TurnComplete)
	}
	return &fakeEngine{session: core.Session{ID: "s1", Model: "claude-opus-5", Turns: turns}}
}

// The whole of U-09 in one press.
func TestOnePressOfControlRSpendsNothing(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})

	if engine.compacted != 0 {
		t.Errorf("one press reached the provider %d times", engine.compacted)
	}
	if cmd != nil {
		// Running it would be the same failure a frame later.
		if msg := cmd(); msg != nil {
			t.Errorf("one press asked for work: %T", msg)
		}
	}
}

// What the offer has to say. A confirmation that leaves any of these out is one people press
// through, and the bound is the part that makes it safe to agree to: the recent turns are where the
// actual work is and summarising those is how an agent loses the thread mid task.
func TestTheOfferNamesTheTurnsTheCostTheBoundAndTheKey(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})

	view := plain(m.Body())
	for what, want := range map[string]string{
		"how much of the conversation goes": "12 of 16 turns",
		"roughly what it costs":             "about 1.9k tokens",
		"what is kept whatever happens":     "last 4 kept",
		"which key goes ahead with it":      "ctrl+r again",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the offer does not say %s (%q):\n%s", what, want, view)
		}
	}

	// And it stays on one line at eighty columns, or it pushes the message box down the screen and
	// gets answered without being read.
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "ctrl+r again") && len([]rune(line)) > 80 {
			t.Errorf("the offer is %d columns wide: %q", len([]rune(line)), line)
		}
	}
}

// The second press is the one that pays, and it pays once.
func TestTheSecondPressCompactsAndTheOfferGoesWithIt(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd == nil {
		t.Fatal("the confirmed press asked for nothing")
	}
	m, _ = m.Update(cmd())

	if engine.compacted != 1 {
		t.Errorf("the engine was asked to compact %d times, want once", engine.compacted)
	}
	if view := plain(m.Body()); strings.Contains(view, "ctrl+r again") {
		t.Errorf("the offer is still on screen after it was taken up:\n%s", view)
	}
}

// The offer lasts exactly one keystroke, like every other confirmation in the program. One that
// outlived a change of mind would eventually be taken up by a key meant for something else.
func TestAnOfferLapsesOnAnyOtherKey(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m = typeText(m, "no thanks")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd != nil {
		cmd()
	}

	if engine.compacted != 0 {
		t.Errorf("a lapsed offer was taken up by a later press: %d calls", engine.compacted)
	}
	if view := plain(m.Body()); !strings.Contains(view, "ctrl+r again") {
		t.Errorf("the later press did not offer again:\n%s", view)
	}
}

// The slash command takes the same two steps. A command that spent on being typed while the key it
// shadows asked first would be the cheaper path to the more expensive outcome.
func TestSlashCompactGoesThroughTheSameConfirmation(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)

	m = typeText(m, "/compact")
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		cmd()
	}

	if engine.compacted != 0 {
		t.Errorf("/compact spent on being typed: %d calls", engine.compacted)
	}
	view := plain(m.Body())
	if !strings.Contains(view, "ctrl+r again") {
		t.Errorf("/compact did not offer anything:\n%s", view)
	}

	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd == nil {
		t.Fatal("the key named in the offer did not go ahead with it")
	}
	_, _ = m.Update(cmd())
	if engine.compacted != 1 {
		t.Errorf("the confirmed command compacted %d times", engine.compacted)
	}
}

// A conversation with nothing to summarise says so rather than offering to summarise none of it and
// then declining.
func TestAShortConversationIsToldThereIsNothingToSummarise(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Model: "claude-opus-5", Turns: []core.Turn{
		turn("t1", "hello", "hi", core.TurnComplete),
	}}}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})

	view := plain(m.Body())
	if !strings.Contains(view, "not enough of this conversation") {
		t.Errorf("a conversation with nothing to compact was not told so:\n%s", view)
	}
	if strings.Contains(view, "ctrl+r again") {
		t.Errorf("an empty compaction was offered anyway:\n%s", view)
	}
	if engine.compacted != 0 {
		t.Errorf("it was sent anyway: %d calls", engine.compacted)
	}
}
