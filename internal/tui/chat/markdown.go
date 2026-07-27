package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// Markdown rendering for a model's reply.
//
// A reply is read once and shown many times: scrolled past, searched, resumed after a restart. It
// has to survive all of that as plain, readable text, which is why every structural marker here is
// kept literally in the output (the "#", the "- ", the "> ", the "**") rather than replaced by
// colour alone. Strip every escape code from a rendered line and the structure is still there.
//
// This also settles the one real safety question in the file: styling is always applied to text
// that has already been wrapped to width, never the other way round. wrapLine's hard break path
// cuts a too-long word by rune position, and a rune position computed against a plain string is not
// the same position once ANSI codes are spliced in around part of it. Style first, wrap second would
// let a cut land inside an escape sequence and corrupt everything after it on that line. wrap.go's
// own wrapWithMarkers works around exactly this for the input cursor, and the same reasoning applies
// here: wrap the plain text, then colour the pieces of the line that resulted.

// RenderMarkdown turns a model's markdown reply into display lines of at most width columns.
func RenderMarkdown(s string, width int) []string {
	// A code block's fence and its indent need somewhere to live. Below that, a block that fits
	// nothing useful is worse than one that is merely cramped, so this is a floor rather than a
	// suggestion.
	if width < 4 {
		width = 4
	}

	var out []string
	lines := strings.Split(s, "\n")
	i := 0
	for i < len(lines) {
		line := lines[i]

		switch {
		case isFence(line):
			lang, code, consumed := extractFence(lines[i:])
			out = append(out, renderCodeBlock(lang, code, width)...)
			i += consumed

		case strings.TrimSpace(line) == "":
			out = append(out, "")
			i++

		case headingLevel(line) > 0:
			out = append(out, renderHeading(line, width)...)
			i++

		case isQuoteLine(line):
			block, consumed := collectWhile(lines[i:], isQuoteLine)
			out = append(out, renderQuote(block, width)...)
			i += consumed

		case listMarker(line) != nil:
			item, consumed := collectListItem(lines[i:])
			out = append(out, renderListItem(item, width)...)
			i += consumed

		default:
			block, consumed := collectWhile(lines[i:], isParagraphLine)
			out = append(out, renderParagraph(strings.Join(block, " "), width, theme.Current().Body)...)
			i += consumed
		}
	}
	return out
}

// collectWhile gathers the leading run of lines matching pred, always taking at least one line so
// callers never loop without making progress.
func collectWhile(lines []string, pred func(string) bool) ([]string, int) {
	n := 1
	for n < len(lines) && pred(lines[n]) {
		n++
	}
	return lines[:n], n
}

// isParagraphLine says whether a line continues a paragraph rather than starting some other block.
//
// A blank line always ends a paragraph. So does anything that looks like the start of a different
// block, because a model's reply routinely follows a sentence directly with a heading or a list, and
// a paragraph that swallowed them would render the heading as plain, wrapped prose.
func isParagraphLine(line string) bool {
	if strings.TrimSpace(line) == "" {
		return false
	}
	return !isFence(line) && headingLevel(line) == 0 && !isQuoteLine(line) && listMarker(line) == nil
}

// renderParagraph reflows a block of source lines into wrapped display lines.
//
// Reflowed rather than wrapped as written, because a model wraps its own output at whatever column
// it felt like, and preserving those breaks would wrap the reply twice, at two different widths, and
// produce a ragged paragraph that has nothing to do with the terminal it is actually shown in.
func renderParagraph(text string, width int, base lipgloss.Style) []string {
	var out []string
	for _, line := range wrap(text, width) {
		out = append(out, renderInline(line, base))
	}
	return out
}

// headingLevel reports 1, 2 or 3 for a line starting with that many "#" characters followed by a
// space, and 0 for anything else. Four or more is left as a paragraph: the vocabulary this project
// asked for stops at h3, and a stray "####" reads more like emphasis than structure.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n < 1 || n > 3 {
		return 0
	}
	rest := line[n:]
	if rest == "" {
		return n
	}
	if rest[0] != ' ' {
		return 0
	}
	return n
}

