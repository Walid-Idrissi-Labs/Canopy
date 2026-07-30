package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
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

// Fork branches the conversation and leaves the original alone, which is what makes trying a second
// approach cheap: the alternative is starting over and retyping the context that got you here.
func TestForkBranchesAtTheLastTurnAndSaysHowToReachIt(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		{ID: "turn-1", Request: core.Message{Text: "first"}, State: core.TurnComplete},
		{ID: "turn-2", Request: core.Message{Text: "second"}, State: core.TurnComplete},
	}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, _ := run(m, "/fork")
	if engine.forkedThrough != "turn-2" {
		t.Errorf("forked through %q, want the last turn", engine.forkedThrough)
	}
	// The conversation on screen is still the original. A fork that moved you into the branch would
	// make it impossible to compare the two, which is the entire reason to have branched.
	if next.SessionID() != "s1" {
		t.Errorf("forking moved the screen to %q", next.SessionID())
	}
	if view := plain(next.Body()); !strings.Contains(view, "canopy pickup 9") {
		t.Errorf("nothing on screen says how to reach the branch:\n%s", view)
	}
}

// The trail is worth having for the refusals. A permission model nobody can inspect is one nobody
// can trust, and "it did not do what I asked" is answered by a refused entry far more often than by
// anything the model said about it.
func TestTheTrailShowsRefusalsWithTheirReason(t *testing.T) {
	trail := permission.NewTrail()
	trail.Record(permission.Entry{
		AgentID: "s1", Tool: "write_file", Arguments: "internal/core/session.go",
		Outcome: permission.Deny, Reason: "changing files needs at least confined trust",
	})

	engine := &fakeEngine{session: core.Session{ID: "s1"}, trail: trail}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, _ := run(m, "/trail")
	view := plain(next.Body())
	for _, want := range []string{"write_file", "confined trust"} {
		if !strings.Contains(view, want) {
			t.Errorf("the trail does not mention %q:\n%s", want, view)
		}
	}
}

