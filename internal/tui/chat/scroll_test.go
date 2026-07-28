package chat_test

// The marker that appears when the conversation has stopped following the tail.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

func scrolledModel(t *testing.T) chat.Model {
	t.Helper()

	m := model(&fakeEngine{session: longConversation(40)})
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	return m
}

// The key the marker names. ctrl+end is what a terminal veteran reaches for and it is not what
// somebody guesses from the arrow they were already scrolling with, so the marker names ctrl+down
// and ctrl+down therefore has to work.
func TestControlDownReturnsToTheTail(t *testing.T) {
	m := scrolledModel(t)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	body := plain(m.Body())
	if !strings.Contains(body, "more below") {
		t.Fatalf("the view did not leave the tail:\n%s", body)
	}
	// The marker has to name a key that does something, or it is worse than no marker.
	if !strings.Contains(body, "ctrl+↓") {
		t.Errorf("the marker does not name the key it is telling somebody to press:\n%s", body)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlDown})
	if strings.Contains(plain(m.Body()), "more below") {
		t.Error("ctrl+down did not return to the tail, so the marker is telling people to press a " +
			"key that does nothing")
	}
}

// The marker takes three rows and it has to take them from the conversation, not from the bottom of
// the screen. Taking them from the bottom pushes the message box off the terminal, which means the
// marker telling you how to get back to what you were typing has hidden what you were typing.
func TestTheMarkerDoesNotPushTheMessageBoxOffScreen(t *testing.T) {
	for _, height := range []int{14, 18, 24, 40} {
		m := chat.New(&fakeEngine{session: longConversation(40)}, "s1", "myproject", "claude")
		m.SetSize(80, height)
		m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

		settled := strings.Count(m.Body(), "\n") + 1

		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
		scrolled := strings.Count(m.Body(), "\n") + 1

		if scrolled > settled {
			t.Errorf("height %d: the body grew from %d lines to %d when the marker appeared, so it "+
				"took its rows from the bottom of the screen rather than from the conversation",
				height, settled, scrolled)
		}
	}
}

// At the tail there is nothing to say, and a marker that is always there is chrome nobody reads.
func TestThereIsNoMarkerWhileFollowingTheTail(t *testing.T) {
	m := scrolledModel(t)

	if body := plain(m.Body()); strings.Contains(body, "ctrl+↓") {
		t.Errorf("the marker is shown while the view is already at the tail:\n%s", body)
	}
}
