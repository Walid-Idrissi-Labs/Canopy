package chat_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

var at = time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

// fakeEngine answers with whatever a test puts in it, so these tests are about what reaches the
// screen rather than about conversations.
type fakeEngine struct {
	tools *core.ToolRegistry

	usingKey   string
	usingModel string

	session    core.Session
	sent       []string
	cancelled  int
	sendErr    error
	compacted  int
	compactErr error
	applied    []session.CompactionResult
	prompt     *session.Prompt
	answers    [][2]bool
	trust      core.TrustLevel
	undone     []string
	undoErr    error

	forkedThrough string
	trail         *permission.Trail

	// mode is what has been chosen, and trust is the ceiling it cannot be raised above. Empty trust
	// means no ceiling, which is what most of these tests want.
	mode           core.Mode
	steered        []string
	queuedSteering []string
	asked          []string
	asideErr       error
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

func (e *fakeEngine) Compact(context.Context, string) (session.CompactionResult, error) {
	e.compacted++
	if e.compactErr != nil {
		return session.CompactionResult{}, e.compactErr
	}
	return session.CompactionResult{
		Summary: "we agreed on bcrypt", Through: 4, TokensBefore: 8000, TokensAfter: 900,
	}, nil
}

func (e *fakeEngine) Apply(_ string, result session.CompactionResult) error {
	e.applied = append(e.applied, result)
	return nil
}

func (e *fakeEngine) Pending(string) (session.Prompt, bool) {
	if e.prompt == nil {
		return session.Prompt{}, false
	}
	return *e.prompt, true
}

func (e *fakeEngine) Answer(_ string, approved, remember bool) bool {
	e.answers = append(e.answers, [2]bool{approved, remember})
	e.prompt = nil
	return true
}

// The mode is what the box shows and what the permission layer decides against, so the fake holds a
// real one rather than answering a constant: a stub that always said "build" would make the
// indicator untestable.
//
// The ceiling is honoured too, since "a keystroke can never give an agent more than its
// configuration allows" is the property most worth being able to test.
func (e *fakeEngine) Mode(string) core.Mode {
	if e.mode.Name != "" {
		return e.mode
	}
	if e.trust != "" {
		return core.ModeForTrust(e.trust)
	}
	// No ceiling configured, so the ordinary default rather than the top of the ladder.
	return core.ModeForTrust(core.TrustStandard)
}

func (e *fakeEngine) SetMode(_ string, mode core.Mode) error {
	if e.trust != "" && !e.trust.AtLeast(mode.Trust) {
		return fmt.Errorf("this agent is %s, so it cannot be put in %s mode", e.trust, mode.Name)
	}
	e.mode = mode
	return nil
}

func (e *fakeEngine) Fork(_, throughTurnID string) (core.Session, error) {
	e.forkedThrough = throughTurnID
	return core.Session{ID: "session-9"}, nil
}

// Nil is a legitimate answer and the one most of these tests want: a conversation with no tools
// attached has nothing recording, and the commands that read the trail have to say so rather than
// falling over.
func (e *fakeEngine) Trail() *permission.Trail { return e.trail }

func (e *fakeEngine) Undo(_ context.Context, _, turnID string) error {
	if e.undoErr != nil {
		return e.undoErr
	}
	e.undone = append(e.undone, turnID)
	return nil
}

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
//
// What to press is not checked here any more, because it is not on this screen: the frame's footer
// owns it and this renders the body alone. It is asserted at the level where both are on screen at
// once, in TestWithKeysOpensOnChat.
func TestTheEmptyScreenIntroducesItself(t *testing.T) {
	view := plain(model(&fakeEngine{}).Body())

	for _, want := range []string{"Canopy", "myproject", "claude"} {
		if !strings.Contains(view, want) {
			t.Errorf("the opening screen does not mention %q:\n%s", want, view)
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

func TestSlashCommandsExpandAtTheInputBoundary(t *testing.T) {
	engine := &fakeEngine{}
	m := model(engine)
	m.SetCommands(config.ResolveCommands(nil, []config.Command{{
		Name: "review", Description: "review one area", Prompt: "Review carefully:\n$ARGUMENTS",
	}}))
	m = typeText(m, "/review auth and $(not-a-shell)")

	m = press(m, tea.KeyEnter)
	if len(engine.sent) != 1 || engine.sent[0] != "Review carefully:\nauth and $(not-a-shell)" {
		t.Errorf("engine received %q", engine.sent)
	}

	// The reusable invocation, not its expanded body, is what up-arrow recalls.
	m = press(m, tea.KeyUp)
	if got := m.InputValue(); got != "/review auth and $(not-a-shell)" {
		t.Errorf("history recalled %q", got)
	}
}

func TestUnknownSlashCommandsStayInTheBoxAndNeverReachTheModel(t *testing.T) {
	engine := &fakeEngine{}
	m := typeText(model(engine), "/typo an argument")

	m = press(m, tea.KeyEnter)
	if len(engine.sent) != 0 {
		t.Errorf("unknown command reached the model: %v", engine.sent)
	}
	if m.InputValue() != "/typo an argument" {
		t.Errorf("the invocation was lost: %q", m.InputValue())
	}
	if !strings.Contains(plain(m.Body()), "unknown command /typo") {
		t.Errorf("the error does not explain what happened:\n%s", plain(m.Body()))
	}
}

func TestDoubleSlashSendsALiteralSlashPrompt(t *testing.T) {
	engine := &fakeEngine{}
	m := press(typeText(model(engine), "//not-a-command"), tea.KeyEnter)

	if len(engine.sent) != 1 || engine.sent[0] != "/not-a-command" {
		t.Errorf("sent %v", engine.sent)
	}
	if !m.InputEmpty() {
		t.Errorf("successful escaped prompt remained in the box: %q", m.InputValue())
	}
}

func TestCommandsListsActiveDefinitionsWithoutCallingTheModel(t *testing.T) {
	engine := &fakeEngine{}
	m := model(engine)
	m.SetCommands(config.ResolveCommands(
		[]config.Command{{Name: "explain", Description: "explain it", Prompt: "explain"}},
		[]config.Command{{Name: "review", Description: "review it", Prompt: "review"}},
	))

	m = press(typeText(m, "/commands"), tea.KeyEnter)
	if len(engine.sent) != 0 {
		t.Errorf("the built-in listing reached the model: %v", engine.sent)
	}
	for _, want := range []string{"/explain", "global", "/review", "project"} {
		if !strings.Contains(m.Notice(), want) {
			t.Errorf("listing %q does not contain %q", m.Notice(), want)
		}
	}
}

// Tab takes whatever the list is pointing at.
//
// This used to complete only when exactly one command matched, and print a row of names when more
// than one did, which asks somebody to already know what they are looking for. There is a list on
// screen now, so tab has an unambiguous answer whether one command matches or six: the highlighted
// one. Which one that is, and how to move it, is tested in menu_test.go.
func TestTabTakesTheHighlightedCommand(t *testing.T) {
	withTwo := func() chat.Model {
		m := model(&fakeEngine{})
		m.SetCommands(config.ResolveCommands(nil, []config.Command{
			{Name: "review", Description: "review it", Prompt: "review"},
			{Name: "release", Description: "release it", Prompt: "release"},
		}))
		return m
	}

	m := press(typeText(withTwo(), "/rev"), tea.KeyTab)
	if m.InputValue() != "/review " || m.Notice() != "review it" {
		t.Errorf("one match completed to input %q notice %q", m.InputValue(), m.Notice())
	}

	// Two matches, alphabetical, so the highlight starts on release.
	m = press(typeText(withTwo(), "/re"), tea.KeyTab)
	if m.InputValue() != "/release " {
		t.Errorf("two matches completed to %q, want the highlighted one", m.InputValue())
	}

	// And down moves it before tab takes it, which is the whole point of there being a list.
	m = press(press(typeText(withTwo(), "/re"), tea.KeyDown), tea.KeyTab)
	if m.InputValue() != "/review " {
		t.Errorf("after moving down, tab took %q", m.InputValue())
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
	if !strings.Contains(plain(m.Body()), "more below") {
		t.Errorf("scrolling up should say the view is no longer following:\n%s", plain(m.Body()))
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlEnd})
	if strings.Contains(plain(m.Body()), "more below") {
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
	if strings.Contains(plain(m.Body()), "more below") {
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

// A meter that appears at eighty percent is one nobody has learned to read by the time it matters,
// and its appearance is itself alarming.
func TestTheContextMeterIsAlwaysVisible(t *testing.T) {
	nearlyEmpty := turn("t1", "hi", "hello", core.TurnComplete)
	nearlyEmpty.Usage = core.Usage{InputTokens: 100, OutputTokens: 10}
	engine := &fakeEngine{session: core.Session{
		ID: "s1", Model: "claude-opus-5", Turns: []core.Turn{nearlyEmpty},
	}}

	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	if !strings.Contains(plain(m.Context()), "context") {
		t.Errorf("the meter is missing from a near empty conversation: %q", plain(m.Context()))
	}
}

// The point of the meter is that somebody sees it coming, so the state where it matters has to say
// what to do about it.
func TestAFullContextSaysHowToFixIt(t *testing.T) {
	full := turn("t1", "hi", "hello", core.TurnComplete)
	full.Usage = core.Usage{InputTokens: 950_000, OutputTokens: 1000}
	engine := &fakeEngine{session: core.Session{
		ID: "s1", Model: "claude-opus-5", Turns: []core.Turn{full},
	}}

	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	context := plain(m.Context())
	if !strings.Contains(context, "compact") {
		t.Errorf("a nearly full context should say what to do: %q", context)
	}
}

// An agent that quietly forgets half of what it was told and carries on answering is undetectable
// from outside. This marker is what makes it detectable.
func TestACompactionIsVisibleInTheTranscript(t *testing.T) {
	turns := make([]core.Turn, 8)
	for i := range turns {
		turns[i] = turn("t", "question", "answer", core.TurnComplete)
	}
	engine := &fakeEngine{session: core.Session{
		ID: "s1", Model: "claude-opus-5", Turns: turns,
		Compactions: []core.Compaction{{
			Summary: "we agreed on bcrypt", Through: 4, At: at,
			TokensBefore: 8000, TokensAfter: 900,
		}},
	}}

	m := model(engine)
	m.SetSize(80, 60)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	view := plain(m.Body())
	if !strings.Contains(view, "summarised") {
		t.Errorf("nothing on screen says the agent no longer has the earlier turns:\n%s", view)
	}
	if !strings.Contains(view, "bcrypt") {
		t.Error("the summary itself should be readable, not just the fact that there was one")
	}
	// The obvious fear on reading that line is that the conversation has been thrown away.
	if !strings.Contains(view, "still here") {
		t.Errorf("the marker should say the turns are kept:\n%s", view)
	}
	// And what it bought, since "compacted" on its own tells nobody whether it was worth the call.
	if !strings.Contains(view, "8k") || !strings.Contains(view, "900") {
		t.Errorf("the marker should say what it saved:\n%s", view)
	}
}

// Somebody who knows they are about to paste a large file has a reason to compact before it, rather
// than waiting for the threshold and the failure that precedes it.
func TestCompactionCanBeAskedForByHand(t *testing.T) {
	turns := make([]core.Turn, 8)
	for i := range turns {
		turns[i] = turn("t", "question", "answer", core.TurnComplete)
	}
	engine := &fakeEngine{session: core.Session{ID: "s1", Model: "claude-opus-5", Turns: turns}}

	m := model(engine)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd == nil {
		t.Fatal("ctrl+r should start a compaction")
	}
	if !strings.Contains(plain(m.Body()), "summarising") {
		t.Errorf("a compaction in progress should say so, since it is a model call and takes as "+
			"long as one:\n%s", plain(m.Body()))
	}

	m, _ = m.Update(cmd())
	if engine.compacted != 1 {
		t.Errorf("the engine was asked to compact %d times, want 1", engine.compacted)
	}
	if len(engine.applied) != 1 {
		t.Fatalf("the result was applied %d times, want 1", len(engine.applied))
	}
	if engine.applied[0].Summary != "we agreed on bcrypt" {
		t.Errorf("applied = %+v", engine.applied[0])
	}
	// And the interface stops saying it is working, or the user is left watching a spinner that
	// will never resolve.
	if strings.Contains(plain(m.Body()), "summarising") {
		t.Error("the compacting indicator is still up after the compaction finished")
	}
}

// A failed compaction must not leave the interface looking like it is still working, and must say
// why rather than silently doing nothing.
func TestAFailedCompactionIsReported(t *testing.T) {
	engine := &fakeEngine{
		session:    core.Session{ID: "s1", Model: "claude-opus-5"},
		compactErr: errNotEnough{},
	}

	m := model(engine)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	m, _ = m.Update(cmd())

	if len(engine.applied) != 0 {
		t.Error("a failed compaction was applied anyway")
	}
	view := plain(m.Body())
	if !strings.Contains(view, "not enough history") {
		t.Errorf("the reason should be on screen:\n%s", view)
	}
	if strings.Contains(view, "summarising") {
		t.Error("the interface still looks like it is compacting after the compaction failed")
	}
}

type errNotEnough struct{}

func (errNotEnough) Error() string { return "there is not enough history to compact yet" }

// The reply goes through the markdown renderer and the question does not. What somebody typed is
// what they typed, and rendering their asterisks as emphasis would change their own words back at
// them.
func TestTheReplyIsRenderedAsMarkdownAndTheQuestionIsNot(t *testing.T) {
	exchange := turn("t1",
		"why does *this* not work?",
		"Because of the loop:\n\n```go\nfor i := range xs {\n    go f(i)\n}\n```\n\nUse a copy.",
		core.TurnComplete)
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{exchange}}}

	m := model(engine)
	m.SetSize(80, 40)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	view := plain(m.Body())
	if !strings.Contains(view, "for i := range xs") {
		t.Errorf("the code block is missing from the reply:\n%s", view)
	}
	// The question keeps its own asterisks, since they are the user's characters.
	if !strings.Contains(view, "*this*") {
		t.Errorf("the question was reformatted:\n%s", view)
	}
}

// A code block wider than the terminal would push the whole frame out and everything above it would
// scroll away, which reads as the program breaking rather than as a long line.
func TestALongCodeLineInAReplyStaysInsideTheFrame(t *testing.T) {
	long := "```go\nfunc x() { " + strings.Repeat("someVeryLongIdentifier + ", 40) + "0 }\n```"
	engine := &fakeEngine{session: core.Session{
		ID: "s1", Turns: []core.Turn{turn("t1", "show me", long, core.TurnComplete)},
	}}

	for _, width := range []int{60, 80, 120} {
		m := chat.New(engine, "s1", "myproject", "claude")
		m.SetSize(width, 40)
		m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

		for i, line := range strings.Split(m.Body(), "\n") {
			if got := len([]rune(plain(line))); got > width {
				t.Errorf("at width %d, line %d is %d columns:\n%s", width, i, got, plain(line))
				break
			}
		}
	}
}

func pendingPrompt(command string) *session.Prompt {
	req := permission.Request{
		AgentID: "s1", SessionID: "s1",
		Tool: "run_command", Kind: core.ToolExecute, Command: command,
	}
	return &session.Prompt{
		SessionID: "s1",
		Request:   req,
		Decision:  permission.Decide(req, core.TrustStandard, permission.NewGrants()),
	}
}

// A command summarised or truncated is a command somebody approved without having seen it.
func TestThePromptShowsTheCommandInFull(t *testing.T) {
	command := "rm -rf ./build && make clean && ./scripts/deploy.sh --production"
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{
			turn("t1", "clean up", "Let me clear the build.", core.TurnAwaitingTools),
		}},
		prompt: pendingPrompt(command),
	}

	m := model(engine)
	m.SetSize(100, 40)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	view := plain(m.Body())
	if !strings.Contains(view, command) {
		t.Errorf("the command being approved is not shown in full:\n%s", view)
	}
	// In words rather than tool names. "run a command" is something somebody can decide about at
	// two in the morning; "run_command" is a symbol from a codebase they have never read.
	if !strings.Contains(view, "run a command") {
		t.Errorf("the prompt should say what is being asked in words:\n%s", view)
	}
	if !m.Awaiting() {
		t.Error("the model should report that a question is up")
	}
}

// The reflex key on a prompt somebody has not read is enter, and enter meaning no is the difference
// between a misread prompt costing a retry and costing a repository.
func TestAnythingOtherThanYesRefuses(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune{'n'}},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeySpace},
	} {
		engine := &fakeEngine{
			session: core.Session{ID: "s1", Turns: []core.Turn{
				turn("t1", "go", "", core.TurnAwaitingTools),
			}},
			prompt: pendingPrompt("rm -rf /"),
		}
		m := model(engine)
		m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
		m.Update(key)

		if len(engine.answers) != 1 {
			t.Errorf("%v: %d answers, want 1", key, len(engine.answers))
			continue
		}
		if engine.answers[0][0] {
			t.Errorf("%v approved a command", key)
		}
	}
}

