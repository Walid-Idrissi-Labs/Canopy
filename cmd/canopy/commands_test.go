package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The P1-06 acceptance criterion: run it against the fake and get four workspaces out.
func TestSnapshotPrintsFourWorkspaces(t *testing.T) {
	var out bytes.Buffer
	if err := runSnapshot(&out); err != nil {
		t.Fatalf("runSnapshot: %v", err)
	}

	var got projectView
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(got.Workspaces) != 4 {
		t.Fatalf("got %d workspaces, want 4", len(got.Workspaces))
	}

	want := map[string]struct {
		green bool
		tests string
	}{
		"feat-login":   {true, "passing"},
		"fix-cache":    {false, "failing"},
		"refactor-api": {true, "passing"},
		"spike-search": {false, "not-configured"},
	}

	for _, w := range got.Workspaces {
		expected, ok := want[w.Name]
		if !ok {
			t.Errorf("unexpected workspace %q", w.Name)
			continue
		}
		if w.Green != expected.green {
			t.Errorf("%s: green = %v, want %v (%s)", w.Name, w.Green, expected.green, w.Reason)
		}
		if w.Tests != expected.tests {
			t.Errorf("%s: tests = %q, want %q", w.Name, w.Tests, expected.tests)
		}
		if strings.TrimSpace(w.Reason) == "" {
			t.Errorf("%s: no reason given, the harness has to be able to explain every verdict", w.Name)
		}
	}
}

// The derived state is the whole point of this output. A harness that printed only stored fields
// would show a run recorded as passing and leave the reader to work out that it no longer applies.
func TestSnapshotReportsDerivedStateNotStoredState(t *testing.T) {
	var out bytes.Buffer
	if err := runSnapshot(&out); err != nil {
		t.Fatalf("runSnapshot: %v", err)
	}

	var got projectView
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	for _, w := range got.Workspaces {
		for _, test := range w.TestDetail {
			if strings.TrimSpace(test.State) == "" {
				t.Errorf("%s/%s: no derived state", w.Name, test.Name)
			}
			if strings.TrimSpace(test.Reason) == "" {
				t.Errorf("%s/%s: no reason", w.Name, test.Name)
			}
			// The revision a run covered is what explains a stale result at a glance.
			if test.TestedRevision == "" {
				t.Errorf("%s/%s: the tested revision is missing", w.Name, test.Name)
			}
		}
		for _, service := range w.ServiceDetail {
			// Liveness and readiness stay separate all the way out to the wire, because a live
			// process that is not answering is a different problem from no process at all.
			if service.ProcessAlive == "" || service.Ready == "" {
				t.Errorf("%s/%s: liveness and readiness should both be reported", w.Name, service.Name)
			}
		}
	}
}

// The demo is the first thing anyone is shown, so it has to actually demonstrate the claim rather
// than print something that looks like it did.
func TestDemoShowsTheStaleFlip(t *testing.T) {
	var out bytes.Buffer
	if err := runDemo(&out); err != nil {
		t.Fatalf("runDemo: %v", err)
	}

	text := out.String()
	before, after, found := strings.Cut(text, "after the edit")
	if !found {
		t.Fatalf("the demo output has no before and after:\n%s", text)
	}

	// refactor-api is the workspace the demo edits.
	beforeLine := lineContaining(before, "refactor-api")
	afterLine := lineContaining(after, "refactor-api")

	if !strings.Contains(beforeLine, "passing") {
		t.Errorf("refactor-api should start passing, got: %s", beforeLine)
	}
	if !strings.Contains(afterLine, "stale") {
		t.Errorf("refactor-api should end stale, got: %s", afterLine)
	}
	if !strings.Contains(text, "revision-changed") {
		t.Error("the demo should show the event that caused the flip, not just the outcome")
	}

	// The other three must be untouched. An edit in one worktree saying anything about another
	// would be a much worse bug than the one this demo is showing off.
	for _, name := range []string{"feat-login", "fix-cache", "spike-search"} {
		if lineContaining(before, name) != lineContaining(after, name) {
			t.Errorf("editing refactor-api changed the row for %s:\n  before: %s\n  after:  %s",
				name, lineContaining(before, name), lineContaining(after, name))
		}
	}
}

// lineContaining returns the matching line with runs of whitespace collapsed.
//
// The collapsing matters: the table is column aligned, so a longer revision in one row widens
// that column for every row. Comparing raw lines would report every workspace as changed the
// moment one of them gets a dirty revision, which is a difference in layout rather than in state.
func lineContaining(text, substring string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, substring) {
			return strings.Join(strings.Fields(line), " ")
		}
	}
	return ""
}

func TestRunRejectsUnknownCommandsAndSources(t *testing.T) {
	if err := run([]string{"nonsense"}); err == nil {
		t.Error("an unknown command should be an error")
	}
	if err := run([]string{"snapshot", "-source", "real"}); err == nil {
		t.Error("an unimplemented source should be an error rather than silently using the fake")
	}
	if err := run([]string{"version"}); err != nil {
		t.Errorf("version: %v", err)
	}
	if err := run(nil); err != nil {
		t.Errorf("no arguments should print usage, not fail: %v", err)
	}
}
