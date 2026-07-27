package core

// The task list an agent keeps while it works, as the rest of the program sees it.
//
// The list lives in the agent package, which owns the tool the agent calls and the rule that only
// one item may be in progress. This is the shape it arrives in everywhere else: on the session
// snapshot, in storage, and on screen. Declared here for the same reason every other shared type
// is, so the screen can render a task list without importing the machinery that produces one.

import "strings"

// TaskState is where one item has got to.
type TaskState string

const (
	TaskPending    TaskState = "pending"
	TaskInProgress TaskState = "in-progress"
	TaskDone       TaskState = "done"
)

// Valid reports whether s is one of the three.
func (s TaskState) Valid() bool {
	switch s {
	case TaskPending, TaskInProgress, TaskDone:
		return true
	default:
		return false
	}
}

// Glyph is the single character shown in front of an item.
//
// Single width, so a state change never shifts the column, and readable with no colour at all,
// which is the same rule every other state display in Canopy follows.
func (s TaskState) Glyph() string {
	switch s {
	case TaskDone:
		return "x"
	case TaskInProgress:
		return ">"
	default:
		return " "
	}
}

// Task is one item on an agent's list.
type Task struct {
	Text  string
	State TaskState

	// Outcome is what actually happened, recorded when the item closes.
	//
	// This is the field that makes a finished list worth reading. Without it, every completed item
	// says "done", and six lines of "done" is a progress bar, not a report. With it, the list an
	// agent leaves behind says what it found, which is the thing somebody who was not watching
	// actually wants.
	//
	// Empty on a pending or in-progress item, and empty is allowed on a finished one too. An agent
	// that has nothing to add should say nothing rather than be made to produce a sentence.
	Outcome string
}

// TaskReporter is implemented by a tool that maintains a task list.
//
// The engine pulls the list through this after each tool call rather than having the tool push it.
// A push would mean the tool holding a reference to the session it belongs to, which is backwards:
// tools are given to sessions, not the other way round, and the one that knew about its session
// would be the one that breaks when an agent is moved into a worktree.
type TaskReporter interface {
	Tasks() []Task
}

// Tasks returns the task list of whichever registered tool keeps one.
//
// Empty when no tool does, which is the normal state for an agent that has not been given the task
// tool, and is rendered as no pane rather than as an empty one.
func (r *ToolRegistry) Tasks() []Task {
	for _, name := range r.order {
		if reporter, ok := r.tools[name].(TaskReporter); ok {
			return reporter.Tasks()
		}
	}
	return nil
}

// TaskSummary is the one line form, for a row in a list of agents.
//
// Says where the work is rather than how much is left. "3/7 wiring the poller" answers what an
// agent is doing right now; "4 remaining" answers a question nobody asked.
func TaskSummary(tasks []Task) string {
	if len(tasks) == 0 {
		return ""
	}

	var done int
	current := ""
	for _, task := range tasks {
		if task.State == TaskDone {
			done++
		}
		if task.State == TaskInProgress && current == "" {
			current = task.Text
		}
	}
	if current == "" {
		// Nothing in progress: either finished or between items, and the counts say which.
		return itoa(done) + " of " + itoa(len(tasks)) + " done"
	}
	return itoa(done+1) + "/" + itoa(len(tasks)) + " " + current
}

// TasksEqual reports whether two lists are the same.
//
// Used to decide whether a change is worth publishing. Without it every tool call would notify the
// interface that the task list had changed, including the great majority that never touch it, and
// the notification is what makes the whole screen redraw.
func TasksEqual(a, b []Task) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TrimTask normalises an item's text.
func TrimTask(s string) string { return strings.TrimSpace(strings.ReplaceAll(s, "\n", " ")) }
