package agents

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Measuring and padding styled text.
//
// Every one of these uses lipgloss.Width rather than len, because styled text carries escape
// sequences whose byte length is not their display width. Getting that wrong pushes a pane past its
// column and the split layout tears, which reads as the program being broken rather than as a long
// line.

// pad extends styled text to a display width.
func pad(s string, width int) string {
	if gap := width - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// truncate shortens styled text to a display width, marking that it did.
//
// The marker matters: a title cut without one reads as the whole title, and somebody comparing two
// agents by their titles would be comparing two prefixes without knowing it.
func truncate(s string, width int) string {
	if width < 4 {
		width = 4
	}
	s = strings.ReplaceAll(s, "\n", " ")
	if lipgloss.Width(s) <= width {
		return s
	}

	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+3 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "..."
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}
