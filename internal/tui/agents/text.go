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

// padPlain is pad, for the left column of a split where the gap has to be exact.
func padPlain(s string, width int) string { return pad(s, width) }

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

// wrapPlain breaks unstyled text into lines of at most width.
func wrapPlain(s string, width int) []string {
	if width < 4 {
		width = 4
	}

	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		if paragraph == "" {
			out = append(out, "")
			continue
		}
		var line strings.Builder
		for _, word := range strings.Fields(paragraph) {
			switch {
			case line.Len() == 0:
				line.WriteString(word)
			case lipgloss.Width(line.String())+1+lipgloss.Width(word) <= width:
				line.WriteString(" " + word)
			default:
				out = append(out, line.String())
				line.Reset()
				line.WriteString(word)
			}
		}
		if line.Len() > 0 {
			out = append(out, line.String())
		}
	}
	return out
}

// tail returns the last n lines of a string.
//
// The end rather than the beginning, because what an agent is doing now is at the bottom of its
// reply and the top is what it was saying half a minute ago.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
