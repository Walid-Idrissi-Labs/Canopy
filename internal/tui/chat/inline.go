package chat

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// Emphasis, without the asterisks.
//
// The renderer used to keep every marker in the output: `**bold**` rendered as `**bold**` in bold,
// and the reason was a good one. Strip every escape code from a line and the structure is still
// there, which is what makes a reply readable when it is copied out, searched, or shown on a
// terminal that does nothing with styling.
//
// That reason holds for structure and not for emphasis. A heading, a list item and a quote all mean
// something a stripped line needs to keep, and they keep a plain-text mark here for exactly that:
// a bullet, a gutter bar, a rule. A pair of asterisks around a word means "read this harder", and a
// reader who has lost the styling has lost nothing by also losing the asterisks. Meanwhile every
// competitor hides them, and a reply full of visible `**` is the loudest way this interface said it
// was not one.
//
// Removing the markers moves where the text has to be wrapped, which is why this file exists rather
// than being four lines inside renderInline. The rule at the top of markdown.go still stands and is
// now enforced by construction: the text is split into styled runs first, the runs are wrapped by
// their own measured width, and styling is applied last, per line. There is no point at which a
// wrap position is computed against a string containing escape codes.

// inline is a run of text carrying one set of emphases.
type inline struct {
	text   string
	bold   bool
	italic bool
	code   bool
	strike bool

	// link is the target of a [text](url) span. The text stays in text, and the target is rendered
	// after it rather than hidden behind it: a terminal hyperlink is invisible to anybody whose
	// terminal does not draw one, and a link whose destination cannot be seen or copied is worse
	// than a slightly longer line.
	link string
}

// parseInline splits a line of markdown into styled runs with the markers removed.
//
// Per line, and tolerant by design. A span whose closing marker is not on this line is not a span:
// its opening marker stays as literal text, which is the same graceful fallback the renderer has
// always had for an unclosed marker anywhere in a reply. A model writing about `a * b` gets a
// multiplication sign, not an italic.
func parseInline(s string) []inline {
	runes := []rune(s)
	var runs []inline
	var plain strings.Builder

	flush := func() {
		if plain.Len() > 0 {
			runs = append(runs, inline{text: plain.String()})
			plain.Reset()
		}
	}
	emit := func(run inline) {
		flush()
		runs = append(runs, run)
	}

	i := 0
	for i < len(runes) {
		// Code first, and unconditionally. Backticks win over every other marker because their
		// whole purpose is to quote text that must not be interpreted, and an asterisk inside them
		// is a character in somebody's code rather than an emphasis they forgot to close.
		if runes[i] == '`' {
			if end := indexRune(runes, i+1, '`'); end >= 0 {
				emit(inline{text: string(runes[i+1 : end]), code: true})
				i = end + 1
				continue
			}
		}

		if text, target, next, ok := matchLink(runes, i); ok {
			emit(inline{text: text, link: target})
			i = next
			continue
		}

		if text, next, ok := matchDelimited(runes, i, "~~"); ok {
			emit(inline{text: text, strike: true})
			i = next
			continue
		}

		// The two-character markers are tried before the one-character ones, or `**bold**` opens an
		// italic on its first asterisk and closes it on its second, leaving the word bare.
		if text, next, ok := matchDelimited(runes, i, "**"); ok {
			emit(inline{text: text, bold: true})
			i = next
			continue
		}
		if text, next, ok := matchDelimited(runes, i, "__"); ok {
			emit(inline{text: text, bold: true})
			i = next
			continue
		}
		if text, next, ok := matchDelimited(runes, i, "*"); ok {
			emit(inline{text: text, italic: true})
			i = next
			continue
		}
		// Underscores are only a marker at a word boundary. snake_case_names are far more common in
		// a coding agent's output than underscore emphasis, and italicising the middle of one is a
		// worse error than missing an italic.
		if text, next, ok := matchDelimited(runes, i, "_"); ok && wordBoundary(runes, i-1) {
			emit(inline{text: text, italic: true})
			i = next
			continue
		}

		plain.WriteRune(runes[i])
		i++
	}
	flush()
	return runs
}

// matchDelimited matches a span opening at pos with the given marker, and reports where it ends.
//
// The flanking rules are the ones the renderer already used: a marker has to be followed by
// something that is not a space to open, and preceded by something that is not a space to close, so
// `2 * 3 * 4` is arithmetic and `a ** b` is not bold.
func matchDelimited(runes []rune, pos int, marker string) (string, int, bool) {
	m := []rune(marker)
	if !hasAt(runes, pos, m) || !flanking(runes, pos+len(m)) {
		return "", 0, false
	}
	for end := pos + len(m); end+len(m) <= len(runes); end++ {
		if !hasAt(runes, end, m) {
			continue
		}
		if !flanking(runes, end-1) {
			continue
		}
		if end == pos+len(m) {
			// Empty span, as in `****`. Not emphasis of anything.
			return "", 0, false
		}
		return string(runes[pos+len(m) : end]), end + len(m), true
	}
	return "", 0, false
}

// matchLink matches [text](target) and reports the pieces.
func matchLink(runes []rune, pos int) (text, target string, next int, ok bool) {
	if pos >= len(runes) || runes[pos] != '[' {
		return "", "", 0, false
	}
	shut := indexRune(runes, pos+1, ']')
	if shut < 0 || shut+1 >= len(runes) || runes[shut+1] != '(' {
		return "", "", 0, false
	}
	end := indexRune(runes, shut+2, ')')
	if end < 0 {
		return "", "", 0, false
	}
	text = string(runes[pos+1 : shut])
	target = strings.TrimSpace(string(runes[shut+2 : end]))
	if text == "" || target == "" || strings.ContainsAny(target, " \t") {
		return "", "", 0, false
	}
	return text, target, end + 1, true
}

