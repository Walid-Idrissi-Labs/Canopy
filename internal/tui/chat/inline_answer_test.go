package chat_test

// Answering a visitor from where you sit. D-50 opens the compact panel as an approval surface on
// the owner's direction, under three guards these tests hold in place: only with the box empty,
// only the oldest question, and only ever once. Printable keys keep answering nothing, which is
// the half of D-47 that survives.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// Enter on an empty box approves the front of the queue, once. Remember stays false from here
// always: the panel may truncate the request, and a standing approval must come from the full
// canonical prompt, which is D-35's line unmoved.
func TestEnterOnAnEmptyBoxApprovesTheVisitorOnce(t *testing.T) {
	engine, m := visited(waitingOn("worker-2", "s2", "npm test"))

	m = press(m, tea.KeyEnter)

	if len(engine.answered) != 1 {
		t.Fatalf("enter gave %d answers, want one", len(engine.answered))
	}
	answer := engine.answered[0]
	if answer.session != "s2" || !answer.approved || answer.remember {
		t.Errorf("enter gave %+v, want s2 approved once", answer)
	}
	// Nothing was sent: the box was empty, so there was no message for enter to mean.
	if len(engine.sent) != 0 {
		t.Errorf("enter also sent a message: %+v", engine.sent)
	}
	// And the screen says what just happened, because an answered panel that simply vanishes reads
	// as a panel that broke.
	if view := plain(m.Body()); !strings.Contains(view, "approved worker-2") {
		t.Errorf("the screen does not say the request was approved:\n%s", view)
	}
}

// Backspace on an empty box declines, which costs the asking agent a retry and nothing else.
func TestBackspaceOnAnEmptyBoxDeclinesTheVisitor(t *testing.T) {
	engine, m := visited(waitingOn("worker-2", "s2", "rm -rf build"))

	m = press(m, tea.KeyBackspace)

	if len(engine.answered) != 1 {
		t.Fatalf("backspace gave %d answers, want one", len(engine.answered))
	}
	if answer := engine.answered[0]; answer.session != "s2" || answer.approved {
		t.Errorf("backspace gave %+v, want s2 declined", answer)
	}
	if view := plain(m.Body()); !strings.Contains(view, "declined worker-2") {
		t.Errorf("the screen does not say the request was declined:\n%s", view)
	}
}

// With anything in the box the keys belong to the message: enter sends and backspace deletes, and
// neither goes anywhere near the visitor. The box being non-empty is what protects a message being
// typed from spending another agent's permission mid word.
func TestATypedMessageKeepsEnterAndBackspaceToItself(t *testing.T) {
	engine, m := visited(waitingOn("worker-2", "s2", "npm test"))

	m = typeText(m, "carry on")
	m = press(m, tea.KeyBackspace)
	_ = press(m, tea.KeyEnter)

	if len(engine.answered) != 0 {
		t.Errorf("typing answered somebody else's question: %+v", engine.answered)
	}
	if len(engine.sent) != 1 || engine.sent[0] != "carry o" {
		t.Errorf("the message did not reach this conversation as typed: %+v", engine.sent)
	}
}

// The keys answer the oldest question, which is the one the panel is showing. A queue that
// answered anything else would spend a different agent's permission than the one on screen.
func TestEnterAnswersTheOldestVisitorAndTheNextComesForward(t *testing.T) {
	engine, m := visited(
		waitingOn("worker-1", "s2", "npm test"),
		waitingOn("worker-2", "s3", "rm -rf build"),
	)

	m = press(m, tea.KeyEnter)

	if len(engine.answered) != 1 || engine.answered[0].session != "s2" {
		t.Fatalf("enter answered %+v, want the oldest question", engine.answered)
	}
	// The next question comes forward, unanswered: the approval was for worker-1 alone.
	if view := plain(m.Body()); !strings.Contains(view, "worker-2") {
		t.Errorf("the next question did not come forward:\n%s", view)
	}
	_ = press(m, tea.KeyBackspace)
	if len(engine.answered) != 2 || engine.answered[1].session != "s3" ||
		engine.answered[1].approved {
		t.Errorf("backspace gave %+v, want s3 declined", engine.answered)
	}
}

// Your own question outranks a visitor on the same key: enter approves this conversation's prompt
// and the visitor is untouched, exactly as the panel's shrunken form says.
func TestYourOwnPromptTakesEnterBeforeAnyVisitor(t *testing.T) {
	engine, m := visited(waitingOn("worker-2", "s2", "npm test"))
	engine.prompt = pendingPrompt("make test")
	m, _ = m.Update(chat.EventMsg{})

	m = press(m, tea.KeyEnter)

	if len(engine.answered) != 1 {
		t.Fatalf("enter gave %d answers, want one for the own prompt", len(engine.answered))
	}
	if answer := engine.answered[0]; answer.session != "s1" || !answer.approved || answer.remember {
		t.Errorf("enter gave %+v, want this conversation approved once", answer)
	}
	// The visitor is still waiting, named on the panel's one-line form.
	if view := plain(m.Body()); !strings.Contains(view, "worker-2") {
		t.Errorf("the visitor was lost while the own prompt was up:\n%s", view)
	}
}

// The panel names the answer keys only while they are live, which is while the box is empty.
func TestThePanelNamesTheKeysOnlyWhileTheyAreLive(t *testing.T) {
	_, m := visited(waitingOn("worker-2", "s2", "npm test"))

	empty := plain(m.Body())
	for _, want := range []string{"approve once", "backspace", "ctrl+g"} {
		if !strings.Contains(empty, want) {
			t.Errorf("with the box empty the panel does not name %q:\n%s", want, empty)
		}
	}

	typing := plain(typeText(m, "half a message").Body())
	if strings.Contains(typing, "approve once") {
		t.Errorf("the panel names an answer key that typing has taken away:\n%s", typing)
	}
	if !strings.Contains(typing, "ctrl+g") {
		t.Errorf("the one key that still works is not named:\n%s", typing)
	}
}
