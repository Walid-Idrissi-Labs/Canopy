package chat

import (
	"strings"
	"testing"
)

const streamFixture = "# Plan\n\nWe will change **three** things:\n\n1. the parser\n2. the lexer\n3. the tests\n\n" +
	"| file | change |\n|---|---|\n| a.go | rename |\n| b.go | split |\n\n" +
	"```go\nfunc main() {\n\n\tfmt.Println(\"hi\")\n}\n```\n\n" +
	"> a quoted note\n> over two lines\n\n- item one\n- item two with `code`\n\n" +
	"Some closing prose that runs long enough to wrap at a narrow width, and then some more words.\n\n" +
	"~~~\nplain fence\n\nwith a blank line\n~~~\n\n" +
	"1. loose item one\n\n2. loose item two\n\n   continued after a blank line\n\n" +
	"- outer\n  - inner\n\n    indented code after a blank line\n\n" +
	"Text right before a table:\n| a | b |\n|---|---|\n| 1 | 2 |\n\n---\n\nDone.\n"

// Rendering a reply as it streams in pieces must produce exactly what rendering it whole produces,
// at every point in the stream, or the incremental path shows something the finished turn will
// not.
func TestStreamingRendersExactlyWhatAWholeRenderDoes(t *testing.T) {
	for _, width := range []int{40, 80, 120} {
		key := "test|" + strings.Repeat("w", width)
		for cut := 1; cut <= len(streamFixture); cut++ {
			text := streamFixture[:cut]
			got := strings.Join(streamingMarkdown(key, text, width), "\n")
			want := strings.Join(RenderMarkdown(text, width), "\n")
			if got != want {
				t.Fatalf("width %d, after %d bytes the streamed render differs\n--- streamed\n%s\n--- whole\n%s",
					width, cut, got, want)
			}
		}
		forgetStreaming(key)
	}
}

func TestReplacedTextIsRenderedAfresh(t *testing.T) {
	key := "replaced"
	_ = streamingMarkdown(key, "first paragraph\n\nsecond\n\nthird", 80)
	got := strings.Join(streamingMarkdown(key, "other paragraph\n\nsecond\n\nthird", 80), "\n")
	if strings.Contains(got, "first") {
		t.Fatalf("stale lines from replaced text: %q", got)
	}
}

// BenchmarkStreamedReply feeds a 50 KB reply in 20-byte chunks and renders after each one, the way
// the screen does while a turn streams.
func BenchmarkStreamedReply(b *testing.B) {
	reply := strings.Repeat(streamFixture, 50_000/len(streamFixture)+1)[:50_000]
	for i := 0; i < b.N; i++ {
		key := "bench"
		for cut := 20; cut <= len(reply); cut += 20 {
			_ = streamingMarkdown(key, reply[:cut], 120)
		}
		forgetStreaming(key)
	}
}

// Random documents built from awkward pieces, fences inside fences, indented fence markers,
// continued list items, tables, are checked at every cut: the streamed render must always be the
// whole render.
func TestStreamingMatchesWholeRendersOfRandomDocuments(t *testing.T) {
	pieces := []string{
		"Plain prose that wraps across the width of the screen more than once.\n\n",
		"```go\nfunc a() {\n\n~~~ not a close\n\n}\n```\n\n",
		"~~~\nplain\n\n```\nstill inside\n~~~\n\n",
		"   ```py\n   indented fence\n\n   ```\n\n",
		"1. item\n\n   continued after a blank\n\n2. next\n\n",
		"- a\n  - b\n\n    code after blank\n\n",
		"| h | i |\n|---|---|\n| 1 | 2 |\n\n",
		"> quote\n> more\n\n",
		"# Heading\n\n",
		"---\n\n",
		"text right before\n```\nfence with no blank before\n```\n",
		"```\nunterminated fence\n\nwith blank lines\n",
	}
	seed := uint32(7)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % n
	}
	for c := 0; c < 60; c++ {
		var doc strings.Builder
		for k := 0; k < 3+next(5); k++ {
			doc.WriteString(pieces[next(len(pieces))])
		}
		text := doc.String()
		key := "random"
		for cut := 1; cut <= len(text); cut++ {
			got := strings.Join(streamingMarkdown(key, text[:cut], 60), "\n")
			want := strings.Join(RenderMarkdown(text[:cut], 60), "\n")
			if got != want {
				t.Fatalf("case %d, cut %d differs\n--- text\n%q\n--- streamed\n%s\n--- whole\n%s", c, cut, text[:cut], got, want)
			}
		}
		forgetStreaming(key)
	}
}
