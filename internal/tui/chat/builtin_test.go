package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// The commands Canopy answers itself.

// run types a command and sends it, returning the model and whatever command came back.
func run(m chat.Model, typed string) (chat.Model, tea.Cmd) {
	return press2(typeText(m, typed), tea.KeyEnter)
}

func press2(m chat.Model, key tea.KeyType) (chat.Model, tea.Cmd) {
	return m.Update(tea.KeyMsg{Type: key})
}

// None of them reach a provider. They are answered before anything is expanded or sent, so they
// never cost a token, which is the point of `/cost` in particular not being a question for a model.
func TestBuiltinsNeverReachTheModel(t *testing.T) {
	for _, command := range config.Builtins() {
		engine := &fakeEngine{session: core.Session{ID: "s1"}}
		m := chat.New(engine, "s1", "canopy", "claude")
		m.SetSize(96, 28)

		if _, _ = run(m, "/"+command.Name); len(engine.sent) != 0 {
			t.Errorf("/%s sent %q to the model", command.Name, engine.sent)
		}
	}
}

// Undo restores the workspace from the checkpoint taken before the last turn, and leaves the
// conversation alone. Undoing the files and deleting the exchange are different things to want, and
// the record of what was tried is the half worth keeping when something did not work.
func TestUndoRestoresTheWorkspaceAndKeepsTheConversation(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		{ID: "turn-1", Request: core.Message{Text: "first"}, State: core.TurnComplete},
		{ID: "turn-2", Request: core.Message{Text: "second"}, State: core.TurnComplete},
	}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, cmd := run(m, "/undo")
	if cmd == nil {
		t.Fatal("/undo did nothing at all")
	}
	// Done off the update loop, like compaction, because restoring a checkpoint runs git over a
	// whole worktree and the frame somebody is looking at must not block on it.
	next, _ = next.Update(cmd())

	if len(engine.undone) != 1 || engine.undone[0] != "turn-2" {
		t.Errorf("undone = %v, want the last turn", engine.undone)
	}
	if got := len(next.Session().Turns); got != 2 {
		t.Errorf("the conversation lost a turn, %d remain", got)
	}
}

// And it says so plainly when there is nothing to undo, rather than reporting a success it did not
// have.
func TestUndoWithNothingToUndoSaysSo(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, _ := run(m, "/undo")
	if len(engine.undone) != 0 {
		t.Errorf("something was undone in an empty conversation: %v", engine.undone)
	}
	if view := plain(next.Body()); !strings.Contains(view, "nothing to undo") {
		t.Errorf("the screen does not say there was nothing to undo:\n%s", view)
	}
}

// Cost has three states and the middle one is the one that matters: a real token count with an
// unknown price. Printing a zero there would be a figure somebody could budget against.
func TestCostSaysWhenThePriceIsNotKnown(t *testing.T) {
	turn := core.Turn{ID: "t1", Request: core.Message{Text: "hello"}, State: core.TurnComplete,
		Usage: core.Usage{InputTokens: 100, OutputTokens: 50}}

	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{turn}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, _ := run(m, "/cost")
	view := plain(next.Body())
	if !strings.Contains(view, "150 tokens") {
		t.Errorf("the token count is missing:\n%s", view)
	}
	if !strings.Contains(view, "cost unknown") {
		t.Errorf("an unpriced provider did not say the cost is unknown:\n%s", view)
	}
	if strings.Contains(view, "$0.0000") {
		t.Errorf("an unknown cost was printed as zero:\n%s", view)
	}
}

// The ones the chat cannot do for itself are asked of the screen around it, rather than being done
// here. Which screen is in front has always been the application's decision, and a chat screen that
// could navigate would be the second place that decision was made.
func TestNavigatingCommandsAskTheApplication(t *testing.T) {
	for _, tc := range []struct{ typed, action string }{
		{"/help", chat.ActionHelp},
		{"/new", chat.ActionNew},
		{"/agents", chat.ActionAgents},
		{"/keys", chat.ActionKeys},
	} {
		m := chat.New(&fakeEngine{session: core.Session{ID: "s1"}}, "s1", "canopy", "claude")
		m.SetSize(96, 28)

		_, cmd := run(m, tc.typed)
		if cmd == nil {
			t.Errorf("%s asked for nothing", tc.typed)
			continue
		}
		action, ok := cmd().(chat.ActionMsg)
		if !ok || action.Action != tc.action {
			t.Errorf("%s asked for %#v, want %q", tc.typed, cmd(), tc.action)
		}
	}
}

// A command file cannot redefine one. Somebody typing /undo wants their workspace back, and a
// repository quietly making that mean something else is the one surprise worth forbidding outright
// rather than resolving by precedence.
func TestACommandFileCannotRedefineABuiltin(t *testing.T) {
	err := config.Project{Commands: []config.Command{
		{Name: "undo", Description: "not the real one", Prompt: "x"},
	}}.Validate()

	if err == nil {
		t.Fatal("a command file was allowed to redefine /undo")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("the refusal does not say the name is reserved: %v", err)
	}
}