func renderHeading(line string, width int) []string {
	t := theme.Current()
	style := t.Heading
	if headingLevel(line) == 1 {
		style = t.Title
	}

	var out []string
	// The hashes stay in the text. They are what makes a heading still read as a heading once every
	// escape code has been stripped, which is the only thing colour can be trusted to survive.
	for _, wrapped := range wrap(line, width) {
		out = append(out, style.Render(wrapped))
	}
	return out
}

// isQuoteLine reports whether a line opens with a block quote marker, allowing up to three leading
// spaces, which is the same leeway CommonMark gives one.
func isQuoteLine(line string) bool {
	return quoteContent(line) != ""
}

// quoteContent strips a block quote marker and returns what is left. A genuinely empty quoted line
// is reported as a single space rather than "", so its one caller can tell it apart from "no marker
// here" without a second return value.
func quoteContent(line string) string {
	spaces := 0
	for spaces < len(line) && spaces < 3 && line[spaces] == ' ' {
		spaces++
	}
	rest := line[spaces:]
	if !strings.HasPrefix(rest, ">") {
		return ""
	}
	rest = strings.TrimPrefix(rest[1:], " ")
	if rest == "" {
		return " "
	}
	return rest
}

func renderQuote(lines []string, width int) []string {
	t := theme.Current()
	const prefix = "> "
	inner := width - lipgloss.Width(prefix)
	if inner < 1 {
		inner = 1
	}

	var text []string
	for _, line := range lines {
		if content := quoteContent(line); content != " " {
			text = append(text, content)
		} else {
			text = append(text, "")
		}
	}

	var out []string
	for _, wrapped := range wrap(strings.Join(text, " "), inner) {
		out = append(out, t.Muted.Render(prefix)+renderInline(wrapped, t.Muted))
	}
	if len(out) == 0 {
		out = append(out, t.Muted.Render(strings.TrimRight(prefix, " ")))
	}
	return out
}

// listItem is one bullet or numbered entry, already stripped of its marker.
type listItem struct {
	marker string // rendered marker, e.g. "- " or "12. "
	indent int    // leading spaces the source line carried, so a nested list steps in further
	text   string
}

// listMarker recognises a bullet ("-", "*", "+") or an ordered marker ("1.", "1)") at the start of a
// line, after up to three leading spaces of nesting, and reports nil for anything else.
func listMarker(line string) *listItem {
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	rest := line[spaces:]
	if rest == "" {
		return nil
	}

	if (rest[0] == '-' || rest[0] == '*' || rest[0] == '+') && startsWithMarkerSpace(rest[1:]) {
		return &listItem{marker: "- ", indent: spaces, text: strings.TrimPrefix(rest[1:], " ")}
	}

	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 || digits >= len(rest) {
		return nil
	}
	if (rest[digits] == '.' || rest[digits] == ')') && startsWithMarkerSpace(rest[digits+1:]) {
		return &listItem{
			marker: rest[:digits] + ". ",
			indent: spaces,
			text:   strings.TrimPrefix(rest[digits+1:], " "),
		}
	}
	return nil
}

// startsWithMarkerSpace requires a marker to be followed by a space or nothing, so "3.14" in the
// middle of a sentence is never mistaken for an ordered list starting at 3.
func startsWithMarkerSpace(rest string) bool {
	return rest == "" || rest[0] == ' '
}

// collectListItem gathers one item's opening line plus any lazily indented continuation lines,
// which is what lets a long list entry wrap onto more than one source line and still be read back
// as one item rather than as a new, unmarked one.
func collectListItem(lines []string) (*listItem, int) {
	item := listMarker(lines[0])
	contentColumn := item.indent + lipgloss.Width(item.marker)

	var continuation []string
	n := 1
	for n < len(lines) {
		line := lines[n]
		if strings.TrimSpace(line) == "" || listMarker(line) != nil || isFence(line) ||
			headingLevel(line) > 0 || isQuoteLine(line) {
			break
		}
		leading := 0
		for leading < len(line) && line[leading] == ' ' {
			leading++
		}
		if leading < contentColumn {
			break
		}
		continuation = append(continuation, strings.TrimSpace(line))
		n++
	}

	if len(continuation) > 0 {
		item.text = strings.Join(append([]string{item.text}, continuation...), " ")
	}
	return item, n
}

