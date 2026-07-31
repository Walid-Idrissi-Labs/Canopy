package tui_test

// Where the name is drawn, and where it is not.
//
// There used to be two drawings of it: a large one in the middle of the opening screen and a small
// one in the corner of the header, with the rule that exactly one of them was on screen at a time.
// The large one is gone. The rule that replaces it is simpler and these tests are what hold it: the
// name is drawn once, in the corner, on every screen, and nothing draws it in the middle of a screen
// any more.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// cornerName is the top row of the C of the drawn name in the header, which is a run it shares with
// nothing else drawn anywhere in the program.
const cornerName = "▄▀▀▀▀ ▄▀▀▀▄"

// largeName is the widest row of the C of the name that used to be drawn in the middle of the
// opening screen. Kept as a string to look for rather than deleted with the drawing, because the
// point of these tests is that it does not come back.
const largeName = "▄███████▄"

// A conversation that has not started draws the name in the corner, like every other screen, and
// draws nothing in the middle.
func TestANewConversationDrawsTheNameOnlyInTheCorner(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launch(store, withOneKey()).(tui.App)
	next, _ := app.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	view := plain(next.(tui.App).View())

	if !strings.Contains(view, cornerName) {
		t.Errorf("the opening screen has no name in the header corner:\n%s", view)
	}
	if strings.Contains(view, largeName) {
		t.Errorf("the large name is back in the middle of the opening screen:\n%s", view)
	}
}

// Nothing writes the name in words in the middle of the screen either. The header writes "canopy" in
// text on every screen, so a second copy under a drawing was saying the same thing twice in one
// eyeful, and there is no drawing left for it to sit under.
func TestTheOpeningScreenDoesNotRepeatTheNameInWords(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launch(store, withOneKey()).(tui.App)
	next, _ := app.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	view := plain(next.(tui.App).View())

	if strings.Contains(view, "a terminal coding agent for running several at once") {
		t.Errorf("the tagline is on the opening screen:\n%s", view)
	}
	// The written name still has to be somewhere, for anything that cannot read block letters.
	if !strings.Contains(view, "canopy") {
		t.Errorf("the name is nowhere in text:\n%s", view)
	}
}

// A conversation in progress draws it in the same place, which is the whole point of the change: the
// corner does not change as somebody starts talking.
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

	if strings.Contains(view, largeName) {
		t.Errorf("the large name is drawn over a conversation:\n%s", view)
	}
	if !strings.Contains(view, cornerName) {
		t.Errorf("the name is not in the corner:\n%s", view)
	}
}

// Stated as the property rather than as two cases, because the property is the requirement: the name
// is in the corner and it does not move, whatever the screen is showing.
func TestTheNameIsInTheCornerWhateverIsOnScreen(t *testing.T) {
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

		if !strings.Contains(view, cornerName) {
			t.Errorf("%s: the name is not drawn in the corner:\n%s", tc.name, view)
		}
		if strings.Contains(view, largeName) {
			t.Errorf("%s: the large name is drawn as well:\n%s", tc.name, view)
		}
	}
}
