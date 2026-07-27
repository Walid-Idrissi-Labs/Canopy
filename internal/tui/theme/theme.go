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

// The brand colours.
//
// Three, chosen by the supervisors, and everything else in the default palette is derived from them
// or forced by meaning. They are declared here rather than inlined so the palette below reads as
// which colour carries which meaning, which is the part worth reviewing.
//
// The background is never set, by any theme. It stays whatever the terminal is, because a program
// that paints its own background either fights the user's carefully chosen scheme or leaves a
// rectangle of the wrong shade wherever a line is shorter than the pane. Terminal programs that
// look at home are the ones that only ever set foregrounds.
const (
	brandPrimary   = "#0c87b7" // the blue the interface is built around
	brandSecondary = "#b4cc03" // the green that means something worked
	brandAccent    = "#b7b7b7" // the grey everything quiet is written in

	// Darker variants, for a light terminal. The brand values are chosen against a dark background
	// and two of them do not have the contrast to be read on white: the green is a highlighter pen
	// and the grey disappears. A theme that is unreadable on half of the terminals it runs on is
	// not a theme, so light gets the same hues at a weight that can actually be read.
	brandPrimaryLight   = "#0a6a8f"
	brandSecondaryLight = "#6f7d02"
	brandAccentLight    = "#5c5c5c"
)

// Default is the built-in palette, adapting to a light or dark terminal.
//
// Two colours in here are not brand colours and cannot be. Danger has to be red and warning has to
// be amber, because those two meanings are carried by convention across every program a user has
// ever used, and overriding them to fit a palette is how a failure comes to look like a success.
// They are tuned to sit beside the brand colours rather than chosen freely.
var Default = Palette{
	Name:      "canopy",
	Text:      lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#e6edf3"},
	Muted:     lipgloss.AdaptiveColor{Light: brandAccentLight, Dark: brandAccent},
	Accent:    lipgloss.AdaptiveColor{Light: brandPrimaryLight, Dark: brandPrimary},
	Success:   lipgloss.AdaptiveColor{Light: brandSecondaryLight, Dark: brandSecondary},
	Danger:    lipgloss.AdaptiveColor{Light: "#c4342b", Dark: "#ef5f5f"},
	Warning:   lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#e0a33a"},
	Info:      lipgloss.AdaptiveColor{Light: brandPrimaryLight, Dark: brandPrimary},
	Border:    lipgloss.AdaptiveColor{Light: "#d6d6d6", Dark: "#3a3a3a"},
	Highlight: lipgloss.AdaptiveColor{Light: "#f2f4f5", Dark: "#16242b"},

	// Syntax highlighting keeps to the same family, so a code block does not look like it was
	// pasted in from another program. Keyword takes the primary, string takes the secondary, and
	// comment takes the grey, which is what a comment should be anyway.
	CodeKeyword: lipgloss.AdaptiveColor{Light: brandPrimaryLight, Dark: brandPrimary},
	CodeString:  lipgloss.AdaptiveColor{Light: brandSecondaryLight, Dark: brandSecondary},
	CodeComment: lipgloss.AdaptiveColor{Light: brandAccentLight, Dark: brandAccent},
	CodeNumber:  lipgloss.AdaptiveColor{Light: "#7a4fbf", Dark: "#b48ce8"},
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