func renderListItem(item *listItem, width int) []string {
	t := theme.Current()
	markerWidth := lipgloss.Width(item.marker)
	hang := item.indent + markerWidth
	inner := width - hang
	if inner < 1 {
		inner = 1
	}

	wrapped := wrap(item.text, inner)
	indentSpaces := strings.Repeat(" ", item.indent)

	var out []string
	for i, line := range wrapped {
		body := renderInline(line, t.Body)
		if i == 0 {
			out = append(out, indentSpaces+t.Key.Render(item.marker)+body)
			continue
		}
		// The hanging indent is what keeps wrapped text lined up under the words above it rather
		// than under the bullet, which is the difference between a list and a ragged left margin.
		out = append(out, indentSpaces+strings.Repeat(" ", markerWidth)+body)
	}
	return out
}

// renderInline finds bold, italic and inline code spans in an already-wrapped plain-text line and
// styles them, leaving every marker character in place in the output.
//
// Run after wrapping rather than before, for the reason at the top of this file: a style applied
// before wrapping can end up split by a hard break landing inside its own escape codes. Run per
// line instead, a span that straddles a wrap point simply fails to find its closing marker on that
// line and falls back to literal text, the same graceful behaviour an unclosed marker gets anywhere
// else in a reply.
func renderInline(line string, base lipgloss.Style) string {
	t := theme.Current()
	runes := []rune(line)
	var out strings.Builder
	var plain strings.Builder

	flush := func() {
		if plain.Len() > 0 {
			out.WriteString(base.Render(plain.String()))
			plain.Reset()
		}
	}

	i := 0
	for i < len(runes) {
		if runes[i] == '`' {
			if end := indexRune(runes, i+1, '`'); end >= 0 {
				flush()
				out.WriteString(t.InlineCode.Render(string(runes[i : end+1])))
				i = end + 1
				continue
			}
		}

		if i+1 < len(runes) && runes[i] == '*' && runes[i+1] == '*' && flanking(runes, i+2) {
			if end := indexPair(runes, i+2, '*', '*'); end >= 0 && flanking(runes, end-1) {
				flush()
				out.WriteString(base.Bold(true).Render(string(runes[i : end+2])))
				i = end + 2
				continue
			}
		}

		if runes[i] == '*' && flanking(runes, i+1) {
			if end := indexSingleStar(runes, i+1); end >= 0 && flanking(runes, end-1) {
				flush()
				out.WriteString(base.Italic(true).Render(string(runes[i : end+1])))
				i = end + 1
				continue
			}
		}

		plain.WriteRune(runes[i])
		i++
	}
	flush()
	return out.String()
}

// flanking reports whether the rune at pos exists and is not a space, which is what stops
// "5 * 3 * 2" from being read as an italic span opening at the first star.
func flanking(runes []rune, pos int) bool {
	return pos >= 0 && pos < len(runes) && runes[pos] != ' '
}

func indexRune(runes []rune, from int, r rune) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == r {
			return i
		}
	}
	return -1
}

// indexPair finds the next occurrence of two runes back to back, starting at from, and returns the
// index of the first of the pair.
func indexPair(runes []rune, from int, a, b rune) int {
	for i := from; i+1 < len(runes); i++ {
		if runes[i] == a && runes[i+1] == b {
			return i
		}
	}
	return -1
}

// indexSingleStar finds the next '*' that does not belong to a "**" pair, so an italic search does
// not stop at the opening of a bold span nested inside it.
func indexSingleStar(runes []rune, from int) int {
	for i := from; i < len(runes); i++ {
		if runes[i] != '*' {
			continue
		}
		if i+1 < len(runes) && runes[i+1] == '*' {
			i++ // part of a "**" pair, not a lone star
			continue
		}
		return i
	}
	return -1
}

// isFence reports whether a line opens or closes a fenced code block.
func isFence(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

// fenceMarker returns the fence character run a line uses. A close has to match: "```go" opened
// with backticks can only be closed by backticks, per the CommonMark rule this borrows, so a stray
// "~~~" inside a backtick-fenced block is source, not a close.
func fenceMarker(line string) string {
	if strings.HasPrefix(strings.TrimLeft(line, " "), "```") {
		return "```"
	}
	return "~~~"
}

// extractFence reads one fenced block starting at lines[0], which must itself satisfy isFence, and
// reports the language tag, the code lines between the fences, and how many source lines were
// consumed including the fences themselves.
//
// An unterminated fence, a reply cut off mid code block, still has to render as a code block rather
// than as a stray line of backticks followed by raw source misread as prose. It consumes the rest
// of the input in that case instead of searching forever for a close that never arrives.
func extractFence(lines []string) (lang string, code []string, consumed int) {
	marker := fenceMarker(lines[0])
	lang = strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(lines[0], " "), marker))

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == marker {
			return lang, lines[1:i], i + 1
		}
	}
	return lang, lines[1:], len(lines)
}

