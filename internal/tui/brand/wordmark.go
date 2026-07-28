package brand

// The name in block letters, at the size it deserves.
//
// The letterforms are drawn on a ten row grid and packed two rows to a line, which is the same trick
// the small wordmark uses on a six row grid. The packing is the whole reason to do it this way: half
// blocks buy twice the vertical resolution a row per row drawing has, and that resolution is what
// puts a curve on the C and a shoulder on the N instead of stairs. A version drawn a row per row in
// full blocks was written first and thrown away, because at that resolution every round letter is a
// rectangle with its corners knocked off.
//
// Five rows out against the small wordmark's three, so it is most of twice the size while being the
// same drawing at heart.
//
// The edge of the drawing is not computed and does not need to be. In a half block drawing the cells
// holding a half block are exactly the sloped and curved parts of the letterform, and the cells
// holding a full block are its solid interior. Colouring those two classes differently traces every
// curve in a line one cell wide, which the interface does because it is the side that knows about
// colour.
//
// Two computed outlines were written before that was noticed and both were thrown away. A halo one
// cell out fills the mouth of the C and the bowl of the P. Flooding from outside first fixes the
// counters and still closes the gaps between letters, because at this size the air between two
// letters is thinner than two outlines. Neither survived being looked at.

import "strings"

// glyphs are the letters as bitmaps, ten rows tall and nine columns wide.
//
// A hash is ink and a dot is air. Written this way rather than as packed half blocks because a packed
// drawing cannot be edited: moving the waist of the A means working out which of three characters
// each affected cell becomes, and getting one wrong is invisible until it is on a screen.
var glyphs = [][]string{
	{ // C
		"..#####..",
		".##...##.",
		"##.......",
		"##.......",
		"##.......",
		"##.......",
		"##.......",
		"##.......",
		".##...##.",
		"..#####..",
	},
	{ // A
		"..#####..",
		".##...##.",
		"##.....##",
		"##.....##",
		"#########",
		"##.....##",
		"##.....##",
		"##.....##",
		"##.....##",
		"##.....##",
	},
	{ // N
		"##.....##",
		"###....##",
		"####...##",
		"##.##..##",
		"##..##.##",
		"##...####",
		"##....###",
		"##.....##",
		"##.....##",
		"##.....##",
	},
	{ // O
		"..#####..",
		".##...##.",
		"##.....##",
		"##.....##",
		"##.....##",
		"##.....##",
		"##.....##",
		"##.....##",
		".##...##.",
		"..#####..",
	},
	{ // P
		"#######..",
		"##....##.",
		"##.....##",
		"##....##.",
		"#######..",
		"##.......",
		"##.......",
		"##.......",
		"##.......",
		"##.......",
	},
	{ // Y
		"##.....##",
		".##...##.",
		"..##.##..",
		"...###...",
		"...##....",
		"...##....",
		"...##....",
		"...##....",
		"...##....",
		"...##....",
	},
}

// glyphWidth and glyphRows are the cell each letter is drawn in.
const (
	glyphWidth = 9
	glyphRows  = 10
)

// letterGap is the air between two letters.
//
// Two columns rather than one. At two cell strokes a single column makes the N and the O read as one
// shape, which is what happens to every block letter word that is set too tight.
const letterGap = 2

// LargeWidth is how many columns the large name occupies.
const LargeWidth = 6*glyphWidth + 5*letterGap

// largeMinimum is the narrowest terminal the large name is drawn in.
//
// Two columns of air on each side, for the reason the mark has them: a wordmark flush against the
// edge of a terminal reads as one that got cut off.
const largeMinimum = LargeWidth + 4

// FitsLarge reports whether the large name can be drawn in the width given.
func FitsLarge(width int) bool { return width >= largeMinimum }

// Large returns the name, five rows of half blocks.
//
// Rows carry no trailing spaces, which is the rule the mark follows too: a trailing space is
// invisible until something pads the line to a width and the drawing is suddenly off centre.
func Large() []string {
	rows := make([]string, glyphRows/2)
	for row := range rows {
		var b strings.Builder
		for i, glyph := range glyphs {
			if i > 0 {
				b.WriteString(strings.Repeat(" ", letterGap))
			}
			b.WriteString(pack(glyph[2*row], glyph[2*row+1]))
		}
		rows[row] = strings.TrimRight(b.String(), " ")
	}
	return rows
}

// pack folds two bitmap rows into one line of half blocks.
//
// Only the three block characters every terminal font has, which is the constraint the mark is drawn
// under and matters more here: this is the first thing a new user sees, and it is not the place to
// find out which font they are running.
func pack(top, bottom string) string {
	upper, lower := []rune(top), []rune(bottom)

	var b strings.Builder
	for i := 0; i < glyphWidth; i++ {
		high := i < len(upper) && upper[i] == '#'
		low := i < len(lower) && lower[i] == '#'

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

// emberFrames are the campfire reduced to a single row, for a corner with no room for the mark.
//
// The base holds still and the flame moves above it, which is the rule the full animation follows and
// the reason a fire across a clearing is restful rather than distracting. Three frames, because two
// reads as a blink.
var emberFrames = [Frames]string{" ▄▄█▄▄ ", "▄▄███▄▄", " ▄███▄ "}

// EmberOut is the fire once it has gone out: a few coals, and no flame above them.
//
// A different shape rather than the same one in a duller colour, so the state survives a terminal
// with no colour in it. It is deliberately the smallest picture here, because a fire that has gone
// out should read as less than one that is burning even before the colour is taken in.
//
// Drawn at the same width as every lit frame, because a mark that changed width when it went out
// would shift the rule it sits on.
const EmberOut = "  ▄▄▄  "

// EmberWidth is how many columns an ember occupies, lit or out.
const EmberWidth = 7

// Ember is the campfire at seven cells wide, for the message box corner.
//
// Out of range steps wrap, for the reason Frame's do: the caller is a ticker whose count only goes
// up, and making every call site remember a modulus is how one of them forgets.
func Ember(step int) string {
	return emberFrames[((step%Frames)+Frames)%Frames]
}
