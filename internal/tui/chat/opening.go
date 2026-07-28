package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// The screen a conversation opens on.
//
// This is the one moment where somebody is guaranteed to be looking and has not yet decided whether
// the tool is worth their time, so it is composed rather than being whatever falls out of an empty
// transcript. It replaces a welcome block that flowed from the top of the conversation area with the
// message box pinned to the floor, which is correct for a conversation and reads as an empty one.
//
// The composition was asked for directly: the drawn name above the message box, the box itself near
// the middle, the commands along the bottom, and the mark in the bottom right corner. The one part
// worth stating precisely is what "near the middle" means, because there are three readings and only
// one of them was wanted. It is not the box centred and not the name centred: **the middle of the
// space between the name and the box sits at the middle of the screen**, so the pair reads as one
// object with the eye landing between them.
//
// The commands along the bottom are the frame's own footer, which already lists them and already
// drops the least important from the right when they do not fit. Drawing a second row of them here
// would be two lists that disagree the first time one of them is edited.

// markMargin is the gap kept to the right of the mark.
//
// Two columns, because a logo flush against the edge of a terminal reads as one that got cut off,
// which is the same thing the brand package refuses to do at the other end by dropping the mark
// rather than clipping it.
const markMargin = 2

// contextIndent lines the bottom left text up with the message box, which sits one column in.
const contextIndent = "  "

// opening is the empty conversation, composed.
type opening struct {
	width  int
	height int

	// box is the message box, already drawn and styled.
	box []string

	// status is the row kept above the box for a notice or an error. Present even when it says
	// nothing, so a message arriving does not shunt the whole composition up a line.
	status string

	// context is the bottom left text: where the agent is working and what it is talking to.
	// Already styled, because it is several styles on one line.
	context []string

	// menu is the command list, drawn under the box. Empty when it is not open.
	menu []string

	// step is where the animation has got to.
	step int
}

func (o opening) render() string {
	head := o.head()
	// The gap between the name and the box: a blank row, and the rows the status needs whether or
	// not it has anything to say. Split rather than dropped in as one string, because a slash
	// command's listing arrives here several lines tall and a single slot would count it as one row
	// and push the box off the bottom of the composition.
	gap := append([]string{""}, strings.Split(o.status, "\n")...)
	block := append(append(append([]string(nil), head...), gap...), o.box...)
	// The command list hangs off the bottom of the box and does not move it. The centring below is
	// computed from the name and the gap alone, so opening the list leaves the name and the box
	// exactly where they were: a menu that shunted the box up the screen as it filtered would be
	// unusable to type into.
	block = append(block, o.menu...)

	// The middle of that gap is what lands on the middle of the screen.
	//
	// Not the middle of the block, which is the version this started as and is subtly wrong: the box
	// is taller than the name, so centring the whole thing pushes the gap up by half the difference
	// and the eye lands on the box rather than between the two. It looked right for exactly as long
	// as the box was three rows tall and the two happened to agree.
	top := (o.height - len(gap) - 2*len(head)) / 2
	if bottom := o.height - len(block); top > bottom {
		top = bottom
	}
	if top < 0 {
		top = 0
	}

	lines := make([]string, 0, o.height)
	for range top {
		lines = append(lines, "")
	}
	lines = append(lines, block...)

	// Whatever is left goes to the floor of the screen, so the mark and the context sit on the
	// bottom rather than floating directly under the box.
	if rest := o.height - len(lines); rest > 0 {
		lines = append(lines, o.floor(rest)...)
	}
	if len(lines) > o.height {
		lines = lines[:o.height]
	}
	return strings.Join(lines, "\n")
}

