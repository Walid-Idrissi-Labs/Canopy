package chat

import (
	"fmt"
	"strings"
)

// terminalSafe turns terminal control characters in untrusted content into visible text.
//
// Tool output and file content are data, even when they contain bytes a terminal would interpret
// as commands. ESC can clear the screen, rewrite earlier lines, set a window title, or place
// attacker-chosen text in the clipboard through OSC 52; carriage return and backspace can make the
// line somebody sees differ from the line Canopy received. Newline and tab are the two controls the
// renderer intentionally understands, so they keep their layout meaning. Every other C0, DEL, and
// 8-bit C1 control is escaped before any Canopy-generated ANSI styling is added.
func terminalSafe(s string) string {
	var out strings.Builder
	out.Grow(len(s))

	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			out.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			_, _ = fmt.Fprintf(&out, `\x%02x`, r)
		case r >= 0x80 && r <= 0x9f:
			_, _ = fmt.Fprintf(&out, `\u%04x`, r)
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
}
