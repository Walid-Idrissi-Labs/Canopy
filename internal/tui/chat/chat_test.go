package chat_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

var at = time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

// fakeEngine answers with whatever a test puts in it, so these tests are about what reaches the
// screen rather than about conversations.
type fakeEngine struct {
	session   core.Session
	sent      []string
	cancelled int
	sendErr   error
}

func (e *fakeEngine) Session(string) (core.Session, bool) { return e.session, true }

func (e *fakeEngine) Send(_, prompt string) (string, error) {
	if e.sendErr != nil {
		return "", e.sendErr
	}
	e.sent = append(e.sent, prompt)
	return "turn", nil
}

func (e *fakeEngine) Cancel(string) { e.cancelled++ }

func (e *fakeEngine) Events(uint64) <-chan core.Event { return make(chan core.Event) }

func model(engine chat.Engine) chat.Model {
	m := chat.New(engine, "s1", "myproject", "claude")
	m.SetSize(80, 24)
	return m
}

func typeText(m chat.Model, s string) chat.Model {
	for _, r := range s {
		if r == ' ' {
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeySpace})
			continue
		}
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func press(m chat.Model, key tea.KeyType) chat.Model {
	m, _ = m.Update(tea.KeyMsg{Type: key})
	return m
}

// plain strips styling so assertions are about content rather than escape codes.
func plain(s string) string {
	var b strings.Builder
	var inEscape bool
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case !inEscape:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func turn(id, ask, reply string, state core.TurnState) core.Turn {
	t := core.Turn{
		ID:        id,
		State:     state,
		Request:   core.Message{Role: core.RoleUser, Text: ask},
		Text:      reply,
		StartedAt: at,
	}
	if state.Terminal() {
		t.EndedAt = at.Add(time.Second)
	}
	return t
}

// The one screen where somebody is guaranteed to be looking and has not yet decided whether the
// tool is worth their time.
func TestTheEmptyScreenIntroducesItself(t *testing.T) {
	view := plain(model(&fakeEngine{}).Body())

	for _, want := range []string{"Canopy", "myproject", "claude", "Type a message"} {
		if !strings.Contains(view, want) {
			t.Errorf("the welcome screen does not mention %q:\n%s", want, view)
		}
	}
}

// The one thing that makes the rest of the program work, said up front rather than discovered when
// the first message fails.
func TestNoCredentialIsSaidPlainly(t *testing.T) {
	m := chat.New(&fakeEngine{}, "s1", "myproject", "")
	m.SetSize(80, 24)

	view := plain(m.Body())
	if !strings.Contains(view, "no credential") {
		t.Errorf("a run with no credential should say so:\n%s", view)
	}
}

func TestTypingAndSending(t *testing.T) {
	engine := &fakeEngine{}
	m := typeText(model(engine), "hello there")

	if got := m.InputValue(); got != "hello there" {
		t.Errorf("input = %q", got)
	}
	if !strings.Contains(plain(m.Body()), "hello there") {
		t.Error("what is being typed should be visible in the box")
	}

	m = press(m, tea.KeyEnter)
	if len(engine.sent) != 1 || engine.sent[0] != "hello there" {
		t.Errorf("sent = %v", engine.sent)
	}
	if m.InputValue() != "" {
		t.Errorf("the box should empty on send, got %q", m.InputValue())
	}
}

// Clearing the box on a failure would mean retyping a message because a provider was busy.
func TestAFailedSendKeepsTheMessage(t *testing.T) {
	engine := &fakeEngine{sendErr: errBusy{}}
	m := typeText(model(engine), "keep me")

	m = press(m, tea.KeyEnter)
	if m.InputValue() != "keep me" {
		t.Errorf("the message was lost on a failed send, got %q", m.InputValue())
	}
	if !strings.Contains(plain(m.Body()), "already working") {
		t.Errorf("the reason should be on screen:\n%s", plain(m.Body()))
	}
}

type errBusy struct{}

func (errBusy) Error() string { return "this session is already working on a turn" }

func TestEmptyMessagesAreNotSent(t *testing.T) {
	engine := &fakeEngine{}
	press(typeText(model(engine), "   "), tea.KeyEnter)

	if len(engine.sent) != 0 {
		t.Errorf("whitespace was sent as a message: %v", engine.sent)
	}
}

func TestBackspaceAndWordDelete(t *testing.T) {
	m := typeText(model(&fakeEngine{}), "hello world")

	m = press(m, tea.KeyBackspace)
	if m.InputValue() != "hello worl" {
		t.Errorf("backspace gave %q", m.InputValue())
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if m.InputValue() != "hello " {
		t.Errorf("ctrl+w should delete a word, got %q", m.InputValue())
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.InputValue() != "" {
		t.Errorf("ctrl+u should clear to the start, got %q", m.InputValue())
	}
}

// The conversation is read from the engine on every refresh, which is what makes a coalesced or
// dropped notification unable to lose a token.
func TestTheTranscriptComesFromTheEngine(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		turn("t1", "what is 2+2", "four", core.TurnComplete),
	}}}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{Kind: core.EventTurnUpdated}})

	view := plain(m.Body())
	if !strings.Contains(view, "what is 2+2") || !strings.Contains(view, "four") {
		t.Errorf("the exchange is not on screen:\n%s", view)
	}
}

