package chat

import (
	"strings"
	"testing"
)

func TestTerminalSafeMakesEveryInterpretableControlVisible(t *testing.T) {
	input := "before\x00\x07\b\r\x1b[2J\x7f\u0085after\nnext\tcolumn"
	got := terminalSafe(input)

	for _, want := range []string{
		`before\x00\x07\x08\x0d\x1b[2J\x7f\u0085after`,
		"next\tcolumn",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("terminalSafe(%q) = %q, missing %q", input, got, want)
		}
	}

	for _, r := range got {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("terminalSafe left control U+%04X in %q", r, got)
		}
	}
}

func TestTerminalSafeDoesNotChangeOrdinaryUnicode(t *testing.T) {
	const input = "مرحبا — café — 日本語\n\tindented"
	if got := terminalSafe(input); got != input {
		t.Errorf("ordinary text changed from %q to %q", input, got)
	}
}