func TestYesApprovesOnceAndAlwaysApprovesWidely(t *testing.T) {
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{turn("t1", "go", "", core.TurnAwaitingTools)}},
		prompt:  pendingPrompt("make test"),
	}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	if len(engine.answers) != 1 || !engine.answers[0][0] || engine.answers[0][1] {
		t.Errorf("y gave %v, want approved once and not remembered", engine.answers)
	}

	engine.prompt = pendingPrompt("make test")
	engine.answers = nil
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	if len(engine.answers) != 1 || !engine.answers[0][0] || !engine.answers[0][1] {
		t.Errorf("a gave %v, want approved and remembered", engine.answers)
	}
}

// Typing an answer to a yes or no question into a text field and wondering why nothing happens is a
// bad minute to give somebody.
func TestAQuestionTakesTheKeyboard(t *testing.T) {
	engine := &fakeEngine{
		session: core.Session{ID: "s1", Turns: []core.Turn{turn("t1", "go", "", core.TurnAwaitingTools)}},
		prompt:  pendingPrompt("make test"),
	}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if m.InputValue() != "" {
		t.Errorf("a keystroke while a question was up went into the message box as %q",
			m.InputValue())
	}
}

func (e *fakeEngine) UseCredential(_, keyName, model string) error {
	e.usingKey, e.usingModel = keyName, model
	return nil
}

func (e *fakeEngine) Steer(_, guidance string) error {
	e.steered = append(e.steered, guidance)
	// The real engine only queues while a turn is running; steering an idle session is a send.
	if _, running := e.session.Active(); running {
		e.queuedSteering = append(e.queuedSteering, guidance)
	}
	return nil
}

func (e *fakeEngine) Steering(string) []string { return e.queuedSteering }

func (e *fakeEngine) Aside(_ context.Context, _, question string) (string, error) {
	e.asked = append(e.asked, question)
	if e.asideErr != nil {
		return "", e.asideErr
	}
	return "the parser lives in internal/config", nil
}

func (e *fakeEngine) Tools() (*core.ToolRegistry, bool) { return e.tools, e.tools != nil }
