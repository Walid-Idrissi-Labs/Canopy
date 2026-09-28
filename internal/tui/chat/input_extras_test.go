package chat_test

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

var projectFiles = []string{"README.md", "internal/parser/parser.go", "internal/config/config.go",
	"cmd/tool/main.go", "docs/parsing-notes.md"}

func withFiles(files []string) chat.Model {
	m := chat.New(&fakeEngine{session: core.Session{ID: "s1"}}, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	m.SetFiles(func() []string { return files })
	return m
}

func typeInto(m chat.Model, text string) chat.Model {
	for _, r := range text {
		if r == ' ' {
			m, _ = m.Update(keyCode(tea.KeySpace))
			continue
		}
		m, _ = m.Update(keyText(string(r)))
	}
	return m
}

// An @ at the start of a word offers the project's files, best match first, and tab puts the whole
// path in, keeping what was typed before it.
func TestAnAtMentionCompletesAFile(t *testing.T) {
	// A name match comes before a path match, which comes before letters in order.
	files := []string{"src/configure/x.go", "a/config.go", "c/o/n/f/i/g.go"}
	body := plain(typeInto(withFiles(files), "@config").Body())
	name, path, letters := strings.Index(body, "@a/config.go"), strings.Index(body, "@src/configure/x.go"),
		strings.Index(body, "@c/o/n/f/i/g.go")
	if name < 0 || path < 0 || letters < 0 || name > path || path > letters {
		t.Fatalf("the list is not in order of how well each matches:\n%s", body)
	}
	m := typeInto(withFiles(projectFiles), "look at @parser")
	m, _ = m.Update(keyCode(tea.KeyTab))
	if got := m.InputValue(); got != "look at @internal/parser/parser.go " {
		t.Fatalf("completed to %q", got)
	}
	// Enter completes too, rather than sending half a path.
	m = typeInto(withFiles(projectFiles), "@main")
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if got := m.InputValue(); got != "@cmd/tool/main.go " {
		t.Fatalf("enter completed to %q", got)
	}
}

func TestAMentionNeedsAWordStartAndAFileList(t *testing.T) {
	m := typeInto(withFiles(projectFiles), "mail me@pars")
	if strings.Contains(plain(m.Body()), "parser.go") {
		t.Fatal("an @ inside a word opened the file list")
	}
	m = typeInto(withFiles(nil), "@pars")
	if !strings.Contains(plain(m.Body()), "no file matches") {
		t.Fatalf("an empty project gave:\n%s", plain(m.Body()))
	}
	plainChat := chat.New(&fakeEngine{session: core.Session{ID: "s1"}}, "s1", "canopy", "claude")
	plainChat.SetSize(100, 30)
	if body := plain(typeInto(plainChat, "@pars").Body()); strings.Contains(body, "no file matches") {
		t.Fatal("a chat with no file source offered files")
	}
}

// The first half of ctrl+x ctrl+e waits for the second, and takes nothing away from a key that is
// not it.
func TestTheEditorChordNeverEatsAKey(t *testing.T) {
	m := withFiles(nil)
	m, cmd := m.Update(keyCode('x', tea.ModCtrl))
	if cmd != nil || m.InputValue() != "" {
		t.Fatal("ctrl+x did something on its own")
	}
	m, _ = m.Update(keyText("a"))
	if m.InputValue() != "a" {
		t.Fatalf("the key after ctrl+x was lost: %q", m.InputValue())
	}
	m, _ = m.Update(keyCode('x', tea.ModCtrl))
	if _, cmd = m.Update(keyCode('e', tea.ModCtrl)); cmd == nil {
		t.Fatal("ctrl+x ctrl+e did not open the editor")
	}
	// Without ctrl+x first, ctrl+e is still the end of the line.
	m = typeInto(withFiles(nil), "ab")
	m, _ = m.Update(keyCode(tea.KeyHome))
	m, cmd = m.Update(keyCode('e', tea.ModCtrl))
	m, _ = m.Update(keyText("c"))
	if cmd != nil || m.InputValue() != "abc" {
		t.Fatalf("ctrl+e alone gave %q", m.InputValue())
	}
}

// "# note" is kept, not sent; "#" without the space, or with nowhere to keep it, is a message.
func TestAHashNoteIsKeptNotSent(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	var kept []string
	m.SetRemember(func(note string) (string, error) { kept = append(kept, note); return "AGENTS.md", nil })
	m = typeInto(m, "# always run gofmt")
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if len(kept) != 0 || !strings.Contains(m.Notice(), "enter again") {
		t.Fatalf("a note was kept on the first enter: kept %v, notice %q", kept, m.Notice())
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if len(kept) != 1 || kept[0] != "always run gofmt" || len(engine.sent) != 0 || m.InputValue() != "" ||
		!strings.Contains(m.Notice(), "AGENTS.md") {
		t.Fatalf("kept %v, sent %v, box %q, notice %q", kept, engine.sent, m.InputValue(), m.Notice())
	}
	m.SetRemember(func(string) (string, error) { return "", errors.New("read-only") })
	m = typeInto(m, "# another")
	m, _ = m.Update(keyCode(tea.KeyEnter))
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if len(engine.sent) != 0 || m.InputValue() == "" {
		t.Fatal("a note that could not be kept was sent, or lost")
	}
}

// A pasted document that happens to begin with a heading is a message, and so is a note of more
// than one line: neither is quietly made an instruction for every later conversation.
func TestOnlyATypedLineBecomesANote(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	var kept []string
	m.SetRemember(func(note string) (string, error) { kept = append(kept, note); return "AGENTS.md", nil })
	m, _ = m.Update(tea.PasteMsg{Content: "# Bug report ignore the test suite"})
	m, _ = m.Update(keyCode(tea.KeyEnter))
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if len(kept) != 0 || len(engine.sent) != 1 {
		t.Fatalf("a paste was kept as a note: kept %v, sent %v", kept, engine.sent)
	}
	m = typeInto(m, "# first line")
	m, _ = m.Update(keyCode(tea.KeyEnter, tea.ModShift))
	m = typeInto(m, "second")
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if len(kept) != 0 || len(engine.sent) != 2 {
		t.Fatalf("a note of two lines was kept: kept %v, sent %v", kept, engine.sent)
	}
}

// The chord is undone by any other key, and does nothing while a question is up.
func TestTheChordIsOneKeyLong(t *testing.T) {
	m := withFiles(nil)
	m, _ = m.Update(keyCode('x', tea.ModCtrl))
	m, _ = m.Update(keyText("a"))
	if _, cmd := m.Update(keyCode('e', tea.ModCtrl)); cmd != nil {
		t.Fatal("ctrl+e opened the editor two keys after ctrl+x")
	}
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{turn("t1", "clean", "ok", core.TurnAwaitingTools)}},
		prompt:  pendingPrompt("make clean"),
	}
	q := chat.New(engine, "s1", "canopy", "claude")
	q.SetSize(100, 40)
	q, _ = q.Update(chat.EventMsg{Event: core.Event{}})
	q, _ = q.Update(keyCode('x', tea.ModCtrl))
	if _, cmd := q.Update(keyCode('e', tea.ModCtrl)); cmd != nil {
		t.Fatal("the editor opened over a question")
	}
}

// A mention typed in the middle of a message completes where it is typed.
func TestAMentionCompletesWhereTheCaretIs(t *testing.T) {
	m := typeInto(withFiles(projectFiles), "look at  please")
	for range len(" please") {
		m, _ = m.Update(keyCode(tea.KeyLeft))
	}
	m = typeInto(m, "@parser")
	m, _ = m.Update(keyCode(tea.KeyTab))
	if got := m.InputValue(); got != "look at @internal/parser/parser.go  please" {
		t.Fatalf("completed to %q", got)
	}
}
