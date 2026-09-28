package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A block comment spanning lines is one token to a real lexer; the old per-line highlighter lost
// it after the first line. Every line of it must carry the comment style, and the text must survive.
func TestAMultiLineCommentIsHighlightedAsOne(t *testing.T) {
	code := []string{"/* first", "   second", "*/", "func main() {}"}
	lines, ok := chromaBlock("go", code, 80)
	if !ok {
		t.Fatal("no lexer for go")
	}
	if len(lines) != len(code) {
		t.Fatalf("%d lines for %d", len(lines), len(code))
	}
	for i, want := range code {
		if got := strings.TrimSpace(ansi.Strip(lines[i])); got != strings.TrimSpace(want) {
			t.Errorf("line %d = %q, want %q", i, got, want)
		}
	}
}

func TestAnUnknownLanguageFallsBack(t *testing.T) {
	if _, ok := chromaBlock("not-a-language-at-all", []string{"x"}, 80); ok {
		t.Fatal("an unknown language claimed a lexer")
	}
}

// Wrapped lines keep within the width, colours included.
func TestHighlightedLinesKeepWithinTheWidth(t *testing.T) {
	lines, _ := chromaBlock("go", []string{"var x = \"" + strings.Repeat("a", 200) + "\""}, 40)
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 40 {
			t.Fatalf("a highlighted line is %d cells wide in a 40-cell column: %q", w, l)
		}
	}
}
