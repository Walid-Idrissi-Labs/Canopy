package brand

// The mark, in motion.
//
// Only the fire and its smoke move. The tent is a fixed silhouette on purpose: a logo that redraws
// itself every frame is a distraction sitting in the corner of a screen somebody is trying to think
// on, and the thing that makes a campfire read as a campfire is that everything around it is still.
//
// The frames are built here and driven from nowhere yet. Driving them needs a command that
// reschedules itself from an Update, and both of the Update functions that could own one are open
// on another branch. See M-08. Building the frames first means that when the wiring lands there is
// nothing to design, only a ticker to add, and it means the shapes can be tested now rather than
// alongside a timer.

import "strings"

// Frames is how many distinct pictures the animation cycles through.
//
// Three. Two reads as a blink and anything past four is a flicker at any interval slow enough to be
// restful, and restful is the requirement: this sits in a corner for as long as the program is
// open.
const Frames = 3

// fire is the flame at each frame, drawn bottom up so the base never moves.
//
// The base staying put is what stops the whole thing looking like it is jumping around. Only the
// tip and the smoke change, which is also how a real fire looks from across a clearing.
var fire = [Frames][]string{
	{"  ▄  ", " ▄█▄ ", "█▀█▀█"},
	{"  ▀  ", " ▄█▄ ", "█▀█▀█"},
	{" ▄ ▄ ", " ▄█▄ ", "█▀█▀█"},
}

// smoke drifts, and never repeats its position between frames.
//
// A wisp that returns to where it was two frames ago reads as a loop rather than as smoke, which is
// the one thing that would make this look cheap.
var smoke = [Frames][]string{
	{"  ▄▀", " ▀▄ ", "  ▄ ", " ▀  "},
	{" ▄▀ ", "▀▄  ", " ▄  ", "  ▀ "},
	{"▄▀  ", " ▀▄ ", "  ▄ ", " ▀  "},
}

// Frame returns the mark drawn at a given step of the animation.
//
// Out of range steps wrap rather than failing, because the caller is a ticker whose count only ever
// goes up and making every call site remember to take a modulus is how one of them forgets.
func Frame(step int) []string {
	step = ((step % Frames) + Frames) % Frames

	lines := Lines()
	out := make([]string, len(lines))
	copy(out, lines)

	// The fire occupies the last three rows and the smoke the four above it, both to the right of
	// the tent. Composed by overwriting columns rather than by rebuilding the row, so the tent
	// cannot drift by a column when the fire changes width.
	overlay(out, len(out)-4, fireColumn, fire[step])
	overlay(out, len(out)-9, smokeColumn, smoke[step])
	return out
}

// fireColumn and smokeColumn are where the moving parts sit, measured from the left of the mark.
const (
	fireColumn  = 24
	smokeColumn = 27
)

// overlay writes a block into the lines at a column, padding short rows to reach it.
func overlay(lines []string, top, column int, block []string) {
	for i, piece := range block {
		row := top + i
		if row < 0 || row >= len(lines) {
			continue
		}
		runes := []rune(lines[row])
		if len(runes) < column {
			runes = append(runes, []rune(strings.Repeat(" ", column-len(runes)))...)
		}
		runes = runes[:column]
		lines[row] = strings.TrimRight(string(runes)+piece, " ")
	}
}