// renderCodeBlock lays out a fenced block between two literal fence lines, so the block is still
// recognisable as code with every escape code stripped.
//
// A line too long to fit is wrapped rather than truncated. Truncating would silently drop source
// text, which is exactly the kind of quiet loss this project refuses to make elsewhere (see D-08 on
// bounded log buffers), and a continuation marker costs one glyph a line to avoid it.
func renderCodeBlock(lang string, code []string, width int) []string {
	t := theme.Current()

	fenceLabel := "```"
	if lang != "" {
		fenceLabel += lang
	}

	const indent = "  "
	const continuation = "↳ " // a single-width arrow, so it never shifts the column budget

	contentWidth := width - lipgloss.Width(indent)
	if contentWidth < 1 {
		contentWidth = 1
	}

	rules, highlighted := languageRules(lang)

	// Wrapped rather than assumed short: the language tag comes from whatever the model wrote after
	// the fence, and nothing stops that being longer than the terminal.
	var out []string
	for _, wrapped := range wrap(fenceLabel, width) {
		out = append(out, t.Muted.Render(wrapped))
	}
	for _, raw := range code {
		raw = expandTabs(raw)
		for j, fragment := range wrapLine(raw, contentWidth) {
			prefix := indent
			if j > 0 {
				prefix = continuation
			}
			out = append(out, prefix+highlightCode(fragment, rules, highlighted, t))
		}
	}
	out = append(out, t.Muted.Render("```"))
	return out
}

// Highlight colours one line of source in a named language.
//
// Exported for the diff review at A7-01, which needs exactly this and nothing else from the
// markdown renderer. A diff hunk is source code with a marker character in front of it, so the
// alternative was a second lexer that would drift from this one the first time a keyword list
// changed. The language is a file extension there rather than a fence tag, and normalizeLang
// already treats them the same.
//
// The caller is responsible for wrapping first. Styling has to follow wrapping and never precede
// it, or the escape sequences get counted as width.
func Highlight(lang, line string) string {
	rules, ok := languageRules(lang)
	return highlightCode(expandTabs(line), rules, ok, theme.Current())
}

// expandTabs replaces a tab with a fixed run of spaces. A raw tab has no fixed display width in a
// terminal, since it jumps to the next tab stop rather than occupying one cell, so it is expanded
// rather than measured. Gofmt output is real code this has to survive: Go source is tab indented.
func expandTabs(line string) string {
	return strings.ReplaceAll(line, "\t", "    ")
}

// langRules is enough grammar to tell a keyword from an identifier, a string from a comment, and a
// number from either, for one language. Not a parser: there is no cross-line state, so a block
// comment or a string that fails to close on the same physical fragment falls back to plain text for
// whatever is left of it rather than reading on into the next line and mis-colouring it.
type langRules struct {
	lineComment       string
	blockCommentStart string
	blockCommentEnd   string
	quotes            string // characters that open and close a string in this language
	keywords          map[string]bool
}

// languageRules maps a fence's language tag to a lexer, and reports false for anything not covered,
// which renders as plain, unhighlighted text rather than a guess.
func languageRules(lang string) (langRules, bool) {
	switch normalizeLang(lang) {
	case "go":
		return langRules{
			lineComment: "//", blockCommentStart: "/*", blockCommentEnd: "*/",
			quotes: "\"'`", keywords: goKeywords,
		}, true
	case "python":
		return langRules{lineComment: "#", quotes: "\"'", keywords: pythonKeywords}, true
	case "javascript":
		return langRules{
			lineComment: "//", blockCommentStart: "/*", blockCommentEnd: "*/",
			quotes: "\"'`", keywords: javascriptKeywords,
		}, true
	case "json":
		return langRules{quotes: "\"", keywords: jsonKeywords}, true
	case "shell":
		return langRules{lineComment: "#", quotes: "\"'", keywords: shellKeywords}, true
	}
	return langRules{}, false
}

