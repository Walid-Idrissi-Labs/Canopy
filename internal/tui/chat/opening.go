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
// The composition is the message box on the middle of the screen, the commands along the bottom, and
// the mark in the bottom right corner.
//
// **There is no name drawn in the middle of it, and that is a reversal.** This screen used to carry
// "canopy" in outlined block letters four times the size of the header's, with the pair arranged so
// that the middle of the space between the name and the box landed on the middle of the screen. It
// was asked for directly that it go. The header draws the name in the top right corner of every
// screen and now draws it here too, which is where a brand belongs once somebody has already opened
// the program: a logo in the middle of a screen is a splash, and this one is a screen somebody is
// about to type into.
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
	// nothing, so a message arriving does not move the box.
	status string

	// context is the bottom left text: where the agent is working and what it is talking to.
	// Already styled, because it is several styles on one line.
	context []string

	// panel is the btw panel, drawn under the box. Empty when it is not open.
	panel []string

	// menu is the command list, drawn under the box. Empty when it is not open.
	menu []string

	// step is where the animation has got to.
	step int
}

func (o opening) render() string {
	// The rows the status needs whether or not it has anything to say. Split rather than dropped in
	// as one string, because a slash command's listing arrives here several lines tall and a single
	// slot would count it as one row and put the box a dozen rows from where it belongs.
	status := strings.Split(o.status, "\n")
	block := append(append([]string(nil), status...), o.box...)
	// The command list and the btw panel hang off the bottom of the box and do not move it. The
	// centring below is computed from the box and the status alone, so opening either leaves the box
	// exactly where it was: a menu that shunted the box up the screen as it filtered would be
	// unusable to type into.
	block = append(block, o.panel...)
	block = append(block, o.menu...)

	// The box itself is what lands on the middle of the screen, with the status stacked in the rows
	// above it.
	//
	// Measured from the box rather than from the box and the status together, which is the reading
	// this started as and is subtly wrong: the status grows by several rows the moment a slash
	// command lists itself, and centring the pair would walk the box down the screen as somebody
	// typed. Reserving the status row already buys a message the right to appear without moving
	// anything; centring on the pair would spend it again.
	top := (o.height-len(o.box))/2 - len(status)
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
