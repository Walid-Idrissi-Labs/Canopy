package agent

// The visible task list an agent keeps as it works.
//
// This is most of what makes a long run followable, and it is nearly all of what makes four agents
// at once comprehensible rather than four scrolling walls of text. Watching an agent without one
// means reading everything it says to work out where it is; with one, the answer is three lines.
//
// The list is maintained by the agent through a tool, not inferred from what it wrote. Inferring it
// would mean a second model reading the first one's output and guessing, which is a new way to be
// wrong about the only summary the user is actually reading.
//
// One rule the tool enforces rather than requests: exactly one item may be in progress. A list where
// four things are all "in progress" is a list of everything the agent has ever touched, which is the
// state every one of these degenerates into if nothing stops it.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// TodoState is where one item has got to.
//
// Three values and no more. "blocked" was considered and left out: an agent that marks an item
// blocked and moves on has produced a list that looks like progress and is not, and the honest
// version of blocked is saying so in the conversation where a person will read it.
type TodoState string

const (
	TodoPending    TodoState = "pending"
	TodoInProgress TodoState = "in-progress"
	TodoDone       TodoState = "done"
)

// AllTodoStates returns every valid state.
func AllTodoStates() []TodoState { return []TodoState{TodoPending, TodoInProgress, TodoDone} }

// Valid reports whether s is one of the three.
func (s TodoState) Valid() bool {
	for _, known := range AllTodoStates() {
		if s == known {
			return true
		}
	}
	return false
}

// Glyph is the single character shown in front of an item.
//
// Single width, so a state change never shifts the column, and readable without colour, which is the
// same rule the test states follow.
func (s TodoState) Glyph() string {
	switch s {
	case TodoDone:
		return "x"
	case TodoInProgress:
		return ">"
	default:
		return " "
	}
}

// Todo is one item on the list.
type Todo struct {
	Text  string
	State TodoState

	// Outcome is what actually happened, recorded when the item closes. See core.Task.Outcome for
	// why it exists: without it every finished item says "done", which is a progress bar rather
	// than a report.
	Outcome string
}

// TodoList is an agent's task list.
//
// Safe for concurrent use, because the tool writes it from the turn's goroutine and the interface
// reads it from the event loop.
type TodoList struct {
	mu    sync.Mutex
	items []Todo
}

// NewTodoList returns an empty list.
func NewTodoList() *TodoList { return &TodoList{} }

// Items returns a copy of the list.
func (l *TodoList) Items() []Todo {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Todo(nil), l.items...)
}

// Replace sets the whole list.
//
// Wholesale rather than by edits, which matters more than it looks. A model that has to name an item
// to change it will eventually name one that does not exist, and the failure is a list that quietly
// stops matching what the agent is doing. Handing over the whole list every time means the list on
// screen is the list the agent currently believes in, always.
func (l *TodoList) Replace(items []Todo) error {
	var inProgress int
	for i, item := range items {
		if strings.TrimSpace(item.Text) == "" {
			return fmt.Errorf("the item at position %d has no text", i+1)
		}
		if !item.State.Valid() {
			return fmt.Errorf("%q is not a state: use pending, in-progress or done", item.State)
		}
		if item.State == TodoInProgress {
			inProgress++
		}
		if item.Outcome != "" && item.State != TodoDone {
			// An outcome on an item that has not finished is a claim about something that has not
			// happened yet, which is the same class of untruth as a test result reported for code
			// that was never run.
			return fmt.Errorf("the item at position %d records an outcome but is %s, "+
				"so mark it done or drop the outcome", i+1, item.State)
		}
	}
	if inProgress > 1 {
		return fmt.Errorf("%d items are in progress at once, which makes the list a record of "+
			"everything touched rather than a statement of where the work is. Mark one", inProgress)
	}

	l.mu.Lock()
	l.items = append([]Todo(nil), items...)
	l.mu.Unlock()
	return nil
}

