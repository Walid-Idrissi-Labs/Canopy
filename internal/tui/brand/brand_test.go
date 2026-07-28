package brand_test

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
)

// M-05. The mark is a scene now, not a lone silhouette, so the rule that every row mirrors about
// its centre is gone. It was protecting against something real, a shape that looks accidentally
// clipped, and that is what is checked here instead. Symmetry was only ever a proxy for it, and it
// is the wrong proxy once there is a fire beside the tent: a campsite that mirrored perfectly would
// look machine generated rather than drawn.
func TestNoRowLooksAccidentallyClipped(t *testing.T) {
	lines := brand.Lines()
	if len(lines) == 0 {
		t.Fatal("the mark is empty")
	}

	for i, line := range lines {
		if trimmed := strings.TrimRight(line, " "); trimmed != line {
			// Trailing spaces are invisible until something centres the block, at which point every
			// row is pushed left by a different amount.
			t.Errorf("row %d carries trailing spaces: %q", i, line)
		}
		if width := len([]rune(line)); width > brand.MarkWidth {
			t.Errorf("row %d is %d columns, wider than the declared %d: %q",
				i, width, brand.MarkWidth, line)
		}
	}

	// At least one row has to reach the declared width, or MarkWidth is a number nobody maintains
	// and every caller reserves space for a mark that is narrower than it claims.
	widest := 0
	for _, line := range lines {
		if w := len([]rune(line)); w > widest {
			widest = w
		}
	}
	if widest != brand.MarkWidth {
		t.Errorf("the widest row is %d columns and MarkWidth says %d", widest, brand.MarkWidth)
	}
}

// The drawn name is the other half of the mark and gets the same treatment.
func TestTheWordmarkIsWhatItSaysItIs(t *testing.T) {
	lines := brand.Wordmark(brand.WordmarkWidth)
	if len(lines) == 0 {
		t.Fatal("the wordmark did not render at exactly its own width")
	}
	if brand.Wordmark(brand.WordmarkWidth-1) != nil {
		t.Error("the wordmark rendered into less room than it needs, so it will be clipped")
	}

	widest := 0
	for i, line := range lines {
		if trimmed := strings.TrimRight(line, " "); trimmed != line {
			t.Errorf("wordmark row %d carries trailing spaces: %q", i, line)
		}
		if w := len([]rune(line)); w > widest {
			widest = w
		}
	}
	if widest != brand.WordmarkWidth {
		t.Errorf("the widest wordmark row is %d and WordmarkWidth says %d", widest, brand.WordmarkWidth)
	}
}

// Only the three block characters every terminal font has. The quadrant and corner blocks look
// better in the two fonts that render them and like a row of missing glyph boxes everywhere else,
// and a logo is not worth finding that out on somebody else's machine.
func TestTheMarkUsesOnlyBlocksEveryFontHas(t *testing.T) {
	allowed := map[rune]bool{' ': true, '█': true, '▀': true, '▄': true}

	for _, line := range brand.Lines() {
		for _, r := range line {
			if !allowed[r] {
				t.Errorf("the mark uses %q, which is not one of the three safe blocks", string(r))
			}
		}
	}
}

// Clipped is worse than absent. Half a tree looks like the program is broken, while the wordmark on
// its own looks deliberate, so the mark is dropped rather than cut.
func TestANarrowTerminalGetsNoMarkRatherThanHalfOne(t *testing.T) {
	if got := brand.Mark(brand.MarkWidth); len(got) != 0 {
		t.Errorf("a terminal exactly as wide as the mark still drew it:\n%s", strings.Join(got, "\n"))
	}
	if brand.Fits(brand.MarkWidth) {
		t.Error("Fits says a terminal with no margin at all is wide enough")
	}
	if !brand.Fits(80) {
		t.Error("Fits refuses an eighty column terminal, which is the common case")
	}
}

