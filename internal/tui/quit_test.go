package tui_test

// Leaving the program, and abandoning a half written message.
//
// Both are destructive in the small: one throws away the screen state somebody was in and the other
// a thought they were typing. The rules are the ones every comparable tool converged on — quitting a
// conversation takes two presses of ctrl+c, escape clears the box when nothing is running — and the
// tests hold Canopy to them because they are exactly the kind of convention a refactor quietly loses.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

func stroke(s string) tea.KeyMsg {
	switch s {
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// quits reports whether a command carries tea.Quit.
func quits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// The first ctrl+c in an idle conversation asks, the second leaves. One press quitting outright is
// the sharpest edge a chat program can have: ctrl+c is also the interrupt key, so the press meant
// for a turn that just finished on its own would otherwise throw somebody out of the program.
func TestCtrlCAsksBeforeQuittingAConversation(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launch(store, withOneKey()).(tui.App)

	next, cmd := app.Update(stroke("ctrl+c"))
	app = next.(tui.App)
	if quits(cmd) {
		t.Fatal("one press of ctrl+c quit the program")
	}
	if !strings.Contains(plain(app.View()), "ctrl+c again") {
		t.Error("the first press does not say a second one will quit")
	}

	_, cmd = app.Update(stroke("ctrl+c"))
	if !quits(cmd) {
		t.Error("the second press of ctrl+c did not quit")
	}
}

// Any other key is a change of mind, and the next ctrl+c starts over rather than firing.
func TestAnotherKeyCancelsTheQuitConfirmation(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launch(store, withOneKey()).(tui.App)

	next, _ := app.Update(stroke("ctrl+c"))
	next, _ = next.(tui.App).Update(stroke("h"))
	next, cmd := next.(tui.App).Update(stroke("ctrl+c"))

	if quits(cmd) {
		t.Error("ctrl+c quit on its first press after the confirmation had been abandoned")
	}
	if !strings.Contains(plain(next.(tui.App).View()), "ctrl+c again") {
		t.Error("the confirmation did not restart cleanly")
	}
}

// Quit works from the screens with no text field too. It did nothing on agents and review, which on
// the two screens with nothing to type into looked like the program refusing to close.
func TestCtrlCQuitsFromTheAgentsScreen(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launch(store, withOneKey()).(tui.App)
	next, _ := app.Update(stroke("ctrl+d"))
	if got := next.(tui.App).Screen(); got != "agents" {
		t.Fatalf("ctrl+d landed on %q, want agents", got)
	}

	_, cmd := next.(tui.App).Update(stroke("ctrl+c"))
	if !quits(cmd) {
		t.Error("ctrl+c does not quit from the agents screen")
	}
}

// A mode the key stopped on is applied on the way out rather than left to a timer that will never
// fire.
//
// The mode is written down with the conversation and restored with it, so a selection abandoned by
// quitting would reopen tomorrow in the mode somebody had just moved away from, which is the one
// setting where being quietly wrong matters most.
func TestQuittingAppliesAModeTheKeyStoppedOn(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{ID: "session-1"}}
	app := launchWith(store, withOneKey(), engine).(tui.App)

	next, _ := app.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := engine.Mode("session-1").Name; got != core.ModeBuild {
		t.Fatalf("the keystroke itself changed the mode to %q", got)
	}

	next, _ = next.(tui.App).Update(stroke("ctrl+c"))
	_, cmd := next.(tui.App).Update(stroke("ctrl+c"))
	if !quits(cmd) {
		t.Fatal("the second press of ctrl+c did not quit")
	}
	if got := engine.Mode("session-1").Name; got != core.ModeRunway {
		t.Errorf("quitting left the conversation in %q, and the key had stopped on runway", got)
	}
}

// Escape clears a half written message once nothing is running, which is what it means in every
// comparable tool. Before this the only way out of a draft was ctrl+u, a key no footer mentions.
func TestEscapeClearsAHalfWrittenMessage(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{ID: "session-1"}}
	app := launchWith(store, withOneKey(), engine).(tui.App)

	next, _ := app.Update(stroke("h"))
	next, _ = next.(tui.App).Update(stroke("i"))
	if got := next.(tui.App).ChatInput(); got != "hi" {
		t.Fatalf("typed %q, want %q", got, "hi")
	}

	next, _ = next.(tui.App).Update(stroke("esc"))
	if got := next.(tui.App).ChatInput(); got != "" {
		t.Errorf("escape left %q in the box", got)
	}
}
