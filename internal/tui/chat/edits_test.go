package chat_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// An agent rewriting your files is the most consequential thing on this screen. Until the diff
// existed the transcript said which file and how long it took, which is the same sentence for a
// typo fix and for a rewrite that deleted a function.
func TestAnEditShowsWhatChanged(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"edit_file",
		`{"path":"internal/git/poller.go","old_text":"func poll() {\n\twait(1)\n}","new_text":"func poll() {\n\twait(5)\n}"}`,
		core.ToolResult{Content: "edited internal/git/poller.go", Duration: 8 * time.Millisecond},
	)}
	body := plain(model(engine).Body())

	if !marked(body, "-", "wait(1)") {
		t.Errorf("the line that went out is not shown as a removal:\n%s", body)
	}
	if !marked(body, "+", "wait(5)") {
		t.Errorf("the line that came in is not shown as an addition:\n%s", body)
	}
	// The tally, so the size of a change is readable without counting rows.
	if !strings.Contains(body, "+1") || !strings.Contains(body, "-1") {
		t.Errorf("the diff does not say how many lines moved:\n%s", body)
	}
	// The unchanged lines are context and must not be marked as changes.
	if marked(body, "+", "func poll") || marked(body, "-", "func poll") {
		t.Errorf("an unchanged line was marked as a change:\n%s", body)
	}
}

// File content is no more trusted than command output. A diff that forwards an escape sequence can
// rewrite the terminal at the exact point somebody is trying to inspect what changed.
func TestADiffCannotEmitTerminalControlSequences(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"edit_file",
		`{"path":"message.txt","old_text":"safe","new_text":"unsafe\u001b[2J\u0007"}`,
		core.ToolResult{Content: "ok", Duration: time.Millisecond},
	)}

	body := model(engine).Body()
	if strings.Contains(body, "\x1b[2J") || strings.ContainsRune(body, '\a') {
		t.Fatalf("file content reached the terminal as an active control sequence: %q", body)
	}
	view := plain(body)
	if !strings.Contains(view, `unsafe\x1b[2J\x07`) {
		t.Errorf("the diff did not make its control characters visible:\n%s", view)
	}
}

// marked reports whether some line of the body carries a diff marker against a piece of text.
//
// A scan rather than a substring match on the whole body, because the gap between the marker and the
// text is indentation the renderer owns, and a test that pins it fails on a change to the layout
// rather than on a change to the meaning.
func marked(body, marker, text string) bool {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, marker) && strings.Contains(trimmed, text) {
			return true
		}
	}
	return false
}

// The marker carries the meaning and the colour reinforces it, which is the rule the review screen's
// diff already follows. A diff read with NO_COLOR set is still a diff.
func TestAnEditIsReadableWithoutColour(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"edit_file",
		`{"path":"a.go","old_text":"one","new_text":"two"}`,
		core.ToolResult{Content: "ok", Duration: time.Millisecond},
	)}
	body := plain(model(engine).Body())

	if !strings.Contains(body, "-") || !strings.Contains(body, "+") {
		t.Errorf("with every escape stripped the diff has no markers left:\n%s", body)
	}
}

// A new file is every line an addition, which is what a diff against nothing is.
func TestWritingAFileShowsItsContentAsAdditions(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"write_file",
		`{"path":"hello.go","content":"package main\n\nfunc main() {}\n"}`,
		core.ToolResult{Content: "wrote hello.go", Duration: 3 * time.Millisecond},
	)}
	body := plain(model(engine).Body())

	if !strings.Contains(body, "+ package main") {
		t.Errorf("the content written is not shown:\n%s", body)
	}
	if !strings.Contains(body, "+3") {
		t.Errorf("the diff does not say how many lines were written:\n%s", body)
	}
}

// A diff is bounded like everything else on this screen, and says so rather than stopping quietly.
func TestALargeEditIsBoundedAndSaysSo(t *testing.T) {
	before := strings.TrimSuffix(strings.Repeat("old line\n", 60), "\n")
	after := strings.TrimSuffix(strings.Repeat("new line\n", 60), "\n")
	input, err := json.Marshal(map[string]string{
		"path": "big.go", "old_text": before, "new_text": after,
	})
	if err != nil {
		t.Fatalf("building the call arguments: %v", err)
	}
	engine := &fakeEngine{session: withCall(
		"edit_file", string(input),
		core.ToolResult{Content: "ok", Duration: time.Millisecond},
	)}
	body := plain(model(engine).Body())

	if strings.Count(body, "new line") >= 60 {
		t.Errorf("the whole rewrite was drawn, so a large edit owns the screen:\n%s", body)
	}
	if !strings.Contains(body, "ctrl+o") {
		t.Errorf("the diff is cut without naming the key that shows the rest:\n%s", body)
	}
}

// The reading view and the checking view, on one key. Following an agent is a glance and the answer
// is what you are waiting for; checking an agent is a read, and then the caps are in the way.
func TestCtrlOShowsEverythingAndFoldsItBack(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"read_file", `{"path":"big.go"}`,
		core.ToolResult{
			Content:  strings.Repeat("a line of the file\n", 40),
			Duration: 30 * time.Millisecond,
		},
	)}

	m := model(engine)
	folded := strings.Count(plain(m.Body()), "a line of the file")

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	opened := strings.Count(plain(m.Body()), "a line of the file")
	if opened <= folded {
		t.Errorf("ctrl+o showed no more of the result than the folded view: %d then %d", folded, opened)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if again := strings.Count(plain(m.Body()), "a line of the file"); again != folded {
		t.Errorf("ctrl+o did not fold the result back: %d, was %d", again, folded)
	}
}

// A call that has sat there for a minute and one that started a moment ago are the same line
// otherwise, and telling them apart is the question somebody watching a stuck agent is asking.
func TestARunningCallSaysHowLongItHasBeenRunning(t *testing.T) {
	engine := &fakeEngine{session: core.Session{
		ID: "s1",
		Turns: []core.Turn{{
			Request:   core.Message{Text: "go"},
			State:     core.TurnAwaitingTools,
			ToolCalls: []core.ToolCall{{ID: "call-1", Name: "run_command", Input: []byte(`{"command":"make"}`)}},
		}},
	}}

	m := model(engine)
	if body := plain(m.Body()); !strings.Contains(body, "running") {
		t.Fatalf("a call still in flight is not marked:\n%s", body)
	}

	// The clock is the screen's, so the count only appears once the screen has watched the call for
	// long enough to have something true to say.
	time.Sleep(1100 * time.Millisecond)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	if body := plain(m.Body()); !strings.Contains(body, "running for") {
		t.Errorf("a call in flight for over a second does not say how long:\n%s", body)
	}
}

// A tool the transcript does not understand gets no diff at all. A diff drawn from arguments this
// code cannot read would be a confident lie about somebody's repository, which is worse than the
// line count it replaces.
func TestAnUnknownWritingToolIsNotGuessedAt(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"mcp_write_thing",
		`{"path":"a.go","old_text":"one","new_text":"two"}`,
		core.ToolResult{Content: "ok", Duration: time.Millisecond},
	)}
	body := plain(model(engine).Body())

	if strings.Contains(body, "+ two") {
		t.Errorf("a diff was invented for a tool whose arguments are not known to mean that:\n%s", body)
	}
}