// Nothing recording is a legitimate state, not an error. A conversation with no tools attached has
// no trail, and the command has to say that rather than falling over on a nil.
func TestTheTrailSaysSoWhenNothingIsRecording(t *testing.T) {
	m := chat.New(&fakeEngine{session: core.Session{ID: "s1"}}, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, _ := run(m, "/trail")
	if view := plain(next.Body()); !strings.Contains(view, "no tool calls are being recorded") {
		t.Errorf("a conversation with no trail did not say so:\n%s", view)
	}
}

// The theme command exists because A9-03 shipped two themes and one way to reach the second, an
// environment variable read at startup. A setting you have to restart the program to change is one
// nobody tries, and a theme nobody tries is a theme that goes unmaintained.
func TestTheThemeCommandChangesThePaletteAndListsTheChoices(t *testing.T) {
	defer theme.Set(theme.Default)

	m := chat.New(&fakeEngine{session: core.Session{ID: "s1"}}, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, _ := run(m, "/theme mono")
	if got := theme.Current().Palette.Name; got != "mono" {
		t.Errorf("the palette is %q after asking for mono", got)
	}

	// With no name it says what is on and what else there is, rather than doing nothing.
	next, _ = run(next, "/theme")
	view := plain(next.Body())
	for _, want := range []string{"mono", "canopy"} {
		if !strings.Contains(view, want) {
			t.Errorf("the bare command does not mention %q:\n%s", want, view)
		}
	}
}

// The code that brings you back, said in the form you would type.
func TestPickupPrintsTheCodeForThisConversation(t *testing.T) {
	m := chat.New(&fakeEngine{session: core.Session{ID: "session-7"}}, "session-7", "canopy", "claude")
	m.SetSize(96, 28)

	next, _ := run(m, "/pickup")
	if view := plain(next.Body()); !strings.Contains(view, "canopy pickup 7") {
		t.Errorf("the pickup code is not on screen:\n%s", view)
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

// The counterpart to steering, and the difference is the whole feature. Steering changes what the
// agent does. This changes nothing: no message is sent, no turn is created, and the answer is never
// part of the conversation the next turn is built from.
func TestBtwAsksWithoutJoiningTheConversation(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		{ID: "turn-1", Request: core.Message{Text: "build the parser"}, State: core.TurnComplete},
	}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, cmd := run(m, "/btw which file holds the parser")
	if cmd == nil {
		t.Fatal("/btw asked nothing")
	}
	next, _ = next.Update(cmd())

	if len(engine.asked) != 1 || engine.asked[0] != "which file holds the parser" {
		t.Errorf("asked = %v", engine.asked)
	}
	// Nothing was sent as a message, and no turn was added.
	if len(engine.sent) != 0 {
		t.Errorf("the question was sent to the model as a message: %v", engine.sent)
	}
	if got := len(next.Session().Turns); got != 1 {
		t.Errorf("the conversation grew to %d turns", got)
	}
	if view := plain(next.Body()); !strings.Contains(view, "internal/config") {
		t.Errorf("the answer is not on screen:\n%s", view)
	}
}

// And it says what it wants when given nothing, rather than asking an empty question.
func TestBtwWithNoQuestionSaysSo(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	next, _ := run(boxedChat(engine), "/btw")

	if len(engine.asked) != 0 {
		t.Errorf("an empty question was asked: %v", engine.asked)
	}
	if view := plain(next.Body()); !strings.Contains(view, "like to know") {
		t.Errorf("the screen does not say what it wants:\n%s", view)
	}
}

func boxedChat(engine chat.Engine) chat.Model {
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 28)
	return m
}

// The answer arrives in a panel of its own, bordered and exactly as wide as the message box, rather
// than as a status line that lasted until the next keystroke.
func TestBtwAnswersArriveInABorderedPanel(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		{ID: "turn-1", Request: core.Message{Text: "build the parser"}, State: core.TurnComplete},
	}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, cmd := run(m, "/btw which file holds the parser")
	next, _ = next.Update(cmd())

	lines := strings.Split(plain(next.Body()), "\n")
	label, answer := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "btw") && strings.Contains(line, "╭") {
			label = i
		}
		if strings.Contains(line, "internal/config") {
			answer = i
		}
	}
	if label < 0 {
		t.Fatalf("no labelled border above the answer:\n%s", strings.Join(lines, "\n"))
	}
	if answer < label {
		t.Fatalf("the answer is outside the panel:\n%s", strings.Join(lines, "\n"))
	}

	// As wide as the message box, measured on the box's own top corner.
	box := -1
	for i := label + 1; i < len(lines); i++ {
		if strings.Contains(lines[i], "╭─") {
			box = i
			break
		}
	}
	if box < 0 {
		t.Fatalf("no message box under the panel:\n%s", strings.Join(lines, "\n"))
	}
	panelEnd := len([]rune(strings.TrimRight(lines[label], " ")))
	boxEnd := len([]rune(strings.TrimRight(lines[box], " ")))
	if panelEnd != boxEnd {
		t.Errorf("the panel ends at column %d and the box at %d, so they are not the same width",
			panelEnd, boxEnd)
	}
}

// Every aside asked in a conversation stays in the panel, and a bare /btw brings them back after
// the panel has been folded away.
func TestPreviousBtwsAreKeptAndABareBtwReopensThem(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		{ID: "turn-1", Request: core.Message{Text: "build the parser"}, State: core.TurnComplete},
	}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 28)

	next, cmd := run(m, "/btw where is the parser")
	next, _ = next.Update(cmd())
	next, cmd = run(next, "/btw why that library")
	next, _ = next.Update(cmd())

	view := plain(next.Body())
	for _, want := range []string{"where is the parser", "why that library"} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel lost %q:\n%s", want, view)
		}
	}

	// Esc folds it away and the questions leave the screen with it.
	closed, _ := press2(next, tea.KeyEsc)
	if strings.Contains(plain(closed.Body()), "where is the parser") {
		t.Errorf("esc did not close the panel:\n%s", plain(closed.Body()))
	}

	// A bare /btw is how they come back.
	reopened, _ := run(closed, "/btw")
	if !strings.Contains(plain(reopened.Body()), "where is the parser") {
		t.Errorf("a bare /btw did not reopen the panel:\n%s", plain(reopened.Body()))
	}
	if len(engine.asked) != 2 {
		t.Errorf("a bare /btw asked the model something: %v", engine.asked)
	}
}

