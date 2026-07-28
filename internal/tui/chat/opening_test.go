package chat_test

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// M-08. The screen a conversation opens on.
//
// These are about where things are, which for this one screen is the requirement rather than a
// detail of it. The old welcome block put the same words on the screen and read as an empty
// conversation, because that is what it was: a transcript with nothing in it and the box on the
// floor underneath.

// openingAt builds an empty conversation at a size.
func openingAt(width, height int) []string {
	m := chat.New(&fakeEngine{}, "s1", "canopy", "claude")
	m.SetSize(width, height)
	return strings.Split(plain(m.Body()), "\n")
}

func rowContaining(lines []string, want string) int {
	for i, line := range lines {
		if strings.Contains(line, want) {
			return i
		}
	}
	return -1
}

// The composition that was asked for, stated precisely.
//
// Not the box centred and not the name centred: the middle of the space between them is what lands
// on the middle of the screen, so the pair reads as one object with the eye falling between the two.
// The three readings look similar in a mock-up and put the box in visibly different places.
func TestTheSpaceBetweenTheNameAndTheBoxIsAtTheMiddleOfTheScreen(t *testing.T) {
	const height = 30
	lines := openingAt(100, height)

	// The bottom of the C, which is the last row of the drawn name. It used to be the written name
	// underneath, then the bottom of a frame around it, and both of those are gone: the header draws
	// "canopy" in text on every screen, so the line under it was saying the same thing twice, and the
	// frame was competing with the letters it was meant to set off.
	name := rowContaining(lines, "▀███████▀")
	box := rowContaining(lines, "╭")
	if name < 0 || box < 0 {
		t.Fatalf("the name is on row %d and the box on row %d:\n%s", name, box, strings.Join(lines, "\n"))
	}
	if box <= name {
		t.Fatalf("the box is above the name, at row %d against row %d", box, name)
	}

	// Doubled rather than halved, so a gap of an even number of rows is not quietly rounded into
	// looking correct.
	middle, screen := name+box, height-1
	if middle < screen-1 || middle > screen+1 {
		t.Errorf("the space between the name and the box is centred on row %.1f of %d:\n%s",
			float64(middle)/2, height, strings.Join(lines, "\n"))
	}
}

// The mark goes in the bottom right corner, which means the corner and not merely somewhere down
// there on the right.
func TestTheMarkSitsInTheBottomRightCorner(t *testing.T) {
	const width = 100
	lines := openingAt(width, 30)

	last := lines[len(lines)-1]
	if !strings.HasSuffix(strings.TrimRight(last, " "), "▄") {
		t.Fatalf("the bottom row does not end in the mark: %q\n%s", last, strings.Join(lines, "\n"))
	}
	// Against the right edge, with the margin that stops a logo looking like it was cut off by the
	// side of the terminal.
	if got := len([]rune(strings.TrimRight(last, " "))); got != width-2 {
		t.Errorf("the mark reaches column %d of %d, so it is not in the corner", got, width)
	}
}

// Giving the flame its own colour means slicing each row of the mark into three pieces and styling
// the middle one. Sliced wrongly that drops or duplicates a glyph, and under a test it is the only
// part of the change that can be seen at all: lipgloss finds no terminal here, so it renders every
// style as plain text and the colours themselves are invisible. The colours are asserted where they
// can be, on the styles, in internal/tui/theming_test.go.
func TestColouringTheFlameLeavesTheMarkExactlyAsDrawn(t *testing.T) {
	const width = 100
	// The margin the opening screen keeps to the right of the mark.
	const margin = 2

	lines := openingAt(width, 30)
	frame := brand.Frame(0)
	start := width - margin - brand.MarkWidth

	for i, want := range frame {
		row := []rune(lines[len(lines)-len(frame)+i])
		if len(row) < start {
			t.Errorf("row %d of the mark stops before column %d: %q", i, start, string(row))
			continue
		}
		if got := strings.TrimRight(string(row[start:]), " "); got != want {
			t.Errorf("row %d of the mark came out as %q, and the brand package draws %q", i, got, want)
		}
	}
}

// A screen too short for the mark gets no mark, rather than one overlapping the message box.
//
// The same rule the brand package applies to width, for the same reason: half a tent looks like the
// program is broken, and a logo drawn over the box somebody is trying to type in is worse than that.
func TestAShortScreenGetsNoMarkRatherThanOneOverTheBox(t *testing.T) {
	lines := openingAt(100, 20)
	body := strings.Join(lines, "\n")

	if strings.Contains(body, "█▀█▀█") {
		t.Errorf("the mark was drawn with no room for it:\n%s", body)
	}
	// And the screen is otherwise intact, or this passes by having drawn nothing at all.
	for _, want := range []string{"Canopy", "╭", "working in canopy"} {
		if !strings.Contains(body, want) {
			t.Errorf("the short screen lost %q:\n%s", want, body)
		}
	}
}

// A line wider than the terminal wraps, which pushes the frame a row taller than the window and
// scrolls everything above it away. It looks like the program breaking rather than like a narrow
// window, so it is checked at every size worth caring about.
func TestNoLineIsWiderThanTheTerminal(t *testing.T) {
	for _, size := range [][2]int{{60, 16}, {64, 20}, {80, 24}, {100, 30}, {120, 40}, {200, 50}} {
		width, height := size[0], size[1]
		for i, line := range openingAt(width, height) {
			if got := len([]rune(line)); got > width {
				t.Errorf("at %dx%d row %d is %d columns: %q", width, height, i, got, line)
			}
		}
	}
}

// Exactly as tall as it was given, because the frame pins its footer by padding whatever it gets to
// the body height. A screen that came back short would float the footer, and one that came back
// long would push it off the bottom.
func TestTheOpeningScreenIsExactlyAsTallAsItWasGiven(t *testing.T) {
	for _, height := range []int{16, 20, 24, 30, 40} {
		if got := len(openingAt(100, height)); got != height {
			t.Errorf("asked for %d rows and got %d", height, got)
		}
	}
}

// And it is gone the moment there is a conversation, which is the other half of the same promise:
// the logo belongs to the empty screen and not to the top of every transcript.
func TestTheOpeningScreenGoesAwayOnceSomethingIsSaid(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{{
		Request: core.Message{Text: "what does this do"},
		Text:    "it opens a conversation",
		State:   core.TurnComplete,
	}}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 30)

	body := plain(m.Body())
	if strings.Contains(body, "▄▀▀▀▀") {
		t.Errorf("the drawn name is still on screen over a conversation:\n%s", body)
	}
	if !strings.Contains(body, "what does this do") {
		t.Errorf("the conversation is not on screen:\n%s", body)
	}
}
