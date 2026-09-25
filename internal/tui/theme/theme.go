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

import (
	"image/color"
	"os"
	"strings"
	"sync/atomic"

	"charm.land/lipgloss/v2"
)

// Palette is the set of colours a theme defines.
//
// Named by meaning, not appearance, so a light theme, a dark theme and a monochrome theme can all
// satisfy the same set without any of the names becoming lies.
type Palette struct {
	Name string

	Text   color.Color
	Muted  color.Color
	Accent color.Color

	Success color.Color
	Danger  color.Color
	Warning color.Color
	Info    color.Color

	Border    color.Color
	Highlight color.Color

	// Flame is the campfire in the mark, and the one colour here named for a thing rather than for a
	// meaning. It gets that exemption because the thing is a drawing and there is no meaning to name
	// it after: a fire is not a success and not a warning, it is a fire.
	//
	// Its own entry rather than Success borrowed, even though the two are the same value in the
	// default palette. Reusing Success would mean a theme could not warm the fire without warming
	// every passing test with it, and would make the mark change colour the day somebody decides
	// green is the wrong colour for a tick.
	Flame color.Color

	// FlameCore is the heart of the fire, a step brighter than Flame. Its own entry for the same
	// reason Flame is: it is a colour for a drawing, and borrowing a meaning for it would tie the
	// middle of a campfire to whatever that meaning does next.
	FlameCore color.Color

	// Smoke is the wisp above the flame, and it gets the same exemption for the same reason.
	Smoke color.Color

	// SmokeFaint is smoke about to disappear: the highest wisp above the fire, a step dimmer than
	// Smoke, which is what makes it read as fading out rather than as a stack of grey marks.
	SmokeFaint color.Color

	// The four categories a hand written lexer can tell apart without a full parser. Named for what
	// a reader is looking at, not for the hue, so a colour blind palette or a light theme can pick
	// values that suit it without any call site changing.
	CodeKeyword color.Color
	CodeString  color.Color
	CodeComment color.Color
	CodeNumber  color.Color
	// CodeFunction and CodeType colour function names and type names; a zero value falls back to
	// the text colour.
	CodeFunction color.Color
	CodeType     color.Color
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

	Logo       lipgloss.Style
	Flame      lipgloss.Style
	FlameCore  lipgloss.Style
	Smoke      lipgloss.Style
	SmokeFaint lipgloss.Style
	Border     lipgloss.Style
	Footer     lipgloss.Style
	Key        lipgloss.Style

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

	CodeFunction lipgloss.Style
	CodeType     lipgloss.Style
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
	Text:      Adaptive{Light: "#1f2328", Dark: "#e6edf3"},
	Muted:     Adaptive{Light: brandAccentLight, Dark: brandAccent},
	Accent:    Adaptive{Light: brandPrimaryLight, Dark: brandPrimary},
	Success:   Adaptive{Light: brandSecondaryLight, Dark: brandSecondary},
	Danger:    Adaptive{Light: "#c4342b", Dark: "#ef5f5f"},
	Warning:   Adaptive{Light: "#9a6700", Dark: "#e0a33a"},
	Info:      Adaptive{Light: brandPrimaryLight, Dark: brandPrimary},
	Border:    Adaptive{Light: "#d6d6d6", Dark: "#3a3a3a"},
	Highlight: Adaptive{Light: "#f2f4f5", Dark: "#16242b"},

	// The campfire takes the secondary brand colour, which is the one place in the interface it is
	// used for something that is not an outcome. It is also what makes the mark carry two of the
	// three brand colours at once rather than one, which is the difference between a logo in a
	// colour and a logo with a palette.
	Flame: Adaptive{Light: brandSecondaryLight, Dark: brandSecondary},

	// The heart of the fire is the same green pushed a step towards white on a dark terminal and a
	// step towards ink on a light one: the same hue at a brighter weight, not a new colour, which is
	// what keeps the fire reading as one thing with depth rather than as two things stacked.
	FlameCore: Adaptive{Light: "#8a9b02", Dark: "#d8ef3a"},

	// And the smoke takes the accent grey, which is the third of the three and the only colour smoke
	// could sensibly be. Between them the mark carries all three brand colours: the tent in the
	// primary, the fire in the secondary, the smoke in the accent.
	Smoke: Adaptive{Light: brandAccentLight, Dark: brandAccent},

	// The highest wisp is the same grey on its way to the background, which is where smoke goes.
	SmokeFaint: Adaptive{Light: "#9c9c9c", Dark: "#6e6e6e"},

	// Syntax highlighting keeps to the same family, so a code block does not look like it was
	// pasted in from another program. Keyword takes the primary, string takes the secondary, and
	// comment takes the grey, which is what a comment should be anyway.
	CodeKeyword: Adaptive{Light: brandPrimaryLight, Dark: brandPrimary},
	CodeString:  Adaptive{Light: brandSecondaryLight, Dark: brandSecondary},
	CodeComment: Adaptive{Light: brandAccentLight, Dark: brandAccent},
	CodeNumber:  Adaptive{Light: "#7a4fbf", Dark: "#b48ce8"},
	// A warm gold for function names and a teal for types, the two colours a reader's eye uses to
	// find the shape of code before reading it.
	CodeFunction: Adaptive{Light: "#8a5a00", Dark: "#e8c27a"},
	CodeType:     Adaptive{Light: "#0f7b6c", Dark: "#5cc9b8"},
}

