package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// The mode ladder, written into the top edge of the message box along with the model.
//
// Both are worth having on screen at all times and neither is worth a row of the terminal. The mode
// is the one that matters: the modes differ in what the agent can do to your files, and somebody who
// believes they are planning while the agent is building finds out afterwards.

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

// shift+tab walks the ladder, and the ladder is ordered by how much can go permanently wrong rather
// than by how much is allowed. That is why runway sits below cruise despite being the more capable
// of the two: it can run anything and it cannot leave you with a workspace that does not build.
//
// What the key walks is a selection. The mode itself changes a moment after the last press, which
// is the subject of the settling tests further down.
func TestTheKeyWalksTheLadderInOrder(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := boxed(engine)

	want := []string{
		core.ModeRunway, core.ModeCruise, core.ModePlan, core.ModeConfined, core.ModeBuild,
	}
	for i, expected := range want {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		got, selecting := m.Selecting()
		if !selecting {
			t.Fatalf("press %d left the key on nothing, want %q", i+1, expected)
		}
		if got != expected {
			t.Fatalf("press %d landed on %q, want %q", i+1, got, expected)
		}
	}
}

// Every mode is a trust level the permission layer decides against, not a word on the screen.
//
// This is the property that makes it worth showing at all. A screen that tracked the mode separately
// could say "plan" over a conversation that was free to write files, which is worse than saying
// nothing: it looks like a guarantee.
func TestEveryModeCarriesTheLevelItClaims(t *testing.T) {
	for _, mode := range core.Modes() {
		engine := &fakeEngine{session: core.Session{ID: "s1"}}
		m := boxed(engine)

		next, _ := run(m, "/mode "+mode.Name)
		if got := engine.Mode("s1"); got.Trust != mode.Trust {
			t.Errorf("%s left the conversation at %q, want %q", mode.Name, got.Trust, mode.Trust)
		}
		if got := next.Mode(); got != mode.Name {
			t.Errorf("the box says %q after asking for %s", got, mode.Name)
		}
		if view := plain(next.Body()); !strings.Contains(view, mode.Name) {
			t.Errorf("the box does not say it is in %s:\n%s", mode.Name, view)
		}
	}
}

// Plan is the one that must be enforced rather than requested, so it is worth asserting on its own:
// the level it sets is the one that denies writes outright.
func TestPlanModeIsReadOnlyAndNotAnInstruction(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := boxed(engine)

	next, _ := run(m, "/mode plan")
	if got := engine.Mode("s1").Trust; got != core.TrustReadOnly {
		t.Errorf("planning left the conversation at %q, so the agent can still write", got)
	}
	if !next.Planning() {
		t.Error("the screen does not consider itself to be planning")
	}
}

// Every mode is also told what it is doing, or the level is enforced and never explained: an agent
// that has not been told it is planning tries to edit, is refused, tries again, and burns the turn
// thrashing against a boundary nobody mentioned.
func TestTheRestrictiveModesTellTheModelWhatTheyAre(t *testing.T) {
	for _, name := range []string{
		core.ModePlan, core.ModeConfined, core.ModeRunway, core.ModeCruise,
	} {
		mode, _ := core.ModeByName(name)
		if strings.TrimSpace(mode.Prompt) == "" {
			t.Errorf("%s has no prompt, so the model finds out by being refused", name)
		}
	}
	// Build is the exception and deliberately so. It is the ordinary way to work, and describing it
	// would spend context telling the model that nothing unusual is going on.
	build, _ := core.ModeByName(core.ModeBuild)
	if build.Prompt != "" {
		t.Error("build carries a prompt, which spends context saying nothing is unusual")
	}
}

// A keystroke must never hand an agent more than its configuration allows.
//
// An agent started read-only is read-only because somebody decided it should be, and a key in a chat
// window is not the place to overrule that. The key skips past what it cannot reach rather than
// stopping, so it still does something, and it can never climb.
func TestTheModeKeyCannotPromoteAConfinedAgent(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}, trust: core.TrustReadOnly}
	m := boxed(engine)

	if got := m.Mode(); got != core.ModePlan {
		t.Fatalf("a read-only conversation opens in %q", got)
	}
	for range len(core.Modes()) + 1 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		if got := engine.Mode("s1").Trust; got != core.TrustReadOnly {
			t.Fatalf("a keystroke promoted a read-only agent to %q", got)
		}
	}
}

