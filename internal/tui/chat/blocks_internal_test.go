package chat

// The one test in this package that is inside it rather than beside it.
//
// Everything else about the chat screen is asserted from outside, through keystrokes and rendered
// frames, which is the right level for behaviour. This is about an invariant between two blocks that
// cannot both be up, and the state that would break it is one no keystroke can currently produce, so
// it is built here directly rather than left to a comment claiming it is handled.

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A panel with nothing in it does not stand in the task block's place.
//
// Both halves of btwUp are load bearing. The panel draws nothing when there is nothing to show, so a
// task block that stood aside for it would leave the screen with neither block on it, which is the
// one arrangement worse than showing both.
func TestAnEmptyBtwPanelDoesNotStandInTheTaskBlocksPlace(t *testing.T) {
	m := Model{
		width:  80,
		height: 24,
		// Open, and with nothing to open over, which is the state the flag alone cannot rule out.
		btwOpen: true,
		session: core.Session{
			ID:    "s1",
			Tasks: []core.Task{{Text: "the task item", State: core.TaskInProgress}},
		},
	}

	if m.btwUp() {
		t.Error("a panel with no asides in it reads as being up")
	}
	if lines := m.btwPanel(); len(lines) != 0 {
		t.Errorf("the empty panel drew %d rows", len(lines))
	}

	block := m.taskPane()
	if len(block) == 0 {
		t.Fatal("the tasks stood aside for a panel that is not there")
	}
	if !strings.Contains(strings.Join(block, "\n"), "the task item") {
		t.Errorf("the task block is up but has no tasks in it:\n%s", strings.Join(block, "\n"))
	}

	// And with something to show it does stand in their place, which is the other half of the same
	// condition and the behaviour U-18 asked for.
	m.asides = []asideExchange{{question: "why", answer: "because"}}
	if !m.btwUp() {
		t.Error("a panel with an aside in it does not read as being up")
	}
	if len(m.taskPane()) != 0 {
		t.Error("both blocks are up at once")
	}
}
