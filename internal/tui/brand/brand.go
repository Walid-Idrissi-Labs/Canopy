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

// mark is a tent.
//
// A canopy is a shelter you sit under, and that is the reading the name is meant to carry: several
// agents working away, and somewhere comfortable to watch them from. It took two attempts to get
// here. The first drawing was a tree canopy, and a wide dome on a narrow stem is not a tree, it is
// a mushroom cloud, which is an unfortunate thing to open a coding tool with.
//
// The doorway is the part doing the work. A plain triangle is a mountain; a triangle with an
// opening in it is somewhere you would go and sit. It is left as a hole rather than filled in, so
// whatever the terminal background is shows through, which is warmer than any colour this package
// could pick and is the same reason nothing here sets a background.
//
// Drawn from the three block characters that every terminal font has, rather than from the quadrant
// and corner blocks that look better in the two fonts they render correctly in and like a row of
// missing glyph boxes everywhere else. This is the first thing a new user sees and it is not the
// place to find out which font they are running.
//
// The guy ropes and their pegs are what stop it reading as a pyramid. A tent is recognisable by the
// things holding it down as much as by the shape itself, and two pegs leaning away on each side
// place it on the ground rather than floating it in the middle of the screen.
//
// Every row is symmetric about the centre column and carries no trailing spaces, so the indent
// alone positions it. An asymmetric one reads as a rendering fault rather than as a drawing.
const mark = `
            █
           ███
          █████
         ███████
        █████████
       ████▀▀▀████
  ▄   ████     ████   ▄
  █  ████       ████  █
 ▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄`

// MarkWidth is how many columns the mark occupies.
const MarkWidth = 25

// minimumWidth is the narrowest terminal the mark is drawn in.
//
// Below it the art is dropped rather than scaled or clipped. A clipped logo is worse than no logo:
// half a tree looks like the program is broken, while the wordmark on its own looks deliberate.
const minimumWidth = MarkWidth + 4

// wordmark is the name in block letters.
//
// The name is drawn as well as written because a mark on its own is not a brand, it is a shape.
// Every terminal program worth recognising, and every one this is measured against, puts its name on
// the screen at launch in letters you can read across a room.
//
// Built from the full block alone. The half blocks are fine for a silhouette, where a rough edge
// reads as shading, and wrong for letterforms, where the same roughness reads as a broken font.
const wordmark = `
█████ █████ █   █ █████ █████ █   █
█     █   █ ██  █ █   █ █   █ █   █
█     █████ █ █ █ █   █ █████ █████
█     █   █ █  ██ █   █ █       █  
█████ █   █ █   █ █████ █       █  `

// WordmarkWidth is how many columns the drawn name occupies.
const WordmarkWidth = 35

// Wordmark returns the name in block letters, or nothing when it will not fit.
//
// Nothing rather than a smaller version, for the same reason the mark is dropped rather than
// clipped: half a word looks like a fault, and the plain text name that always accompanies it
// already covers the narrow case.
func Wordmark(width int) []string {
	if width < WordmarkWidth {
		return nil
	}
	return strings.Split(strings.TrimLeft(wordmark, "\n"), "\n")
}

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