// The bare command lists the ladder with what each one may change, because that is the question
// somebody actually has when they type it: not what it is called, but what it can do to their code.
func TestTheBareModeCommandExplainsTheLadder(t *testing.T) {
	next, _ := run(boxed(&fakeEngine{session: core.Session{ID: "s1"}}), "/mode")

	view := plain(next.Body())
	for _, mode := range core.Modes() {
		if !strings.Contains(view, mode.Description) {
			t.Errorf("the listing does not explain %s:\n%s", mode.Name, view)
		}
	}
}

// Steering corrects the agent without throwing away what it has done, which is the distinction the
// whole feature rests on. Interrupting to correct means discarding the work in progress, and usually
// the reasoning that led to it.
func TestSteerQueuesGuidanceRatherThanInterrupting(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := boxed(engine)

	next, _ := run(m, "/steer use the existing parser")
	if len(engine.steered) != 1 || engine.steered[0] != "use the existing parser" {
		t.Errorf("steered = %v", engine.steered)
	}
	if engine.cancelled != 0 {
		t.Errorf("steering cancelled the turn %d times, and it must never cancel", engine.cancelled)
	}
	_ = next
}

// And it says what it needs when given nothing, rather than queueing an empty correction.
func TestSteerWithNothingToSaySaysSo(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	next, _ := run(boxed(engine), "/steer")

	if len(engine.steered) != 0 {
		t.Errorf("an empty correction was queued: %v", engine.steered)
	}
	if view := plain(next.Body()); !strings.Contains(view, "do differently") {
		t.Errorf("the screen does not say what it wants:\n%s", view)
	}
}

// A one line box reads as a search field, and what people write here is several sentences or a
// pasted stack trace.
func TestTheBoxHasRoomToWriteIn(t *testing.T) {
	view := plain(boxed(&fakeEngine{session: core.Session{ID: "s1"}}).Body())

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

// The key skips what it cannot reach rather than stopping on it.
//
// An agent capped at standard can plan, use confined, and build, and cannot do either broad mode.
// The key still has to do something: stopping would read as a jammed key, and refusing outright
// would mean it could not reach the narrower modes either, which it certainly can.
func TestTheKeySkipsModesThisAgentCannotEnter(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}, trust: core.TrustStandard}
	m := boxed(engine)

	if got := m.Mode(); got != core.ModeBuild {
		t.Fatalf("an agent capped at standard opens in %q", got)
	}

	// runway and cruise both need broad, so the next reachable stop is plan.
	landing := func() string {
		t.Helper()
		got, selecting := m.Selecting()
		if !selecting {
			t.Fatal("the key stopped on nothing at all")
		}
		return got
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := landing(); got != core.ModePlan {
		t.Errorf("the key landed on %q, want it to skip past what it cannot enter", got)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := landing(); got != core.ModeConfined {
		t.Errorf("the key landed on %q, want the confined posture between plan and build", got)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if got := landing(); got != core.ModeBuild {
		t.Errorf("the key landed on %q after confined, want build", got)
	}
}

// A queued correction stays on screen, as itself, until it is delivered. The old feedback was one
// sentence in the status row that vanished on the next keystroke, which read as the correction
// having been swallowed — and a person who believes that types it again.
func TestQueuedSteeringStaysOnScreenUntilDelivered(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{{
		ID: "turn-1", Request: core.Message{Text: "build it"}, Text: "on it",
		State: core.TurnStreaming,
	}}}}
	m := boxed(engine)

	next, _ := run(m, "/steer use the existing parser")
	view := plain(next.Body())
	if !strings.Contains(view, "steering") || !strings.Contains(view, "use the existing parser") {
		t.Fatalf("the queued guidance is not on screen:\n%s", view)
	}

	// Still there after other keystrokes, which is the whole point.
	next = typeText(next, "meanwhile")
	if !strings.Contains(plain(next.Body()), "use the existing parser") {
		t.Errorf("the guidance vanished on the next keystroke:\n%s", plain(next.Body()))
	}

	// And gone the moment the engine no longer holds it, because by then it is an ordinary message
	// in the transcript and there is nothing left to wait for.
	engine.queuedSteering = nil
	if strings.Contains(plain(next.Body()), "use the existing parser") {
		t.Errorf("the pane outlived the queue:\n%s", plain(next.Body()))
	}
}
