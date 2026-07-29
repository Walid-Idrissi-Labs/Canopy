package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// Another agent's permission prompt, surfaced on the conversation somebody is actually sitting on.
// D-47: it may reach you anywhere, and only a deliberate keystroke lets your hand answer it.

// waitingOn is a question raised by an agent that is not the one on screen.
func waitingOn(agent, sessionID, command string) session.Waiting {
	req := permission.Request{
		AgentID: sessionID, SessionID: sessionID,
		Tool: "run_command", Kind: core.ToolExecute, Command: command,
	}
	return session.Waiting{
		SessionID: sessionID,
		Agent:     agent,
		Request:   req,
		Decision:  permission.Decide(req, core.TrustStandard, permission.NewGrants()),
	}
}

// visited builds a conversation with somebody else's questions waiting on it.
func visited(waiting ...session.Waiting) (*fakeEngine, chat.Model) {
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{turn("t1", "hello", "hi", core.TurnComplete)}},
		waiting: waiting,
	}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	return engine, m
}

// The panel says who is asking and what approving would cover, because "an agent needs you" with no
// name is a message that sends somebody to look through every screen they have open.
func TestASubagentsQuestionAppearsWithItsNameAndScope(t *testing.T) {
	_, m := visited(waitingOn("worker-2", "s2", "npm test"))

	view := plain(m.Body())
	for _, want := range []string{"worker-2", "needs you", "npm test", "ctrl+g"} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel is missing %q:\n%s", want, view)
		}
	}

	// The scope is what the always key would cover, so it belongs on the line that offers it, which
	// is only drawn once the panel has the keyboard.
	focused, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	scope := waitingOn("worker-2", "s2", "npm test").Scope().String()
	if !strings.Contains(plain(focused.Body()), scope) {
		t.Errorf("the focused panel does not say what always would cover (%q):\n%s",
			scope, plain(focused.Body()))
	}
}

// The whole of D-47 in one test: a keystroke aimed at your own conversation must never spend another
// agent's permission, so everything you would ordinarily type answers nothing.
func TestTypingAndSendingAnswersNobodyElsesQuestion(t *testing.T) {
	engine, m := visited(waitingOn("worker-2", "s2", "rm -rf build"))

	m = typeText(m, "y")
	m = typeText(m, "a")
	m = typeText(m, "carry on with the refactor")
	m = press(m, tea.KeyEnter)

	if len(engine.answered) != 0 {
		t.Errorf("typing answered somebody else's question: %+v", engine.answered)
	}
	if len(engine.sent) != 1 {
		t.Fatalf("the message did not reach this conversation: %+v", engine.sent)
	}
	// And the question is still there afterwards, which is what makes it safe to keep working.
	if !strings.Contains(plain(m.Body()), "worker-2") {
		t.Errorf("the panel left without anybody answering it:\n%s", plain(m.Body()))
	}
}

// The focus key is the step between seeing and answering, and after it y approves exactly the agent
// that asked rather than the conversation on screen.
func TestFocusingThenYesApprovesTheAgentThatAsked(t *testing.T) {
	engine, m := visited(waitingOn("worker-2", "s2", "npm test"))

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if len(engine.answered) != 1 {
		t.Fatalf("%d questions were answered", len(engine.answered))
	}
	answer := engine.answered[0]
	if answer.session != "s2" {
		t.Errorf("the answer went to %q, want the session that asked", answer.session)
	}
	if !answer.approved || answer.remember {
		t.Errorf("y gave %+v, want approved once", answer)
	}
	if strings.Contains(plain(m.Body()), "worker-2") {
		t.Errorf("the panel is still up after the question was answered:\n%s", plain(m.Body()))
	}
}

// The other two answers, and the one that changes nothing. Refusing has to be reachable or the only
// way to say no to another agent is to walk to its screen.
func TestTheFocusedPanelAlwaysAndRefuseAndLeaveIt(t *testing.T) {
	for _, run := range []struct {
		key      rune
		approved bool
		remember bool
	}{
		{'a', true, true},
		{'n', false, false},
	} {
		engine, m := visited(waitingOn("worker-2", "s2", "npm test"))
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{run.key}})

		if len(engine.answered) != 1 {
			t.Fatalf("%c answered %d questions", run.key, len(engine.answered))
		}
		got := engine.answered[0]
		if got.approved != run.approved || got.remember != run.remember {
			t.Errorf("%c gave %+v", run.key, got)
		}
	}

	// Esc hands the keyboard back without answering, and typing goes to the box again.
	engine, m := visited(waitingOn("worker-2", "s2", "npm test"))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = typeText(m, "y")

	if len(engine.answered) != 0 {
		t.Errorf("leaving the panel answered something: %+v", engine.answered)
	}
	if m.InputValue() != "y" {
		t.Errorf("after esc the keyboard is still the panel's, the box holds %q", m.InputValue())
	}
}