// New builds the styles for a palette.
func New(p Palette) Theme {
	return Theme{
		Palette:    p,
		Title:      lipgloss.NewStyle().Bold(true).Foreground(p.Text),
		Heading:    lipgloss.NewStyle().Bold(true).Foreground(p.Muted),
		Body:       lipgloss.NewStyle().Foreground(p.Text),
		Muted:      lipgloss.NewStyle().Foreground(p.Muted),
		Selected:   lipgloss.NewStyle().Bold(true).Foreground(p.Text),
		Success:    lipgloss.NewStyle().Foreground(p.Success),
		Danger:     lipgloss.NewStyle().Foreground(p.Danger),
		Warning:    lipgloss.NewStyle().Foreground(p.Warning),
		Info:       lipgloss.NewStyle().Foreground(p.Info),
		Logo:       lipgloss.NewStyle().Bold(true).Foreground(p.Accent),
		Flame:      lipgloss.NewStyle().Bold(true).Foreground(p.Flame),
		FlameCore:  lipgloss.NewStyle().Bold(true).Foreground(p.FlameCore),
		Smoke:      lipgloss.NewStyle().Foreground(p.Smoke),
		SmokeFaint: lipgloss.NewStyle().Foreground(p.SmokeFaint),
		Border:     lipgloss.NewStyle().Foreground(p.Border),
		Footer:     lipgloss.NewStyle().Foreground(p.Muted),
		Key:        lipgloss.NewStyle().Bold(true).Foreground(p.Accent),
		Cursor:     lipgloss.NewStyle().Reverse(true),

		InlineCode: lipgloss.NewStyle().Foreground(p.Text).Background(p.Highlight),

		CodeKeyword: lipgloss.NewStyle().Foreground(p.CodeKeyword),
		CodeString:  lipgloss.NewStyle().Foreground(p.CodeString),
		CodeComment: lipgloss.NewStyle().Foreground(p.CodeComment),
		CodeNumber:  lipgloss.NewStyle().Foreground(p.CodeNumber),

		CodeFunction: lipgloss.NewStyle().Foreground(orText(p.CodeFunction, p.Text)),
		CodeType:     lipgloss.NewStyle().Foreground(orText(p.CodeType, p.Text)),
	}
}

// Current returns the active theme.
//
// A function rather than a variable so that switching themes at A9-03 is a change here and nowhere
// else. Call sites already ask every render.
func Current() Theme { return current }

var current = New(fromEnvironment())

// EnvName is the variable that picks a theme by name.
const EnvName = "CANOPY_THEME"