// Every drawn row has to stay inside the width it was given, or the frame wraps and the whole
// screen looks like it broke.
func TestTheMarkStaysInsideTheWidthItWasGiven(t *testing.T) {
	for _, width := range []int{26, 40, 80, 120, 200} {
		for _, line := range brand.Mark(width) {
			if len([]rune(line)) > width {
				t.Errorf("at %d columns a row is %d wide: %q", width, len([]rune(line)), line)
			}
		}
	}
}

// The tagline is shared rather than written twice, so the launch screen and the empty conversation
// cannot end up describing the program differently.
func TestThereIsOneTagline(t *testing.T) {
	if strings.TrimSpace(brand.Tagline) == "" {
		t.Error("the tagline is empty, so both screens will show a blank line under the name")
	}
}

// The animation is not driven from anywhere yet, so these are the only thing standing between the
// frames and a wiring commit that discovers they were wrong all along.
func TestEveryAnimationFrameIsTheSameSizeAsTheMark(t *testing.T) {
	still := brand.Lines()

	for step := range brand.Frames {
		frame := brand.Frame(step)
		if len(frame) != len(still) {
			t.Errorf("frame %d has %d rows and the mark has %d", step, len(frame), len(still))
			continue
		}
		for i, line := range frame {
			if width := len([]rune(line)); width > brand.MarkWidth {
				// A frame wider than the mark would push the corner it sits in around as it played.
				t.Errorf("frame %d row %d is %d columns, over the declared %d: %q",
					step, i, width, brand.MarkWidth, line)
			}
			if trimmed := strings.TrimRight(line, " "); trimmed != line {
				t.Errorf("frame %d row %d carries trailing spaces: %q", step, i, line)
			}
		}
	}
}

// The still mark and the first frame are the same picture.
//
// The corner starts its ticker at step zero, so anything else means the logo shifts the instant the
// animation begins, which is exactly what it did: the fire was overlaid one column left of where the
// mark draws it, so the campfire slid sideways under a tent that stayed where it was. Nothing caught
// it, because the test below pins the columns left of the fire and the fire is not one of them.
//
// Keeping the two identical is also what stops a drawn mark and an animated mark drifting into two
// different logos, since there is now only one of them to maintain.
func TestTheFirstFrameIsTheStillMark(t *testing.T) {
	still := strings.Join(brand.Lines(), "\n")
	if first := strings.Join(brand.Frame(0), "\n"); first != still {
		t.Errorf("frame 0 is not the mark, so the logo moves on the first tick:\nmark\n%s\n\nframe 0\n%s",
			still, first)
	}
}

// The tent is the fixed part. If the animation moved it, the logo would jitter in the corner of a
// screen somebody is trying to think on, which is the opposite of what it is there for.
//
// Pinned by column rather than by row, which is the correction this test needed. The obvious
// version asserts that the top rows never change, on the assumption that the tent is up there and
// the fire is down at the bottom. It is not: the smoke rises past the peak, so rows 0 to 3 hold
// tent and smoke at once and every one of them changes every frame. The invariant that actually
// holds, and the one worth having, is that nothing left of the fire ever moves.
func TestTheTentDoesNotMoveBetweenFrames(t *testing.T) {
	// Everything the animation is allowed to touch starts here.
	const firstMovingColumn = 24

	fixed := func(line string) string {
		runes := []rune(line)
		if len(runes) > firstMovingColumn {
			runes = runes[:firstMovingColumn]
		}
		return strings.TrimRight(string(runes), " ")
	}

	first := brand.Frame(0)
	for step := 1; step < brand.Frames; step++ {
		frame := brand.Frame(step)
		for i := range first {
			if i >= len(frame) {
				break
			}
			if fixed(frame[i]) != fixed(first[i]) {
				t.Errorf("the tent moved on row %d between frame 0 and frame %d:\n  %q\n  %q",
					i, step, fixed(first[i]), fixed(frame[i]))
			}
		}
	}
}

