// Package theme is the single source of colour and text style.
//
// Themes ship late, at A9-03. This package exists now because the expensive version of theming is
// retrofitting it after two hundred call sites have each picked their own colour. Routing
// everything through one palette from the start makes adding a theme a data change rather than a
// refactor, and costs nothing today.
//
// The rule that makes it work: no other package constructs a colour. If a call site needs one it
// belongs here, named for what it means rather than what it looks like. "Danger" survives a theme
// change; "red" does not.
package theme

import "github.com/charmbracelet/lipgloss"

// Palette is the set of colours a theme defines.
//
// Named by meaning, not appearance, so a light theme, a dark theme and a monochrome theme can all
// satisfy the same set without any of the names becoming lies.
type Palette struct {
	Name string

	Text   lipgloss.TerminalColor
	Muted  lipgloss.TerminalColor
	Accent lipgloss.TerminalColor

	Success lipgloss.TerminalColor
	Danger  lipgloss.TerminalColor
	Warning lipgloss.TerminalColor
	Info    lipgloss.TerminalColor

	Border    lipgloss.TerminalColor
	Highlight lipgloss.TerminalColor

	// The four categories a hand written lexer can tell apart without a full parser. Named for what
	// a reader is looking at, not for the hue, so a colour blind palette or a light theme can pick
	// values that suit it without any call site changing.
	CodeKeyword lipgloss.TerminalColor
	CodeString  lipgloss.TerminalColor
	CodeComment lipgloss.TerminalColor
	CodeNumber  lipgloss.TerminalColor
}

// Theme is a palette plus the styles derived from it.
//
// Styles are built once when the theme is selected rather than on every render, because building a
// lipgloss style per row per frame is measurable once several agents are streaming at once.
type Theme struct {
	Palette Palette

	Title    lipgloss.Style
	Heading  lipgloss.Style
	Body     lipgloss.Style
	Muted    lipgloss.Style
	Selected lipgloss.Style

	Success lipgloss.Style
	Danger  lipgloss.Style
	Warning lipgloss.Style
	Info    lipgloss.Style

	Logo   lipgloss.Style
	Border lipgloss.Style
	Footer lipgloss.Style
	Key    lipgloss.Style

	// Cursor stands in for the terminal's own cursor, which cannot be positioned inside composed
	// text without tracking every style's effect on the offset. A reversed cell is what terminal
	// editors fall back to, and it survives a theme change because it takes its colours from
	// whatever is already there.
	Cursor lipgloss.Style

	// InlineCode marks a span of text as code without needing a colour of its own: it reuses
	// Highlight as a background rather than adding one, since "a chip of a different shade" is the
	// same meaning Highlight already carries.
	InlineCode lipgloss.Style

	CodeKeyword lipgloss.Style
	CodeString  lipgloss.Style
	CodeComment lipgloss.Style
	CodeNumber  lipgloss.Style
}

// Default is the built-in palette, adapting to a light or dark terminal.
var Default = Palette{
	Name:      "canopy",
	Text:      lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#e6edf3"},
	Muted:     lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"},
	Accent:    lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"},
	Success:   lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"},
	Danger:    lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"},
	Warning:   lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"},
	Info:      lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"},
	Border:    lipgloss.AdaptiveColor{Light: "#d1d9e0", Dark: "#30363d"},
	Highlight: lipgloss.AdaptiveColor{Light: "#f6f8fa", Dark: "#161b22"},

	CodeKeyword: lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#d2a8ff"},
	CodeString:  lipgloss.AdaptiveColor{Light: "#0a7d33", Dark: "#7ee787"},
	CodeComment: lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"},
	CodeNumber:  lipgloss.AdaptiveColor{Light: "#0550ae", Dark: "#79c0ff"},
}

