package chat_test

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// M-02. Changing one word of a prompt and sending it again is the single most common thing anybody
// does with a coding agent, and without this it means retyping the whole message.
func TestUpRecallsWhatWasSentBefore(t *testing.T) {
	engine := &fakeEngine{}
	m := model(engine)

	m = press(typeText(m, "first"), tea.KeyEnter)
	m = press(typeText(m, "second"), tea.KeyEnter)

	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "second" {
		t.Errorf("one press of up gave %q, want the message just sent", got)
	}
	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "first" {
		t.Errorf("two presses gave %q, want the one before that", got)
	}
}

// Walking past the oldest has to stop rather than wrap. Wrapping puts the newest message under a
// key somebody is holding down to get to the oldest, which is how the wrong prompt gets sent.
func TestWalkingPastTheOldestStops(t *testing.T) {
	engine := &fakeEngine{}
	m := press(typeText(model(engine), "only one"), tea.KeyEnter)

	for range 5 {
		m = press(m, tea.KeyUp)
	}
	if got := m.InputValue(); got != "only one" {
		t.Errorf("holding up gave %q", got)
	}
}

// The half typed message is the thing this feature is most likely to eat. Pressing up out of
// curiosity and losing a paragraph you were composing is what teaches people not to touch the
// arrow keys.
func TestAHalfTypedMessageComesBack(t *testing.T) {
	engine := &fakeEngine{}
	m := press(typeText(model(engine), "sent earlier"), tea.KeyEnter)
	m = typeText(m, "half written")

	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "sent earlier" {
		t.Fatalf("up gave %q", got)
	}

	m = press(m, tea.KeyDown)
	if got := m.InputValue(); got != "half written" {
		t.Errorf("coming back down gave %q, want what was being written", got)
	}
}

// Down with nothing recalled must not clear the box. The key is within reach of somebody scrolling
// and it would silently delete what they had typed.
func TestDownWithNothingRecalledLeavesTheBoxAlone(t *testing.T) {
	engine := &fakeEngine{}
	m := typeText(model(engine), "still writing")

	m = press(m, tea.KeyDown)
	if got := m.InputValue(); got != "still writing" {
		t.Errorf("down emptied the box, leaving %q", got)
	}
}

// Editing a recalled message detaches it. The alternative is an edit that silently disappears the
// next time an arrow key is pressed, which looks like the program losing your work.
func TestEditingARecalledMessageKeepsTheEdit(t *testing.T) {
	engine := &fakeEngine{}
	m := press(typeText(model(engine), "run the tests"), tea.KeyEnter)

	m = press(m, tea.KeyUp)
	m = typeText(m, " again")
	if got := m.InputValue(); got != "run the tests again" {
		t.Fatalf("input = %q", got)
	}

	m = press(m, tea.KeyDown)
	if got := m.InputValue(); got != "run the tests again" {
		t.Errorf("down threw the edit away, leaving %q", got)
	}
}

// A message the engine refused is still in the box. Filing it as well would put it on screen and in
// the history at once, so the next press of up offers a message that was never sent.
func TestARefusedMessageIsNotFiled(t *testing.T) {
	engine := &fakeEngine{sendErr: errBusy{}}
	m := press(typeText(model(engine), "went nowhere"), tea.KeyEnter)

	if got := m.InputValue(); got != "went nowhere" {
		t.Fatalf("the refused message left the box, leaving %q", got)
	}
	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "went nowhere" {
		t.Errorf("up changed the box to %q, so the refused message was filed", got)
	}
}

// Sixty is the cap, and it drops from the front. An unbounded list is a slow leak in the one part
// of the program that runs for hours.
func TestHistoryStopsAtItsLimit(t *testing.T) {
	engine := &fakeEngine{}
	m := model(engine)

	for i := range chat.HistoryLimit + 10 {
		m = press(typeText(m, fmt.Sprintf("message %d", i)), tea.KeyEnter)
	}

	// Walking all the way back reaches the oldest that survived, and no further.
	for range chat.HistoryLimit + 20 {
		m = press(m, tea.KeyUp)
	}
	want := fmt.Sprintf("message %d", 10)
	if got := m.InputValue(); got != want {
		t.Errorf("the oldest recallable message is %q, want %q", got, want)
	}
}

// Sending the same thing twice is usually a retry. Two identical entries mean two presses of up to
// get past one message, which is the small annoyance that stops people using this at all.
func TestTheSameMessageTwiceIsFiledOnce(t *testing.T) {
	engine := &fakeEngine{}
	m := model(engine)

	m = press(typeText(m, "retry"), tea.KeyEnter)
	m = press(typeText(m, "retry"), tea.KeyEnter)
	m = press(typeText(m, "different"), tea.KeyEnter)

	m = press(m, tea.KeyUp)
	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "retry" {
		t.Fatalf("input = %q", got)
	}
	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "retry" {
		t.Errorf("the duplicate was filed twice, so a third press gave %q", got)
	}
}

// History belongs to the conversation. Shared, the first press of up in one agent's conversation
// offers the message you sent to a different agent, which is at best noise and at worst sent.
func TestHistoryDoesNotFollowYouIntoAnotherConversation(t *testing.T) {
	engine := &fakeEngine{}
	m := press(typeText(model(engine), "for the first agent"), tea.KeyEnter)

	engine.session = core.Session{ID: "s2"}
	m.SetSession("s2", "other")

	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "" {
		t.Errorf("up in a fresh conversation offered %q", got)
	}
}

// Opening a conversation started yesterday has to have its history too, or the feature works only
// on conversations that happen to have been started since the program launched.
func TestAnOldConversationCanRecallItsOwnMessages(t *testing.T) {
	engine := &fakeEngine{session: core.Session{
		ID: "s1",
		Turns: []core.Turn{
			{Request: core.Message{Text: "what does this package do"}, Text: "it polls", State: core.TurnComplete},
			{Request: core.Message{Text: "add a test for it"}, Text: "done", State: core.TurnComplete},
		},
	}}

	m := model(engine)
	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "add a test for it" {
		t.Errorf("up in a resumed conversation gave %q", got)
	}
	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "what does this package do" {
		t.Errorf("the second press gave %q", got)
	}
}

// The keys have to be findable. A binding nobody knows about is a binding nobody uses.
func TestTheFooterAndTheBoxAgreeThatUpIsHistory(t *testing.T) {
	engine := &fakeEngine{}
	m := press(typeText(model(engine), "something"), tea.KeyEnter)

	m = press(m, tea.KeyUp)
	if !strings.Contains(plain(m.Body()), "something") {
		t.Error("the recalled message is not visible in the box")
	}
}