// Every state that is not complete leaves text that reads like an answer. The label is the only
// thing between that text and somebody acting on it.
func TestPartialAnswersAreLabelled(t *testing.T) {
	cases := []struct {
		state core.TurnState
		want  string
	}{
		{core.TurnInterrupted, "stopped"},
		{core.TurnRefused, "declined"},
		{core.TurnTruncated, "cut off"},
	}

	for _, tc := range cases {
		engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
			turn("t1", "go", "this looks like a finished answer", tc.state),
		}}}
		m := model(engine)
		m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

		view := plain(m.Body())
		if !strings.Contains(view, tc.want) {
			t.Errorf("%s was not labelled on screen, so a partial reply reads as complete:\n%s",
				tc.state, view)
		}
	}
}

// A completed answer speaks for itself, and a line under every reply saying "complete" trains
// people to stop reading the ones that matter.
func TestACompletedAnswerCarriesNoLabel(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		turn("t1", "go", "here is the answer", core.TurnComplete),
	}}}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	view := plain(m.Body())
	if strings.Contains(view, "[") {
		t.Errorf("a finished answer should carry no status marker:\n%s", view)
	}
}

func TestAFailedTurnShowsWhy(t *testing.T) {
	failed := turn("t1", "go", "", core.TurnFailed)
	failed.Error = "the credential was rejected"
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{failed}}}

	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	if !strings.Contains(plain(m.Body()), "rejected") {
		t.Errorf("a failed turn should say why on screen:\n%s", plain(m.Body()))
	}
}

// With a reply streaming, escape means stop. A screen that navigated away instead would abandon a
// running turn out of sight.
func TestEscapeStopsARunningTurn(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		turn("t1", "go", "half an", core.TurnStreaming),
	}}}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	if !m.Working() {
		t.Fatal("a streaming turn should read as working")
	}
	press(m, tea.KeyEsc)
	if engine.cancelled != 1 {
		t.Errorf("escape cancelled %d times, want 1", engine.cancelled)
	}
}

func TestEscapeWithNothingRunningDoesNotCancel(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		turn("t1", "go", "done", core.TurnComplete),
	}}}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	press(m, tea.KeyEsc)
	if engine.cancelled != 0 {
		t.Error("escape with nothing running should not cancel anything")
	}
}

// A view that has silently stopped following the tail looks identical to one where nothing is
// happening.
func TestScrollingAwayFromTheTailSaysSo(t *testing.T) {
	var turns []core.Turn
	for i := 0; i < 40; i++ {
		turns = append(turns, turn("t", "question", "answer", core.TurnComplete))
	}
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: turns}}

	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if !strings.Contains(plain(m.Body()), "more lines below") {
		t.Errorf("scrolling up should say the view is no longer following:\n%s", plain(m.Body()))
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if strings.Contains(plain(m.Body()), "more lines below") {
		t.Error("ctrl+end should return to following the tail")
	}
}

// Somebody who scrolled up to read something old and then asked a question is asking about now.
func TestSendingReturnsToTheTail(t *testing.T) {
	var turns []core.Turn
	for i := 0; i < 40; i++ {
		turns = append(turns, turn("t", "question", "answer", core.TurnComplete))
	}
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: turns}}

	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})

	m = press(typeText(m, "next question"), tea.KeyEnter)
	if strings.Contains(plain(m.Body()), "more lines below") {
		t.Error("sending a message should return to the tail")
	}
}

// The frame is a fixed size, so a body wider or taller than it was given corrupts the whole
// screen rather than just its own part of it.
func TestTheBodyFitsTheSpaceItWasGiven(t *testing.T) {
	var turns []core.Turn
	for i := 0; i < 40; i++ {
		turns = append(turns, turn("t", strings.Repeat("word ", 40), strings.Repeat("x", 500),
			core.TurnComplete))
	}
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: turns}}

	for _, size := range [][2]int{{80, 24}, {60, 12}, {200, 60}} {
		m := chat.New(engine, "s1", "myproject", "claude")
		m.SetSize(size[0], size[1])
		m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

		lines := strings.Split(m.Body(), "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: body is %d lines, which pushes the footer off the screen",
				size[0], size[1], len(lines))
		}
		for i, line := range lines {
			if width := len([]rune(plain(line))); width > size[0] {
				t.Errorf("%dx%d: line %d is %d columns wide:\n%s",
					size[0], size[1], i, width, plain(line))
				break
			}
		}
	}
}

// The header is where somebody notices a session getting expensive.
func TestTheContextLineShowsSpend(t *testing.T) {
	spent := turn("t1", "go", "done", core.TurnComplete)
	spent.Usage = core.Usage{InputTokens: 1000, OutputTokens: 200, CostUSD: 0.01, CostKnown: true}
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{spent}}}

	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	context := plain(m.Context())
	if !strings.Contains(context, "1200 tokens") {
		t.Errorf("context = %q, want the token total", context)
	}
	if !strings.Contains(context, "$0.0100") {
		t.Errorf("context = %q, want the cost", context)
	}
}

// A zero rendered as a dollar figure would read as "this was free", which is a different claim
// from "we could not price it".
func TestUnpricedSpendSaysUnknown(t *testing.T) {
	spent := turn("t1", "go", "done", core.TurnComplete)
	spent.Usage = core.Usage{InputTokens: 1000, OutputTokens: 200}
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{spent}}}

	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	if !strings.Contains(plain(m.Context()), "cost unknown") {
		t.Errorf("context = %q, want an explicit unknown rather than a zero", plain(m.Context()))
	}
}
