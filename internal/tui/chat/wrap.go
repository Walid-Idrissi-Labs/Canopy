package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Text wrapping, done here rather than by lipgloss, because two things need it and neither is a
// plain paragraph: the transcript wraps text that already carries styling, and the input wraps text
// with a cursor rendered inside it. Both need to know where the breaks landed, which a function
// that returns a single string cannot tell them.

// wrap breaks a string into display lines no wider than width.
//
// Breaks at spaces where it can and mid word where it cannot, because a word longer than the
// terminal is usually a path or a URL, and refusing to break it would push the layout wider than
// the window rather than being helpfully unbroken.
func wrap(s string, width int) []string {
	if width < 1 {
		width = 1
	}

	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		out = append(out, wrapLine(paragraph, width)...)
	}
	return out
}

func wrapLine(line string, width int) []string {
	if lipgloss.Width(line) <= width {
		return []string{line}
	}

	var out []string
	var current strings.Builder
	var currentWidth int

	flush := func() {
		out = append(out, current.String())
		current.Reset()
		currentWidth = 0
	}

	for _, word := range splitKeepingSpaces(line) {
		wordWidth := lipgloss.Width(word)

		// A trailing space that would push past the edge is dropped rather than wrapped, since a
		// line ending in whitespace looks like a stray indent on the next one.
		if strings.TrimSpace(word) == "" && currentWidth+wordWidth > width {
			continue
		}

		if currentWidth+wordWidth > width && currentWidth > 0 {
			flush()
			if strings.TrimSpace(word) == "" {
				continue
			}
		}

		// A single word wider than the line has to be broken, or it would run past the edge.
		//
		// Broken by cell and not by rune. The budget here is a count of terminal columns, and a rune
		// is not a column: a CJK character occupies two. Cutting a rune count against a cell budget
		// is how a line of full-width text with no spaces in it, which is an ordinary sentence in
		// Japanese or Chinese, came out at twice the width of the terminal and wrapped the frame.
		for lipgloss.Width(word) > width {
			take := width - currentWidth
			if take <= 0 {
				flush()
				take = width
			}
			head, tail := cutCells(word, take)
			if head == "" {
				// Nothing fits in what is left of this line, not even one cell of it. Start a fresh
				// line rather than spinning here writing nothing.
				flush()
				continue
			}
			current.WriteString(head)
			word = tail
			flush()
		}

		current.WriteString(word)
		currentWidth += lipgloss.Width(word)
	}

	if current.Len() > 0 || len(out) == 0 {
		out = append(out, current.String())
	}
	return out
}

// splitKeepingSpaces breaks a line into words and the runs of spaces between them, so wrapping can
// drop a break's whitespace without losing the spacing inside a line.
func splitKeepingSpaces(line string) []string {
	var out []string
	var current strings.Builder
	var inSpace bool

	for i, r := range line {
		isSpace := r == ' ' || r == '\t'
		if i > 0 && isSpace != inSpace {
			out = append(out, current.String())
			current.Reset()
		}
		inSpace = isSpace
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

// wrapWithMarkers wraps three pieces as one string while keeping them distinguishable.
//
// The middle piece is a styled cursor cell whose display width is one but whose byte length is not,
// so wrapping the concatenation directly would push every line short by the length of the escape
// codes. Wrapping the plain text and splicing the marker back in at the recorded position is what
// keeps the box the width it says it is.
func wrapWithMarkers(before, marker, after string, width int) []string {
	// The marker occupies one cell, so a single placeholder rune stands in for it during wrapping
	// and is replaced afterwards. Chosen from a private use area precisely because it cannot appear
	// in anything a user types.
	const placeholder = ''

	plain := before + string(placeholder) + after
	lines := wrap(plain, width)

	for i, line := range lines {
		if strings.ContainsRune(line, placeholder) {
			lines[i] = strings.Replace(line, string(placeholder), marker, 1)
		}
	}
	return lines
}