// A panel taller than its window scrolls, and the scroll stops at both ends.
func TestTheBtwPanelScrollsAndStopsAtItsEnds(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		{ID: "turn-1", Request: core.Message{Text: "x"}, State: core.TurnComplete},
	}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(96, 40)

	next := m
	var cmd tea.Cmd
	for _, q := range []string{"one", "two", "three", "four", "five", "six"} {
		next, cmd = run(next, "/btw "+q)
		next, _ = next.Update(cmd())
	}

	// Six exchanges of two lines with blanks between them overflow eight rows, so the first
	// question is off the top until the panel is scrolled.
	if view := plain(next.Body()); strings.Contains(view, "? one") {
		t.Fatalf("the panel shows more than its window:\n%s", view)
	}
	for range 20 {
		next, _ = next.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if view := plain(next.Body()); !strings.Contains(view, "? one") {
		t.Errorf("scrolling up never reaches the first aside:\n%s", view)
	}
	for range 40 {
		next, _ = next.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if view := plain(next.Body()); !strings.Contains(view, "? six") {
		t.Errorf("scrolling back down never returns to the newest aside:\n%s", view)
	}
}

// The screen no longer owns the history. A conversation opened tomorrow opens with the questions
// somebody asked it today, which is what makes an aside worth asking rather than a note to lose.
func TestAsidesAreThereWhenTheConversationIsOpenedAgain(t *testing.T) {
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{
			{ID: "turn-1", Request: core.Message{Text: "build the parser"}, State: core.TurnComplete},
		}},
		asides: map[string][]session.Aside{
			"s1": {
				{Question: "where is the parser", Answer: "in internal/config"},
				{Question: "why that library", Answer: "it was already a dependency"},
			},
		},
	}

	// Opening the screen is what a restart looks like from here: a new model over the same engine.
	m := boxedChat(engine)

	// A bare /btw opens the panel over what was asked before, and asks the model nothing.
	next, _ := run(m, "/btw")
	view := plain(next.Body())
	for _, want := range []string{"where is the parser", "in internal/config", "why that library"} {
		if !strings.Contains(view, want) {
			t.Errorf("the reopened panel is missing %q:\n%s", want, view)
		}
	}
	if len(engine.asked) != 0 {
		t.Errorf("a bare /btw over a loaded history asked the model something: %v", engine.asked)
	}
}

// The asides on screen belong to the conversation on screen. Carrying them across would put answers
// about one agent's work over another agent's transcript.
func TestMovingToAnotherConversationBringsItsOwnAsides(t *testing.T) {
	engine := &fakeEngine{
		sessions: map[string]core.Session{
			"s1": {ID: "s1"},
			"s2": {ID: "s2"},
		},
		asides: map[string][]session.Aside{
			"s1": {{Question: "about the parser", Answer: "in internal/config"}},
			"s2": {{Question: "about the poller", Answer: "every two seconds"}},
		},
	}

	m := boxedChat(engine)
	m.SetSession("s2", "worker-2")

	next, _ := run(m, "/btw")
	view := plain(next.Body())
	if !strings.Contains(view, "about the poller") {
		t.Errorf("the conversation moved to does not show its own asides:\n%s", view)
	}
	if strings.Contains(view, "about the parser") {
		t.Errorf("an aside from the conversation that was left is still on screen:\n%s", view)
	}
}

// And with nothing asked anywhere it still explains itself rather than opening an empty box.
func TestABareBtwWithNoHistoryStillSaysWhatItWants(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1"}}
	next, _ := run(boxedChat(engine), "/btw")

	if view := plain(next.Body()); !strings.Contains(view, "like to know") {
		t.Errorf("a bare /btw with no history does not say what it wants:\n%s", view)
	}
}