// New builds the styles for a palette.
func New(p Palette) Theme {
	return Theme{
		Palette:  p,
		Title:    lipgloss.NewStyle().Bold(true).Foreground(p.Text),
		Heading:  lipgloss.NewStyle().Bold(true).Foreground(p.Muted),
		Body:     lipgloss.NewStyle().Foreground(p.Text),
		Muted:    lipgloss.NewStyle().Foreground(p.Muted),
		Selected: lipgloss.NewStyle().Bold(true).Foreground(p.Text),
		Success:  lipgloss.NewStyle().Foreground(p.Success),
		Danger:   lipgloss.NewStyle().Foreground(p.Danger),
		Warning:  lipgloss.NewStyle().Foreground(p.Warning),
		Info:     lipgloss.NewStyle().Foreground(p.Info),
		Logo:     lipgloss.NewStyle().Bold(true).Foreground(p.Accent),
		Border:   lipgloss.NewStyle().Foreground(p.Border),
		Footer:   lipgloss.NewStyle().Foreground(p.Muted),
		Key:      lipgloss.NewStyle().Bold(true).Foreground(p.Accent),
		Cursor:   lipgloss.NewStyle().Reverse(true),

		InlineCode: lipgloss.NewStyle().Foreground(p.Text).Background(p.Highlight),

		CodeKeyword: lipgloss.NewStyle().Foreground(p.CodeKeyword),
		CodeString:  lipgloss.NewStyle().Foreground(p.CodeString),
		CodeComment: lipgloss.NewStyle().Foreground(p.CodeComment),
		CodeNumber:  lipgloss.NewStyle().Foreground(p.CodeNumber),
	}
}

// Current returns the active theme.
//
// A function rather than a variable so that switching themes at A9-03 is a change here and nowhere
// else. Call sites already ask every render.
func Current() Theme { return current }

var current = New(Default)

// listeners are told when the theme changes.
//
// Needed because lipgloss.TerminalColor cannot be implemented outside lipgloss: it has an
// unexported method, so there is no way to write a colour value that resolves lazily. Anything
// holding a colour rather than a style therefore has to be told to go and fetch a new one, and a
// call site that reads a stale colour is exactly the bug this package exists to prevent.
var listeners []func()

// OnChange registers a callback, and calls it once immediately so the caller starts consistent.
func OnChange(f func()) {
	listeners = append(listeners, f)
	f()
}

// Set replaces the active theme.
func Set(p Palette) {
	current = New(p)
	for _, notify := range listeners {
		notify()
	}
}

// Monochrome is the second theme, and the one that proves the first is not cheating.
//
// Every state in Canopy is identified by a word and a glyph, and colour is only ever an accelerant.
// A palette with no colour in it at all is the test of that claim: if the interface is unreadable
// here, then somewhere a meaning is being carried by a hue, and it was already invisible to a
// colour blind reader and to anybody running with NO_COLOR set.
//
// It is also the honest choice on a terminal whose own palette fights the default one, which is
// most of the sixteen colour ones.
var Monochrome = Palette{
	Name:      "mono",
	Text:      lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
	Muted:     lipgloss.AdaptiveColor{Light: "#666666", Dark: "#999999"},
	Accent:    lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
	Success:   lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
	Danger:    lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
	Warning:   lipgloss.AdaptiveColor{Light: "#666666", Dark: "#999999"},
	Info:      lipgloss.AdaptiveColor{Light: "#666666", Dark: "#999999"},
	Border:    lipgloss.AdaptiveColor{Light: "#999999", Dark: "#666666"},
	Highlight: lipgloss.AdaptiveColor{Light: "#eeeeee", Dark: "#222222"},

	CodeKeyword: lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
	CodeString:  lipgloss.AdaptiveColor{Light: "#666666", Dark: "#999999"},
	CodeComment: lipgloss.AdaptiveColor{Light: "#999999", Dark: "#666666"},
	CodeNumber:  lipgloss.AdaptiveColor{Light: "#000000", Dark: "#ffffff"},
}

// All returns every theme that ships.
func All() []Palette { return []Palette{Default, Monochrome} }

// ByName returns a theme by name, and whether it exists.
func ByName(name string) (Palette, bool) {
	for _, palette := range All() {
		if palette.Name == name {
			return palette, true
		}
	}
	return Palette{}, false
}

// Names returns every theme name, for an error message that tells somebody what they could have
// typed instead.
func Names() []string {
	names := make([]string, 0, len(All()))
	for _, palette := range All() {
		names = append(names, palette.Name)
	}
	return names
}
