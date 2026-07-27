package chat_test

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func withTasks(tasks ...core.Task) core.Session {
	return core.Session{
		ID:    "s1",
		Tasks: tasks,
		Turns: []core.Turn{{
			Request: core.Message{Text: "sort out the poller"},
			Text:    "working on it",
			State:   core.TurnComplete,
		}},
	}
}

// M-03. Watching an agent without a task list means reading everything it says to work out where it
// is. With one, the answer is three lines, and those three lines are most of what makes a long run
// followable.
func TestTheTaskListIsOnScreen(t *testing.T) {
	engine := &fakeEngine{session: withTasks(
		core.Task{Text: "read the poller", State: core.TaskDone, Outcome: "it polls every two seconds"},
		core.Task{Text: "add the interval flag", State: core.TaskInProgress},
		core.Task{Text: "write a test for it", State: core.TaskPending},
	)}
	body := plain(model(engine).Body())

	for _, want := range []string{"read the poller", "add the interval flag", "write a test for it"} {
		if !strings.Contains(body, want) {
			t.Errorf("the pane does not list %q:\n%s", want, body)
		}
	}
	// The outcome is the field that makes a finished list worth reading. Without it every completed
	// item says "done", and six lines of "done" is a progress bar rather than a report.
	if !strings.Contains(body, "it polls every two seconds") {
		t.Errorf("a finished item does not say what happened:\n%s", body)
	}
}

// Every state has to be tellable apart with no colour at all, which is the same rule the rest of
// the interface follows and the reason there is a second theme.
func TestTaskStatesReadWithoutColour(t *testing.T) {
	engine := &fakeEngine{session: withTasks(
		core.Task{Text: "finished thing", State: core.TaskDone},
		core.Task{Text: "current thing", State: core.TaskInProgress},
		core.Task{Text: "later thing", State: core.TaskPending},
	)}
	body := plain(model(engine).Body())

	if !strings.Contains(body, "[x] finished thing") {
		t.Errorf("a finished item is not marked:\n%s", body)
	}
	if !strings.Contains(body, "[>] current thing") {
		t.Errorf("the item in progress is not marked:\n%s", body)
	}
	if !strings.Contains(body, "[ ] later thing") {
		t.Errorf("a pending item is not marked:\n%s", body)
	}
}

// A pane that grows without bound pushes the message box off the bottom of the terminal, which is
// invisible until somebody with a long list cannot see what they are typing.
func TestALongListCollapsesRatherThanEatingTheScreen(t *testing.T) {
	var tasks []core.Task
	for i := range 20 {
		state := core.TaskPending
		if i == 3 {
			state = core.TaskInProgress
		} else if i < 3 {
			state = core.TaskDone
		}
		tasks = append(tasks, core.Task{Text: "step " + string(rune('a'+i)), State: state})
	}

	m := model(&fakeEngine{session: withTasks(tasks...)})
	body := m.Body()

	if got := strings.Count(body, "\n") + 1; got > 24 {
		t.Errorf("the screen is %d lines tall, want no more than the 24 it was given", got)
	}
	// Collapsed to where the work is, rather than cut off at an arbitrary item. The end of a list
	// is where the unfinished work lives, so truncating hides exactly the wrong half.
	if !strings.Contains(plain(body), "4/20") {
		t.Errorf("the collapsed pane does not say where the work is:\n%s", plain(body))
	}
}

// The message box has to stay on screen and stay usable whatever the list is doing.
func TestTheMessageBoxSurvivesATaskList(t *testing.T) {
	engine := &fakeEngine{session: withTasks(
		core.Task{Text: "one", State: core.TaskDone},
		core.Task{Text: "two", State: core.TaskInProgress},
		core.Task{Text: "three", State: core.TaskPending},
		core.Task{Text: "four", State: core.TaskPending},
	)}
	m := typeText(model(engine), "still typing")
	body := m.Body()

	if got := strings.Count(body, "\n") + 1; got > 24 {
		t.Errorf("the screen is %d lines tall with a task pane on it", got)
	}
	if !strings.Contains(plain(body), "still typing") {
		t.Errorf("the message box was pushed off the screen by the task pane:\n%s", plain(body))
	}
}

// No list is not an empty list. An agent that was never given the task tool should not get a blank
// rectangle where a pane would be.
func TestNoTasksMeansNoPane(t *testing.T) {
	engine := &fakeEngine{session: core.Session{
		ID:    "s1",
		Turns: []core.Turn{{Request: core.Message{Text: "hi"}, Text: "hello", State: core.TurnComplete}},
	}}
	body := plain(model(engine).Body())

	if strings.Contains(body, "[ ]") || strings.Contains(body, "tasks") {
		t.Errorf("a conversation with no task list drew a pane anyway:\n%s", body)
	}
}