func hasAt(runes []rune, pos int, marker []rune) bool {
	if pos+len(marker) > len(runes) {
		return false
	}
	for i, r := range marker {
		if runes[pos+i] != r {
			return false
		}
	}
	return true
}

// wordBoundary reports whether the position is outside a word, which is what makes an underscore a
// marker rather than part of an identifier.
func wordBoundary(runes []rune, pos int) bool {
	if pos < 0 || pos >= len(runes) {
		return true
	}
	r := runes[pos]
	return r == ' ' || r == '\t' || r == '(' || r == '[' || r == '{' || r == '"' || r == '\''
}

// visible is the text a run puts on screen, which is what has to be measured for wrapping.
func (r inline) visible() string {
	if r.link != "" && r.link != r.text {
		return r.text + " (" + r.link + ")"
	}
	return r.text
}

// wrapInline fills lines of at most width cells from styled runs.
//
// Greedy, on word boundaries, measuring cells rather than runes. The word is the unit rather than
// the run, because a bold phrase of six words has to be allowed to break between them like any other
// text; the style rides along with each piece.
func wrapInline(runs []inline, width int) [][]inline {
	if width < 1 {
		width = 1
	}

	type token struct {
		run   inline
		space bool
	}
	var tokens []token
	for _, run := range runs {
		if run.code || run.link != "" {
			// Kept whole. Breaking inside a code span or a URL produces two things that each look
			// like a shorter version of themselves, which is how a copied command silently becomes
			// the wrong command.
			tokens = append(tokens, token{run: run})
			continue
		}
		parts := splitKeepingSpaces(run.text)
		for _, part := range parts {
			piece := run
			piece.text = part
			tokens = append(tokens, token{run: piece, space: strings.TrimSpace(part) == ""})
		}
	}

	var lines [][]inline
	var current []inline
	used := 0

	flush := func() {
		if len(current) > 0 {
			lines = append(lines, current)
			current = nil
		}
		used = 0
	}

	for _, tok := range tokens {
		w := lipgloss.Width(tok.run.visible())
		if used+w > width && used > 0 {
			flush()
			if tok.space {
				// A space that fell at a break is the break. Carrying it to the next line would
				// indent it by one column for no reason.
				continue
			}
		}
		// A single token wider than the line is broken by cell, which is the only case where the
		// text itself has to be cut rather than moved.
		for lipgloss.Width(tok.run.visible()) > width {
			head, tail := cutCells(tok.run.visible(), width-used)
			if head == "" {
				// Only reachable when this line has no room left at all, since cutCells always
				// takes a character when it has any budget. Breaking rather than continuing, so
				// that if that guarantee ever changes this becomes a layout fault and not a
				// hang: the loop below cannot be allowed to depend on it silently.
				if used == 0 {
					break
				}
				flush()
				continue
			}
			piece := tok.run
			piece.text, piece.link = head, ""
			current = append(current, piece)
			flush()
			tok.run.text, tok.run.link = tail, ""
		}
		if tok.run.text == "" {
			continue
		}
		current = append(current, tok.run)
		used += lipgloss.Width(tok.run.visible())
	}
	flush()

	if len(lines) == 0 {
		lines = append(lines, nil)
	}
	return lines
}

// cutCells splits a string at a cell budget, which is the measurement a terminal actually uses.
//
// By cell and not by rune. A rune count against a cell budget is the bug that lets a line of
// full-width text render at twice the width of the terminal and wrap the whole frame.
//
// **With any budget at all, this always takes at least one character.** That is the property both
// hard-break loops depend on to terminate, and leaving it out froze the interface. A budget of one
// cell against a two cell character, a CJK ideograph or an emoji, used to return nothing: the caller
// would start a fresh line, ask again, get nothing again, and spin forever inside View with the
// screen stopped and ctrl+c unanswered. It was reachable from ordinary model output, a checkmark in
// an indented list or a table with enough columns, on an eighty column terminal.
//
// So a character wider than the whole budget is emitted anyway and overflows it. One column of
// overflow on a line too narrow to hold one character is a cosmetic fault on a display nobody can
// read regardless. Not returning is not a fault, it is the end of the session.
func cutCells(s string, budget int) (head, tail string) {
	if budget <= 0 || s == "" {
		return "", s
	}
	used := 0
	for i, r := range s {
		w := lipgloss.Width(string(r))
		if used+w > budget {
			if i == 0 {
				// Nothing fits, not even this one character. Take it regardless, so the caller
				// advances.
				_, size := utf8.DecodeRuneInString(s)
				return s[:size], s[size:]
			}
			return s[:i], s[i:]
		}
		used += w
	}
	return s, ""
}

// renderRuns styles one wrapped line.
func renderRuns(line []inline, base lipgloss.Style) string {
	t := theme.Current()
	var out strings.Builder
	for _, run := range line {
		switch {
		case run.code:
			out.WriteString(t.InlineCode.Render(run.text))
		case run.link != "":
			out.WriteString(base.Underline(true).Render(run.text))
			if run.link != run.text {
				out.WriteString(t.Muted.Render(" (" + run.link + ")"))
			}
		default:
			style := base
			if run.bold {
				style = style.Bold(true)
			}
			if run.italic {
				style = style.Italic(true)
			}
			if run.strike {
				style = style.Strikethrough(true)
			}
			out.WriteString(style.Render(run.text))
		}
	}
	return out.String()
}

// renderInlineText is the whole path for one block of prose: parse, wrap, style.
func renderInlineText(text string, width int, base lipgloss.Style) []string {
	var out []string
	for _, line := range wrapInline(parseInline(text), width) {
		out = append(out, renderRuns(line, base))
	}
	return out
}
