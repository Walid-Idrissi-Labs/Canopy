package chat_test

import (
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
	m = typed(t, append(keys, keyCode(tea.KeyHome), keyCode(tea.KeyRight, tea.ModAlt), keyText("X"))...)
	if m.InputValue() != "fixX the parser" {
		t.Fatalf("home, alt+right then X gave %q", m.InputValue())
	}
}