// Several waiting is the case this is really for: a fan out where three agents hit the same prompt.
// The oldest is shown, the rest are counted, and answering advances rather than clearing the lot.
func TestTwoWaitingShowTheOldestAndACountAndAnsweringAdvances(t *testing.T) {
	engine, m := visited(
		waitingOn("worker-1", "s2", "npm test"),
		waitingOn("worker-2", "s3", "rm -rf build"),
	)

	view := plain(m.Body())
	if !strings.Contains(view, "worker-1") {
		t.Errorf("the oldest question is not the one on screen:\n%s", view)
	}
	if !strings.Contains(view, "worker-2 is waiting") {
		t.Errorf("the second agent is not counted:\n%s", view)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if engine.answered[0].session != "s2" {
		t.Errorf("the first answer went to %q", engine.answered[0].session)
	}
	if next := plain(m.Body()); !strings.Contains(next, "worker-2") {
		t.Errorf("the next question did not come forward:\n%s", next)
	}

	// The keyboard goes back to the conversation with it, and the one behind needs its own ctrl+g.
	// Two keystrokes per agent rather than one, on purpose: focus is consent to answer one question,
	// and a panel that inherited it would spend the next keystroke on an agent nobody agreed to.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 1 {
		t.Fatalf("the focus was inherited by the next question: %+v", engine.answered)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 2 || engine.answered[1].session != "s3" {
		t.Errorf("the second answer went to %+v", engine.answered)
	}
}

// Your own question outranks a visitor and keeps exactly the shape it had, and the count of who else
// is waiting stays visible so the second agent is not hidden behind the first.
func TestYourOwnQuestionComesFirstAndTheOthersAreStillCounted(t *testing.T) {
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{turn("t1", "go", "", core.TurnAwaitingTools)}},
		prompt:  pendingPrompt("make test"),
		waiting: []session.Waiting{waitingOn("worker-2", "s2", "npm test")},
	}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	view := plain(m.Body())
	if !strings.Contains(view, "make test") {
		t.Errorf("this conversation's own question is not on screen:\n%s", view)
	}
	if !strings.Contains(view, "worker-2 is waiting") {
		t.Errorf("the other agent is not counted while the own prompt is up:\n%s", view)
	}

	// And the own prompt still answers exactly as it did before any of this existed: y approves it,
	// and the visitor is untouched.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answers) != 1 || !engine.answers[0][0] {
		t.Errorf("the own prompt did not take the y: %+v", engine.answers)
	}
	if len(engine.answered) != 1 || engine.answered[0].session != "s1" {
		t.Errorf("y answered %+v, want this conversation's own question", engine.answered)
	}
}

// The panel takes its rows from the conversation like every other block above the box, or the
// message box walks off the bottom of a short terminal exactly when somebody needs it.
func TestTheQuestionPanelDoesNotOverflowTheFrame(t *testing.T) {
	_, m := visited(
		waitingOn("worker-1", "s2", strings.Repeat("a very long command ", 20)),
		waitingOn("worker-2", "s3", "rm -rf build"),
	)
	m.SetSize(80, 24)

	focused, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	for _, body := range []string{m.Body(), focused.Body()} {
		lines := strings.Split(body, "\n")
		if len(lines) > 24 {
			t.Errorf("the body is %d rows tall in a 24 row frame", len(lines))
		}
		for _, line := range lines {
			if width := len([]rune(plain(line))); width > 80 {
				t.Errorf("a line is %d columns wide: %q", width, plain(line))
			}
		}
	}
}

// The scenario D-47 exists to forbid, reproduced end to end.
//
// Focus another agent's question, have your own arrive and take precedence, answer yours with y, and
// then type an ordinary sentence. The leading y of that sentence used to approve the command the
// subagent was waiting on, because focus was cleared on esc, on an emptied queue and on nothing
// else: your own prompt took the screen and the panel kept the keyboard without saying so.
func TestYourOwnPromptTakesTheFocusBackFromAVisitor(t *testing.T) {
	engine, m := visited(waitingOn("worker-2", "s2", "rm -rf build"))

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if !strings.Contains(plain(m.Body()), "your keys answer this one") {
		t.Fatalf("the panel does not say it has the keyboard:\n%s", plain(m.Body()))
	}

	// This conversation is asked something of its own, which outranks a visitor.
	engine.prompt = pendingPrompt("make test")
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	// Answered the way it always was, and the y belongs to it.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 1 || engine.answered[0].session != "s1" {
		t.Fatalf("the own prompt did not take the y: %+v", engine.answered)
	}

	// And then an ordinary sentence, which begins with the same letter.
	m = typeText(m, "yes please carry on")
	m = press(m, tea.KeyEnter)

	for _, answer := range engine.answered {
		if answer.session == "s2" {
			t.Fatalf("typing spent the subagent's permission: %+v", engine.answered)
		}
	}
	if m.InputValue() != "" {
		t.Errorf("the sentence did not reach the message box, %q is left in it", m.InputValue())
	}
	if len(engine.sent) != 1 || engine.sent[0] != "yes please carry on" {
		t.Errorf("the conversation received %+v", engine.sent)
	}
	// The subagent is still waiting, untouched, which is the whole point of it not having been
	// answered by accident.
	if !strings.Contains(plain(m.Body()), "worker-2") {
		t.Errorf("the subagent's question left without anybody answering it:\n%s", plain(m.Body()))
	}
}

