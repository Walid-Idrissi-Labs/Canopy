package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func setTasks(t *testing.T, tool core.Tool, args string) core.ToolResult {
	t.Helper()
	result, err := tool.Run(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

func TestAListIsWhatTheAgentLastSaidItWas(t *testing.T) {
	list := NewTodoList()
	tool := TodoTool(list)

	setTasks(t, tool, `{"tasks":[
		{"text":"read the auth package","state":"done"},
		{"text":"replace the token check","state":"in-progress"},
		{"text":"update the tests","state":"pending"}]}`)

	if got := len(list.Items()); got != 3 {
		t.Fatalf("%d items, want 3", got)
	}

	// Replaced wholesale rather than edited. A model that names an item to change it will eventually
	// name one that does not exist, and the failure is a list that quietly stops matching the work.
	setTasks(t, tool, `{"tasks":[{"text":"something else entirely","state":"in-progress"}]}`)
	items := list.Items()
	if len(items) != 1 || items[0].Text != "something else entirely" {
		t.Errorf("the list is %+v, want only what was last sent", items)
	}
}

// A list where four things are in progress is a list of everything the agent has ever touched,
// which is the state every one of these degenerates into if nothing stops it.
func TestOnlyOneItemMayBeInProgress(t *testing.T) {
	list := NewTodoList()
	tool := TodoTool(list)

	result := setTasks(t, tool, `{"tasks":[
		{"text":"one","state":"in-progress"},
		{"text":"two","state":"in-progress"}]}`)

	if !result.IsError {
		t.Fatal("two items in progress at once was accepted")
	}
	if !strings.Contains(result.Content, "Mark one") {
		t.Errorf("the refusal does not say what to do: %q", result.Content)
	}
	if len(list.Items()) != 0 {
		t.Error("the refused list was stored anyway")
	}
}

func TestTheThingsThatMakeAListMeaningless(t *testing.T) {
	cases := []struct {
		name string
		args string
		says string
	}{
		{"an item with no text", `{"tasks":[{"text":"  ","state":"pending"}]}`, "no text"},
		{"a state that does not exist", `{"tasks":[{"text":"one","state":"blocked"}]}`, "not a state"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			list := NewTodoList()
			result := setTasks(t, TodoTool(list), c.args)
			if !result.IsError {
				t.Fatalf("%s was accepted", c.name)
			}
			if !strings.Contains(result.Content, c.says) {
				t.Errorf("the refusal reads %q", result.Content)
			}
		})
	}
}

// The summary is what a row in a list of six agents shows, so it has to say where the work is in
// about forty characters.
func TestTheSummarySaysWhereTheWorkIs(t *testing.T) {
	list := NewTodoList()
	tool := TodoTool(list)

	if list.Summary() != "" {
		t.Errorf("an empty list summarises as %q, want nothing at all", list.Summary())
	}

	setTasks(t, tool, `{"tasks":[
		{"text":"read the auth package","state":"done"},
		{"text":"replace the token check","state":"in-progress"},
		{"text":"update the tests","state":"pending"}]}`)

	summary := list.Summary()
	if !strings.Contains(summary, "replace the token check") {
		t.Errorf("the summary does not say what is happening now: %q", summary)
	}
	if !strings.Contains(summary, "2/3") {
		t.Errorf("the summary does not say how far through it is: %q", summary)
	}

	setTasks(t, tool, `{"tasks":[
		{"text":"read the auth package","state":"done"},
		{"text":"replace the token check","state":"done"}]}`)
	if finished := list.Summary(); !strings.Contains(finished, "2 of 2 done") {
		t.Errorf("a finished list summarises as %q", finished)
	}
}

// Readable without colour, and the glyphs are single width so a state change never shifts a column.
func TestTheRenderedListReadsWithoutColour(t *testing.T) {
	list := NewTodoList()
	setTasks(t, TodoTool(list), `{"tasks":[
		{"text":"one","state":"done"},
		{"text":"two","state":"in-progress"},
		{"text":"three","state":"pending"}]}`)

	lines := list.Render()
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3", len(lines))
	}

	width := len([]rune(lines[0])) - len([]rune("one"))
	for i, line := range lines {
		text := []string{"one", "two", "three"}[i]
		if got := len([]rune(line)) - len([]rune(text)); got != width {
			t.Errorf("line %d has a prefix %d wide, want %d: the column shifts when a state changes",
				i, got, width)
		}
	}
	if !strings.HasPrefix(lines[0], "[x]") || !strings.HasPrefix(lines[1], "[>]") {
		t.Errorf("the glyphs are %q and %q", lines[0][:3], lines[1][:3])
	}
}

// Written from a turn's goroutine, read from the event loop, which is two goroutines whatever the
// interface does.
func TestTheListIsSafeToReadWhileTheAgentWrites(t *testing.T) {
	list := NewTodoList()
	tool := TodoTool(list)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			_, _ = tool.Run(context.Background(),
				json.RawMessage(`{"tasks":[{"text":"one","state":"in-progress"}]}`))
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = list.Summary()
			_ = list.Render()
		}
	}()
	wg.Wait()
}
