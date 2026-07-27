package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// The visual language carries the product promise, so it is defined here in one place rather than
// being spread across the rendering code.
//
// The governing rule from DECISIONS.md D-10: every state is identified by a word, and colour is
// only ever an accelerant. Strip the colour and the dashboard still reads correctly. That is not
// only an accessibility concern. A status the user cannot read is a status the user cannot trust,
// which is the same failure as a status that is wrong.
//
// The second rule, easy to lose: stale is styled neutrally, never as an alarm. A dashboard that
// shouts every time you touch a file teaches people to ignore it, and an ignored status is
// untrustworthy for a different reason than a false one.
//
// **Every colour here comes from the theme and none is constructed locally.** That is the rule
// internal/tui/theme opens with, and this file used to break it: it declared its own six adaptive
// colours, duplicating the default palette by value. The effect was that selecting the monochrome
// theme changed the chat and the agents view and left the worktree monitor, the review screen and
// the help overlay in full colour, because those three render through this file. The test that was
// supposed to catch it looped over both themes and then asserted only that every state has a word
// and a single width glyph, which is true whatever the palette is, so it passed throughout.

// themed is a style that resolves against whatever theme is active when it renders.
//
// A function behind a Render method rather than a plain lipgloss.Style, because a style built at
// package initialisation captures the palette that was current at initialisation, and the whole
// point of a theme is that it can change afterwards. The call sites are unchanged: they still say
// styleMuted.Render(text).
type themed func() lipgloss.Style

func (t themed) Render(strs ...string) string { return t().Render(strs...) }

// The bare colours, for the few call sites that need a colour rather than a style.
//
// A diff line is the honest example: it wants a green plus sign in front of syntax highlighted
// source, so it composes its own style and cannot use a ready made one. These follow the theme
// through the change hook rather than by being read lazily, because lipgloss.TerminalColor has an
// unexported method and cannot be implemented outside lipgloss.
var (
	colorPass    lipgloss.TerminalColor
	colorFail    lipgloss.TerminalColor
	colorStale   lipgloss.TerminalColor
	colorPending lipgloss.TerminalColor
	colorMuted   lipgloss.TerminalColor
	colorText    lipgloss.TerminalColor
)

func init() { theme.OnChange(refreshColours) }

func refreshColours() {
	t := theme.Current()
	colorPass = t.Success.GetForeground()
	colorFail = t.Danger.GetForeground()
	colorStale = t.Warning.GetForeground()
	colorPending = t.Info.GetForeground()
	colorMuted = t.Muted.GetForeground()
	colorText = t.Body.GetForeground()
}

var (
	styleHeader   = themed(func() lipgloss.Style { return theme.Current().Heading })
	styleTitle    = themed(func() lipgloss.Style { return theme.Current().Title })
	styleMuted    = themed(func() lipgloss.Style { return theme.Current().Muted })
	styleSelected = themed(func() lipgloss.Style { return theme.Current().Selected })
	styleFooter   = themed(func() lipgloss.Style { return theme.Current().Footer })
	styleReason   = themed(func() lipgloss.Style { return theme.Current().Muted })
	styleCaveat   = themed(func() lipgloss.Style { return theme.Current().Warning })
)

// statusText is a state rendered for display: a glyph, the word that actually carries the
// meaning, and the colour that is allowed to reinforce it but never to replace it.
type statusText struct {
	glyph string
	word  string
	style lipgloss.Style
}

func (s statusText) plain() string {
	if s.glyph == "" {
		return s.word
	}
	return s.glyph + " " + s.word
}

func (s statusText) render() string {
	return s.style.Render(s.plain())
}

// testStatus maps a test state to its display form. The words come from D-10 and are fixed. The
// glyphs are this layer's choice, and every one is single width so a change of state can never
// shift the columns.
//
// Built per call rather than once, which is what lets it follow the theme. These are called during
// a render that is already walking a table, so the cost is a struct and a map lookup.
func testStatus(state core.TestState) statusText {
	t := theme.Current()

	switch state {
	case core.TestPassing:
		return statusText{"✓", "PASS", t.Success}
	case core.TestFailing:
		return statusText{"✗", "FAIL", t.Danger}
	case core.TestStale:
		// Amber, not red. Stale means "ask me again", not "this is broken".
		return statusText{"~", "STALE", t.Warning}
	case core.TestRunning:
		return statusText{">", "RUN", t.Info}
	case core.TestQueued:
		return statusText{"·", "QUEUED", t.Info}
	case core.TestError:
		return statusText{"!", "ERROR", t.Danger}
	case core.TestCancelled:
		return statusText{"-", "CANCEL", t.Muted}
	case core.TestNotConfigured:
		return statusText{" ", "NOT SET", t.Muted}
	case core.TestUnknown:
		return statusText{"?", "UNKNOWN", t.Muted}
	default:
		// An unrecognised state is shown as itself rather than being silently normalised into
		// something familiar. If the vocabulary grows and this switch is not updated, that should
		// be visible on screen, not hidden behind a plausible looking icon.
		return statusText{"?", string(state), t.Danger}
	}
}

// serviceStatus maps a service state to its display form.
func serviceStatus(state core.ServiceState) statusText {
	t := theme.Current()

	switch state {
	case core.ServiceHealthy:
		return statusText{"✓", "UP", t.Success}
	case core.ServiceUnhealthy:
		return statusText{"✗", "SICK", t.Danger}
	case core.ServiceCrashed:
		return statusText{"!", "CRASH", t.Danger}
	case core.ServiceStarting:
		return statusText{">", "START", t.Info}
	case core.ServiceStopping:
		return statusText{"-", "STOP", t.Info}
	case core.ServiceStopped:
		return statusText{"·", "DOWN", t.Muted}
	case core.ServiceNotConfigured:
		return statusText{" ", "NOT SET", t.Muted}
	case core.ServiceUnknown:
		return statusText{"?", "UNKNOWN", t.Muted}
	default:
		return statusText{"?", string(state), t.Danger}
	}
}

// verifiedStatus is the roll-up indicator.
//
// It says YES or NO rather than showing a tick alone, because a bare tick invites the reader to
// see a green shape and stop looking. The word forces the eye past it to the columns that say
// which evidence produced it.
func verifiedStatus(rollup core.Rollup) statusText {
	t := theme.Current()

	if rollup.Green {
		return statusText{"✓", "YES", t.Success}
	}
	return statusText{" ", "NO", t.Muted}
}
