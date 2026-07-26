package tui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
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

var (
	colorPass    = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	colorFail    = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	colorStale   = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	colorPending = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"}
	colorText    = lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#e6edf3"}
)

var (
	styleHeader   = lipgloss.NewStyle().Bold(true).Foreground(colorMuted)
	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	styleMuted    = lipgloss.NewStyle().Foreground(colorMuted)
	styleSelected = lipgloss.NewStyle().Bold(true).Foreground(colorText)
	styleFooter   = lipgloss.NewStyle().Foreground(colorMuted)
	styleReason   = lipgloss.NewStyle().Foreground(colorMuted)
	styleCaveat   = lipgloss.NewStyle().Foreground(colorStale)
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
func testStatus(state core.TestState) statusText {
	switch state {
	case core.TestPassing:
		return statusText{"\u2713", "PASS", lipgloss.NewStyle().Foreground(colorPass)}
	case core.TestFailing:
		return statusText{"\u2717", "FAIL", lipgloss.NewStyle().Foreground(colorFail)}
	case core.TestStale:
		// Amber, not red. Stale means "ask me again", not "this is broken".
		return statusText{"~", "STALE", lipgloss.NewStyle().Foreground(colorStale)}
	case core.TestRunning:
		return statusText{">", "RUN", lipgloss.NewStyle().Foreground(colorPending)}
	case core.TestQueued:
		return statusText{"\u00b7", "QUEUED", lipgloss.NewStyle().Foreground(colorPending)}
	case core.TestError:
		return statusText{"!", "ERROR", lipgloss.NewStyle().Foreground(colorFail)}
	case core.TestCancelled:
		return statusText{"-", "CANCEL", lipgloss.NewStyle().Foreground(colorMuted)}
	case core.TestNotConfigured:
		return statusText{" ", "NOT SET", lipgloss.NewStyle().Foreground(colorMuted)}
	case core.TestUnknown:
		return statusText{"?", "UNKNOWN", lipgloss.NewStyle().Foreground(colorMuted)}
	default:
		// An unrecognised state is shown as itself rather than being silently normalised into
		// something familiar. If the vocabulary grows and this switch is not updated, that should
		// be visible on screen, not hidden behind a plausible looking icon.
		return statusText{"?", string(state), lipgloss.NewStyle().Foreground(colorFail)}
	}
}

// serviceStatus maps a service state to its display form.
func serviceStatus(state core.ServiceState) statusText {
	switch state {
	case core.ServiceHealthy:
		return statusText{"\u2713", "UP", lipgloss.NewStyle().Foreground(colorPass)}
	case core.ServiceUnhealthy:
		return statusText{"\u2717", "SICK", lipgloss.NewStyle().Foreground(colorFail)}
	case core.ServiceCrashed:
		return statusText{"!", "CRASH", lipgloss.NewStyle().Foreground(colorFail)}
	case core.ServiceStarting:
		return statusText{">", "START", lipgloss.NewStyle().Foreground(colorPending)}
	case core.ServiceStopping:
		return statusText{"-", "STOP", lipgloss.NewStyle().Foreground(colorPending)}
	case core.ServiceStopped:
		return statusText{"\u00b7", "DOWN", lipgloss.NewStyle().Foreground(colorMuted)}
	case core.ServiceNotConfigured:
		return statusText{" ", "NOT SET", lipgloss.NewStyle().Foreground(colorMuted)}
	case core.ServiceUnknown:
		return statusText{"?", "UNKNOWN", lipgloss.NewStyle().Foreground(colorMuted)}
	default:
		return statusText{"?", string(state), lipgloss.NewStyle().Foreground(colorFail)}
	}
}

// verifiedStatus is the roll-up indicator.
//
// It says YES or NO rather than showing a tick alone, because a bare tick invites the reader to
// see a green shape and stop looking. The word forces the eye past it to the columns that say
// which evidence produced it.
func verifiedStatus(rollup core.Rollup) statusText {
	if rollup.Green {
		return statusText{"\u2713", "YES", lipgloss.NewStyle().Foreground(colorPass)}
	}
	return statusText{" ", "NO", lipgloss.NewStyle().Foreground(colorMuted)}
}
