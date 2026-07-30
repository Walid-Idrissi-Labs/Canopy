package chat_test

// Selecting conversation text with the mouse.
//
// Mouse reporting takes the terminal's native drag-to-select away, and these are the tests for the
// feature that gives it back: drag over the conversation and the text is on the clipboard at
// release, with a confirmation that goes away by itself. The one rule worth defending hardest is
// what is *not* selectable — the box, the panels, the chrome — because an interface that can be
// copied as text ends up pasted into bug reports looking like output.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// selectable builds a chat with one finished exchange and a clipboard recorder.
func selectable(t *testing.T) (chat.Model, *[]string) {
	t.Helper()

	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		turn("t1", "where is the parser", "The parser lives in internal/config.", core.TurnComplete),
	}}}
	m := chat.New(engine, "s1", "myproject", "claude")
	m.SetSize(80, 24)

	var copied []string
	m.SetClipboard(func(text string) error {
		copied = append(copied, text)
		return nil
	})
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	return m, &copied
}

// rowAndCol finds where a piece of text sits in the body, in the coordinates a mouse reports.
func rowAndCol(t *testing.T, m chat.Model, want string) (row, col int) {
	t.Helper()

	for r, line := range strings.Split(plain(m.Body()), "\n") {
		if c := strings.Index(line, want); c >= 0 {
			return r, len([]rune(line[:c]))
		}
	}
	t.Fatalf("%q is not on screen:\n%s", want, plain(m.Body()))
	return 0, 0
}

func mouse(m chat.Model, action tea.MouseAction, button tea.MouseButton, x, y int) chat.Model {
	next, _ := m.Update(tea.MouseMsg{Action: action, Button: button, X: x, Y: y})
	return next
}

// Dragging over a reply puts exactly what was dragged over on the clipboard, and says so.
func TestDraggingOverTheConversationCopiesIt(t *testing.T) {
	m, copied := selectable(t)

	row, col := rowAndCol(t, m, "parser lives")
	m = mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, col, row)
	m = mouse(m, tea.MouseActionMotion, tea.MouseButtonLeft, col+11, row)
	next, cmd := m.Update(tea.MouseMsg{
		Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft, X: col + 11, Y: row,
	})

	if len(*copied) != 1 || (*copied)[0] != "parser lives" {
		t.Fatalf("clipboard = %q, want [\"parser lives\"]", *copied)
	}
	if !strings.Contains(plain(next.Body()), "copied to clipboard") {
		t.Errorf("nothing says the text was copied:\n%s", plain(next.Body()))
	}
	if cmd == nil {
		t.Fatal("no timer was started, so the confirmation would stay forever")
	}

	// The confirmation takes itself away when the timer fires.
	next, _ = next.Update(cmd())
	if strings.Contains(plain(next.Body()), "copied to clipboard") {
		t.Errorf("the confirmation did not go away:\n%s", plain(next.Body()))
	}
}

// A drag spanning several rows copies them joined by newlines, trailing padding trimmed.
func TestASelectionAcrossLinesCopiesWholeLines(t *testing.T) {
	m, copied := selectable(t)

	fromRow, fromCol := rowAndCol(t, m, "where is the parser")
	toRow, _ := rowAndCol(t, m, "The parser lives")
	m = mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, fromCol, fromRow)
	m = mouse(m, tea.MouseActionMotion, tea.MouseButtonLeft, 79, toRow)
	// The release is what copies; the model after it is not read again in this test.
	_ = mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, 79, toRow)

	if len(*copied) != 1 {
		t.Fatalf("clipboard = %q, want one entry", *copied)
	}
	text := (*copied)[0]
	if !strings.Contains(text, "where is the parser") ||
		!strings.Contains(text, "The parser lives in internal/config.") {
		t.Errorf("the selection lost a line:\n%q", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("a copied line carries trailing padding: %q", line)
		}
	}
}

// The interface is not selectable at all. A drag that starts on the message box, the chrome under
// the conversation, copies nothing and claims nothing.
func TestTheInterfaceIsNotSelectable(t *testing.T) {
	m, copied := selectable(t)

	boxRow, _ := rowAndCol(t, m, "╭─")
	m = mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, 4, boxRow)
	m = mouse(m, tea.MouseActionMotion, tea.MouseButtonLeft, 20, boxRow)
	m = mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, 20, boxRow)

	if len(*copied) != 0 {
		t.Errorf("dragging over the box copied %q", *copied)
	}
	if strings.Contains(plain(m.Body()), "copied to clipboard") {
		t.Errorf("the screen claims a copy that never happened:\n%s", plain(m.Body()))
	}
}

// A click with no drag copies nothing: an empty selection on the clipboard would replace whatever
// was there with nothing, silently.
func TestABareClickCopiesNothing(t *testing.T) {
	m, copied := selectable(t)

	row, col := rowAndCol(t, m, "parser")
	m = mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, col, row)
	// The release decides whether anything is copied; the model after it is not read again.
	_ = mouse(m, tea.MouseActionRelease, tea.MouseButtonLeft, col, row)

	if len(*copied) != 0 {
		t.Errorf("a bare click copied %q", *copied)
	}
}

// The selection reads the same on screen as what lands on the clipboard: the highlight must not
// change the text it sits on.
func TestHighlightingChangesNoText(t *testing.T) {
	m, _ := selectable(t)
	before := plain(m.Body())

	row, col := rowAndCol(t, m, "parser lives")
	m = mouse(m, tea.MouseActionPress, tea.MouseButtonLeft, col, row)
	m = mouse(m, tea.MouseActionMotion, tea.MouseButtonLeft, col+11, row)

	if after := plain(m.Body()); after != before {
		t.Errorf("selecting changed the text on screen:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
