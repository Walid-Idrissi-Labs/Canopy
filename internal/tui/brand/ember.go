package brand

// The campfire on the message box, in pieces.
//
// It used to be one row that flickered. It is now a still base woven into the box's own rule, a tip
// above it that does the flickering, and a wisp or two of smoke above that. The base holding still is
// the same rule the mark follows: a fire reads as a fire because everything around the flame is
// steady, and a base that jumped would read as the box breaking.
//
// This lived in wordmark.go, under the large name it shared a file with. That name is gone, since
// the screen a conversation opens on drew it and no longer does, so the embers have the file to
// themselves and it is named for them.

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
