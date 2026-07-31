package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Terminal width is measured in cells, not runes. A CJK model label or emoji may occupy two cells;
// clipping by rune count lets the picker cross its frame even though ASCII-only tests still pass.
func TestModelPickerClipKeepsWideTextInsideTheFrame(t *testing.T) {
	for _, tc := range []struct {
		name  string
		text  string
		width int
	}{
		{name: "CJK", text: "模型模型模型", width: 6},
		{name: "emoji", text: "model-🧠-extended", width: 9},
		{name: "combining", text: "mode\u0301l-extended", width: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := clip(tc.text, tc.width)
			if width := lipgloss.Width(got); width > tc.width {
				t.Fatalf("clip(%q, %d) is %d cells wide: %q", tc.text, tc.width, width, got)
			}
			if !strings.HasSuffix(got, "…") {
				t.Errorf("clipped text does not mark what was removed: %q", got)
			}
		})
	}
}

func TestModelPickerClipLeavesTextThatFitsAlone(t *testing.T) {
	const text = "模型"
	if got := clip(text, 4); got != text {
		t.Errorf("clip changed text that fits: %q", got)
	}
}