// normalizeLang folds the common spellings and file extensions a model uses on a fence into one
// canonical name per family. TypeScript shares JavaScript's rules rather than getting its own: the
// four categories this lexer draws are the same in both, and duplicating the table would just be
// somewhere for the two to quietly drift apart.
func normalizeLang(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "go", "golang":
		return "go"
	case "python", "py", "py3":
		return "python"
	case "javascript", "js", "jsx", "mjs", "cjs", "typescript", "ts", "tsx":
		return "javascript"
	case "json", "jsonc", "json5":
		return "json"
	case "shell", "sh", "bash", "zsh", "console":
		return "shell"
	}
	return ""
}

// tokenKind is one of the four categories a hand written lexer can tell apart, plus plain for
// everything else: whitespace, punctuation and identifiers that are not one of the language's
// reserved words.
type tokenKind int

const (
	tokPlain tokenKind = iota
	tokKeyword
	tokString
	tokComment
	tokNumber
)

// codeToken is a run of source that belongs to one category. Concatenating every token's text, in
// order, reconstructs the fragment exactly, which is what lets highlightCode add colour without
// ever changing what the fragment says or how wide it is.
type codeToken struct {
	text string
	kind tokenKind
}

// lexFragment tokenises one already-wrapped fragment of source.
//
// Kept apart from rendering so the categorisation can be tested directly: a terminal that is not
// attached to a TTY, which is every terminal a test runs in, renders every style identically with no
// colour at all, so a test that only looked at rendered output could not tell a keyword from an
// identifier apart even if the lexer had them backwards.
func lexFragment(fragment string, rules langRules) []codeToken {
	runes := []rune(fragment)
	var tokens []codeToken
	var plain strings.Builder

	flush := func() {
		if plain.Len() > 0 {
			tokens = append(tokens, codeToken{text: plain.String(), kind: tokPlain})
			plain.Reset()
		}
	}

	i := 0
	for i < len(runes) {
		if rules.lineComment != "" && hasPrefixAt(runes, i, rules.lineComment) {
			flush()
			tokens = append(tokens, codeToken{text: string(runes[i:]), kind: tokComment})
			return tokens
		}

		if rules.blockCommentStart != "" && hasPrefixAt(runes, i, rules.blockCommentStart) {
			if end := indexString(runes, i+len(rules.blockCommentStart), rules.blockCommentEnd); end >= 0 {
				flush()
				stop := end + len([]rune(rules.blockCommentEnd))
				tokens = append(tokens, codeToken{text: string(runes[i:stop]), kind: tokComment})
				i = stop
				continue
			}
		}

		if strings.ContainsRune(rules.quotes, runes[i]) {
			quote := runes[i]
			flush()
			if end := indexClosingQuote(runes, i+1, quote); end >= 0 {
				tokens = append(tokens, codeToken{text: string(runes[i : end+1]), kind: tokString})
				i = end + 1
			} else {
				tokens = append(tokens, codeToken{text: string(runes[i:]), kind: tokString})
				i = len(runes)
			}
			continue
		}

		if isASCIIDigit(runes[i]) {
			end := i
			for end < len(runes) && isNumberRune(runes[end]) {
				end++
			}
			flush()
			tokens = append(tokens, codeToken{text: string(runes[i:end]), kind: tokNumber})
			i = end
			continue
		}

		if isIdentStart(runes[i]) {
			end := i
			for end < len(runes) && isIdentRune(runes[end]) {
				end++
			}
			word := string(runes[i:end])
			if rules.keywords[word] {
				flush()
				tokens = append(tokens, codeToken{text: word, kind: tokKeyword})
			} else {
				plain.WriteString(word)
			}
			i = end
			continue
		}

		plain.WriteRune(runes[i])
		i++
	}
	flush()
	return tokens
}

// styleFor maps a token category to the theme style that carries it, so a new category never means
// a call site guessing at a colour: it means a new named field on Theme, alongside these four.
func styleFor(kind tokenKind, t theme.Theme) lipgloss.Style {
	switch kind {
	case tokKeyword:
		return t.CodeKeyword
	case tokString:
		return t.CodeString
	case tokComment:
		return t.CodeComment
	case tokNumber:
		return t.CodeNumber
	default:
		return t.Body
	}
}

