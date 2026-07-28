package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The invariant the rest of the layout depends on. BodyHeight subtracts a fixed amount of chrome, so
// a header that is four lines on somebody's terminal pushes their footer off the bottom of the
// screen and the last line of every conversation with it.
func TestTheHeaderIsAlwaysThreeLines(t *testing.T) {
	long := []string{
		"a-very-long-agent-name-indeed", "~/some/deeply/nested/project/path",
		"a-credential-name", "128.4k tokens, $12.3456", "▰▰▰▰▰▰▰▱",
	}

	for _, width := range []int{60, 72, 80, 100, 120, 200, 400} {
		for _, status := range []Status{
			{},
			{Screen: "chat", Mode: "cruise", Parts: long},
			{Screen: "worktrees", Parts: []string{strings.Repeat("x", 300)}},
		} {
			got := Header(Dimensions{Width: width, Height: 30}, status)
			if lines := strings.Count(got, "\n") + 1; lines != 3 {
				t.Errorf("width %d: header is %d lines, want 3", width, lines)
			}
		}
	}
}

// A header wider than the terminal wraps, and a wrapped header is the four line case above arriving
// by a different route.
func TestTheHeaderNeverExceedsTheTerminalWidth(t *testing.T) {
	for _, width := range []int{60, 72, 80, 100, 120} {
		got := Header(Dimensions{Width: width, Height: 30}, Status{
			Screen: "chat",
			Mode:   "runway",
			Parts: []string{
				"agent-two", "~/dev/canopy", "nemotron",
				"64.2k tokens, $3.1416", "▰▰▰▰▱▱▱▱", "and something else entirely",
			},
		})
		for i, line := range strings.Split(got, "\n") {
			if w := lipgloss.Width(line); w != width {
				t.Errorf("width %d: line %d renders %d cells, want exactly %d", width, i, w, width)
			}
		}
	}
}

// Details are dropped from the right, and the two facts that say where you are and what an agent may
// do without asking are never among the casualties.
func TestANarrowHeaderKeepsTheScreenAndTheMode(t *testing.T) {
	got := Header(Dimensions{Width: 60, Height: 30}, Status{
		Screen: "chat",
		Mode:   "cruise",
		Parts:  []string{"main", "~/a/very/long/path/that/will/not/fit", "some-credential"},
	})

	if !strings.Contains(got, "chat") {
		t.Error("the screen name was dropped, so a narrow terminal cannot say where it is")
	}
	if !strings.Contains(got, "cruise") {
		t.Error("the mode was dropped. Cruise runs commands without asking, and a narrow window " +
			"is not a reason to stop saying so")
	}
	if !strings.Contains(got, "canopy") {
		t.Error("the program's own name was dropped")
	}
}

// The mode is coloured by how much it can do unattended, so somebody who walked away from an agent
// in cruise can tell from across a desk. Under NO_COLOR there is nothing to assert about colour, so
// this asserts the styles differ rather than what they are.
func TestTheRiskiestModesAreNotStyledLikeTheSafeOnes(t *testing.T) {
	safe := modeStyle("build")
	for _, risky := range []string{"cruise", "runway"} {
		if modeStyle(risky).GetForeground() == safe.GetForeground() {
			t.Errorf("%q is styled the same as build, so the one mode that acts without asking "+
				"looks exactly like the one that does not", risky)
		}
	}
}
