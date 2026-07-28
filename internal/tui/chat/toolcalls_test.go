package chat_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// withCall builds a finished turn that used one tool.
func withCall(name, input string, result core.ToolResult) core.Session {
	result.CallID = "call-1"
	return core.Session{
		ID: "s1",
		Turns: []core.Turn{{
			Request:     core.Message{Text: "do the thing"},
			Text:        "done",
			State:       core.TurnComplete,
			ToolCalls:   []core.ToolCall{{ID: "call-1", Name: name, Input: []byte(input)}},
			ToolResults: []core.ToolResult{result},
		}},
	}
}

// M-01. A line saying `[run_command]` and nothing else is the same information as a spinner: it
// says something happened and refuses to say what. Somebody watching an agent work on their own
// repository needs to know which file, whether it worked, and how long it took.
func TestAToolCallSaysWhatItTouched(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"read_file", `{"path":"internal/git/poller.go"}`,
		core.ToolResult{Content: "one\ntwo\nthree", Duration: 12 * time.Millisecond},
	)}
	body := plain(model(engine).Body())

	if !strings.Contains(body, "read_file") {
		t.Errorf("the tool is not named:\n%s", body)
	}
	if !strings.Contains(body, "internal/git/poller.go") {
		t.Errorf("the call does not say which file it read:\n%s", body)
	}
	if !strings.Contains(body, "12ms") {
		t.Errorf("the call does not say how long it took:\n%s", body)
	}
	// A tick rather than the word, which is what the renderer draws now. Asserted on the mark
	// itself because a call that succeeded and one that failed have to be tellable apart at a
	// glance, and the glance is the whole reason the outcome is a symbol.
	if !strings.Contains(body, "✓") {
		t.Errorf("the call does not say whether it worked:\n%s", body)
	}
}

// A failed call that looks exactly like a successful one is how an agent ends up appearing
// productive while getting nowhere.
func TestAFailedToolCallSaysSoAndWhy(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"run_command", `{"command":"go test ./..."}`,
		core.ToolResult{
			Content:  "refused: this agent may not run commands\nsecond line",
			IsError:  true,
			Duration: 2 * time.Second,
		},
	)}
	body := plain(model(engine).Body())

	if !strings.Contains(body, "failed") {
		t.Errorf("a failed call is not marked as one:\n%s", body)
	}
	if !strings.Contains(body, "2.0s") {
		t.Errorf("the failure does not say how long it took:\n%s", body)
	}
	if !strings.Contains(body, "may not run commands") {
		t.Errorf("the reason is not shown, so the user cannot tell what to fix:\n%s", body)
	}
	if strings.Contains(body, "second line") {
		t.Errorf("the whole error was printed rather than its first line:\n%s", body)
	}
}

// A call with no result yet is either running or waiting on a person. Said in words, because
// otherwise a call that has sat there for a minute looks the same as one that finished instantly.
func TestAToolCallWithNoResultYetSaysItIsRunning(t *testing.T) {
	engine := &fakeEngine{session: core.Session{
		ID: "s1",
		Turns: []core.Turn{{
			Request:   core.Message{Text: "go"},
			State:     core.TurnAwaitingTools,
			ToolCalls: []core.ToolCall{{ID: "call-1", Name: "run_command", Input: []byte(`{"command":"make"}`)}},
		}},
	}}
	body := plain(model(engine).Body())

	if !strings.Contains(body, "running") {
		t.Errorf("a call still in flight is not marked:\n%s", body)
	}
}

// Results come back in whatever order the tools finish, so pairing by position would attribute one
// tool's failure to another tool's call.
func TestResultsArePairedByIdRatherThanByPosition(t *testing.T) {
	engine := &fakeEngine{session: core.Session{
		ID: "s1",
		Turns: []core.Turn{{
			Request: core.Message{Text: "go"},
			State:   core.TurnComplete,
			ToolCalls: []core.ToolCall{
				{ID: "a", Name: "read_file", Input: []byte(`{"path":"first.go"}`)},
				{ID: "b", Name: "write_file", Input: []byte(`{"path":"second.go"}`)},
			},
			// Deliberately out of order, which is what happens when the second tool finishes first.
			ToolResults: []core.ToolResult{
				{CallID: "b", Content: "written", Duration: time.Millisecond},
				{CallID: "a", Content: "boom", IsError: true, Duration: time.Millisecond},
			},
		}},
	}}
	body := plain(model(engine).Body())

	readAt := strings.Index(body, "read_file")
	writeAt := strings.Index(body, "write_file")
	failedAt := strings.Index(body, "failed")
	if readAt < 0 || writeAt < 0 || failedAt < 0 {
		t.Fatalf("the calls are not rendered as expected:\n%s", body)
	}
	// The failure belongs to read_file, which is listed first, so it must appear before write_file.
	if failedAt > writeAt {
		t.Errorf("the failure was attributed to the wrong call:\n%s", body)
	}
}

// The interface renders calls from tools it has never heard of, which is what MCP servers and
// anything added later will be. A table of known tools would render those as bare names.
func TestAnUnknownToolStillGetsALabel(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"some_third_party_thing", `{"ticket":"ENG-1421"}`,
		core.ToolResult{Content: "ok", Duration: time.Millisecond},
	)}
	body := plain(model(engine).Body())

	if !strings.Contains(body, "ENG-1421") {
		t.Errorf("an unrecognised argument was not shown at all:\n%s", body)
	}
}

// The output belongs to the model, not the screen. A thousand line file printed into the
// conversation buries the reply that follows it, and the reply is what somebody is waiting for.
func TestALargeResultIsSummarisedRatherThanPrinted(t *testing.T) {
	engine := &fakeEngine{session: withCall(
		"read_file", `{"path":"big.go"}`,
		core.ToolResult{
			Content:  strings.Repeat("a line of the file\n", 400),
			Duration: 30 * time.Millisecond,
		},
	)}
	body := plain(model(engine).Body())

	if strings.Count(body, "a line of the file") > 0 {
		t.Errorf("the tool output was printed into the conversation:\n%s", body[:400])
	}
	if !strings.Contains(body, "401 lines") {
		t.Errorf("the size of the result is not stated:\n%s", body)
	}
}

// Nanoseconds on a call that took four minutes is a number nobody can read at a glance, and the
// glance is the entire point of putting it there.
func TestDurationsAreReadableAtEveryScale(t *testing.T) {
	for _, tc := range []struct {
		took time.Duration
		want string
	}{
		{5 * time.Millisecond, "5ms"},
		{200 * time.Microsecond, "under a ms"},
		{1500 * time.Millisecond, "1.5s"},
		{95 * time.Second, "1m 35s"},
	} {
		engine := &fakeEngine{session: withCall(
			"read_file", `{"path":"x.go"}`, core.ToolResult{Content: "ok", Duration: tc.took},
		)}
		if body := plain(model(engine).Body()); !strings.Contains(body, tc.want) {
			t.Errorf("%s was not rendered as %q:\n%s", tc.took, tc.want, body)
		}
	}
}

// A long path must not wrap the argument line onto three rows, which turns a glance into a
// paragraph.
func TestALongArgumentIsTruncatedRatherThanWrapped(t *testing.T) {
	long := "internal/" + strings.Repeat("deeply/nested/", 12) + "file.go"
	engine := &fakeEngine{session: withCall(
		"read_file", `{"path":"`+long+`"}`, core.ToolResult{Content: "ok", Duration: time.Millisecond},
	)}

	for _, line := range strings.Split(plain(model(engine).Body()), "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("a line is %d columns wide: %q", len([]rune(line)), line)
		}
	}
}
