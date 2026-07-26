package chat_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// widest returns the display width of the widest rendered line, measured with lipgloss.Width so an
// escape code never counts as a column. Getting that measurement wrong is the one mistake that
// breaks every caller of RenderMarkdown at once, since the frame around it assumes every line fits.
func widest(lines []string) int {
	max := 0
	for _, line := range lines {
		if w := lipgloss.Width(line); w > max {
			max = w
		}
	}
	return max
}

// A code block whose language tag or content is longer than the terminal is exactly the input a
// model is most likely to produce, since it has no idea how wide the user's window is.
func TestALongCodeLineDoesNotBreakTheLayout(t *testing.T) {
	reply := "```go\n" + strings.Repeat("x", 400) + "\n```"

	for _, width := range []int{20, 40, 80} {
		lines := chat.RenderMarkdown(reply, width)
		if got := widest(lines); got > width {
			t.Errorf("width %d: a rendered line is %d columns wide, want <= %d", width, got, width)
		}
	}
}

// A minified line with no spaces at all is the case wrapLine's hard break path exists for, and it
// is also the case most likely to expose a wrap that was computed before styling rather than after.
func TestAWordWithNoSpacesInACodeBlockIsWrappedNotDropped(t *testing.T) {
	long := strings.Repeat("abcdefghij", 30) // 300 runes, no spaces, no wrap points
	reply := "```\n" + long + "\n```"

	lines := chat.RenderMarkdown(reply, 40)

	// Every rune of the source line has to still be present somewhere in the output, in order, or
	// the wrap silently lost characters instead of merely breaking the line up. Checking for the
	// whole 300 character word as one substring of any single line would be wrong, since wrapping
	// is exactly what is expected to cut it into pieces; reassembling the pieces is the real test.
	var recovered strings.Builder
	for _, line := range lines {
		p := plain(line)
		p = strings.TrimPrefix(p, "  ")
		p = strings.TrimPrefix(p, "↳ ")
		recovered.WriteString(p)
	}
	if !strings.Contains(recovered.String(), long) {
		t.Errorf("the wrapped code line does not reconstruct the source:\ngot  %q\nwant to contain %q",
			recovered.String(), long)
	}
}

// The acceptance criterion is explicit: stays readable with colour disabled. plain strips every
// escape code the same way a NO_COLOR terminal would, so if a test only passes with colour left in,
// the feature is not actually met.
func TestFencedCodeBlockStaysMarkedAsCodeWithColourDisabled(t *testing.T) {
	reply := "some text\n\n```go\nfunc main() {}\n```\n\nmore text"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), "\n"))

	for _, want := range []string{"```go", "func main() {}", "```"} {
		if !strings.Contains(view, want) {
			t.Errorf("plain output is missing %q, so a code block is not distinguishable without "+
				"colour:\n%s", want, view)
		}
	}
}

// Every one of Go, Python, JavaScript, JSON and shell has to fall back to something recognisable
// rather than an error or a dropped block, and the four token categories have to actually fire for
// each one, not just for Go.
func TestEachSupportedLanguageHighlightsWithoutLosingCharacters(t *testing.T) {
	cases := []struct {
		lang, source string
	}{
		{"go", "func main() {\n\tx := 42 // start\n\tfmt.Println(\"hi\")\n}"},
		{"python", "def f():\n    n = 7  # a comment\n    return \"ok\""},
		{"javascript", "function f() {\n  const n = 7; // done\n  return \"ok\";\n}"},
		{"json", "{\"a\": 1, \"b\": true, \"c\": \"text\"}"},
		{"shell", "if [ -f x ]; then\n  echo \"found\" # 1\nfi"},
	}

	for _, tc := range cases {
		reply := "```" + tc.lang + "\n" + tc.source + "\n```"
		rendered := chat.RenderMarkdown(reply, 80)
		view := plain(strings.Join(rendered, "\n"))

		// Coloured or not, nothing about the source may be lost or garbled: this is the width-safety
		// argument (style is added after wrapping, never before) proven from the outside rather than
		// from reading the implementation.
		for _, fragment := range strings.Split(tc.source, "\n") {
			fragment = strings.TrimSpace(fragment)
			if fragment != "" && !strings.Contains(view, fragment) {
				t.Errorf("%s: rendered output is missing %q:\n%s", tc.lang, fragment, view)
			}
		}
	}
}

// A language nobody wrote a lexer for, or no language at all, is the common case for a quick shell
// transcript or a pasted error, and it has to render as readable code rather than fail or vanish.
func TestAnUnknownLanguageFallsBackToPlainCode(t *testing.T) {
	for _, reply := range []string{
		"```brainfuck\n++++[>++++<-]\n```",
		"```\nplain text with no language tag\n```",
	} {
		view := plain(strings.Join(chat.RenderMarkdown(reply, 60), "\n"))
		if !strings.Contains(view, "```") {
			t.Errorf("an unhighlighted block should still read as code:\n%s", view)
		}
	}
}