// The flame is drawn in a colour of its own, so something has to say where it is. If the region
// drifts off the fire, the caller colours part of the tent instead and the mark looks like a
// rendering fault rather than like a campsite.
//
// Checked against every frame, because the region is one rectangle and the fire is three pictures:
// a region that fitted only the frame it was measured from would be wrong two thirds of the time.
//
// Scoped to the flame's own rows, which is a correction. The first version of this asserted that
// nothing anywhere else in the mark changed inside the fire's columns, and that is simply false: the
// smoke drifts through the same columns four rows higher. Sharing columns with the smoke is fine,
// because the smoke is drawn in the mark's own colour like everything else.
func TestTheFireRegionCoversTheFlameAndNothingElse(t *testing.T) {
	row, column, height, width := brand.FireRegion()

	still := brand.Lines()
	if row < 0 || row+height > len(still) {
		t.Fatalf("the fire region is rows %d to %d of a mark %d rows tall", row, row+height, len(still))
	}
	// The ground line is not fire. Including it would run the flame's colour along the whole width
	// of the mark, which is the one row that is definitely the campsite and not the campfire.
	if row+height > len(still)-1 {
		t.Errorf("the fire region reaches row %d, which is the ground line", len(still)-1)
	}

	var moves bool
	for step := range brand.Frames {
		frame := brand.Frame(step)
		for i := row; i < row+height; i++ {
			// Everything on the flame's rows that is outside the region has to be the same in every
			// frame as it is in the still mark. If it is not, the region is in the wrong place or too
			// narrow, and a caller would colour part of the flame and leave the rest behind.
			if before(still[i], column) != before(frame[i], column) {
				t.Errorf("frame %d moves something on row %d to the left of the fire region", step, i)
			}
			if after(still[i], column+width) != after(frame[i], column+width) {
				t.Errorf("frame %d moves something on row %d to the right of the fire region", step, i)
			}
			if window(still[i], column, width) != window(frame[i], column, width) {
				moves = true
			}
		}
	}
	if !moves {
		t.Error("nothing inside the fire region ever changes between frames, so it is not on the fire")
	}
}

// window is the runes a row shows across a span of columns, padded when the row stops short.
func window(line string, column, width int) string {
	runes := []rune(line)
	out := make([]rune, 0, width)
	for i := column; i < column+width; i++ {
		if i < len(runes) {
			out = append(out, runes[i])
			continue
		}
		out = append(out, ' ')
	}
	return string(out)
}

func before(line string, column int) string {
	runes := []rune(line)
	if len(runes) > column {
		runes = runes[:column]
	}
	return strings.TrimRight(string(runes), " ")
}

func after(line string, column int) string {
	runes := []rune(line)
	if len(runes) <= column {
		return ""
	}
	return strings.TrimRight(string(runes[column:]), " ")
}

// A wisp that returns to where it was reads as a loop rather than as smoke.
func TestNoTwoFramesAreIdentical(t *testing.T) {
	seen := map[string]int{}
	for step := range brand.Frames {
		key := strings.Join(brand.Frame(step), "\n")
		if other, repeat := seen[key]; repeat {
			t.Errorf("frames %d and %d are the same picture", other, step)
		}
		seen[key] = step
	}
}

// The caller is a ticker whose count only goes up, so making every call site remember a modulus is
// how one of them forgets.
func TestAStepBeyondTheLastFrameWraps(t *testing.T) {
	for _, step := range []int{brand.Frames, brand.Frames * 4, -1, -brand.Frames - 1} {
		if got := brand.Frame(step); len(got) == 0 {
			t.Errorf("step %d produced nothing", step)
		}
	}
	if strings.Join(brand.Frame(brand.Frames), "\n") != strings.Join(brand.Frame(0), "\n") {
		t.Error("the animation does not return to its first frame after a full cycle")
	}
}
