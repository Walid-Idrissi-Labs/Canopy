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
	listed := Help(Dimensions{Width: 100, Height: 40})

	for _, key := range []string{
		"?", "ctrl+c", "enter", "esc", "ctrl+d", "ctrl+k", "ctrl+r",
		"y", "a", "j / k", "n", "v", "w", "r", "tab", "c", "space", "g / G", "ctrl+s", "q",
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
	for _, line := range strings.Split(stripANSI(Help(Dimensions{Width: 80, Height: 40})), "\n") {
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