// Tasks is the list in the shape the rest of the program uses.
//
// The conversion exists so nothing outside this package has to know about TodoState, and so the
// screen can render a task list without importing the machinery that produces one.
func (l *TodoList) Tasks() []core.Task {
	items := l.Items()
	out := make([]core.Task, 0, len(items))
	for _, item := range items {
		out = append(out, core.Task{
			Text:    item.Text,
			State:   core.TaskState(item.State),
			Outcome: item.Outcome,
		})
	}
	return out
}

// Summary is the one line form, for a row in a list of agents.
func (l *TodoList) Summary() string {
	items := l.Items()
	if len(items) == 0 {
		return ""
	}

	var done int
	current := ""
	for _, item := range items {
		if item.State == TodoDone {
			done++
		}
		if item.State == TodoInProgress && current == "" {
			current = item.Text
		}
	}
	if current == "" {
		// Nothing in progress. Either finished or between items, and the counts say which.
		return fmt.Sprintf("%d of %d done", done, len(items))
	}
	return fmt.Sprintf("%d/%d %s", done+1, len(items), current)
}

// Render is the list as lines, for an agent's pane.
func (l *TodoList) Render() []string {
	items := l.Items()
	if len(items) == 0 {
		return nil
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprintf("[%s] %s", item.State.Glyph(), item.Text))
	}
	return out
}

// TodoTool lets an agent maintain its own list.
func TodoTool(list *TodoList) core.Tool { return &todoTool{list: list} }

type todoTool struct{ list *TodoList }

func (t *todoTool) Name() string { return "set_tasks" }

// Tasks makes the tool itself a core.TaskReporter.
//
// The registry holds tools, not the things behind them, so a Tasks method on the list alone leaves
// the engine looking at a registry full of tools none of which reports a task list. That is exactly
// what happened, and the list was maintained correctly and displayed nowhere.
func (t *todoTool) Tasks() []core.Task { return t.list.Tasks() }

// Kind is read, which looks wrong for a tool that writes something and is not.
//
// The permission model asks about reaching the world: files, commands, the network. This writes a
// display Canopy owns, inside Canopy, and nothing else can see it. Classifying it as a write would
// mean a read-only agent could not tell anybody what it was doing, which is the opposite of what
// read-only is for.
func (t *todoTool) Kind() core.ToolKind { return core.ToolRead }

func (t *todoTool) Description() string {
	return "Replace your task list, which the user sees while you work. Call this when you start " +
		"a job with several steps, and again each time a step finishes. Send the whole list every " +
		"time, not just what changed. Exactly one item may be in-progress. Keep items short and " +
		"in the user's terms, not in terms of the tools you are about to call."
}

func (t *todoTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"tasks": {
				"type": "array",
				"description": "The whole list, in order.",
				"items": {
					"type": "object",
					"properties": {
						"text": {"type": "string", "description": "What the step is, in a few words."},
						"state": {"type": "string", "enum": ["pending", "in-progress", "done"]}
					},
					"required": ["text", "state"]
				}
			}
		},
		"required": ["tasks"]
	}`)
}

func (t *todoTool) Run(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Tasks []struct {
			Text    string `json:"text"`
			State   string `json:"state"`
			Outcome string `json:"outcome"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return core.ToolResult{Content: fmt.Sprintf("the arguments are not valid JSON: %v", err), IsError: true}, nil
	}

	items := make([]Todo, 0, len(args.Tasks))
	for _, task := range args.Tasks {
		items = append(items, Todo{
			Text:    strings.TrimSpace(task.Text),
			State:   TodoState(task.State),
			Outcome: strings.TrimSpace(task.Outcome),
		})
	}
	if err := t.list.Replace(items); err != nil {
		return core.ToolResult{Content: err.Error(), IsError: true}, nil
	}

	return core.ToolResult{Content: fmt.Sprintf("Task list updated: %s", t.list.Summary())}, nil
}
