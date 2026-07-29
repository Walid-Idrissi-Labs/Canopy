package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// Cycling past a mode is not choosing it.
//
// The key walks a ladder, so most journeys along it go through modes nobody wanted, and a mode
// takes hold at the next tool call rather than at the next message. Applying each rung as the key
// went past it would hand a working agent the read-only level for a fraction of a second, and
// whichever tool call landed in that fraction would be refused by a mode nobody meant to be in.
// The mode the key stops on is the one that means anything, so it is the only one applied.

func settle(t *testing.T, m chat.Model, cmd tea.Cmd) chat.Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("the key started no timer, so the mode it stopped on would never be applied")
	}
	next, _ := m.Update(cmd())
	return next
}

func inMode(name string) *fakeEngine {
	mode, _ := core.ModeByName(name)
	return &fakeEngine{session: core.Session{ID: "s1"}, mode: mode}
}

// The bug this exists to prevent, written as the journey somebody actually makes: a cruising agent
// is already running commands, and the way back to build goes through plan.
func TestCyclingPastAModeNeverPutsTheAgentInIt(t *testing.T) {
	engine := inMode(core.ModeCruise)
	m := boxed(engine)

	// cruise to build is three rungs, and the first of them is the read-only one.
	var cmd tea.Cmd
	for press := range 3 {
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		if got := engine.Mode("s1").Name; got != core.ModeCruise {
			t.Fatalf("press %d put the agent in %q on the way past it", press+1, got)
		}
	}

	// The model is finished with here: what matters now is what the conversation was put into.
	_ = settle(t, m, cmd)

	if got := engine.Mode("s1").Name; got != core.ModeBuild {
		t.Fatalf("the key stopped on build and the agent is in %q", got)
	}
	if len(engine.entered) != 1 || engine.entered[0] != core.ModeBuild {
		t.Errorf("the conversation entered %v, want build alone and nothing on the way to it",
			engine.entered)
	}
}

// And the mode does arrive on its own, without anything else having to happen. A selection that
// waited for the next message would be a key that had stopped working mid turn, which is the one
// time the modes matter most.
func TestTheModeTheKeyStopsOnArrivesByItself(t *testing.T) {
	engine := inMode(core.ModeCruise)
	m := boxed(engine)

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := engine.Mode("s1").Name; got != core.ModeCruise {
		t.Fatalf("the mode changed on the keystroke itself, leaving the agent in %q", got)
	}

	m = settle(t, m, cmd)

	if got := engine.Mode("s1").Name; got != core.ModePlan {
		t.Errorf("the agent is in %q a moment after the key stopped on plan", got)
	}
	if _, selecting := m.Selecting(); selecting {
		t.Error("the key still reports a selection after it was applied")
	}
}

// Each press supersedes the one before it. Without that, walking the ladder would apply every rung
// along it a moment late, which is the same bug with a delay attached.
func TestTheTimerFromAModeWalkedPastIsDropped(t *testing.T) {
	engine := inMode(core.ModeCruise)
	m := boxed(engine)

	m, abandoned := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m, kept := m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})

	m, _ = m.Update(abandoned())
	if got := engine.Mode("s1").Name; got != core.ModeCruise {
		t.Fatalf("a timer belonging to a mode that was passed through applied it: the agent is in %q",
			got)
	}

	_, _ = m.Update(kept())
	if got := engine.Mode("s1").Name; got != core.ModeConfined {
		t.Errorf("the timer for the mode the key stopped on left the agent in %q", got)
	}
}

// Somebody who presses the key and then sends a message has stopped cycling, and the message is
// governed by the mode they chose rather than by the one they were leaving.
func TestSendingAMessageAppliesTheSelectionFirst(t *testing.T) {
	engine := inMode(core.ModeCruise)
	m := boxed(engine)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	_, _ = run(m, "work out what to do")

	if len(engine.sentIn) != 1 {
		t.Fatalf("the message was sent %d times", len(engine.sentIn))
	}
	if engine.sentIn[0] != core.ModePlan {
		t.Errorf("the message was sent in %q, and the key had already stopped on plan",
			engine.sentIn[0])
	}
}

// A selection belongs to the conversation it was made in. Leaving applies it rather than dropping
// it, because a key that showed a mode, said what it does and then did nothing is worse than a key
// with no delay on it at all.
func TestLeavingTheConversationAppliesTheSelection(t *testing.T) {
	engine := inMode(core.ModeCruise)
	m := boxed(engine)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m.SetSession("s2", "other")

	if got := engine.Mode("s1").Name; got != core.ModePlan {
		t.Errorf("the selection was dropped on the way out: the conversation is in %q", got)
	}
	if _, selecting := m.Selecting(); selecting {
		t.Error("a selection made in one conversation followed the screen into another")
	}
}

// Naming a mode outright is not cycling through one, so it happens at once and supersedes whatever
// the key was in the middle of.
func TestNamingAModeSupersedesASelection(t *testing.T) {
	engine := inMode(core.ModeCruise)
	m := boxed(engine)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m, _ = run(m, "/mode confined")

	if got := engine.Mode("s1").Name; got != core.ModeConfined {
		t.Fatalf("the named mode did not take effect: the conversation is in %q", got)
	}
	if _, selecting := m.Selecting(); selecting {
		t.Error("the key is still settling on something the person has overruled by name")
	}
	for _, entered := range engine.entered {
		if entered == core.ModePlan {
			t.Error("the superseded selection was applied as well as the named mode")
		}
	}
}

// The box says what is enforced and what is coming, and never confuses the two. A screen that
// showed the selection alone would put "plan" over a conversation the permission layer was still
// letting run commands, which looks exactly like a guarantee and is not one.
func TestTheBoxSaysWhatIsEnforcedWhileTheKeyIsSettling(t *testing.T) {
	m := boxed(inMode(core.ModeCruise))

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})

	// The top edge alone. The notice under the wordmark names the mode too, and this is about what
	// the edge of the message box says, which is the part that is on screen at all times.
	var edge string
	for _, line := range strings.Split(plain(m.Body()), "\n") {
		if strings.Contains(line, "╭") {
			edge = line
			break
		}
	}
	if edge == "" {
		t.Fatal("the message box has no top edge")
	}

	if !strings.Contains(edge, core.ModeCruise) {
		t.Errorf("the edge stopped saying which mode is actually in effect: %q", edge)
	}
	if !strings.Contains(edge, core.ModePlan) {
		t.Errorf("the edge does not say where the key has got to: %q", edge)
	}
	if strings.Index(edge, core.ModeCruise) > strings.Index(edge, core.ModePlan) {
		t.Errorf("the edge puts the mode that is coming before the one in effect: %q", edge)
	}
}
