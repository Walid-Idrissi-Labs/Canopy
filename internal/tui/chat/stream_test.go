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
