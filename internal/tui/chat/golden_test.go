package chat_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/screens"
)

// golden compares a rendered screen, colours stripped, with its stored snapshot. The layout is
// what a change to the renderer or to Bubble Tea can break without any other test noticing; set
// CANOPY_UPDATE_GOLDENS=1 to write the snapshots again after a change that is meant.
func golden(t *testing.T, name, got string) {
	t.Helper()
	if err := screens.Keep("chat", name, got); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "golden", name+".txt")
	got = strings.Join(trimRight(strings.Split(plain(got), "\n")), "\n") + "\n"
	if os.Getenv("CANOPY_UPDATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no snapshot for %s; run with CANOPY_UPDATE_GOLDENS=1 to write it", name)
	}
	if string(want) != got {
		t.Errorf("%s changed; if that is meant, run with CANOPY_UPDATE_GOLDENS=1.\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func trimRight(lines []string) []string {
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

// A working turn as it is drawn: the question, what the model said, a tool call with its result,
// the answer after it, and the footer, at the three sizes people use.
func TestGoldenAWorkingTurn(t *testing.T) {
	start := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	call := core.ToolCall{ID: "c1", Name: "read_file", Input: []byte(`{"path":"internal/parse/parse.go"}`)}
	result := core.ToolResult{CallID: "c1", Content: "package parse\n\nfunc Parse(s string) (int, error) {\n\treturn 0, nil\n}"}
	turn := core.Turn{
		ID: "t1", State: core.TurnComplete, Model: "claude-opus-5",
		StartedAt: start, EndedAt: start.Add(42 * time.Second),
		Request:     core.Message{Role: core.RoleUser, Text: "why does Parse return zero for \"12\"?"},
		Text:        "Reading the parser first.It never converts: `Parse` returns `0, nil` for any input.\n\n```go\nn, err := strconv.Atoi(s)\n```",
		ToolCalls:   []core.ToolCall{call},
		ToolResults: []core.ToolResult{result},
		Steps: []core.Message{
			{Role: core.RoleAssistant, Text: "Reading the parser first.", ToolCalls: []core.ToolCall{call}},
			{Role: core.RoleUser, ToolResults: []core.ToolResult{result}},
			{Role: core.RoleAssistant, Text: "It never converts: `Parse` returns `0, nil` for any input.\n\n```go\nn, err := strconv.Atoi(s)\n```"},
		},
		Usage: core.Usage{InputTokens: 1200, CacheReadTokens: 18000, CacheWriteTokens: 900, OutputTokens: 340,
			CostUSD: 0.0214, CostKnown: true},
	}
	engine := &fakeEngine{session: core.Session{ID: "s1", Model: "claude-opus-5", Turns: []core.Turn{turn}}}
	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {200, 50}} {
		m := chat.New(engine, "s1", "canopy", "claude")
		m.SetSize(size.w, size.h)
		golden(t, fmt.Sprintf("working-turn-%dx%d", size.w, size.h), m.Body())
	}
}
