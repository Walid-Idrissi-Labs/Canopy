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

// The panel says who is asking and what they want, but remains a summary rather than an approval
// surface. The focus key names the exact conversation whose full prompt must be opened.
func TestASubagentsQuestionAppearsWithItsNameAndScope(t *testing.T) {
	_, m := visited(waitingOn("worker-2", "s2", "npm test"))

	view := plain(m.Body())
	for _, want := range []string{"worker-2", "needs you", "npm test", "ctrl+g", "full request"} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel is missing %q:\n%s", want, view)
		}
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if cmd == nil {
		t.Fatal("ctrl+g did not ask the application to open the request")
	}
	target, ok := cmd().(chat.SwitchMsg)
	if !ok || target.SessionID != "s2" || target.AgentName != "worker-2" {
		t.Fatalf("ctrl+g targeted %#v, want worker-2's conversation", target)
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

// The focus step opens the asking conversation. Only its ordinary, full prompt may accept y, so the
// command on the wire and the command on screen cannot diverge through the compact visitor panel.
func TestFocusingOpensTheFullRequestBeforeYesCanApprove(t *testing.T) {
	const command = "deploy --region eu-west-1 --account production --confirm irreversible"
	engine, m := visited(waitingOn("worker-2", "s2", command))

	unchanged, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if cmd == nil {
		t.Fatal("ctrl+g did not produce a switch")
	}
	target := cmd().(chat.SwitchMsg)

	// The compact panel never owns y, even after the focus keystroke. Until the application applies
	// the switch, it remains ordinary text in this conversation.
	unchanged, _ = unchanged.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 0 {
		t.Fatalf("the summary panel approved a request: %+v", engine.answered)
	}

	// Simulate the application opening the destination. The engine now exposes that conversation's
	// real prompt; SetSession renders the same full request the permission layer will answer.
	engine.prompt = pendingPrompt(command)
	unchanged.SetSession(target.SessionID, target.AgentName)
	view := plain(unchanged.Body())
	if !strings.Contains(view, command) {
		t.Fatalf("the asking conversation did not show the canonical command in full:\n%s", view)
	}

	unchanged, _ = unchanged.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 1 {
		t.Fatalf("%d questions were answered", len(engine.answered))
	}
	answer := engine.answered[0]
	if answer.session != "s2" || !answer.approved || answer.remember {
		t.Errorf("y gave %+v, want worker-2 approved once", answer)
	}
}

// None of the approval alphabet is active on the compact surface. It may summarize a long request
// to protect the frame, because the only action it can take is opening the canonical prompt.
func TestTheCompactPanelNeverAcceptsAnApprovalAnswer(t *testing.T) {
	for _, key := range []rune{'y', 'a', 'n'} {
		engine, m := visited(waitingOn("worker-2", "s2", "npm test"))
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		if len(engine.answered) != 0 {
			t.Errorf("%c answered a compact visitor prompt: %+v", key, engine.answered)
		}
		if m.InputValue() != string(key) {
			t.Errorf("%c did not remain ordinary conversation input, box holds %q", key, m.InputValue())
		}
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

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	first := cmd().(chat.SwitchMsg)
	if first.SessionID != "s2" {
		t.Fatalf("the focus target was %q, want the oldest question", first.SessionID)
	}
	engine.prompt = pendingPrompt("npm test")
	m.SetSession(first.SessionID, first.AgentName)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if engine.answered[0].session != "s2" {
		t.Errorf("the first answer went to %q", engine.answered[0].session)
	}
	m.SetSession("s1", "")
	if next := plain(m.Body()); !strings.Contains(next, "worker-2") {
		t.Errorf("the next question did not come forward:\n%s", next)
	}

	// Returning to the original conversation does not focus whoever moved up. It remains ordinary
	// conversation input until a fresh ctrl+g opens that agent's own prompt.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 1 {
		t.Fatalf("the focus was inherited by the next question: %+v", engine.answered)
	}

	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	second := cmd().(chat.SwitchMsg)
	engine.prompt = pendingPrompt("rm -rf build")
	m.SetSession(second.SessionID, second.AgentName)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 2 || engine.answered[1].session != "s3" {
		t.Errorf("the second answer went to %+v", engine.answered)
	}
	m.SetSession("s1", "")
	// With the queue emptied the panel goes entirely, and the rows it was spending go back to the
	// conversation.
	if view := plain(m.Body()); strings.Contains(view, "worker-1") || strings.Contains(view, "worker-2") {
		t.Errorf("the panel is still up with nothing left in it:\n%s", view)
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

	// And with your own question dealt with, the visitor stops being a count and becomes the panel
	// again. It still only offers to open the asking conversation.
	view = plain(m.Body())
	if !strings.Contains(view, "worker-2") || !strings.Contains(view, "open the full request") {
		t.Errorf("the visitor did not come forward once the own prompt was answered:\n%s", view)
	}
	if strings.Contains(view, "y once") || strings.Contains(view, "a always") {
		t.Errorf("the compact panel offered approval keys:\n%s", view)
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

// Producing a switch message does not give the compact panel a hidden claim on later keystrokes.
// Until the application actually opens the destination, this conversation remains the sole owner
// of its prompt and input box.
func TestAQueuedSwitchCannotRouteLaterTypingToAVisitor(t *testing.T) {
	engine, m := visited(waitingOn("worker-2", "s2", "rm -rf build"))

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if cmd == nil {
		t.Fatal("ctrl+g did not produce a switch message")
	}

	// Before the application handles that message, this conversation is asked something of its own.
	engine.prompt = pendingPrompt("make test")
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if len(engine.answered) != 1 || engine.answered[0].session != "s1" {
		t.Fatalf("the own prompt did not take the y: %+v", engine.answered)
	}

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

// The switch message names the question visible when ctrl+g was pressed. If that question leaves
// before the application handles the message, it does not silently retarget whoever moved forward.
func TestAQueuedSwitchDoesNotRetargetTheNextWaitingAgent(t *testing.T) {
	engine, m := visited(
		waitingOn("worker-1", "s2", "npm test"),
		waitingOn("worker-2", "s3", "rm -rf build"),
	)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})

	// Answered somewhere else entirely, and one ordinary event later the panel knows.
	engine.waiting = engine.waiting[1:]
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	target := cmd().(chat.SwitchMsg)
	if target.SessionID != "s2" {
		t.Errorf("the queued switch retargeted %q, want the question originally shown", target.SessionID)
	}

	view := plain(m.Body())
	if !strings.Contains(view, "worker-2") {
		t.Errorf("the next waiter is not on screen:\n%s", view)
	}

	_, nextCmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	next := nextCmd().(chat.SwitchMsg)
	if next.SessionID != "s3" {
		t.Errorf("a fresh focus targeted %q, want the question now shown", next.SessionID)
	}
}

// Answering happens on the asking conversation. Returning to the original one rebuilds its compact
// queue from engine truth, and a later question from the same agent remains visible.
func TestReturningAfterAnAnswerReadsTheCurrentQueue(t *testing.T) {
	engine, m := visited(
		waitingOn("worker-1", "s2", "npm test"),
		waitingOn("worker-2", "s3", "rm -rf build"),
	)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	target := cmd().(chat.SwitchMsg)
	engine.prompt = pendingPrompt("npm test")
	m.SetSession(target.SessionID, target.AgentName)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m.SetSession("s1", "")
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	view := plain(m.Body())
	if strings.Contains(view, "worker-1") {
		t.Errorf("the answered question is still in the queue:\n%s", view)
	}
	if !strings.Contains(view, "worker-2") {
		t.Errorf("the one still waiting is not on screen:\n%s", view)
	}

	engine.waiting = []session.Waiting{waitingOn("worker-1", "s2", "npm run build")}
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	view = plain(m.Body())
	if !strings.Contains(view, "worker-1") || !strings.Contains(view, "npm run build") {
		t.Errorf("a later question from the same agent is being suppressed:\n%s", view)
	}
}
