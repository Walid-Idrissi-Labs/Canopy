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
// The letters carry a thin edge, and how it is drawn is the part that took getting right. A halo
// painted one whole cell out fills the mouth of the C and the bowl of the P, and a shadow cast to
// one side reads as a light source rather than as an edge; both shipped briefly and neither survived
// being looked at. This version computes the edge at bitmap resolution: every air cell touching a
// letter cell, diagonals included, becomes edge. That is half a terminal cell of edge vertically and
// one column horizontally, which is as thin as the three safe block characters can draw, and because
// it is computed from the letterform the two cannot drift apart.
//
// A cell whose top half is letter and bottom half is edge cannot be drawn with a foreground alone,
// so this package does not draw at all: it hands back what each half of each cell is, and the caller
// styles a half block with the letter colour on one axis and the edge colour on the other. That
// keeps the rule that brand constructs no colours, and it is what lets the edge trace every curve
// exactly instead of stopping wherever the packing grid happens to fall.

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
// Three columns rather than two, which is the edge's doing. Each letter's edge takes one column of
// the gap from its own side, and at two columns the edges of neighbouring letters would meet and
// weld the word into one shape. Three leaves a column of true air between them.
const letterGap = 3

// edgePad is the margin the edge needs around the word: one bitmap column each side and one bitmap
// row above and below, so the edge of the first and last letters is not clipped off.
const edgePad = 1

// LargeWidth is how many columns the large name occupies, edge included.
const LargeWidth = 6*glyphWidth + 5*letterGap + 2*edgePad

// LargeRows is how many terminal rows the large name occupies: the ten letter rows plus the edge row
// above and below, packed two to a line.
const LargeRows = (glyphRows + 2*edgePad) / 2

// largeMinimum is the narrowest terminal the large name is drawn in.
//
// Two columns of air on each side, for the reason the mark has them: a wordmark flush against the
// edge of a terminal reads as one that got cut off.
const largeMinimum = LargeWidth + 4

// FitsLarge reports whether the large name can be drawn in the width given.
func FitsLarge(width int) bool { return width >= largeMinimum }

// Half says what one half of a terminal cell is carrying.
type Half uint8

const (
	// Air is the terminal background showing through.
	Air Half = iota
	// Ink is the letter itself.
	Ink
	// Edge is the thin border traced around the letter.
	Edge
)

// HalfCell is one terminal cell of the large name, split into the two halves a half block can
// address. The caller draws it: same class top and bottom is a full block or a space, and a split
// cell is a half block styled with one class as foreground and the other as background.
type HalfCell struct {
	Top    Half
	Bottom Half
}

// Large returns the name as half cells, LargeRows tall and LargeWidth wide.
//
// Cells rather than strings, because two of the cell states cannot be expressed as a rune: a cell
// that is letter on one half and edge on the other needs two colours at once, and which colours
// those are is the caller's decision, not this package's.
func Large() [][]HalfCell {
	bitmap := wordBitmap()
	rows := len(bitmap)

	out := make([][]HalfCell, rows/2)
	for row := range out {
		line := make([]HalfCell, len(bitmap[0]))
		for column := range line {
			line[column] = HalfCell{
				Top:    classify(bitmap, 2*row, column),
				Bottom: classify(bitmap, 2*row+1, column),
			}
		}
		out[row] = line
	}
	return out
}

// classify says what one bitmap cell is: letter, the edge around a letter, or air.
//
// Edge is any air cell touching ink, diagonals included. Diagonals matter: without them the edge
// breaks into dashes along the N's stroke and the Y's arms, and a border with holes in it reads as a
// rendering fault rather than as a line.
func classify(bitmap []string, row, column int) Half {
	if ink(bitmap, row, column) {
		return Ink
	}
	for dr := -1; dr <= 1; dr++ {
		for dc := -1; dc <= 1; dc++ {
			if ink(bitmap, row+dr, column+dc) {
				return Edge
			}
		}
	}
	return Air
}