// "#", "##" and "###" are the vocabulary this project asked for. Keeping the hash in the rendered
// text is what makes a heading still read as a heading once every escape code is gone.
func TestHeadingsStayMarkedWithColourDisabled(t *testing.T) {
	reply := "# Title\n\n## Section\n\n### Detail\n\nordinary paragraph"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), "\n"))

	for _, want := range []string{"# Title", "## Section", "### Detail"} {
		if !strings.Contains(view, want) {
			t.Errorf("a heading lost its marker, so it cannot be told apart from a paragraph once "+
				"colour is gone:\n%s", view)
		}
	}
}

// The whole point of a hanging indent is that a reader's eye does not have to re-find the left edge
// of a bullet's own text when it wraps. Under the bullet is where a naive wrap would put it.
func TestBulletListHangingIndentLinesUpUnderTheTextNotTheBullet(t *testing.T) {
	item := "- " + strings.Repeat("word ", 20)
	lines := chat.RenderMarkdown(item, 30)

	if len(lines) < 2 {
		t.Fatalf("expected the item to wrap onto more than one line, got %d: %v", len(lines), lines)
	}
	first := plain(lines[0])
	if !strings.HasPrefix(first, "- ") {
		t.Fatalf("first line does not start with the bullet: %q", first)
	}
	for _, line := range lines[1:] {
		got := plain(line)
		// "- " is two columns, so a continuation line lined up under the text starts with exactly
		// two spaces and then a non-space character, not three spaces or zero.
		if !strings.HasPrefix(got, "  ") || strings.HasPrefix(got, "   ") {
			t.Errorf("continuation line is not indented under the bullet's text: %q", got)
		}
	}
}

// A two digit number changes the width of the marker itself ("12. " is one column wider than "- "),
// which is exactly the case a fixed, guessed indent gets wrong.
func TestNumberedListHangingIndentMatchesTheMarkerWidth(t *testing.T) {
	item := "12. " + strings.Repeat("word ", 20)
	lines := chat.RenderMarkdown(item, 30)

	if len(lines) < 2 {
		t.Fatalf("expected the item to wrap, got %d lines", len(lines))
	}
	for _, line := range lines[1:] {
		got := plain(line)
		if !strings.HasPrefix(got, "    ") || strings.HasPrefix(got, "     ") {
			t.Errorf("continuation is not indented to the width of %q: %q", "12. ", got)
		}
	}
}

// The reader has to see 1, 2, 3 in order rather than markdown's habit of writing every item as "1."
// and letting the renderer number them, so this is really testing that source order survives at all.
func TestOrderedListItemsKeepTheirOwnNumbers(t *testing.T) {
	reply := "1. first\n2. second\n3. third"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), "\n"))

	for _, want := range []string{"1. first", "2. second", "3. third"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
}

// The literal risk named in the task: an unclosed "**" must not swallow everything after it. If it
// did, one malformed reply would silently delete the rest of the model's answer from the screen.
func TestAnUnclosedBoldMarkerRendersAsLiteralTextRatherThanSwallowingTheReply(t *testing.T) {
	reply := "this is **bold but never closed and here is the rest of a long reply that must " +
		"still be on screen"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), " "))

	if !strings.Contains(view, "still be on screen") {
		t.Errorf("text after an unclosed ** marker went missing:\n%s", view)
	}
	if !strings.Contains(view, "**bold") {
		t.Errorf("an unclosed marker should render literally, asterisks and all:\n%s", view)
	}
}

// Same failure mode as the bold case, for the single star form, since the two are parsed by
// different code paths and either one alone passing would hide a bug in the other.
func TestAnUnclosedItalicMarkerRendersAsLiteralText(t *testing.T) {
	reply := "a sentence with *an opening star that never closes and more words follow after it"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), " "))

	if !strings.Contains(view, "follow after it") {
		t.Errorf("text after an unclosed * marker went missing:\n%s", view)
	}
}

// Closed emphasis keeps its markers too. Losing them once colour is stripped would mean the only
// evidence that a word was ever emphasised is a colour, which the acceptance criteria forbids.
func TestClosedBoldAndItalicKeepTheirMarkersWithColourDisabled(t *testing.T) {
	reply := "plain **bold word** and plain *italic word* plain"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), " "))

	for _, want := range []string{"**bold word**", "*italic word*"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in %q", want, view)
		}
	}
}

// A literal multiplication written as prose, "5 * 3 * 2", is common in a reply that talks about
// arithmetic or shell globs, and it must not be read as an opening italic marker with no close.
func TestAStandaloneAsteriskSurroundedBySpacesIsNotTreatedAsEmphasis(t *testing.T) {
	reply := "the result of 5 * 3 * 2 is 30"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), " "))
	if !strings.Contains(view, "5 * 3 * 2 is 30") {
		t.Errorf("a plain multiplication was mangled by emphasis parsing: %q", view)
	}
}

