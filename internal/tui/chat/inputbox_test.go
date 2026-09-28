package chat_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

func draft(t *testing.T, engine *fakeEngine, lines ...string) chat.Model {
	t.Helper()
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 40)
	for i, line := range lines {
		if i > 0 {
			m, _ = m.Update(keyCode(tea.KeyEnter, tea.ModShift))
		}
		for _, r := range line {
			m, _ = m.Update(keyText(string(r)))
		}
	}
	return m
}

// Up and down move through a draft of several lines; only from its first line does up recall what
// was sent, and the draft is there again on the way back.
func TestUpAndDownMoveThroughADraft(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 40)
	for _, r := range "sent before" {
		m, _ = m.Update(keyText(string(r)))
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	for i, line := range []string{"one", "two", "three", "four"} {
		if i > 0 {
			m, _ = m.Update(keyCode(tea.KeyEnter, tea.ModShift))
		}
		for _, r := range line {
			m, _ = m.Update(keyText(string(r)))
		}
	}
	written := "one\ntwo\nthree\nfour"
	for range 3 {
		m, _ = m.Update(keyCode(tea.KeyUp))
	}
	if m.InputValue() != written {
		t.Fatalf("moving up through the draft changed it: %q", m.InputValue())
	}
	m, _ = m.Update(keyCode(tea.KeyUp))
	if m.InputValue() != "sent before" {
		t.Fatalf("up from the first line recalled %q", m.InputValue())
	}
	m, _ = m.Update(keyCode(tea.KeyDown))
	if m.InputValue() != written {
		t.Fatalf("down did not bring the draft back: %q", m.InputValue())
	}

	// Up keeps the caret's column: from the end of "four" up three lines is after "one".
	box := draft(t, engine, "one", "two", "three", "four")
	for range 3 {
		box, _ = box.Update(keyCode(tea.KeyUp))
	}
	box, _ = box.Update(keyText("X"))
	if box.InputValue() != "oneX\ntwo\nthree\nfour" {
		t.Fatalf("typed after moving up: %q", box.InputValue())
	}
	box, _ = box.Update(keyCode(tea.KeyDown))
	box, _ = box.Update(keyText("Y"))
	if box.InputValue() != "oneX\ntwoY\nthree\nfour" {
		t.Fatalf("typed after moving down: %q", box.InputValue())
	}
}

func TestKillToTheEndOfTheLine(t *testing.T) {
	m := draft(t, &fakeEngine{session: core.Session{ID: "s1"}}, "keep this drop this", "next line")
	m, _ = m.Update(keyCode(tea.KeyUp))
	m, _ = m.Update(keyCode(tea.KeyHome))
	for range len("keep this") {
		m, _ = m.Update(keyCode(tea.KeyRight))
	}
	m, _ = m.Update(keyCode('k', tea.ModAlt))
	if m.InputValue() != "keep this\nnext line" {
		t.Fatalf("alt+k left %q", m.InputValue())
	}
}

// A long paste shows its size, and all of it is sent.
func TestALongPasteShowsItsSizeAndIsSentWhole(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 40)
	var lines []string
	for i := 0; i < 200; i++ {
		lines = append(lines, "line")
	}
	m, _ = m.Update(tea.PasteMsg{Content: strings.Join(lines, "\n")})
	if !strings.Contains(plain(m.Body()), "200 lines pasted") {
		t.Fatalf("the box does not say how much was pasted:\n%s", plain(m.Body()))
	}
	// Moved up into it, the text itself is shown, since that is what moving up is for.
	up, _ := m.Update(keyCode(tea.KeyUp))
	if strings.Contains(plain(up.Body()), "lines pasted") {
		t.Fatal("the summary stayed with the caret inside the paste")
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if len(engine.sent) != 1 || strings.Count(engine.sent[0], "line") != 200 {
		t.Fatal("the paste was not sent whole")
	}
}

// A draft taller than the box scrolls with the caret, so moving up to its first line shows it.
func TestTheBoxScrollsWithTheCaret(t *testing.T) {
	var lines []string
	for i := 0; i < 15; i++ {
		lines = append(lines, "row"+string(rune('a'+i)))
	}
	m := draft(t, &fakeEngine{session: core.Session{ID: "s1"}}, lines...)
	if strings.Contains(plain(m.Body()), "rowa") {
		t.Skip("the box is tall enough to show every line, so this proves nothing")
	}
	for range 14 {
		m, _ = m.Update(keyCode(tea.KeyUp))
	}
	if !strings.Contains(plain(m.Body()), "rowa") {
		t.Fatalf("the first line is not on screen with the caret on it:\n%s", plain(m.Body()))
	}
}
