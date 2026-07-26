package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

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

// BodyHeight is the number of lines a screen may use, once the chrome is accounted for.
func (d Dimensions) BodyHeight() int {
	const chrome = 4 // header, blank, blank, footer
	if h := d.Height - chrome; h > 0 {
		return h
	}
	return 1
}

// logo is drawn on the splash.
//
// Deliberately small. A full width banner looks impressive in a screenshot and is in the way every
// time you actually start the program, which is the more common experience by a wide margin.
const logo = `
   ___
  / __|__ _ _ _  ___ _ __ _  _
 | (__/ _` + "`" + ` | ' \/ _ \ '_ \ || |
  \___\__,_|_||_\___/ .__/\_, |
                    |_|   |__/    `

// Splash renders the launch screen.
func Splash(d Dimensions, subtitle string) string {
	t := theme.Current()

	// The name appears as text as well as art. Block letters are unreadable to a screen reader,
	// unrecognisable in a narrow terminal, and unsearchable in a pasted bug report.
	block := t.Logo.Render(strings.TrimLeft(logo, "\n")) + "\n\n" +
		t.Title.Render("Canopy") + "\n" +
		t.Muted.Render(subtitle)

	if !d.Usable() {
		return block
	}
	return lipgloss.Place(d.Width, d.Height, lipgloss.Center, lipgloss.Center, block)
}

// Frame composes a screen: title bar, body, and a footer pinned to the bottom.
func Frame(d Dimensions, title, context, body, footer string) string {
	t := theme.Current()

	var b strings.Builder
	b.WriteString(t.Title.Render(title))
	if context != "" {
		b.WriteString(t.Muted.Render("  " + context))
	}
	b.WriteString("\n")
	b.WriteString(t.Border.Render(strings.Repeat("─", maxInt(1, minInt(d.Width, 200)))))
	b.WriteString("\n\n")

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
