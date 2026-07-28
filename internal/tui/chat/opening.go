package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

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
	// The gap between the name and the box: a blank row, and the row the status keeps whether or not
	// it has anything to say.
	gap := []string{"", o.status}
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

	// The large name inside a frame, which is the version worth looking at, and the packed one
	// behind it for a terminal that cannot afford eight rows on a logo.
	if brand.FitsLarge(o.width) && o.height >= largeHeadHeight {
		indent := o.indentFor(brand.LargeWidth)

		// No written name under it, which is a departure from the rule below and was asked for
		// directly. The rule is not abandoned: the header above this draws "canopy" in text on every
		// screen, so the written name is still on screen for anything that cannot read block letters,
		// it is simply not repeated twice in the same eyeful.
		head := make([]string, 0, len(brand.Large()))
		for _, line := range brand.Large() {
			head = append(head, indent+edged(line))
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
// Two colours rather than one, which is what stops the mark reading as a stencil: the tent takes the
// primary and the flame takes the secondary, so the logo carries two thirds of the palette instead
// of being a shape in a single hue. The brand package hands over where the fire is rather than what
// colour it should be, because it constructs no colours at all.
func paint(row int, line string) string {
	t := theme.Current()

	fireTop, fireColumn, fireHeight, fireWidth := brand.FireRegion()
	if inside(row, fireTop, fireHeight) {
		return tint(line, fireColumn, fireWidth, t.Flame)
	}

	smokeTop, smokeColumn, smokeHeight, smokeWidth := brand.SmokeRegion()
	if inside(row, smokeTop, smokeHeight) {
		return tint(line, smokeColumn, smokeWidth, t.Smoke)
	}
	return t.Logo.Render(line)
}

func inside(row, top, height int) bool { return row >= top && row < top+height }

// tint styles one span of a row differently from the rest of it.
func tint(line string, column, width int, style lipgloss.Style) string {
	logo := theme.Current().Logo

	runes := []rune(line)
	if len(runes) <= column {
		return logo.Render(line)
	}
	end := column + width
	if end > len(runes) {
		end = len(runes)
	}
	return logo.Render(string(runes[:column])) +
		style.Render(string(runes[column:end])) +
		logo.Render(string(runes[end:]))
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

// edged renders one row of the drawn name with its edges picked out.
//
// The letterform is drawn in half blocks, and that carries a fact worth using: a cell holding a half
// block is a sloped or curved part of the outline, and a cell holding a full block is solid interior.
// Colouring the two classes differently traces every curve in a line one cell wide, at no cost in
// space and with no second drawing to maintain.
//
// The edge takes the text colour rather than a literal white, so it is near white on a dark terminal
// and near black on a light one. A hardcoded white would vanish on half the terminals it ran on.
//
// Two computed outlines were tried first and thrown away. Both filled the gaps between the letters,
// because at this size the air between two letters is thinner than two outlines meeting in it.
func edged(line string) string {
	t := theme.Current()

	var b strings.Builder
	var run []rune
	solid := false

	flush := func() {
		if len(run) == 0 {
			return
		}
		if solid {
			b.WriteString(t.Logo.Render(string(run)))
		} else {
			b.WriteString(t.Body.Render(string(run)))
		}
		run = run[:0]
	}

	for _, r := range line {
		switch r {
		case '█':
			if !solid {
				flush()
				solid = true
			}
		case '▀', '▄':
			if solid {
				flush()
				solid = false
			}
		default:
			// Air. Written as is, so a run of spaces does not carry an escape sequence.
			flush()
			solid = false
			b.WriteRune(r)
			continue
		}
		run = append(run, r)
	}
	flush()
	return b.String()
}
