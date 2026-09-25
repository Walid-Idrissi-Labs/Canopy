package chat_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

func typed(t *testing.T, keys ...tea.KeyPressMsg) chat.Model {
	t.Helper()
	m := chat.New(&fakeEngine{session: core.Session{ID: "s1"}}, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	for _, k := range keys {
		m, _ = m.Update(k)
	}
	return m
}

// While a question is up its keys are answers, so a paste is not taken into the box behind it.
func TestAPasteWaitsWhileAQuestionIsUp(t *testing.T) {
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{
			turn("t1", "clean up", "Let me clear the build.", core.TurnAwaitingTools),
		}},
		prompt: pendingPrompt("make clean"),
	}
	m := model(engine)
	m.SetSize(100, 40)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	if !m.Awaiting() {
		t.Fatal("no question is up, so this test proves nothing")
	}
	m, _ = m.Update(tea.PasteMsg{Content: "y"})
	if m.InputValue() != "" {
		t.Fatalf("a paste went into the box behind a question: %q", m.InputValue())
	}
}

// A paste that begins a command opens the command list, as typing it does.
func TestAPastedSlashOpensTheCommandList(t *testing.T) {
	m := withCommands(core.Session{ID: "s1"})
	m, _ = m.Update(tea.PasteMsg{Content: "/ch"})
	if rowIndex(rowsOf(m), "/changelog") < 0 {
		t.Fatalf("the command list did not open on a pasted slash:\n%s", plain(m.Body()))
	}
}

// Text that arrives with ctrl or alt held is a shortcut, not text.
func TestAModifiedKeyIsNotTyped(t *testing.T) {
	m := typed(t, tea.KeyPressMsg{Code: 'x', Text: "x", Mod: tea.ModAlt}, tea.KeyPressMsg{Code: 'y', Text: "y", Mod: tea.ModCtrl})
	if m.InputValue() != "" {
		t.Fatalf("a modified key was typed: %q", m.InputValue())
	}
}

// Shift+enter, which a terminal with the kitty keyboard protocol reports, is a line break.
func TestShiftEnterIsALineBreak(t *testing.T) {
	m := typed(t, keyText("a"), keyCode(tea.KeyEnter, tea.ModShift), keyText("b"))
	if m.InputValue() != "a\nb" {
		t.Fatalf("shift+enter gave %q", m.InputValue())
	}
}

// A paste is a keystroke as far as an offer or a complaint is concerned: it ends both.
func TestAPasteEndsAnOfferAndAComplaint(t *testing.T) {
	turns := make([]core.Turn, 8)
	for i := range turns {
		turns[i] = turn("t", "question", "answer", core.TurnComplete)
	}
	engine := &fakeEngine{session: core.Session{ID: "s1", Model: "claude-opus-5", Turns: turns}}
	m := model(engine)
	m, _ = m.Update(keyCode('r', tea.ModCtrl))
	m, _ = m.Update(tea.PasteMsg{Content: "x"})
	if _, cmd := m.Update(keyCode('r', tea.ModCtrl)); cmd != nil {
		t.Fatal("an offer to compact survived a paste and was taken up by the next ctrl+r")
	}

	short := &fakeEngine{session: core.Session{ID: "s1", Model: "claude-opus-5", Turns: []core.Turn{
		turn("t1", "hello", "hi", core.TurnComplete),
	}}}
	m = model(short)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m, _ = m.Update(keyCode('r', tea.ModCtrl))
	if !strings.Contains(plain(m.Body()), "not enough of this conversation") {
		t.Fatal("no complaint is up, so this test proves nothing")
	}
	m, _ = m.Update(tea.PasteMsg{Content: "x"})
	if strings.Contains(plain(m.Body()), "not enough of this conversation") {
		t.Fatal("a complaint about the last key outlived a paste")
	}
}

// Option on a Mac arrives as alt: option+delete removes a word and option+arrows move by one, the
// habits every other text field there has taught.
func TestAltEditsByWord(t *testing.T) {
	var keys []tea.KeyPressMsg
	for _, r := range "fix the parser" {
		keys = append(keys, keyText(string(r)))
	}
	m := typed(t, append(keys, keyCode(tea.KeyBackspace, tea.ModAlt))...)
	if m.InputValue() != "fix the " {
		t.Fatalf("alt+backspace left %q", m.InputValue())
	}
	m = typed(t, append(keys, keyCode(tea.KeyLeft, tea.ModAlt), keyText("X"))...)
	if m.InputValue() != "fix the Xparser" {
		t.Fatalf("alt+left then X gave %q", m.InputValue())
	}
	for _, tc := range []struct {
		name string
		keys []tea.KeyPressMsg
		want string
	}{
		{"alt+right", []tea.KeyPressMsg{keyCode(tea.KeyHome), keyCode(tea.KeyRight, tea.ModAlt), keyText("X")}, "fixX the parser"},
		{"ctrl+right", []tea.KeyPressMsg{keyCode(tea.KeyHome), keyCode(tea.KeyRight, tea.ModCtrl), keyText("X")}, "fixX the parser"},
		{"alt+f", []tea.KeyPressMsg{keyCode(tea.KeyHome), keyCode('f', tea.ModAlt), keyText("X")}, "fixX the parser"},
		{"ctrl+left", []tea.KeyPressMsg{keyCode(tea.KeyLeft, tea.ModCtrl), keyText("X")}, "fix the Xparser"},
		{"alt+b", []tea.KeyPressMsg{keyCode('b', tea.ModAlt), keyText("X")}, "fix the Xparser"},
		{"alt+delete", []tea.KeyPressMsg{keyCode(tea.KeyHome), keyCode(tea.KeyDelete, tea.ModAlt)}, "ix the parser"},
	} {
		if m := typed(t, append(append([]tea.KeyPressMsg(nil), keys...), tc.keys...)...); m.InputValue() != tc.want {
			t.Errorf("%s gave %q, want %q", tc.name, m.InputValue(), tc.want)
		}
	}
	// A line break ends a word, so option+delete after the first word of a line stops at the break.
	m = typed(t, keyText("one"), keyCode(tea.KeyEnter, tea.ModShift), keyText("two"), keyCode(tea.KeyBackspace, tea.ModAlt))
	if m.InputValue() != "one\n" {
		t.Fatalf("option+delete across a line break left %q", m.InputValue())
	}
}

// Alt with up or down still walks what was sent, as the plain arrows do.
func TestAltArrowsWalkHistory(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	m, _ = m.Update(keyText("earlier"))
	m, _ = m.Update(keyCode(tea.KeyEnter))
	m, _ = m.Update(keyCode(tea.KeyUp, tea.ModAlt))
	if m.InputValue() != "earlier" {
		t.Fatalf("alt+up recalled %q", m.InputValue())
	}
	m, _ = m.Update(keyCode(tea.KeyDown, tea.ModAlt))
	if m.InputValue() != "" {
		t.Fatalf("alt+down left %q", m.InputValue())
	}
}
