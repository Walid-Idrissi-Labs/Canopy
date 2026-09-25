package chat

import (
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"testing"
)

func TestTerminalSafeMakesEveryInterpretableControlVisible(t *testing.T) {
	input := "before\x00\x07\b\r\x1b[2J\x7f\u0085after\nnext\tcolumn"
	got := terminalSafe(input)

	for _, want := range []string{
		`before\x00\x07\x08\x0d\x1b[2J\x7f\u0085after`,
		"next\tcolumn",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("terminalSafe(%q) = %q, missing %q", input, got, want)
		}
	}

	for _, r := range got {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			t.Errorf("terminalSafe left control U+%04X in %q", r, got)
		}
	}
}

func TestTerminalSafeDoesNotChangeOrdinaryUnicode(t *testing.T) {
	const input = "مرحبا — café — 日本語\n\tindented"
	if got := terminalSafe(input); got != input {
		t.Errorf("ordinary text changed from %q to %q", input, got)
	}
}

// A reply, its thinking and its error are model output, and model output can repeat escape
// sequences it read in a file or a web page. None of them may reach the terminal as control codes.
func TestARenderedTurnCarriesNoRawEscapeSequence(t *testing.T) {
	payloads := []string{
		"\x1b]52;c;cHduZWQ=\x07",       // OSC 52: write the clipboard
		"\x1b]0;owned\x07",             // set the window title
		"\x1b]8;;https://evil/\x1b\\x", // OSC 8 hyperlink
		"\x1b[2J\x1b[H",                // clear and home
		"\x1b[201~",                    // end a bracketed paste early
	}
	for _, p := range payloads {
		turn := core.Turn{
			Request:  core.Message{Text: "q" + p},
			Thinking: "think" + p,
			Text:     "answer " + p,
			Error:    "failed " + p,
			State:    core.TurnFailed,
		}
		for _, line := range renderTurn(turn, 80, "", nil, Detail{}) {
			if strings.Contains(line, "\x1b]") || strings.Contains(line, "\x1b[2J") || strings.Contains(line, "\x1b[201~") {
				t.Fatalf("payload %q reached the terminal raw in %q", p, line)
			}
		}
	}
}

// A finished turn says which model answered, how much it read and wrote, and how much came from
// the cache.
func TestAFinishedTurnHasAFooter(t *testing.T) {
	turn := core.Turn{ID: "t", State: core.TurnComplete, Model: "claude-opus-5", Text: "ok",
		Request: core.Message{Text: "q"},
		Usage:   core.Usage{InputTokens: 100, CacheReadTokens: 900, OutputTokens: 50, CostUSD: 0.0123, CostKnown: true}}
	joined := strings.Join(renderTurn(turn, 100, "", nil, Detail{}), "\n")
	for _, want := range []string{"claude-opus-5", "1.0k in, 50 out", "90% cached", "$0.0123"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the footer lacks %q:\n%s", want, joined)
		}
	}
}
