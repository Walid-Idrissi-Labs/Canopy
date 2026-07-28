package brand

// The name in block letters, at the size it deserves.
//
// The old wordmark was three rows of half blocks. It read well and it was small: on a wide terminal
// the name of the program was the least prominent thing on a screen with nothing else on it. This is
// the same idea at twice the height and a row per row rather than packed two to a line, which is what
// lets the strokes be two cells thick. A one cell stroke is what makes block letters look like a
// diagram of letters instead of letters.
//
// There is no per letter outline, and that is a decision rather than an omission. A halo one cell out
// was drawn, looked at, and thrown away: with two cell strokes and the air a six letter word can
// afford at this size, the halo fills the mouth of the C and the bowl of the P and the whole thing
// reads as a smudge. A drop shadow has the same problem one diagonal over. The letters carry
// themselves at this size, and the contour that does help is a frame around the word, which the
// interface draws because it is the side that knows about colour.

import "strings"

// letters are the six glyphs, each seven columns wide on a six row grid.
//
// Written out per letter rather than as six long rows, because a row of all six is unreadable in a
// diff and impossible to adjust: changing the A means counting columns through the other five.
var letters = [][]string{
	{ // C
		" █████ ",
		"██   ██",
		"██     ",
		"██     ",
		"██   ██",
		" █████ ",
	},
	{ // A
		" █████ ",
		"██   ██",
		"██   ██",
		"███████",
		"██   ██",
		"██   ██",
	},
	{ // N
		"██   ██",
		"███  ██",
		"████ ██",
		"██ ████",
		"██  ███",
		"██   ██",
	},
	{ // O
		" █████ ",
		"██   ██",
		"██   ██",
		"██   ██",
		"██   ██",
		" █████ ",
	},
	{ // P
		"██████ ",
		"██   ██",
		"██   ██",
		"██████ ",
		"██     ",
		"██     ",
	},
	{ // Y
		"██   ██",
		" ██ ██ ",
		"  ███  ",
		"   ██  ",
		"   ██  ",
		"   ██  ",
	},
}

// letterGap is the air between two letters.
//
// Two columns rather than one. At two cell strokes a single column of air makes the N and the O read
// as one shape, which is the failure mode of every block letter word that is set too tight.
const letterGap = 2

// LargeWidth is how many columns the large name occupies.
const LargeWidth = 6*7 + 5*letterGap

// largeMinimum is the narrowest terminal the large name is drawn in.
//
// Two columns of air on each side, for the reason the mark has them: a wordmark flush against the
// edge of a terminal reads as one that got cut off.
const largeMinimum = LargeWidth + 4

// FitsLarge reports whether the large name can be drawn in the width given.
func FitsLarge(width int) bool { return width >= largeMinimum }

// Large returns the name at full height.
//
// Rows carry no trailing spaces, which is the same rule the mark follows: a trailing space is
// invisible until something pads a line to a width and the drawing is suddenly off centre.
func Large() []string {
	rows := make([]string, len(letters[0]))
	for row := range rows {
		var b strings.Builder
		for i, letter := range letters {
			if i > 0 {
				b.WriteString(strings.Repeat(" ", letterGap))
			}
			b.WriteString(letter[row])
		}
		rows[row] = strings.TrimRight(b.String(), " ")
	}
	return rows
}
