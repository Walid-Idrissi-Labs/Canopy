package chat_test

// The campfire on the message box, and what it says.
//
// The mark in the corner of the opening screen is the same fire, and it leaves with that screen.
// Losing it entirely the moment somebody says something makes the program feel like two programs, so
// it moves onto the box. What earns it the space is that it is lit while the agent is working and out
// when it is not: the spinner says that too, in the status row, and this says it in the corner of the
// thing somebody is already looking at.

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

func chatWith(t *testing.T, turns []core.Turn) string {
	t.Helper()

	m := model(&fakeEngine{session: core.Session{ID: "s1", Turns: turns}})
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	return plain(m.Body())
}

func lit(view string) bool {
	for i := 0; i < brand.Frames; i++ {
		if strings.Contains(view, brand.Ember(i)) {
			return true
		}
	}
	return false
}

// The opening screen draws the full mark, fire and all, so a second one on the box would be two
// campfires on a screen with room for neither.
func TestThereIsNoEmberOnTheOpeningScreen(t *testing.T) {
	view := chatWith(t, nil)

	if strings.Contains(view, brand.EmberOut) || lit(view) {
		t.Errorf("the box carries a fire while the full mark is already on screen:\n%s", view)
	}
}

// A finished turn puts it out. The shape changes as well as the colour, so this still reads on a
// terminal with no colour in it.
func TestTheFireIsOutWhenTheAgentIsNotWorking(t *testing.T) {
	view := chatWith(t, []core.Turn{{
		Request: core.Message{Text: "hello"}, Text: "Hi.", State: core.TurnComplete,
	}})

	if !strings.Contains(view, brand.EmberOut) {
		t.Errorf("the fire is still burning after the agent finished:\n%s", view)
	}
	if lit(view) {
		t.Errorf("a lit frame is on screen while nothing is running:\n%s", view)
	}
}

// And a turn in flight lights it.
func TestTheFireIsLitWhileTheAgentWorks(t *testing.T) {
	view := chatWith(t, []core.Turn{{
		Request: core.Message{Text: "hello"}, Text: "Thinking", State: core.TurnStreaming,
	}})

	if !lit(view) {
		t.Errorf("the fire is out while the agent is working:\n%s", view)
	}
	if strings.Contains(view, brand.EmberOut) {
		t.Errorf("the coals are drawn while the agent is working:\n%s", view)
	}
}

// Lit or out, it is the same width, or the rule it sits on shifts every time a turn starts.
func TestTheEmberIsTheSameWidthLitOrOut(t *testing.T) {
	if got := len([]rune(brand.EmberOut)); got != brand.EmberWidth {
		t.Errorf("the coals are %d cells, want %d", got, brand.EmberWidth)
	}
	for i := 0; i < brand.Frames; i++ {
		if got := len([]rune(brand.Ember(i))); got != brand.EmberWidth {
			t.Errorf("frame %d is %d cells, want %d", i, got, brand.EmberWidth)
		}
	}
}

// statusRowAbove returns the line directly above the message box, which is where the wisp goes.
//
// Located by the box rather than by searching for the smoke, because the smoke is made of the same
// two characters as half the drawings in this program and a substring search for it finds coals,
// spinner frames and the tent.
func statusRowAbove(t *testing.T, view string) string {
	t.Helper()

	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if strings.Contains(line, "╭─") && i > 0 {
			return lines[i-1]
		}
	}
	t.Fatalf("no message box in:\n%s", view)
	return ""
}

// columnOf is where a drawing starts, counted in cells rather than bytes.
//
// strings.Index answers in bytes, and every character in these drawings is three bytes, so a byte
// answer is nearly three times the column and compares against nothing useful.
func columnOf(line, want string) int {
	at := strings.Index(line, want)
	if at < 0 {
		return -1
	}
	return len([]rune(line[:at]))
}

// Smoke rises only from a fire that is burning, and it rises one row.
//
// One row is the whole constraint: the status row above the box is where it goes, and anything
// higher would be drifting up through the conversation.
func TestSmokeRisesOnlyFromALitFire(t *testing.T) {
	working := chatWith(t, []core.Turn{{
		Request: core.Message{Text: "hello"}, Text: "Thinking", State: core.TurnStreaming,
	}})
	if !smokeIn(statusRowAbove(t, working)) {
		t.Errorf("no smoke above a burning fire:\n%s", working)
	}

	done := chatWith(t, []core.Turn{{
		Request: core.Message{Text: "hello"}, Text: "Done.", State: core.TurnComplete,
	}})
	if smokeIn(statusRowAbove(t, done)) {
		t.Errorf("smoke is still rising from coals:\n%s", done)
	}
}

// The wisp sits over the flame rather than near it, which is what stops it reading as a stray mark
// somebody left in the status row.
func TestSmokeSitsDirectlyOverTheFire(t *testing.T) {
	view := chatWith(t, []core.Turn{{
		Request: core.Message{Text: "hello"}, Text: "Thinking", State: core.TurnStreaming,
	}})
	lines := strings.Split(view, "\n")

	fire, status := -1, ""
	for i, line := range lines {
		for f := 0; f < brand.Frames; f++ {
			if at := columnOf(line, brand.Ember(f)); at >= 0 {
				fire, status = at, lines[i-1]
			}
		}
	}
	if fire < 0 {
		t.Fatalf("no fire on the box:\n%s", view)
	}

	smoke := -1
	for f := 0; f < brand.Frames; f++ {
		if at := columnOf(status, strings.TrimRight(brand.EmberSmoke(f), " ")); at >= 0 {
			smoke = at
		}
	}
	if smoke < 0 {
		t.Fatalf("no smoke in the status row:\n%s", view)
	}

	// The wisp is drawn inside a block the same width as the fire, and its own leading spaces place
	// it, so it lands somewhere within the fire's span rather than exactly on its first column.
	if smoke < fire || smoke >= fire+brand.EmberWidth {
		t.Errorf("the smoke starts at column %d and the fire spans %d to %d, so it is not over it",
			smoke, fire, fire+brand.EmberWidth-1)
	}
}

// Every frame of both is the same width, or the fire shifts under its own smoke as it flickers.
func TestTheSmokeIsTheSameWidthAsTheFire(t *testing.T) {
	for i := 0; i < brand.Frames; i++ {
		if got := len([]rune(brand.EmberSmoke(i))); got != brand.EmberWidth {
			t.Errorf("smoke frame %d is %d cells, want %d", i, got, brand.EmberWidth)
		}
	}
}

// smokeIn reports whether a line carries a wisp, matched on the trimmed frame so the leading spaces
// that position it do not have to be reproduced here.
func smokeIn(line string) bool {
	for i := 0; i < brand.Frames; i++ {
		if wisp := strings.TrimRight(brand.EmberSmoke(i), " "); columnOf(line, wisp) >= 0 {
			return true
		}
	}
	return false
}
