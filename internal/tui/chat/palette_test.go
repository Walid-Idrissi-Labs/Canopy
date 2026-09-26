package chat_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
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

// A question that arrives while the palette is up takes the keyboard back.
func TestAQuestionClosesThePalette(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{turn("t1", "go", "", core.TurnStreaming)}}}
	m := palette(t, engine)
	engine.session.Turns[0].State = core.TurnAwaitingTools
	engine.prompt = pendingPrompt("make clean")
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m, _ = m.Update(keyText("x"))
	if strings.Contains(plain(m.Body()), "enter runs, esc closes") {
		t.Fatal("the palette kept the keyboard from a question")
	}
}

// A file mention is added after what is typed, with a space between; a long list scrolls with the
// selection.
func TestThePaletteMentionsAfterTheDraftAndScrolls(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 40)
	var files []string
	for i := 0; i < 20; i++ {
		files = append(files, fmt.Sprintf("pkg/file%02d.go", i))
	}
	m.SetFiles(func() []string { return files })
	m = typePalette(m, "see")
	m, _ = m.Update(keyCode('p', tea.ModCtrl))
	m = typePalette(m, "file")
	for range 12 {
		m, _ = m.Update(keyCode(tea.KeyDown))
	}
	if !strings.Contains(plain(m.Body()), "> @pkg/file12.go") {
		t.Fatalf("the list did not scroll with the selection:\n%s", plain(m.Body()))
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if m.InputValue() != "see @pkg/file12.go " {
		t.Fatalf("box %q", m.InputValue())
	}
}

type fakeHistory struct{ hits []session.SearchHit }

func (h fakeHistory) Sessions() []core.Session  { return nil }
func (h fakeHistory) InThisProject(string) bool { return true }
func (h fakeHistory) SearchHistory(string, int) []session.SearchHit {
	return h.hits
}

// What was said is searched once typing pauses, from three letters, never finds the conversation on
// screen, and an answer to a query typed over since is dropped.
func TestThePaletteSearchesWhatWasSaidWhenTypingPauses(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(120, 40)
	m.SetHistory(fakeHistory{hits: []session.SearchHit{
		{SessionID: "s1", SessionTitle: "this one", Excerpt: "bcrypt here"},
		{SessionID: "s4", SessionTitle: "auth work", Excerpt: "<<bcrypt>> cost"},
	}})
	m, _ = m.Update(keyCode('p', tea.ModCtrl))
	m, _ = m.Update(keyText("b"))
	if _, cmd := m.Update(keyText("c")); cmd != nil {
		t.Fatal("two letters started a search")
	}
	m, _ = m.Update(keyText("c"))
	m, stale := m.Update(keyText("r"))
	m, fresh := m.Update(keyText("y"))
	if stale == nil || fresh == nil {
		t.Fatal("no search was started")
	}
	if _, cmd := m.Update(stale()); cmd != nil {
		t.Fatal("a search typed over was still run")
	}
	m, run := m.Update(fresh())
	answer := run()
	// An answer that arrives after the palette was closed and opened on other words is dropped, as
	// is one for a query typed over, even where the generation happens to agree.
	reopened, _ := m.Update(keyCode(tea.KeyEscape))
	reopened, _ = reopened.Update(keyCode('p', tea.ModCtrl))
	for _, r := range "xyzw" {
		reopened, _ = reopened.Update(keyText(string(r)))
	}
	reopened, _ = reopened.Update(answer)
	if strings.Contains(plain(reopened.Body()), "said in auth work") {
		t.Fatal("an answer for other words landed in a reopened palette")
	}
	m, _ = m.Update(answer)
	body := plain(m.Body())
	if !strings.Contains(body, "said in auth work") || strings.Contains(body, "this one") {
		t.Fatalf("found:\n%s", body)
	}
}
