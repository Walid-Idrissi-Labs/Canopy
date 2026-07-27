package tui

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// The overlay has to be exhaustive. One that lists most of the bindings teaches people it cannot be
// trusted, and then they stop opening it. Every key the application routes is asserted here by hand
// rather than counted, because a count would pass while listing the wrong ones.
func TestEveryKeyTheApplicationHandlesIsListed(t *testing.T) {
	// Tall enough that nothing scrolls, so this is about what the table contains rather than about
	// what happens to be on screen. That the rest is reachable by scrolling is a separate test.
	listed := Help(Dimensions{Width: 100, Height: HelpHeight(100) + 8})

	for _, key := range []string{
		"?", "ctrl+c", "enter", "esc", "ctrl+d", "ctrl+k", "ctrl+r", "ctrl+n", "up / down",
		"y", "a", "j / k", "n", "v", "w", "r", "tab", "c", "space", "g / G", "ctrl+s", "q",
		"1 / 2 / 3", "m", "d / x", "K",
	} {
		if !strings.Contains(listed, key) {
			t.Errorf("the overlay does not list %q", key)
		}
	}

	// And every entry says what it does, since a key with no description is a key nobody will press.
	for _, line := range strings.Split(stripANSI(listed), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !strings.HasPrefix(line, "  ") {
			continue
		}
		if len(strings.Fields(trimmed)) < 2 {
			t.Errorf("this binding has no description: %q", trimmed)
		}
	}
}

func TestTheOverlayIsReadableAtEightyColumns(t *testing.T) {
	for _, line := range strings.Split(stripANSI(Help(Dimensions{Width: 80, Height: HelpHeight(80) + 8})), "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("a line is %d columns wide: %q", len([]rune(line)), line)
		}
	}
}

// The second theme exists to prove the first is not cheating. If the interface is unreadable with
// no colour in it, then somewhere a meaning is carried by a hue, and it was already invisible to a
// colour blind reader and to anybody running with NO_COLOR set.
func TestBothThemesCarryMeaningWithoutColour(t *testing.T) {
	if len(theme.All()) < 2 {
		t.Fatalf("%d themes, want at least two", len(theme.All()))
	}

	for _, palette := range theme.All() {
		theme.Set(palette)
		t.Run(palette.Name, func(t *testing.T) {
			for _, status := range []statusText{
				testStatus("passing"), testStatus("failing"), testStatus("stale"),
				testStatus("unknown"), testStatus("not-configured"),
			} {
				if strings.TrimSpace(status.word) == "" {
					t.Error("a state has no word, so it depends on colour alone")
				}
				if status.glyph == "" {
					t.Error("a state has no glyph")
				}
				if len([]rune(status.glyph)) != 1 {
					t.Errorf("the glyph %q is not one cell, so the column shifts when the state changes",
						status.glyph)
				}
			}
		})
	}
	theme.Set(theme.Default)
}

func TestAThemeCanBeLookedUpByNameAndAnUnknownOneSaysWhatExists(t *testing.T) {
	if _, ok := theme.ByName("mono"); !ok {
		t.Error("the monochrome theme cannot be found by name")
	}
	if _, ok := theme.ByName("solarized"); ok {
		t.Error("a theme that does not exist was found")
	}
	if names := theme.Names(); len(names) != len(theme.All()) {
		t.Errorf("Names returned %v for %d themes", names, len(theme.All()))
	}
}

// The overlay is taller than a short terminal and has to stay so, because the alternative is
// dropping bindings to make it fit and an overlay that lists most of the keys is one people stop
// trusting. So it scrolls, and everything in it has to be reachable that way.
func TestEveryBindingIsReachableByScrolling(t *testing.T) {
	dim := Dimensions{Width: 100, Height: 24}

	var seen strings.Builder
	for from := 0; from <= HelpHeight(dim.Width); from++ {
		seen.WriteString(stripANSI(HelpFrom(dim, from)))
		seen.WriteString("\n")
	}
	all := seen.String()

	for _, key := range []string{
		"?", "ctrl+c", "enter", "esc", "ctrl+d", "ctrl+k", "ctrl+r", "ctrl+n", "up / down",
		"y", "a", "j / k", "n", "v", "w", "r", "tab", "c", "space", "g / G", "ctrl+s", "q",
		"1 / 2 / 3", "m", "d / x",
	} {
		if !strings.Contains(all, key) {
			t.Errorf("scrolling the whole overlay never shows %q", key)
		}
	}
	// And the section headings, since a binding with no context is a key with no screen.
	for _, heading := range []string{
		"anywhere", "chat", "agents", "review", "a diff", "writing a commit",
		"worktree monitor", "credentials",
	} {
		if !strings.Contains(all, heading) {
			t.Errorf("scrolling the whole overlay never shows the %q section", heading)
		}
	}
}

// The frame is a fixed height. An overlay taller than the body pushes everything above it off the
// top of the terminal, which looks like the program breaking rather than like a long list.
func TestTheOverlayNeverOverflowsTheFrame(t *testing.T) {
	for _, dim := range []Dimensions{
		{Width: 80, Height: 24}, {Width: 100, Height: 30}, {Width: 200, Height: 14},
		{Width: 80, Height: 60},
	} {
		body := HelpFrom(dim, 0)
		if got := strings.Count(body, "\n") + 1; got > dim.BodyHeight() {
			t.Errorf("at %dx%d the overlay is %d lines and the body is %d",
				dim.Width, dim.Height, got, dim.BodyHeight())
		}
		for _, line := range strings.Split(stripANSI(body), "\n") {
			if len([]rune(line)) > dim.Width {
				t.Errorf("at %dx%d a line is %d columns: %q",
					dim.Width, dim.Height, len([]rune(line)), line)
			}
		}
	}
}

// Scrolling past the end has to stop at the end rather than running off into blank space, which
// reads as the list having been lost.
func TestScrollingPastTheEndStops(t *testing.T) {
	dim := Dimensions{Width: 80, Height: 24}

	far := stripANSI(HelpFrom(dim, 10_000))
	if strings.TrimSpace(far) == "" {
		t.Fatal("scrolling far past the end left a blank screen")
	}
	if !strings.Contains(far, "the end of the list") {
		t.Errorf("the bottom of the list does not say it is the bottom:\n%s", far)
	}
}
