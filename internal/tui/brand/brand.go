// Package brand is the Canopy mark.
//
// Its own package because two screens draw it and neither can import the other. The splash draws it
// at launch and the empty conversation draws it again, which is the moment somebody has just
// started a new chat and is looking at a screen with nothing on it. Those are the same picture and
// they should not be two copies that drift.
//
// Nothing here knows about colour. The caller styles the lines it gets back, so the mark works in
// both themes and under NO_COLOR without this package having an opinion about any of it.
package brand

import "strings"

// mark is a canopy over a trunk.
//
// Drawn from the three block characters that every terminal font has, rather than from the quadrant
// and corner blocks that look better in the two fonts they render correctly in and like a row of
// missing glyph boxes everywhere else. This is the first thing a new user sees and it is not the
// place to find out which font they are running.
//
// The silhouette is symmetric about its centre column on every row. An asymmetric one reads as a
// rendering fault rather than as a drawing, which is the opposite of the point.
const mark = `
       ▄▄▄▄▄▄▄
    ▄███████████▄
  ▄███████████████▄
  ▀███████████████▀
     ▀▀▀▀███▀▀▀▀
         ███
        ▄███▄`

// MarkWidth is how many columns the mark occupies.
const MarkWidth = 21

// minimumWidth is the narrowest terminal the mark is drawn in.
//
// Below it the art is dropped rather than scaled or clipped. A clipped logo is worse than no logo:
// half a tree looks like the program is broken, while the wordmark on its own looks deliberate.
const minimumWidth = MarkWidth + 4

// Tagline is the one line description, kept here so the splash and the empty conversation cannot
// describe the program differently.
const Tagline = "a terminal coding agent for running several at once"

// Fits reports whether the mark can be drawn in the width given.
func Fits(width int) bool { return width >= minimumWidth }

// Lines is the mark itself, with no indent.
//
// For callers that do their own centring, such as the launch screen, which centres the whole block
// vertically and horizontally at once.
func Lines() []string { return strings.Split(strings.TrimLeft(mark, "\n"), "\n") }

// Mark returns the logo as display lines, indented to sit under the width given.
//
// Empty when there is not enough room, which callers are expected to handle by showing the wordmark
// alone. Returning a clipped mark instead would push the decision into every caller and they would
// not all make it the same way.
func Mark(width int) []string {
	if !Fits(width) {
		return nil
	}

	lines := Lines()
	pad := (width - MarkWidth) / 2
	// Capped so a very wide terminal does not push the mark into the middle of nowhere while the
	// text under it stays where the eye expects to find it.
	if pad > 6 {
		pad = 6
	}
	if pad <= 0 {
		return lines
	}

	indent := strings.Repeat(" ", pad)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, indent+line)
	}
	return out
}