// head is the name, drawn and written.
//
// Both, always. Block letters are unreadable to a screen reader, unrecognisable in a narrow terminal
// and unsearchable in a pasted bug report, so the drawn name never replaces the written one, it only
// carries the look.
func (o opening) head() []string {
	t := theme.Current()

	// The large name with its edge picked out, which is the version worth looking at, and the packed
	// one behind it for a terminal that cannot afford six rows on a logo.
	if brand.FitsLarge(o.width) && o.height >= largeHeadHeight {
		indent := o.indentFor(brand.LargeWidth)

		// No written name under it, which is a departure from the rule below and was asked for
		// directly. The rule is not abandoned: the header above this draws "canopy" in text on every
		// screen, so the written name is still on screen for anything that cannot read block letters,
		// it is simply not repeated twice in the same eyeful.
		head := make([]string, 0, brand.LargeRows)
		for _, row := range brand.Large() {
			head = append(head, strings.TrimRight(indent+outlined(row), " "))
		}
		return head
	}

	if drawn := brand.Wordmark(o.width); drawn != nil {
		// One indent for the whole block rather than one per row. The rows are different lengths
		// once their trailing spaces are trimmed, so centring each on its own draws the letters as a
		// staircase: the wordmark is one picture, not three lines of text that happen to be stacked.
		indent := o.indentFor(brand.WordmarkWidth)
		head := make([]string, 0, len(drawn)+1)
		for _, line := range drawn {
			head = append(head, indent+t.Logo.Render(line))
		}
		return append(head, o.centre(o.fits("Canopy, "+brand.Tagline, "Canopy"), t.Muted))
	}

	// Too narrow for the drawn name, so the written one carries it alone.
	head := []string{o.centre("Canopy", t.Title)}
	if len([]rune(brand.Tagline)) <= o.width {
		head = append(head, o.centre(brand.Tagline, t.Muted))
	}
	return head
}

// floor is the bottom band: the context on the left, the mark on the right.
//
// They share rows rather than stacking. Stacked they cost the mark's nine lines plus the context's
// two, and under a message box that is already sitting on the middle of the screen there is not that
// much room left on an ordinary terminal. Side by side they cost nine, which is what a thirty row
// window can afford.
//
// Composed by concatenation rather than by writing into a grid of cells, because both sides are
// already styled and counting columns through escape sequences is how a layout ends up off by the
// length of a colour.
func (o opening) floor(rows int) []string {
	mark := o.mark(rows)

	lines := make([]string, rows)
	// Both are pinned to the bottom of the band, which is the floor of the screen.
	contextTop := rows - len(o.context)
	markTop := rows - len(mark)

	for i := range lines {
		var left string
		if i >= contextTop {
			left = contextIndent + o.context[i-contextTop]
		}
		if i < markTop {
			lines[i] = left
			continue
		}
		row := i - markTop
		lines[i] = o.beside(left, paint(row, mark[row]))
	}
	return lines
}

// paint styles one row of the mark, giving the campfire a colour of its own.
//
// Three colours rather than one, which is what stops the mark reading as a stencil: the tent takes
// the primary, the flame takes the secondary with its heart a step brighter, and the smoke takes
// the accent, so the logo carries the whole palette instead of being a shape in a single hue. The
// brand package hands over where the fire is rather than what colour it should be, because it
// constructs no colours at all.
func paint(row int, line string) string {
	t := theme.Current()

	fireTop, fireColumn, fireHeight, fireWidth := brand.FireRegion()
	coreTop, coreColumn, coreHeight, coreWidth := brand.FireCoreRegion()
	if inside(row, coreTop, coreHeight) {
		// The heart of the fire sits inside the flame's own rows, so this row is three spans of
		// flame, core and flame again, with the tent's colour either side.
		return tint(line,
			span{fireColumn, coreColumn - fireColumn, t.Flame},
			span{coreColumn, coreWidth, t.FlameCore},
			span{coreColumn + coreWidth, fireColumn + fireWidth - coreColumn - coreWidth, t.Flame})
	}
	if inside(row, fireTop, fireHeight) {
		return tint(line, span{fireColumn, fireWidth, t.Flame})
	}

	smokeTop, smokeColumn, smokeHeight, smokeWidth := brand.SmokeRegion()
	if inside(row, smokeTop, smokeHeight) {
		return tint(line, span{smokeColumn, smokeWidth, t.Smoke})
	}
	return t.Logo.Render(line)
}

