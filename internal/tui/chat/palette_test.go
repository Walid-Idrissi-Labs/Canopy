package chat_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

func palette(t *testing.T, engine *fakeEngine) chat.Model {
	t.Helper()
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 40)
	m.SetFiles(func() []string { return []string{"internal/parser/parser.go"} })
	m.SetCommands(config.ResolveCommands(nil, []config.Command{{Name: "audit", Description: "check deps", Prompt: "audit it"}}))
	m, _ = m.Update(keyCode('p', tea.ModCtrl))
	return m
}

func typePalette(m chat.Model, text string) chat.Model {
	for _, r := range text {
		if r == ' ' {
			m, _ = m.Update(keyCode(tea.KeySpace))
			continue
		}
		m, _ = m.Update(keyText(string(r)))
	}
	return m
}

// Letters in order find an entry, and enter runs a built-in as if it had been typed, leaving what
// was in the box where it was.
func TestThePaletteRunsWhatItFinds(t *testing.T) {
	defer theme.Set(theme.Default)
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 40)
	m = typePalette(m, "half written")
	m, _ = m.Update(keyCode('p', tea.ModCtrl))
	m = typePalette(m, "thnord")
	if m.InputValue() != "half written" {
		t.Fatalf("typing into the palette reached the box: %q", m.InputValue())
	}
	if !strings.Contains(plain(m.Body()), "> theme nord") {
		t.Fatalf("the palette does not offer theme nord first:\n%s", plain(m.Body()))
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if theme.Current().Palette.Name != "nord" {
		t.Fatalf("the palette is %q", theme.Current().Palette.Name)
	}
	if m.InputValue() != "half written" || len(engine.sent) != 0 {
		t.Fatalf("box %q, sent %v", m.InputValue(), engine.sent)
	}
}

// A project's own command sends a prompt, so the palette puts it in the box and sends nothing.
func TestThePaletteNeverSendsAPrompt(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := typePalette(palette(t, engine), "audit")
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if len(engine.sent) != 0 || m.InputValue() != "/audit " {
		t.Fatalf("sent %v, box %q", engine.sent, m.InputValue())
	}
	m = typePalette(palette(t, engine), "parser")
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if m.InputValue() != "@internal/parser/parser.go " {
		t.Fatalf("a file became %q", m.InputValue())
	}
}

func TestThePaletteClosesOnEscapeAndWaitsForAQuestion(t *testing.T) {
	m := palette(t, &fakeEngine{session: core.Session{ID: "s1"}})
	if !strings.Contains(plain(m.Body()), "ctrl+p") {
		t.Fatal("the palette did not open")
	}
	m, _ = m.Update(keyCode(tea.KeyEsc))
	if strings.Contains(plain(m.Body()), "enter runs, esc closes") {
		t.Fatal("esc left the palette open")
	}
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{turn("t1", "clean", "ok", core.TurnAwaitingTools)}},
		prompt:  pendingPrompt("make clean"),
	}
	m = chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 40)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m, _ = m.Update(keyCode('p', tea.ModCtrl))
	if strings.Contains(plain(m.Body()), "enter runs, esc closes") {
		t.Fatal("the palette opened over a question")
	}
}
