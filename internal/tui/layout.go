package tui

import (
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// The frame every screen is drawn inside.
//
// Screens produce their body and nothing else. The header, the footer and the vertical padding
// that pins the footer to the bottom are done once, here, so a new screen cannot get the chrome
// subtly wrong and so the whole application resizes as one thing.

// Dimensions are the terminal size, with sensible values before the first resize message arrives.
type Dimensions struct {
	Width  int
	Height int
}

const (
	minWidth  = 60
	minHeight = 12
)

// Usable reports whether the terminal is big enough to draw the application.
func (d Dimensions) Usable() bool {
	return d.Width >= minWidth && d.Height >= minHeight
}

// HeaderHeight is how many lines the top bar takes.
//
// Three ordinarily, and five where there is room to put the drawn name in the corner. It is a
// function of the terminal rather than a constant because the name is worth two rows on a window
// that has them and is worth nothing at all on one that does not: a logo that costs somebody two
// lines of their conversation is a logo working against the program it belongs to.
func (d Dimensions) HeaderHeight() int {
	if d.Width >= wordmarkMinWidth && d.Height >= wordmarkMinHeight {
		return 5
	}
	return 3
}

// wordmarkMinWidth and wordmarkMinHeight are where the drawn name in the header starts paying for
// itself.
//
// The width is the name plus enough room for the details beside it to still be worth reading; below
// it the name would be pushing out the facts, which is the wrong way round. The height is where two
// extra rows of chrome stop being a meaningful share of the screen.
const (
	wordmarkMinWidth  = brand.WordmarkWidth + 40
	wordmarkMinHeight = 30
)

// BodyHeight is the number of lines a screen may use, once the chrome is accounted for.
//
// The header plus one. That one line is the gap between the body and the footer, and the arithmetic
// is written as a relationship rather than as a number so that changing the header's height cannot
// silently push every screen's footer off the bottom.
func (d Dimensions) BodyHeight() int {
	if h := d.Height - d.HeaderHeight() - 1; h > 0 {
		return h
	}
	return 1
}

// A launch screen lived here, drawing the mark and the name for nine hundred milliseconds before
// the application appeared. It is gone: a splash is a delay between somebody typing a command and
// reaching the thing they typed it for, and the argument for one was recognition, which the screen
// a conversation opens on already does while also being usable.

// Frame composes a screen: header bar, body, and a footer pinned to the bottom.
//
// The header is three lines and used to be a title, a rule and a blank. The count has to stay the
// same, because BodyHeight subtracts a fixed amount of chrome and a header one line taller would
// push every screen's footer off the bottom at once.
func Frame(d Dimensions, s Status, body, footer string) string {
	t := theme.Current()

	var b strings.Builder
	b.WriteString(Header(d, s))
	b.WriteString("\n")

	bodyLines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	b.WriteString(strings.Join(bodyLines, "\n"))

	// Pin the footer to the bottom by padding, rather than letting it float directly under
	// however much content there happens to be. A footer that moves as content grows reads as a
	// stray line of text instead of part of the frame.
	if pad := d.BodyHeight() - len(bodyLines); pad > 0 {
		b.WriteString(strings.Repeat("\n", pad))
	}

	// The frame owns the indent, not the caller. Leaving it to each screen is how one footer ends
	// up flush left while the rest are not, which is exactly what happened before this moved.
	b.WriteString("\n")
	b.WriteString("  " + t.Footer.Render(strings.TrimLeft(footer, " ")))
	return b.String()
}

// TooSmall renders the message shown when the terminal cannot fit the application.
//
// Refusing to draw is the honest option. A layout squeezed below its minimum produces wrapped,
// overlapping output that looks like a rendering bug, and the user cannot tell the difference
// between "your window is too small" and "this program is broken".
func TooSmall(d Dimensions) string {
	t := theme.Current()
	return t.Warning.Render("Terminal too small.") + "\n" +
		t.Muted.Render("Canopy needs at least "+
			itoa(minWidth)+"x"+itoa(minHeight)+", this is "+
			itoa(d.Width)+"x"+itoa(d.Height)+".")
}

// Keys formats a footer hint list, so every screen's footer looks the same.
//
// Hints are dropped from the right when they do not fit, rather than wrapping. A footer that wraps
// pushes the whole frame a line taller than the terminal and everything above it scrolls away,
// which looks like the program breaking rather than like a narrow window. Callers put the hints in
// order of importance and the least important ones are what go.
func Keys(width int, pairs ...string) string {
	t := theme.Current()

	const gap = "   "
	// The indent Frame adds, plus a column of slack so a full width footer does not sit flush
	// against the edge.
	available := width - 3

	var parts []string
	var used int
	for i := 0; i+1 < len(pairs); i += 2 {
		cost := len(pairs[i]) + 1 + len(pairs[i+1])
		if len(parts) > 0 {
			cost += len(gap)
		}
		if used+cost > available {
			break
		}
		parts = append(parts, t.Key.Render(pairs[i])+" "+t.Footer.Render(pairs[i+1]))
		used += cost
	}
	return strings.Join(parts, t.Footer.Render(gap))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
