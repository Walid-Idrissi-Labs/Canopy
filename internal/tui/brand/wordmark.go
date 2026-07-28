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
// The name casts a shadow rather than carrying an outline, and getting there took three attempts. A
// halo one cell out fills the mouth of the C and the bowl of the P. Flooding in from the edge first
// fixes the counters and still closes the gaps between letters, because at this size the air between
// two letters is thinner than two outlines meeting in it. Colouring the half block cells differently
// from the full block ones traces every curve exactly, and reads as an edge drawn on the letters
// rather than as anything the letters are sitting in.
//
// A shadow one column to the side touches no counter and no gap. See LargeShadow. Neither survived being looked at.

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
const largeMinimum = LargeShadowWidth + 4

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
// The middle frame is the mark's own fire, which is drawn as `█▀█▀█`: flames with gaps between them
// rather than a solid mound, which is what makes a handful of cells read as burning instead of as a
// block. The frames either side change that texture and leave the outline alone, so the fire
// flickers where a fire flickers and its base holds still.
var emberFrames = [Frames]string{"▄▄█▀█▄▄", "▄█▀█▀█▄", "▄▄█▄█▄▄"}

// emberSmoke is the wisp above the flame, one row up.
//
// It drifts and never returns to where it was two frames ago, which is the rule the mark's own smoke
// follows: a wisp that repeats its position reads as a loop rather than as smoke.
//
// One row and no more. It sits in the status row above the message box, and smoke climbing further
// than that would be drifting through somebody's conversation.
var emberSmoke = [Frames]string{"   ▄▀  ", "  ▀▄   ", "  ▄    "}

// EmberSmoke is the wisp above the fire, at the same width so it lines up over it.
func EmberSmoke(step int) string {
	return emberSmoke[((step%Frames)+Frames)%Frames]
}

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

// shadowOffset is how far the shadow is cast, in bitmap columns.
//
// One column right and nothing down. Down was tried at half a cell and at a full cell: half leaves
// stray marks on the flat tops of the N and the P, and a full cell puts the shadow inside the mouth
// of the C and the bowl of the P, which is the same failure the two computed outlines had before it.
// Straight to the side is the one offset that touches no counter, and it reads as a light source off
// to the left rather than as an outline, which is what it is for.
const shadowOffset = 1

// LargeShadowWidth is the width of the name with its shadow, which is what a caller centres on.
const LargeShadowWidth = LargeWidth + shadowOffset

// LargeShadow is the shadow the name casts, packed to the same five rows as Large.
//
// Computed from the letterform rather than drawn beside it, so the two cannot drift apart. A cell is
// shadow when the letter does not occupy it and does occupy the cell one column to its left, which is
// the whole rule.
//
// Composited by the caller: where a cell holds both, the letter wins. A shadow drawn over its own
// letter is not a shadow.
func LargeShadow() []string {
	bitmap := wordBitmap()
	width := len(bitmap[0]) + shadowOffset

	cast := make([]string, glyphRows)
	for row := range bitmap {
		var b strings.Builder
		for column := 0; column < width; column++ {
			if !ink(bitmap, row, column) && ink(bitmap, row, column-shadowOffset) {
				b.WriteRune('#')
				continue
			}
			b.WriteRune('.')
		}
		cast[row] = b.String()
	}

	out := make([]string, glyphRows/2)
	for row := range out {
		var b strings.Builder
		for column := 0; column < width; column++ {
			high := cast[2*row][column] == '#'
			low := cast[2*row+1][column] == '#'
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
		out[row] = strings.TrimRight(b.String(), " ")
	}
	return out
}

// wordBitmap is the whole word as one bitmap, ten rows tall.
//
// Assembled once rather than per letter, because a shadow computed letter by letter would not know
// about the gaps and the last stroke of one letter would cast into the first of the next.
func wordBitmap() []string {
	rows := make([]string, glyphRows)
	for row := range rows {
		var b strings.Builder
		for i, glyph := range glyphs {
			if i > 0 {
				b.WriteString(strings.Repeat(".", letterGap))
			}
			b.WriteString(glyph[row])
		}
		rows[row] = b.String()
	}
	return rows
}

// ink reports whether a bitmap cell is part of a letter.
func ink(bitmap []string, row, column int) bool {
	if row < 0 || row >= len(bitmap) {
		return false
	}
	if column < 0 || column >= len(bitmap[row]) {
		return false
	}
	return bitmap[row][column] == '#'
}
