package chat_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// The renderer must always return.
//
// It did not. Both hard-break loops asked cutCells for a piece of a word, got nothing back when the
// budget was one cell and the next character was two, started a fresh line, and asked again with the
// same budget. The screen froze inside View, so nothing redrew and ctrl+c was never read: the only
// way out was killing the process, and whatever the agent was doing carried on without a screen.
//
// It was reachable from ordinary model output. A checkmark inside an indented list item, or a table
// with enough columns, at widths this program uses on purpose: twenty is the floor TranscriptWith
// imposes and the width every agents-mosaic pane renders at.
//
// A watchdog rather than relying on the go test timeout, so a regression names the input that broke
// instead of dumping every goroutine in the process.
func within(t *testing.T, name string, f func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); f() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Errorf("%s did not return, so the interface would be frozen", name)
	}
}

func TestTheRendererAlwaysReturnsOnNarrowFullWidthText(t *testing.T) {
	wide := []string{"日本語日本語", "✅ done", "🚀🚀🚀", "한국어테스트"}

	for _, text := range wide {
		for _, width := range []int{1, 2, 3, 4, 20, 80} {
			within(t, "paragraph "+text, func() { chat.RenderMarkdown(text, width) })
			within(t, "quote "+text, func() { chat.RenderMarkdown("> "+text, width) })
			within(t, "heading "+text, func() { chat.RenderMarkdown("# "+text, width) })
			within(t, "code "+text, func() { chat.RenderMarkdown("```\n"+text+"\n```", width) })

			// Leading spaces on a list item are unbounded and come from the model, so the width
			// left for the text is model controlled and can reach one column at any terminal size.
			for _, indent := range []int{0, 4, 16, 17, 40, 77} {
				within(t, "indented bullet", func() {
					chat.RenderMarkdown(strings.Repeat(" ", indent)+"- "+text, width)
				})
			}
		}
	}
}

// A table narrow enough that its columns cannot hold one character each. The column count comes from
// the model, so this is the same class of input as the list indent above.
func TestATableWithMoreColumnsThanRoomStillReturns(t *testing.T) {
	for _, columns := range []int{2, 8, 12, 21, 40} {
		var head, sep, row strings.Builder
		head.WriteString("|")
		sep.WriteString("|")
		row.WriteString("|")
		for range columns {
			head.WriteString(" h |")
			sep.WriteString("---|")
			row.WriteString(" 日本語 |")
		}
		table := head.String() + "\n" + sep.String() + "\n" + row.String()

		for _, width := range []int{20, 38, 80} {
			within(t, "table", func() { chat.RenderMarkdown(table, width) })
		}
	}
}

// Through the real entry point, at the width every mosaic pane draws at.
func TestTheTranscriptReturnsAtTheMosaicPaneWidth(t *testing.T) {
	session := core.Session{ID: "s1", Turns: []core.Turn{{
		ID: "t1", State: core.TurnComplete, Request: core.Message{Text: "x"},
		Text: strings.Repeat(" ", 17) + "- ✅ done\n\n| a | b |\n|---|---|\n| 日本語 | 日本語 |",
	}}}
	for _, width := range []int{20, 40, 100} {
		within(t, "transcript", func() { chat.Transcript(session, width, ".", nil) })
	}
}

// Nothing is silently dropped by the character that overflows. A budget too small for a character
// still emits it, which is what lets the loop advance, and the text has to survive that.
func TestNothingIsLostWhenACharacterIsWiderThanTheLine(t *testing.T) {
	text := "日本語"
	var recovered strings.Builder
	for _, line := range chat.RenderMarkdown(text, 1) {
		recovered.WriteString(strings.TrimSpace(plain(line)))
	}
	if got := recovered.String(); got != text {
		t.Errorf("rendering at width 1 lost text:\ngot  %q\nwant %q", got, text)
	}
}
