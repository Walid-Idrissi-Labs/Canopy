package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// borderTop is the top left corner of the frame the blocks above the message box share. Written as
// the character the renderer emits, so the assertion is about what a terminal receives.
const borderTop = "\u256d"

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

// The three states have to be three different things to look at, or the block is a list of items
// that all read the same and the colour is decoration.
//
// Asserted on what a terminal would actually receive rather than on the style values, because the
// question is whether the rows differ on screen. Under go test lipgloss finds no terminal and
// strips every colour, which is why the profile is forced here and put back afterwards.
func TestTheThreeTaskStatesAreThreeDifferentRows(t *testing.T) {
	saved := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(saved)

	engine := &fakeEngine{session: withTasks(
		core.Task{Text: "finished thing", State: core.TaskDone},
		core.Task{Text: "current thing", State: core.TaskInProgress},
		core.Task{Text: "later thing", State: core.TaskPending},
	)}

	colours := map[string]string{}
	for _, line := range strings.Split(model(engine).Body(), "\n") {
		for _, text := range []string{"finished thing", "current thing", "later thing"} {
			if strings.Contains(plain(line), text) {
				colours[text] = escapes(line)
			}
		}
	}

	if len(colours) != 3 {
		t.Fatalf("found %d of the three rows: %+v", len(colours), colours)
	}
	if colours["finished thing"] == colours["later thing"] {
		t.Errorf("a done row and a pending row are drawn identically: %q", colours["later thing"])
	}
	if colours["current thing"] == colours["later thing"] {
		t.Errorf("the row in progress looks the same as the ones not started: %q", colours["current thing"])
	}
	if colours["current thing"] == colours["finished thing"] {
		t.Errorf("the row in progress looks the same as the finished one: %q", colours["current thing"])
	}
}

// escapes is the styling of a line with the text taken out, which is what "drawn differently" means
// once the words are the same shape.
func escapes(line string) string {
	var b strings.Builder
	var inEscape bool
	for _, r := range line {
		switch {
		case r == 0x1b:
			inEscape = true
			b.WriteRune(r)
		case inEscape:
			b.WriteRune(r)
			if r == 'm' {
				inEscape = false
			}
		}
	}
	return b.String()
}

// The list is a block with a frame, wearing the chrome the btw panel wears, because both are
// standing notes above the box rather than part of what the agent said.
func TestTheTaskListIsABlockAndNotLooseLines(t *testing.T) {
	engine := &fakeEngine{session: withTasks(
		core.Task{Text: "one", State: core.TaskInProgress},
	)}
	body := plain(model(engine).Body())

	if !strings.Contains(body, "tasks") {
		t.Errorf("the block is not labelled:\n%s", body)
	}
	// The same frame the btw panel draws, which is the claim worth holding: one function draws both.
	if !strings.Contains(body, borderTop) {
		t.Errorf("the task list has no frame around it:\n%s", body)
	}
}

// Both blocks at once is two framed panels over one message box, on a screen whose whole layout
// argument is that the conversation wins ties. The btw stands in the tasks' place and gives it back.
func TestTheBtwPanelStandsInTheTaskBlocksPlace(t *testing.T) {
	engine := &fakeEngine{
		session:   withTasks(core.Task{Text: "the task item", State: core.TaskInProgress}),
		asideText: "because the poller is slow",
	}
	m := model(engine)

	if !strings.Contains(plain(m.Body()), "the task item") {
		t.Fatalf("the tasks are not on screen to begin with:\n%s", plain(m.Body()))
	}

	m, cmd := run(m, "/btw why is it slow")
	if cmd == nil {
		t.Fatal("/btw produced no command")
	}
	m, _ = m.Update(cmd())

	opened := plain(m.Body())
	if !strings.Contains(opened, "because the poller is slow") {
		t.Fatalf("the btw panel did not open:\n%s", opened)
	}
	if strings.Contains(opened, "the task item") {
		t.Errorf("both blocks are up at once:\n%s", opened)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	closed := plain(m.Body())
	if !strings.Contains(closed, "the task item") {
		t.Errorf("closing the btw did not bring the tasks back:\n%s", closed)
	}
	if strings.Contains(closed, "because the poller is slow") {
		t.Errorf("the btw panel is still up after esc:\n%s", closed)
	}
}

// Whichever block is up takes its rows from the conversation, so the message box stays on the floor
// of the frame in either state.
func TestEitherBlockLeavesTheFrameIntact(t *testing.T) {
	engine := &fakeEngine{
		session: withTasks(
			core.Task{Text: "one", State: core.TaskDone, Outcome: "it was already there"},
			core.Task{Text: "two", State: core.TaskInProgress},
			core.Task{Text: "three", State: core.TaskPending},
		),
		asideText: strings.Repeat("a long answer that wraps several times over. ", 12),
	}
	m := model(engine)
	m.SetSize(80, 24)

	withTaskBlock := m.Body()

	m, cmd := run(m, "/btw why")
	m, _ = m.Update(cmd())
	withBtw := m.Body()

	for name, body := range map[string]string{"tasks": withTaskBlock, "btw": withBtw} {
		lines := strings.Split(body, "\n")
		if len(lines) > 24 {
			t.Errorf("with the %s block up the body is %d rows in a 24 row frame", name, len(lines))
		}
		for _, line := range lines {
			if width := len([]rune(plain(line))); width > 80 {
				t.Errorf("with the %s block up a line is %d columns: %q", name, width, plain(line))
			}
		}
	}
}