func inside(row, top, height int) bool { return row >= top && row < top+height }

// span is a stretch of a row that takes a style of its own.
type span struct {
	column int
	width  int
	style  lipgloss.Style
}

// tint styles stretches of a row differently from the rest of it, which is drawn as the logo.
//
// Spans are taken in order and clipped to the row, so a span past the end of a short line colours
// nothing rather than panicking, which is what lets every row of the mark go through one function
// whether or not the fire reaches it.
func tint(line string, spans ...span) string {
	logo := theme.Current().Logo
	runes := []rune(line)

	var b strings.Builder
	at := 0
	for _, s := range spans {
		if s.width <= 0 || at >= len(runes) {
			continue
		}
		start := s.column
		if start < at {
			start = at
		}
		if start > len(runes) {
			break
		}
		if start > at {
			b.WriteString(logo.Render(string(runes[at:start])))
			at = start
		}
		end := s.column + s.width
		if end > len(runes) {
			end = len(runes)
		}
		if end > at {
			b.WriteString(s.style.Render(string(runes[at:end])))
			at = end
		}
	}
	if at < len(runes) {
		b.WriteString(logo.Render(string(runes[at:])))
	}
	return b.String()
}

// mark is the logo at the current step, or nothing when there is no room for it.
//
// Dropped rather than clipped or shrunk, which is the rule the brand package already applies to
// width, and the reason is the same here: half a tent looks like the program is broken, while a
// screen carrying the drawn name and no mark looks deliberate. On a short terminal that is what
// happens, and it is the right trade, because the alternative is a logo overlapping the message box.
func (o opening) mark(rows int) []string {
	if !brand.Fits(o.width) {
		return nil
	}
	// Enough width for the widest line of context and the mark beside it, with a column between
	// them. Without this the two would overlap on a narrow terminal, which is worse than no mark.
	widest := 0
	for _, line := range o.context {
		if w := lipgloss.Width(line); w > widest {
			widest = w
		}
	}
	if o.width-widest-len(contextIndent)-1 < brand.MarkWidth+markMargin {
		return nil
	}

	frame := brand.Frame(o.step)
	if rows < len(frame) {
		return nil
	}
	return frame
}

// beside puts one already styled row of the mark against the right hand edge, after whatever is on
// the left.
func (o opening) beside(left, row string) string {
	pad := o.width - markMargin - brand.MarkWidth - lipgloss.Width(left)
	if pad < 1 {
		// Unreachable while mark does its width check, and here so that a future change to that
		// check cannot produce a line wider than the terminal, which wraps the whole frame.
		return left
	}
	return left + strings.Repeat(" ", pad) + row
}

// centre puts a line in the middle of the width.
//
// Measured on what the line draws rather than on how many bytes it is, so a styled string is not
// pushed left by the length of its escape codes. Uncapped, unlike the indent the old welcome block
// used: everything here is centred against the same width, so there is nothing for a wide terminal
// to pull the logo away from.
func (o opening) centre(text string, style lipgloss.Style) string {
	return o.indentFor(lipgloss.Width(text)) + style.Render(text)
}

// indentFor is the left margin that centres a block of a known width.
func (o opening) indentFor(block int) string {
	pad := (o.width - block) / 2
	if pad <= 0 {
		return ""
	}
	return strings.Repeat(" ", pad)
}

// fits returns the first string that fits the width, so a narrow terminal loses the description
// rather than wrapping the frame around it.
func (o opening) fits(long, short string) string {
	if len([]rune(long)) <= o.width {
		return long
	}
	return short
}

// largeHeadHeight is the terminal height below which the large name is not worth its rows.
//
// Eight rows of logo plus a tagline on a twenty row window leaves no conversation, and the opening
// screen is a screen somebody is about to type into rather than a poster.
const largeHeadHeight = 26

