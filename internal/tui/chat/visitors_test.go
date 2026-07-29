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

	// The panel keeps the keyboard for the one behind it, which is what makes answering three of
	// them three keystrokes rather than six.
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