// wordBitmap is the whole word as one bitmap, with the margin the edge draws in.
//
// Assembled once rather than per letter, because an edge computed letter by letter would not know
// about the gaps and could weld the last stroke of one letter to the first of the next.
func wordBitmap() []string {
	width := 6*glyphWidth + 5*letterGap + 2*edgePad
	blank := dots(width)

	rows := make([]string, 0, glyphRows+2*edgePad)
	rows = append(rows, blank)
	for row := 0; row < glyphRows; row++ {
		line := make([]byte, 0, width)
		line = append(line, '.')
		for i, glyph := range glyphs {
			if i > 0 {
				line = append(line, dots(letterGap)...)
			}
			line = append(line, glyph[row]...)
		}
		line = append(line, '.')
		rows = append(rows, string(line))
	}
	return append(rows, blank)
}

func dots(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = '.'
	}
	return string(out)
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

// The campfire on the message box, in pieces.
//
// It used to be one row that flickered. It is now a still base woven into the box's own rule, a tip
// above it that does the flickering, and a wisp or two of smoke above that. The base holding still is
// the same rule the mark follows: a fire reads as a fire because everything around the flame is
// steady, and a base that jumped would read as the box breaking.

// EmberBase is the bed of the fire, riding on the box's top edge.
//
// Constant rather than a frame, because it is drawn into the border rule and a border that changed
// shape three times a second would pull the eye every time. The flames with gaps between them are
// what make seven cells read as burning rather than as a lump.
const EmberBase = "▄█▀█▀█▄"

// EmberCoreColumn and EmberCoreWidth are the heart of the base, for a caller that wants the centre
// of the fire a shade brighter than its ends. Measured here so the two cannot drift apart.
const (
	EmberCoreColumn = 2
	EmberCoreWidth  = 3
)

// emberTips are the flame above the base, one row up, and the part that moves.
var emberTips = [Frames]string{"  ▄█▄  ", " ▄▀█ ▄ ", "  ▄█▀▄ "}

// EmberTip is the flame above the base at a step, at the same width so it lines up over it.
func EmberTip(step int) string {
	return emberTips[((step%Frames)+Frames)%Frames]
}

// emberWisps are the smoke, rising away from the fire and thinning as it goes.
//
// Two heights. The near wisp sits one row above the tip and the far one a row above that, and the
// far one is deliberately sparser: smoke that keeps its density as it climbs reads as a column of
// marks, and smoke that thins reads as smoke fading out. Neither returns to where it was on the
// previous frame, which is the rule all the smoke in this program follows: a wisp that repeats its
// position reads as a loop.
var emberWisps = [2][Frames]string{
	{"   ▄▀  ", "  ▀▄   ", "    ▄▀ "},
	{"  ▀    ", "    ▀  ", "   ▀   "},
}

// EmberWisp is the smoke at a step and a rise. Rise one is the row above the tip and rise two the
// row above that; anything else is empty, because smoke climbing further than two rows would be
// drifting up through somebody's conversation.
func EmberWisp(step, rise int) string {
	if rise < 1 || rise > len(emberWisps) {
		return ""
	}
	return emberWisps[rise-1][((step%Frames)+Frames)%Frames]
}

// EmberOut is the fire once it has gone out: a low bed of coals, and no flame above them.
//
// A different shape rather than the same one in a duller colour, so the state survives a terminal
// with no colour in it. It is deliberately the smallest picture here, because a fire that has gone
// out should read as less than one that is burning even before the colour is taken in. The raised
// cell in the middle is what makes it coals rather than a smudge: a burnt out fire is not flat, it
// is a heap with the last of the heat in the centre, which is also where a caller that has two
// greys puts the darker one down and the lighter one in the middle.
const EmberOut = " ▄▄▀▄▄ "

// EmberWidth is how many columns an ember occupies, lit or out, tip and wisps included.
const EmberWidth = 7