// outlined draws one row of the large name: the letters in the brand blue with their thin edge in
// the brand grey, which the theme already adapts for a light terminal. A literal grey would
// disappear on half the terminals it ran on.
//
// The brand package hands over half cells rather than runes because two of the states cannot be a
// rune at all: a cell that is letter on top and edge below is a half block with one colour as its
// foreground and the other as its background, and which colours those are is decided here, where
// the theme lives. Runs of one style are rendered together rather than a cell at a time, so a row
// of the logo carries a handful of escape sequences instead of one per column.
func outlined(cells []brand.HalfCell) string {
	t := theme.Current()

	// A terminal with no colour at all gets the letters and their edge as one silhouette. The split
	// cells below depend on a background colour to carry their second half, and with the styling
	// stripped that half simply vanishes, leaving the word looking moth eaten. Drawing the union is
	// the honest degradation: the same letterforms, a shade bolder, in whatever the terminal prints.
	if lipgloss.ColorProfile() == termenv.Ascii {
		return silhouette(cells)
	}

	// The letters in the brand blue and the edge in the brand grey. Grey rather than the text
	// colour, which was tried first: a near white edge carries almost as much weight as the letters
	// and reads as a second stroke, while the grey sits behind the blue and reads as the thin line
	// it is meant to be.
	colour := func(h brand.Half) lipgloss.TerminalColor {
		if h == brand.Ink {
			return t.Palette.Accent
		}
		return t.Palette.Muted
	}

	// The shapes a half cell can take, and the style each needs. Split cells of one class use a
	// plain foreground; a cell carrying letter and edge at once draws the letter half as the glyph
	// and paints the edge half with the background, which is the one way a terminal draws two
	// colours in one cell. The letter being the glyph is what keeps this honest under NO_COLOR:
	// with the styling stripped the letter half still prints and the edge half quietly goes,
	// so a colourless terminal sees the clean letterforms rather than a texture of leftovers.
	draw := func(c brand.HalfCell) (rune, lipgloss.Style) {
		switch {
		case c.Top == brand.Air && c.Bottom == brand.Air:
			return ' ', lipgloss.NewStyle()
		case c.Top == c.Bottom:
			return '█', lipgloss.NewStyle().Foreground(colour(c.Top))
		case c.Top == brand.Air:
			return '▄', lipgloss.NewStyle().Foreground(colour(c.Bottom))
		case c.Bottom == brand.Air:
			return '▀', lipgloss.NewStyle().Foreground(colour(c.Top))
		case c.Top == brand.Ink:
			return '▀', lipgloss.NewStyle().Foreground(colour(c.Top)).Background(colour(c.Bottom))
		default:
			return '▄', lipgloss.NewStyle().Foreground(colour(c.Bottom)).Background(colour(c.Top))
		}
	}

	var b strings.Builder
	var run []rune
	last := brand.HalfCell{}
	started := false

	flush := func() {
		if !started || len(run) == 0 {
			return
		}
		_, style := draw(last)
		b.WriteString(style.Render(string(run)))
		run = run[:0]
	}

	for _, cell := range cells {
		if started && cell != last {
			flush()
		}
		glyph, _ := draw(cell)
		run = append(run, glyph)
		last, started = cell, true
	}
	flush()
	return b.String()
}

// silhouette draws one row of the large name with letter and edge as one shape, for a terminal
// that cannot tell the two apart anyway.
func silhouette(cells []brand.HalfCell) string {
	var b strings.Builder
	for _, cell := range cells {
		high, low := cell.Top != brand.Air, cell.Bottom != brand.Air
		switch {
		case high && low:
			b.WriteRune('█')
		case high:
			b.WriteRune('▀')
		case low:
			b.WriteRune('▄')
		default:
			b.WriteRune(' ')
		}
	}
	return b.String()
}
