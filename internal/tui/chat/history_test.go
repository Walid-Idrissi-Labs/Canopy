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

// The wheel is the conversation's, and the arrow keys are the message box's.
//
// In the alternate screen most terminals translate the wheel into arrow key sequences, which is how
// less scrolls and is fine right up until the arrow keys mean something. Once up recalled the last
// message, scrolling back to reread an answer would replace what was being typed with an old
// prompt. Canopy asks for mouse reporting so the wheel arrives as a wheel, and this is the test
// that says the two never went back to being the same thing.
func TestTheWheelScrollsAndDoesNotRecallHistory(t *testing.T) {
	engine := &fakeEngine{}
	m := press(typeText(model(engine), "an earlier prompt"), tea.KeyEnter)
	m = typeText(m, "half written")

	m = wheel(m, tea.MouseButtonWheelUp)
	m = wheel(m, tea.MouseButtonWheelUp)

	if got := m.InputValue(); got != "half written" {
		t.Errorf("scrolling changed the message box to %q", got)
	}
}

// And the other direction: the arrow keys must not scroll the conversation, or reaching for an old
// prompt would move the view out from under whatever was being read.
func TestTheArrowKeysDoNotScrollTheConversation(t *testing.T) {
	engine := &fakeEngine{session: longConversation(30)}
	m := model(engine)

	if strings.Contains(plain(m.Body()), "more lines below") {
		t.Fatal("the view does not start at the tail, so this test proves nothing")
	}

	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "question 29" {
		t.Fatalf("up recalled %q, so the keys are not doing what this test assumes", got)
	}
	if strings.Contains(plain(m.Body()), "more lines below") {
		t.Errorf("recalling a message scrolled the conversation away from the tail:\n%s",
			plain(m.Body()))
	}
}

func TestTheWheelScrollsTheConversation(t *testing.T) {
	engine := &fakeEngine{session: longConversation(40)}
	m := model(engine)

	atTail := plain(m.Body())
	scrolled := wheel(m, tea.MouseButtonWheelUp)
	if plain(scrolled.Body()) == atTail {
		t.Error("a notch of the wheel did not move the conversation")
	}
	if !strings.Contains(plain(scrolled.Body()), "more lines below") {
		t.Errorf("scrolling away from the tail is not reported:\n%s", plain(scrolled.Body()))
	}
}

// Spinning the wheel at the top must not build up a count that then takes the same number of
// notches to come back down, which reads as the scroll having stopped working.
func TestScrollingPastTheTopComesStraightBack(t *testing.T) {
	engine := &fakeEngine{session: longConversation(12)}
	m := model(engine)

	for range 50 {
		m = wheel(m, tea.MouseButtonWheelUp)
	}
	for range 20 {
		m = wheel(m, tea.MouseButtonWheelDown)
	}

	if strings.Contains(plain(m.Body()), "more lines below") {
		t.Errorf("the view is still held above the tail after scrolling back down:\n%s", plain(m.Body()))
	}
}

// A click is not a scroll. Anything that is not the wheel has to leave the view where it was.
func TestAClickDoesNothing(t *testing.T) {
	engine := &fakeEngine{session: longConversation(20)}
	m := model(engine)

	before := plain(m.Body())
	m = wheel(m, tea.MouseButtonLeft)
	if plain(m.Body()) != before {
		t.Error("a click moved the conversation")
	}
}

func wheel(m chat.Model, button tea.MouseButton) chat.Model {
	m, _ = m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: button})
	return m
}

func longConversation(turns int) core.Session {
	s := core.Session{ID: "s1"}
	for i := range turns {
		s.Turns = append(s.Turns, core.Turn{
			Request: core.Message{Text: fmt.Sprintf("question %d", i)},
			Text:    fmt.Sprintf("answer %d", i),
			State:   core.TurnComplete,
		})
	}
	return s
}
