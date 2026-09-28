// Package paste makes pasted text safe to put in a text field: what a terminal sends in a bracketed
// paste is whatever was on the clipboard, escape sequences and carriage returns included.
package paste

import "strings"

// tabWidth is how many spaces a pasted tab becomes. A tab kept as a tab is counted as one cell and
// drawn as however many the terminal likes, so the caret and the text drift apart.
const tabWidth = 4

// Lines is text for a field that holds several lines: line breaks in any convention become newlines,
// tabs become spaces, and every other control character is dropped.
func Lines(text string) string {
	text = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\t", strings.Repeat(" ", tabWidth)).Replace(text)
	return strings.Map(func(r rune) rune {
		if r == '\n' || printable(r) {
			return r
		}
		return -1
	}, text)
}

// Line is text for a one-line field: Lines with the line breaks removed and the ends trimmed, since
// a newline copied with a name or a key is not part of it.
func Line(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(Lines(text), "\n", ""))
}

func printable(r rune) bool {
	return r >= 0x20 && r != 0x7f && (r < 0x80 || r > 0x9f)
}