// And the focus key cannot arm an invisible focus while your own question is up. Every key belongs
// to your own prompt while it is on screen, which is what it has always meant: this one refuses it,
// visibly, rather than quietly claiming the keyboard for somebody else.
func TestTheFocusKeyDoesNothingWhileYourOwnQuestionIsUp(t *testing.T) {
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{turn("t1", "go", "", core.TurnAwaitingTools)}},
		prompt:  pendingPrompt("make test"),
		waiting: []session.Waiting{waitingOn("worker-2", "s2", "npm test")},
	}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})

	// It went to the own prompt, which refuses on any key that is not y or a. That is today's
	// behaviour and the point here is the other half: no focus was armed behind it.
	if len(engine.answered) != 1 || engine.answered[0].session != "s1" || engine.answered[0].approved {
		t.Fatalf("the own prompt did not treat it as a refusal: %+v", engine.answered)
	}
	m = typeText(m, "y")
	for _, answer := range engine.answered {
		if answer.session == "s2" {
			t.Fatalf("ctrl+g armed a focus while the own prompt was up: %+v", engine.answered)
		}
	}
	if m.InputValue() != "y" {
		t.Errorf("the keystroke did not reach the message box, it holds %q", m.InputValue())
	}
}

// Focus is a claim on one question, not on whatever is at the front of the queue when the key lands.
// The question somebody walked to can be answered on its own screen or from the agents view in the
// meantime, and the keystroke they were about to press must not land on whoever moved up.
func TestAFocusedQuestionAnsweredElsewhereDoesNotPassTheKeyboardOn(t *testing.T) {
	engine, m := visited(
		waitingOn("worker-1", "s2", "npm test"),
		waitingOn("worker-2", "s3", "rm -rf build"),
	)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})

	// Answered somewhere else entirely, and one ordinary event later the panel knows.
	engine.waiting = engine.waiting[1:]
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 0 {
		t.Errorf("the keystroke landed on %+v, want nothing", engine.answered)
	}

	view := plain(m.Body())
	if !strings.Contains(view, "worker-2") {
		t.Errorf("the next waiter is not on screen:\n%s", view)
	}
	if strings.Contains(view, "your keys answer this one") {
		t.Errorf("the panel inherited a focus nobody gave it:\n%s", view)
	}
	// A fresh ctrl+g is what answers the one that moved up.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 1 || engine.answered[0].session != "s3" {
		t.Errorf("the fresh focus answered %+v", engine.answered)
	}
}

// A question answered from here stays gone, rather than coming back on the next event.
//
// The engine goes on listing it until the goroutine the answer unblocked wakes up, and an event
// arrives for every agent in the project, so the panel used to redraw the answered question with the
// cursor on it: an invitation to answer something twice.
func TestAnAnsweredQuestionDoesNotComeBackOnTheNextEvent(t *testing.T) {
	engine, m := visited(
		waitingOn("worker-1", "s2", "npm test"),
		waitingOn("worker-2", "s3", "rm -rf build"),
	)

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})

	// The fake removes the answered entry, so the engine is put back to what a real one looks like
	// in the window before the parked goroutine wakes: still listing what was just answered.
	answered := append([]session.Waiting(nil), engine.waiting...)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	engine.waiting = answered

	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	view := plain(m.Body())
	if strings.Contains(view, "worker-1") {
		t.Errorf("the answered question came back on the next event:\n%s", view)
	}
	if !strings.Contains(view, "worker-2") {
		t.Errorf("the one still waiting is not on screen:\n%s", view)
	}

	// The note lasts exactly as long as the engine goes on listing the answered question, and no
	// longer. The parked goroutine wakes, the entry leaves, and the next question that agent raises
	// shows like any other rather than being suppressed by a note about the last one.
	engine.waiting = []session.Waiting{waitingOn("worker-2", "s3", "rm -rf build")}
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	engine.waiting = []session.Waiting{waitingOn("worker-1", "s2", "npm run build")}
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	view = plain(m.Body())
	if !strings.Contains(view, "worker-1") || !strings.Contains(view, "npm run build") {
		t.Errorf("a later question from the same agent is being suppressed:\n%s", view)
	}
}
