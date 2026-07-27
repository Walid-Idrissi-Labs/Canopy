package brand_test

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
)

// M-05. An asymmetric silhouette reads as a rendering fault rather than as a drawing, and the first
// thing a new user sees is not the place to look broken.
func TestTheMarkIsSymmetricOnEveryRow(t *testing.T) {
	for i, line := range brand.Lines() {
		runes := []rune(line)
		trimmed := strings.TrimRight(line, " ")
		if trimmed != line {
			t.Errorf("row %d carries trailing spaces, which move its centre: %q", i, line)
		}

		left := len(runes) - len([]rune(strings.TrimLeft(line, " ")))
		right := brand.MarkWidth - len(runes)
		if right < 0 {
			t.Fatalf("row %d is %d columns, wider than the declared %d: %q",
				i, len(runes), brand.MarkWidth, line)
		}
		if left != right {
			t.Errorf("row %d sits %d in from the left and %d from the right: %q",
				i, left, right, line)
		}
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
