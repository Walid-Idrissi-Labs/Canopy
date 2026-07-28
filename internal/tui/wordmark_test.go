package tui_test

// Which of the two drawings of the name is on screen, and when.
//
// There are two: a large one in the middle of the opening screen and a small one in the corner of the
// header. Both at once is one too many, and which one goes is not arbitrary. The opening screen has
// nothing else on it and the name is the point of it; a conversation in progress has a transcript on
// it and the name belongs in the corner.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// drawings counts how many of the two drawings of the name are on screen.
//
// Each is identified by the top row of its C, which is a run neither shares with the other and
// neither shares with anything else drawn anywhere in the program.
func drawings(view string) int {
	found := 0
	for _, top := range []string{"▄███████▄", "▄▀▀▀▀ ▄▀▀▀▄"} {
		if strings.Contains(view, top) {
			found++
		}
	}
	return found
}

// A conversation that has not started draws the name once, in the middle, at full size.
func TestANewConversationDrawsTheNameOnlyInTheMiddle(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launch(store, withOneKey()).(tui.App)
	next, _ := app.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	view := plain(next.(tui.App).View())

	if !strings.Contains(view, "▄███████▄") {
		t.Fatalf("the large name is not on the opening screen:\n%s", view)
	}
	if strings.Contains(view, "▄▀▀▀▀ ▄▀▀▀▄") {
		t.Errorf("the small name is in the header as well, so the name is drawn twice on a screen "+
			"whose whole job is to draw it once:\n%s", view)
	}
}

// The sentence under the large name is gone. The header writes "canopy" in text on every screen, so
// it was saying the same thing twice in one eyeful.
func TestTheOpeningScreenDoesNotRepeatTheNameInWords(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launch(store, withOneKey()).(tui.App)
	next, _ := app.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	view := plain(next.(tui.App).View())

	if strings.Contains(view, "a terminal coding agent for running several at once") {
		t.Errorf("the tagline is still under the drawn name:\n%s", view)
	}
	// The written name still has to be somewhere, for anything that cannot read block letters.
	if !strings.Contains(view, "canopy") {
		t.Errorf("the name is nowhere in text:\n%s", view)
	}
}

// Once there is a conversation the large one goes with the opening screen, and the corner picks it up.
func TestAStartedConversationDrawsTheNameOnlyInTheCorner(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{
		ID: "session-1",
		Turns: []core.Turn{{
			Request: core.Message{Text: "hello"},
			Text:    "Hi there.",
			State:   core.TurnComplete,
		}},
	}}

	app := launchWith(store, withOneKey(), engine).(tui.App)
	next, _ := app.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	view := plain(next.(tui.App).View())

	if strings.Contains(view, "▄███████▄") {
		t.Errorf("the large name is still drawn over a conversation:\n%s", view)
	}
	if !strings.Contains(view, "▄▀▀▀▀ ▄▀▀▀▄") {
		t.Errorf("the name did not move to the corner:\n%s", view)
	}
}

// Whichever state it is in, exactly one drawing of the name is on screen.
func TestTheNameIsNeverDrawnTwiceAtOnce(t *testing.T) {
	store := fake.New()
	defer store.Close()

	for _, tc := range []struct {
		name   string
		engine *stubEngine
	}{
		{"a new conversation", &stubEngine{}},
		{"one in progress", &stubEngine{session: core.Session{
			ID:    "session-1",
			Turns: []core.Turn{{Request: core.Message{Text: "x"}, Text: "y", State: core.TurnComplete}},
		}}},
	} {
		app := launchWith(store, withOneKey(), tc.engine).(tui.App)
		next, _ := app.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
		view := plain(next.(tui.App).View())

		if got := drawings(view); got != 1 {
			t.Errorf("%s: %d drawings of the name on screen, want exactly one:\n%s",
				tc.name, got, view)
		}
	}
}
