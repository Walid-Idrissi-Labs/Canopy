package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// Plan and build, written into the top edge of the message box along with the model.
//
// Both are worth having on screen at all times and neither is worth a row of the terminal. The mode
// is the one that matters: plan and build differ in whether the agent can change your files, and
// somebody who believes they are planning while the agent is building finds out afterwards.

func boxed(engine chat.Engine) chat.Model {
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 30)
	return m
}

func TestTheBoxSaysWhichModeAndWhichModel(t *testing.T) {
	view := plain(boxed(&fakeEngine{
		session: core.Session{ID: "s1", Model: "claude-opus-5"},
	}).Body())

	for _, want := range []string{"build", "claude-opus-5"} {
		if !strings.Contains(view, want) {
			t.Errorf("the message box does not say %q:\n%s", want, view)
		}
	}
}

// The indicator follows the trust level rather than a flag of its own.
//
// This is the property that makes it worth showing at all. Plan mode is enforced by the permission
// layer, and a screen that tracked the mode separately could say "plan" over a conversation that was
// free to write files, which is worse than saying nothing: it looks like a guarantee.
func TestPlanModeIsTheTrustLevelAndNotAWordOnTheScreen(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := boxed(engine)

	if got := m.Mode(); got != "build" {
		t.Fatalf("a fresh conversation opens in %q", got)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if engine.trust != core.TrustReadOnly {
		t.Errorf("planning left the conversation at %q, so the agent can still write", engine.trust)
	}
	if got := m.Mode(); got != "plan" {
		t.Errorf("the box says %q over a read-only conversation", got)
	}
	if view := plain(m.Body()); !strings.Contains(view, "plan") {
		t.Errorf("the box does not say it is planning:\n%s", view)
	}
}

// And leaving plan mode goes back to the level it left, not to a guess at one.
//
// An agent running at broad trust that planned and then went back to building would otherwise come
// out at standard. That is a silent demotion, and the kind nobody notices until a command they
// expected to run stops to ask permission.
func TestLeavingPlanModeGoesBackToTheLevelItLeft(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}, trust: core.TrustBroad}
	m := boxed(engine)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})

	if engine.trust != core.TrustBroad {
		t.Errorf("came back from planning at %q, having gone in at broad", engine.trust)
	}
	if got := m.Mode(); got != "build" {
		t.Errorf("the box says %q after leaving plan mode", got)
	}
}

// A keystroke must never hand an agent more than it started with.
//
// An agent that is read-only by its own profile is planning because that is all it can do, and the
// key that leaves plan mode has nothing to leave to. Saying so matters, because a key that silently
// does nothing reads as a key that is broken.
func TestTheModeKeyCannotPromoteAReadOnlyAgent(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}, trust: core.TrustReadOnly}
	m := boxed(engine)

	if got := m.Mode(); got != "plan" {
		t.Fatalf("a read-only conversation opens in %q", got)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if engine.trust != core.TrustReadOnly {
		t.Errorf("a keystroke promoted a read-only agent to %q", engine.trust)
	}
	if view := plain(m.Body()); !strings.Contains(view, "read-only") {
		t.Errorf("nothing on screen says why the key did nothing:\n%s", view)
	}
}

// A one line box reads as a search field, and what people write here is several sentences or a
// pasted stack trace. A box that grows to fit the first thing typed into it jogs the whole screen
// up on the second word.
func TestTheBoxHasRoomToWriteIn(t *testing.T) {
	view := plain(boxed(&fakeEngine{session: core.Session{ID: "s1"}}).Body())

	// Two walls per row.
	if rows := strings.Count(view, "│") / 2; rows < 3 {
		t.Errorf("the message box is %d rows tall with nothing in it:\n%s", rows, view)
	}
}

// A model name too long for the edge is dropped rather than cut, because half a model name looks
// like a different model, and knowing which one is answering is the entire point of showing it.
func TestALongModelNameIsDroppedRatherThanTruncated(t *testing.T) {
	long := strings.Repeat("provider/some-very-long-model-name", 3)
	m := chat.New(&fakeEngine{session: core.Session{ID: "s1", Model: long}}, "s1", "canopy", "claude")
	m.SetSize(60, 20)

	view := plain(m.Body())
	for _, line := range strings.Split(view, "\n") {
		if len([]rune(line)) > 60 {
			t.Fatalf("a %d column line in a 60 column terminal: %q", len([]rune(line)), line)
		}
	}
	if strings.Contains(view, "provider/some-very-long") {
		t.Errorf("the model name was drawn into a box with no room for it:\n%s", view)
	}
}