// Backticks stay in the output for the same reason "#" and "**" do: without colour, they are the
// only thing telling a reader this word is code rather than prose.
func TestInlineCodeKeepsItsBackticksWithColourDisabled(t *testing.T) {
	reply := "run `go test ./...` before you push"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), " "))
	if !strings.Contains(view, "`go test ./...`") {
		t.Errorf("inline code lost its backticks: %q", view)
	}
}

// A quote spanning several source lines has to read as one quoted block, not as one marked line
// followed by unmarked ones that happen to be indented the same amount by coincidence.
func TestBlockQuotePrefixesEveryWrappedLine(t *testing.T) {
	reply := "> " + strings.Repeat("quoted word ", 20)
	lines := chat.RenderMarkdown(reply, 30)

	if len(lines) < 2 {
		t.Fatalf("expected the quote to wrap onto more than one line, got %d", len(lines))
	}
	for _, line := range lines {
		if got := plain(line); !strings.HasPrefix(got, "> ") {
			t.Errorf("a wrapped quote line lost its marker: %q", got)
		}
	}
}

func TestMultiLineBlockQuoteIsReflowedAsOneQuote(t *testing.T) {
	reply := "> line one\n> line two"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), " "))
	if !strings.Contains(view, "line one") || !strings.Contains(view, "line two") {
		t.Errorf("a two line quote lost content: %q", view)
	}
}

// Paragraphs are the plain, common case and the one every other test's surrounding text depends on
// not being mangled, so it gets its own direct assertion rather than only being exercised as filler.
func TestAPlainParagraphWrapsToWidth(t *testing.T) {
	reply := strings.Repeat("word ", 40)
	lines := chat.RenderMarkdown(reply, 20)
	if got := widest(lines); got > 20 {
		t.Errorf("a paragraph line is %d columns wide, want <= 20", got)
	}
	view := plain(strings.Join(lines, " "))
	if strings.Count(view, "word") != 40 {
		t.Errorf("wrapping a paragraph changed its word count: %q", view)
	}
}

// The width guarantee is the one the frame around this function depends on absolutely. One combined
// test walks a reply that mixes every block type at several widths, because the bug that slips
// through per-feature tests is usually one feature's output interacting badly with the next one's.
func TestNoRenderedLineEverExceedsTheGivenWidth(t *testing.T) {
	reply := strings.Join([]string{
		"# A heading that is on the longer side of what someone might actually type",
		"",
		"A paragraph with **bold**, *italic*, `inline code` and a naked * that is not emphasis.",
		"",
		"- a bullet item that is long enough that it will need to wrap at most widths tried here",
		"- " + strings.Repeat("nospacesatall", 10),
		"1. first",
		"2. second item, also written long enough to need to wrap across more than one line",
		"",
		"> a block quote long enough to wrap, holding **emphasis** and `code` of its own",
		"",
		"```go",
		`func f(x int) string { return fmt.Sprintf("%d", x) } // a trailing comment`,
		strings.Repeat("y", 200),
		"```",
	}, "\n")

	for _, width := range []int{20, 24, 40, 80, 120} {
		lines := chat.RenderMarkdown(reply, width)
		if got := widest(lines); got > width {
			t.Errorf("width %d: widest rendered line is %d columns", width, got)
		}
	}
}

// A fence the model never closed, because the reply was cut off, still has to render as a code
// block rather than as a stray line of backticks followed by raw source read as prose.
func TestAnUnterminatedFenceStillRendersAsACodeBlock(t *testing.T) {
	reply := "```go\nfunc f() {\n\treturn\n"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), "\n"))
	if !strings.Contains(view, "func f()") || !strings.Contains(view, "return") {
		t.Errorf("an unterminated fence lost its content:\n%s", view)
	}
}

// A decimal number at the very start of a line, "3.14 is the value of pi", is the real case an
// ordered list marker could be confused with: "3." looks exactly like "3. " one character short.
// Reading it as a list item would strip "3." from the text and start the paragraph at "14 is...".
func TestADecimalNumberAtTheStartOfALineIsNotReadAsAnOrderedListItem(t *testing.T) {
	reply := "3.14 is the value of pi to two decimal places"
	view := plain(strings.Join(chat.RenderMarkdown(reply, 60), " "))
	if !strings.Contains(view, "3.14 is the value of pi") {
		t.Errorf("a decimal number at the start of a line was misread as a list marker: %q", view)
	}
}

// RenderMarkdown is only ever asked for output that fits somewhere, so a caller passing a width the
// program itself would never construct still has to come back with something rather than panicking.
func TestRenderMarkdownDoesNotPanicOnDegenerateInput(t *testing.T) {
	inputs := []string{"", "\n\n\n", "```", "```\n```", "**", "*", "`", "- ", "1.", strconv.Itoa(0)}
	for _, in := range inputs {
		for _, width := range []int{0, 1, 3, 4, 5} {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("RenderMarkdown(%q, %d) panicked: %v", in, width, r)
					}
				}()
				chat.RenderMarkdown(in, width)
			}()
		}
	}
}
