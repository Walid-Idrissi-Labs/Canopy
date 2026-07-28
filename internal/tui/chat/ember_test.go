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
