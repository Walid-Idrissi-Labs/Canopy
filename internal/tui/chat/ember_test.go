package chat_test

// The campfire on the message box, and what it says.
//
// The mark in the corner of the opening screen is the same fire, and it leaves with that screen.
// Losing it entirely the moment somebody says something makes the program feel like two programs, so
// it moves onto the box. What earns it the space is that it is lit while the agent is working and out
// when it is not: the spinner says that too, in the status row, and this says it in the corner of the
// thing somebody is already looking at.
//
// It is drawn in three parts now: a still base woven into the box's own rule, a tip above it that
// flickers, and smoke that rises two rows into the conversation and fades as it goes. The tests below
// hold the parts together — a tip that drifted off its base or smoke that climbed too high would read
// as a rendering fault, not a fire.

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
	return strings.Contains(view, brand.EmberBase)
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
		t.Errorf("a lit fire is on screen while nothing is running:\n%s", view)
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

// Lit or out, every part of it is the same width, or the rule it sits on shifts every time a turn
// starts and the tip slides off its base as the animation plays.
func TestTheEmberIsTheSameWidthLitOrOut(t *testing.T) {
	if got := len([]rune(brand.EmberOut)); got != brand.EmberWidth {
		t.Errorf("the coals are %d cells, want %d", got, brand.EmberWidth)
	}
	if got := len([]rune(brand.EmberBase)); got != brand.EmberWidth {
		t.Errorf("the base is %d cells, want %d", got, brand.EmberWidth)
	}
	for i := 0; i < brand.Frames; i++ {
		if got := len([]rune(brand.EmberTip(i))); got != brand.EmberWidth {
			t.Errorf("tip frame %d is %d cells, want %d", i, got, brand.EmberWidth)
		}
		for rise := 1; rise <= 2; rise++ {
			if got := len([]rune(brand.EmberWisp(i, rise))); got != brand.EmberWidth {
				t.Errorf("wisp frame %d at rise %d is %d cells, want %d",
					i, rise, got, brand.EmberWidth)
			}
		}
	}
}

// The heart of the base has to land on the base, or the caller brightens a stretch of border rule.
func TestTheEmberCoreIsInsideTheBase(t *testing.T) {
	if brand.EmberCoreColumn < 0 ||
		brand.EmberCoreColumn+brand.EmberCoreWidth > brand.EmberWidth {
		t.Errorf("the core spans columns %d to %d of a base %d wide",
			brand.EmberCoreColumn, brand.EmberCoreColumn+brand.EmberCoreWidth, brand.EmberWidth)
	}
}

// boxRow is where the top of the message box is drawn.
func boxRow(t *testing.T, lines []string) int {
	t.Helper()

	for i, line := range lines {
		if strings.Contains(line, "╭─") {
			return i
		}
	}
	t.Fatalf("no message box in:\n%s", strings.Join(lines, "\n"))
	return -1
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

// tipIn is where the flame's tip starts on a line, or -1.
func tipIn(line string) int {
	for f := 0; f < brand.Frames; f++ {
		if at := columnOf(line, strings.TrimSpace(brand.EmberTip(f))); at >= 0 {
			return at
		}
	}
	return -1
}

// wispIn is where a wisp at a rise starts on a line, or -1.
func wispIn(line string, rise int) int {
	for f := 0; f < brand.Frames; f++ {
		if wisp := strings.TrimSpace(brand.EmberWisp(f, rise)); wisp != "" {
			if at := columnOf(line, wisp); at >= 0 {
				return at
			}
		}
	}
	return -1
}

// The flame and its smoke rise only from a fire that is burning, and the smoke stops two rows up:
// any higher and it would be drifting through somebody's conversation.
func TestSmokeRisesOnlyFromALitFire(t *testing.T) {
	working := chatWith(t, []core.Turn{{
		Request: core.Message{Text: "hello"}, Text: "Thinking", State: core.TurnStreaming,
	}})
	lines := strings.Split(working, "\n")
	box := boxRow(t, lines)

	if box < 3 {
		t.Fatalf("no room above the box for the fire:\n%s", working)
	}
	if tipIn(lines[box-1]) < 0 {
		t.Errorf("no flame above a burning base:\n%s", working)
	}
	if wispIn(lines[box-2], 1) < 0 {
		t.Errorf("no smoke rising from the flame:\n%s", working)
	}
	if wispIn(lines[box-3], 2) < 0 {
		t.Errorf("the smoke does not fade upwards, it just stops:\n%s", working)
	}
	for row := 0; row <= box-4; row++ {
		if wispIn(lines[row], 1) >= 0 || wispIn(lines[row], 2) >= 0 {
			t.Errorf("smoke has climbed to row %d, which is too far up the conversation:\n%s",
				row, working)
		}
	}

	done := chatWith(t, []core.Turn{{
		Request: core.Message{Text: "hello"}, Text: "Done.", State: core.TurnComplete,
	}})
	doneLines := strings.Split(done, "\n")
	doneBox := boxRow(t, doneLines)
	for _, row := range []int{doneBox - 1, doneBox - 2, doneBox - 3} {
		if row < 0 {
			continue
		}
		if tipIn(doneLines[row]) >= 0 || wispIn(doneLines[row], 1) >= 0 || wispIn(doneLines[row], 2) >= 0 {
			t.Errorf("smoke is still rising from coals:\n%s", done)
		}
	}
}

// Each part sits over the one below it rather than near it, which is what stops the stack reading
// as stray marks somebody left in the margin.
func TestTheFireStacksOverItsBase(t *testing.T) {
	view := chatWith(t, []core.Turn{{
		Request: core.Message{Text: "hello"}, Text: "Thinking", State: core.TurnStreaming,
	}})
	lines := strings.Split(view, "\n")
	box := boxRow(t, lines)

	base := columnOf(lines[box], brand.EmberBase)
	if base < 0 {
		t.Fatalf("no base on the box's rule:\n%s", view)
	}

	within := func(name string, at int) {
		t.Helper()
		if at < base || at >= base+brand.EmberWidth {
			t.Errorf("the %s starts at column %d and the base spans %d to %d, so it is not over it",
				name, at, base, base+brand.EmberWidth-1)
		}
	}

	tip := tipIn(lines[box-1])
	if tip < 0 {
		t.Fatalf("no flame in the status row:\n%s", view)
	}
	within("flame", tip)

	if near := wispIn(lines[box-2], 1); near >= 0 {
		within("near wisp", near)
	}
	if far := wispIn(lines[box-3], 2); far >= 0 {
		within("far wisp", far)
	}
}

// The far wisp is sparser than the near one on every frame, which is what "fading" means in three
// block characters: less of it the higher it gets.
func TestTheSmokeThinsAsItRises(t *testing.T) {
	for f := 0; f < brand.Frames; f++ {
		near := len(strings.ReplaceAll(brand.EmberWisp(f, 1), " ", ""))
		far := len(strings.ReplaceAll(brand.EmberWisp(f, 2), " ", ""))
		if far >= near {
			t.Errorf("frame %d: the far wisp carries %d cells against the near one's %d",
				f, far, near)
		}
	}
}