// highlightCode styles one already-wrapped fragment of source, or renders it as plain body text
// when the language is not one this lexer covers. Run per fragment rather than per source line, for
// the same width safety reason renderInline runs per wrapped line: styling has to follow wrapping,
// never precede it.
func highlightCode(fragment string, rules langRules, ok bool, t theme.Theme) string {
	if !ok || fragment == "" {
		return t.Body.Render(fragment)
	}

	var out strings.Builder
	for _, tok := range lexFragment(fragment, rules) {
		out.WriteString(styleFor(tok.kind, t).Render(tok.text))
	}
	return out.String()
}

func hasPrefixAt(runes []rune, i int, prefix string) bool {
	p := []rune(prefix)
	if i+len(p) > len(runes) {
		return false
	}
	for j, r := range p {
		if runes[i+j] != r {
			return false
		}
	}
	return true
}

// indexString finds the next occurrence of needle starting at from, treating runes the way
// hasPrefixAt does, and returns the index it starts at or -1.
func indexString(runes []rune, from int, needle string) int {
	for i := from; i <= len(runes)-len([]rune(needle)); i++ {
		if hasPrefixAt(runes, i, needle) {
			return i
		}
	}
	return -1
}

// indexClosingQuote finds the rune that closes a string opened by quote, skipping a backslash
// escaped quote so `"a \" b"` closes at the real end rather than at the escaped character.
func indexClosingQuote(runes []rune, from int, quote rune) int {
	for i := from; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			i++ // the escaped character, whatever it is, cannot close the string
		case quote:
			return i
		}
	}
	return -1
}

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

// isNumberRune covers what a number literal looks like across the languages this lexer knows:
// decimal digits, a decimal point, hex digits and the 'x' that introduces them, and the underscore
// Go and JavaScript both allow as a digit separator. Loose on purpose. A number highlighted one
// character too wide is a cosmetic slip; a real parser for five numeric grammars is not what "good
// enough" asked for.
func isNumberRune(r rune) bool {
	switch {
	case isASCIIDigit(r):
		return true
	case r == '.' || r == '_' || r == 'x' || r == 'X':
		return true
	case r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		return true
	}
	return false
}

func isIdentStart(r rune) bool {
	return r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isIdentRune(r rune) bool {
	return isIdentStart(r) || isASCIIDigit(r)
}

func keywordSet(words ...string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// goKeywords is the language's reserved words plus the predeclared identifiers and basic types a
// reader actually wants coloured: an unadorned keyword list would leave int, string, true and nil
// looking like ordinary identifiers, which is not how any Go highlighter anyone has used reads.
var goKeywords = keywordSet(
	"break", "case", "chan", "const", "continue", "default", "defer", "else", "fallthrough", "for",
	"func", "go", "goto", "if", "import", "interface", "map", "package", "range", "return", "select",
	"struct", "switch", "type", "var",
	"true", "false", "nil", "iota", "any", "error",
	"bool", "byte", "rune", "string", "int", "int8", "int16", "int32", "int64",
	"uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64",
)

var pythonKeywords = keywordSet(
	"False", "None", "True", "and", "as", "assert", "async", "await", "break", "class", "continue",
	"def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in",
	"is", "lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield",
)

var javascriptKeywords = keywordSet(
	"break", "case", "catch", "class", "const", "continue", "debugger", "default", "delete", "do",
	"else", "export", "extends", "finally", "for", "function", "if", "import", "in", "instanceof",
	"let", "new", "return", "super", "switch", "this", "throw", "try", "typeof", "var", "void",
	"while", "with", "yield", "async", "await", "static", "get", "set", "of",
	"true", "false", "null", "undefined",
	"interface", "type", "enum", "implements", "private", "public", "protected", "readonly", "as",
	"from", "namespace", "declare",
)

var jsonKeywords = keywordSet("true", "false", "null")

var shellKeywords = keywordSet(
	"if", "then", "elif", "else", "fi", "for", "while", "until", "do", "done", "case", "esac",
	"function", "in", "select", "time", "return", "break", "continue",
	"local", "export", "readonly", "declare",
)