// fromEnvironment decides which palette to start in.
//
// A9-03 shipped two themes, both tested, and no way to reach the second one: it was selectable from
// code and from nowhere else, which is the same as not existing. This is the smallest thing that
// fixes that, and it deliberately reads the environment rather than a config file, because the
// terminal is where this decision is already made for every other program on the machine.
//
// NO_COLOR wins over an explicit choice. It is a promise the whole ecosystem makes, the user set it
// on purpose, and a program that honours it only when nothing else was configured does not honour
// it. An unrecognised name falls back to the default rather than failing: a typo in an environment
// variable should not stop the program from starting, and the theme is the one setting where being
// wrong is immediately visible anyway.
func fromEnvironment() Palette {
	// Present and not literally "0" is the convention, so NO_COLOR= empty still counts as set.
	if value, present := os.LookupEnv("NO_COLOR"); present && value != "0" {
		return Monochrome
	}
	if name := strings.TrimSpace(os.Getenv(EnvName)); name != "" {
		if palette, ok := ByName(strings.ToLower(name)); ok {
			return palette
		}
	}
	return Default
}

// listeners are told when the theme changes.
//
// Needed because a colour already taken out of the palette, or a line already drawn and cached,
// does not change when the theme does. Anything holding one has to be told to go and fetch a new
// one, and a call site that reads a stale colour is exactly the bug this package exists to prevent.
var listeners []func()

// OnChange registers a callback, and calls it once immediately so the caller starts consistent.
func OnChange(f func()) {
	listeners = append(listeners, f)
	f()
}

// Set replaces the active theme.
func Set(p Palette) {
	current = New(p)
	changed()
}

func changed() {
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
	Name:       "mono",
	Text:       Adaptive{Light: "#000000", Dark: "#ffffff"},
	Muted:      Adaptive{Light: "#666666", Dark: "#999999"},
	Accent:     Adaptive{Light: "#000000", Dark: "#ffffff"},
	Success:    Adaptive{Light: "#000000", Dark: "#ffffff"},
	Danger:     Adaptive{Light: "#000000", Dark: "#ffffff"},
	Warning:    Adaptive{Light: "#666666", Dark: "#999999"},
	Info:       Adaptive{Light: "#666666", Dark: "#999999"},
	Border:     Adaptive{Light: "#999999", Dark: "#666666"},
	Highlight:  Adaptive{Light: "#eeeeee", Dark: "#222222"},
	Flame:      Adaptive{Light: "#666666", Dark: "#999999"},
	FlameCore:  Adaptive{Light: "#000000", Dark: "#ffffff"},
	Smoke:      Adaptive{Light: "#999999", Dark: "#666666"},
	SmokeFaint: Adaptive{Light: "#bbbbbb", Dark: "#444444"},

	CodeKeyword: Adaptive{Light: "#000000", Dark: "#ffffff"},
	CodeString:  Adaptive{Light: "#666666", Dark: "#999999"},
	CodeComment: Adaptive{Light: "#999999", Dark: "#666666"},
	CodeNumber:  Adaptive{Light: "#000000", Dark: "#ffffff"},
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

func orText(c, text color.Color) color.Color {
	if c == nil {
		return text
	}
	return c
}

// Adaptive is a colour with a value for light terminals and one for dark ones, chosen when it is
// drawn, from what the terminal reported about its background (see SetDark). A hex string each.
type Adaptive struct {
	Light, Dark string
}

// RGBA is the colour for the background the terminal has, which makes Adaptive a color.Color.
func (a Adaptive) RGBA() (r, g, b, alpha uint32) {
	if dark.Load() {
		return lipgloss.Color(a.Dark).RGBA()
	}
	return lipgloss.Color(a.Light).RGBA()
}

// dark is whether the terminal's background is dark. Assumed until the terminal says otherwise,
// since most are.
var dark atomic.Bool

func init() { dark.Store(true) }

// SetDark records the terminal's background, as Bubble Tea reports it. The first frame is drawn
// before the terminal answers, on the dark assumption, so a change is a theme change: whatever was
// drawn and kept in the old colours is thrown away, as it is when the palette changes.
func SetDark(isDark bool) {
	if dark.Swap(isDark) != isDark {
		changed()
	}
}

// Dark reports whether adaptive colours are drawing for a dark background.
func Dark() bool { return dark.Load() }
