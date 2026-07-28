package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// The identical helper in dashboard_test.go lives in the external test package, which cannot see
// the unexported things these tests are about.
var escapes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripped(s string) string { return escapes.ReplaceAllString(s, "") }

// colourKey makes a style's foreground comparable.
//
// Compared as the value the style is holding rather than as rendered text or as resolved RGBA,
// because under `go test` lipgloss finds no terminal: it renders with the styling stripped and
// resolves every adaptive colour to black. Both of those would compare two identical things and
// pass whatever the styles actually were, which is exactly the shape of test this file replaces.
func colourKey(style lipgloss.Style) string {
	fg := style.GetForeground()
	if fg == nil {
		return "none"
	}
	return fmt.Sprintf("%#v", fg)
}

// under runs f with a theme selected and puts the previous one back.
func under(t *testing.T, palette theme.Palette, f func()) {
	t.Helper()
	theme.Set(palette)
	defer theme.Set(theme.Default)
	f()
}

// The bug this file exists for.
//
// internal/tui/theme opens by saying no other package constructs a colour. styles.go did anyway: it
// declared six adaptive colours of its own that duplicated the default palette by value. Selecting
// the monochrome theme therefore changed the chat and the agents view and left the worktree
// monitor, the review screen and the help overlay in full colour, because those three render
// through styles.go.
//
// TestBothThemesCarryMeaningWithoutColour looked like it covered this. It loops over every theme and
// calls Set, and then asserts that each state has a word and a single width glyph, which is true
// whatever the palette is. The loop made it read as a theming test while its body was entirely
// theme independent, so it passed for as long as the hole was open.
func TestAStatusActuallyFollowsTheSelectedTheme(t *testing.T) {
	var coloured, mono string

	under(t, theme.Default, func() { coloured = colourKey(testStatus(core.TestPassing).style) })
	under(t, theme.Monochrome, func() { mono = colourKey(testStatus(core.TestPassing).style) })

	if coloured == mono {
		t.Errorf("a passing test takes the colour %s under both themes, so it does not follow the "+
			"selected one", coloured)
	}
	// The word survives either way, which is the separate promise and the one D-10 makes.
	under(t, theme.Monochrome, func() {
		if out := testStatus(core.TestPassing).plain(); !strings.Contains(out, "PASS") {
			t.Errorf("the word was lost with no colour: %q", out)
		}
	})
}

// The three screens that were left behind, asserted through the shared styles they render with.
func TestTheSharedStylesFollowTheSelectedTheme(t *testing.T) {
	for _, tc := range []struct {
		name  string
		style themed
	}{
		{"muted", styleMuted},
		{"title", styleTitle},
		{"header", styleHeader},
		{"caveat", styleCaveat},
		{"selected", styleSelected},
		{"footer", styleFooter},
	} {
		var coloured, mono string
		under(t, theme.Default, func() { coloured = colourKey(tc.style()) })
		under(t, theme.Monochrome, func() { mono = colourKey(tc.style()) })

		if coloured == mono {
			t.Errorf("style %q is identical under both themes, so it is not themed", tc.name)
		}
	}
}

// The campfire in the mark has a colour of its own.
//
// Two colours in one logo is what stops it reading as a stencil, and the whole reason the palette
// carries a Flame at all rather than reusing Success: a theme has to be able to warm the fire
// without warming every passing test with it.
func TestTheFlameIsNotTheSameColourAsTheMark(t *testing.T) {
	under(t, theme.Default, func() {
		current := theme.Current()
		if colourKey(current.Flame) == colourKey(current.Logo) {
			t.Errorf("the flame and the tent are both %s, so the mark is a shape in one colour",
				colourKey(current.Logo))
		}
	})

	// And it follows the theme like everything else, or a terminal running with no colour gets a
	// green fire in the corner of an otherwise monochrome screen.
	var coloured, mono string
	under(t, theme.Default, func() { coloured = colourKey(theme.Current().Flame) })
	under(t, theme.Monochrome, func() { mono = colourKey(theme.Current().Flame) })
	if coloured == mono {
		t.Errorf("the flame is %s under both themes, so it is not themed", coloured)
	}
}

// The bare colours are the awkward case: lipgloss.TerminalColor cannot be implemented outside
// lipgloss, so these cannot resolve lazily and are refreshed through a change hook instead. A hook
// nobody fires is the failure mode, and it looks exactly like the bug that was just fixed.
func TestTheBareColoursAreRefreshedWhenTheThemeChanges(t *testing.T) {
	sample := func() [4]string {
		return [4]string{
			bare(colorPass), bare(colorFail), bare(colorStale), bare(colorPending),
		}
	}

	var coloured, mono [4]string
	under(t, theme.Default, func() { coloured = sample() })
	under(t, theme.Monochrome, func() { mono = sample() })

	if coloured == mono {
		t.Errorf("the diff colours did not change with the theme: %v", coloured)
	}
}

// Every state has to stay tellable apart with no colour at all, which is the claim the monochrome
// theme exists to test. Asserted on what actually reaches the terminal with the styling stripped,
// rather than on the fields the struct happens to carry.
func TestEveryStateIsDistinguishableWithTheColourStripped(t *testing.T) {
	under(t, theme.Monochrome, func() {
		seen := map[string]core.TestState{}
		for _, state := range []core.TestState{
			core.TestPassing, core.TestFailing, core.TestStale, core.TestRunning,
			core.TestQueued, core.TestError, core.TestCancelled, core.TestNotConfigured,
			core.TestUnknown,
		} {
			text := strings.TrimSpace(stripped(testStatus(state).render()))
			if text == "" {
				t.Errorf("%s renders as nothing once the colour is gone", state)
				continue
			}
			if other, clash := seen[text]; clash {
				t.Errorf("%s and %s both render as %q, so they are the same to a reader with no colour",
					state, other, text)
			}
			seen[text] = state
		}
	})
}

// bare makes one of the loose colours comparable, for the same reason as colourKey.
func bare(c lipgloss.TerminalColor) string {
	if c == nil {
		return "none"
	}
	return fmt.Sprintf("%#v", c)
}
