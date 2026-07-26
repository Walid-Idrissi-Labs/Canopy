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
	}
}

// Current returns the active theme.
//
// A function rather than a variable so that switching themes at A9-03 is a change here and nowhere
// else. Call sites already ask every render.
func Current() Theme { return current }

var current = New(Default)

// Set replaces the active theme.
func Set(p Palette) { current = New(p) }
