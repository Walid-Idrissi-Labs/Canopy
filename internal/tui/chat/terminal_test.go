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

// A finished turn is drawn in the order it happened: the conclusion written after reading a file
// appears after the read, not above it.
func TestAFinishedTurnIsDrawnInTheOrderItHappened(t *testing.T) {
	call := core.ToolCall{ID: "c1", Name: "read_file", Input: []byte(`{"path":"main.go"}`)}
	turn := core.Turn{
		ID: "t1", State: core.TurnComplete,
		Request:     core.Message{Role: core.RoleUser, Text: "read it"},
		Text:        "Looking first.Concluded after reading.",
		ToolCalls:   []core.ToolCall{call},
		ToolResults: []core.ToolResult{{CallID: "c1", Content: "package main"}},
		Steps: []core.Message{
			{Role: core.RoleAssistant, Text: "Looking first.", ToolCalls: []core.ToolCall{call}},
			{Role: core.RoleUser, ToolResults: []core.ToolResult{{CallID: "c1", Content: "package main"}}},
			{Role: core.RoleAssistant, Text: "Concluded after reading."},
		},
	}
	joined := strings.Join(renderTurn(turn, 80, "", nil, Detail{}), "\n")
	first, read, last := strings.Index(joined, "Looking first."), strings.Index(joined, "main.go"), strings.Index(joined, "Concluded after reading.")
	if first < 0 || read < 0 || last < 0 || first >= read || read >= last {
		t.Fatalf("the turn is not in the order it happened:\n%s", joined)
	}
}
