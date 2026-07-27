package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/agent"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// enginewithTasks builds an engine whose tool set includes a task list.
func engineWithTasks(t *testing.T) (*Engine, *agent.TodoList) {
	t.Helper()

	list := agent.NewTodoList()
	registry := core.NewToolRegistry()
	if err := registry.Register(agent.TodoTool(list)); err != nil {
		t.Fatalf("Register: %v", err)
	}

	e := New(fixedResolver{client: &scriptedClient{}, id: anthropicID()})
	t.Cleanup(e.Close)
	e.WithTools(registry, core.TrustStandard, nil)
	return e, list
}

// setTasksThrough runs the task tool the way a turn would, then asks the engine to catch up.
func setTasksThrough(t *testing.T, e *Engine, sessionID, args string) {
	t.Helper()

	e.mu.Lock()
	registry, _ := e.toolsForLocked(sessionID)
	e.mu.Unlock()

	tool, ok := registry.Get("set_tasks")
	if !ok {
		t.Fatal("there is no set_tasks tool")
	}
	result, err := tool.Run(t.Context(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("running set_tasks: %v", err)
	}
	if result.IsError {
		t.Fatalf("set_tasks refused: %s", result.Content)
	}
	e.refreshTasks(sessionID)
}

// M-03. The list has to reach the session snapshot, because the snapshot is the only thing the
// screen reads. A list that exists in the tool and nowhere else is a list nobody can see.
func TestTheTaskListReachesTheSession(t *testing.T) {
	e, _ := engineWithTasks(t)
	created := e.Create("claude", "claude-opus-5")

	setTasksThrough(t, e, created.ID, `{"tasks":[
		{"text":"read the poller","state":"done","outcome":"it polls every two seconds"},
		{"text":"add the flag","state":"in-progress"}
	]}`)

	got, ok := e.Session(created.ID)
	if !ok {
		t.Fatal("the session disappeared")
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("%d tasks on the session, want 2: %+v", len(got.Tasks), got.Tasks)
	}
	if got.Tasks[0].Outcome != "it polls every two seconds" {
		t.Errorf("the outcome did not reach the session: %q", got.Tasks[0].Outcome)
	}
	if got.Tasks[1].State != core.TaskInProgress {
		t.Errorf("the state did not reach the session: %q", got.Tasks[1].State)
	}
}

// The change has to be announced, or the screen shows the new list only when something else happens
// to redraw it, which for an agent thinking about its next step is not soon.
func TestChangingTheTaskListIsPublished(t *testing.T) {
	e, _ := engineWithTasks(t)
	created := e.Create("claude", "claude-opus-5")

	events := e.Events(0)
	setTasksThrough(t, e, created.ID, `{"tasks":[{"text":"one","state":"in-progress"}]}`)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind == core.EventSessionsChanged && event.SessionID == created.ID {
				return
			}
		case <-deadline:
			t.Fatal("changing the task list published nothing, so the screen would not redraw " +
				"until something else happened to make it")
		}
	}
}

// Almost no tool call touches the task list, and publishing on every one of them would redraw the
// whole screen on every file read.
//
// Asserted on the session rather than on the event stream, because "nothing was published" is a
// claim about an absence and waiting for an absence on a channel is how a test becomes a coin toss.
// The stamp moves only on the path that also publishes, so it answers the same question and
// answers it deterministically.
func TestAnUnchangedTaskListChangesNothing(t *testing.T) {
	e, _ := engineWithTasks(t)
	created := e.Create("claude", "claude-opus-5")

	setTasksThrough(t, e, created.ID, `{"tasks":[{"text":"one","state":"in-progress"}]}`)
	before, ok := e.Session(created.ID)
	if !ok || len(before.Tasks) != 1 {
		t.Fatalf("the list was not set, so this test proves nothing: %+v", before.Tasks)
	}

	// The same list again, which is what a turn full of file reads amounts to.
	for range 5 {
		e.refreshTasks(created.ID)
	}

	after, _ := e.Session(created.ID)
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("an unchanged task list moved the session's timestamp from %s to %s, so every "+
			"tool call would redraw the screen", before.UpdatedAt, after.UpdatedAt)
	}
}

// A session that was never given the task tool must not acquire an empty list, or every ordinary
// conversation grows a pane with nothing in it.
func TestASessionWithNoTaskToolStaysWithoutTasks(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{}, id: anthropicID()})
	t.Cleanup(e.Close)
	e.WithTools(core.NewToolRegistry(), core.TrustStandard, nil)

	created := e.Create("claude", "claude-opus-5")
	e.refreshTasks(created.ID)

	got, _ := e.Session(created.ID)
	if len(got.Tasks) != 0 {
		t.Errorf("a session with no task tool has %+v", got.Tasks)
	}
}
