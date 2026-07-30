package agents_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/agents"
)

var at = time.Date(2026, time.July, 27, 3, 0, 0, 0, time.UTC)

type fakeEngine struct {
	statuses []session.AgentStatus
	sessions map[string]core.Session
	added    []session.Agent
	addErr   error
	answered []answeredCall
}

// answeredCall is one reply the view sent through Answer, and on whose behalf.
type answeredCall struct {
	session  string
	approved bool
	remember bool
}

func (e *fakeEngine) Answer(sessionID string, approved, remember bool) bool {
	e.answered = append(e.answered,
		answeredCall{session: sessionID, approved: approved, remember: remember})
	// The answered agent stops waiting, which is what the real engine's next status read says.
	for i := range e.statuses {
		if e.statuses[i].Agent.SessionID == sessionID {
			e.statuses[i].State = core.AgentWorking
			e.statuses[i].Waiting = ""
		}
	}
	return true
}

func (e *fakeEngine) AgentStatuses() []session.AgentStatus { return e.statuses }

func (e *fakeEngine) Session(id string) (core.Session, bool) {
	s, ok := e.sessions[id]
	return s, ok
}

func (e *fakeEngine) AddAgent(_ context.Context, agent session.Agent) (session.Agent, error) {
	if e.addErr != nil {
		return session.Agent{}, e.addErr
	}
	agent.SessionID = "s-" + agent.Name
	e.added = append(e.added, agent)
	e.statuses = append(e.statuses, session.AgentStatus{Agent: agent, State: core.AgentIdle})
	e.sessions[agent.SessionID] = conversation("nothing yet")
	return agent, nil
}

func status(name string, state core.AgentState, title string) session.AgentStatus {
	return session.AgentStatus{
		Agent: session.Agent{Name: name, SessionID: "s-" + name, KeyName: "claude"},
		State: state,
		Title: title,
		Turns: 3,
		Usage: core.Usage{InputTokens: 1000, OutputTokens: 200, CostUSD: 0.01, CostKnown: true},
	}
}

func conversation(text string) core.Session {
	return core.Session{Turns: []core.Turn{{
		ID: "t1", State: core.TurnComplete,
		Request:   core.Message{Role: core.RoleUser, Text: "what is happening"},
		Text:      text,
		StartedAt: at, EndedAt: at.Add(time.Second),
	}}}
}

func engine(statuses ...session.AgentStatus) *fakeEngine {
	e := &fakeEngine{statuses: statuses, sessions: map[string]core.Session{}}
	for _, s := range statuses {
		e.sessions[s.Agent.SessionID] = conversation("a reply from " + s.Agent.Name)
	}
	return e
}

func model(e *fakeEngine) agents.Model {
	m := agents.New(e)
	m.SetSize(100, 30)
	return m
}

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

func key(m agents.Model, s string) agents.Model {
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return m
}

// The question the list answers is what everyone is doing.
func TestTheListShowsEveryAgentAndWhatItIsDoing(t *testing.T) {
	m := model(engine(
		status("parser", core.AgentWorking, "fix the tokeniser"),
		status("docs", core.AgentIdle, "rewrite the readme"),
	))

	view := plain(m.Body())
	for _, want := range []string{"parser", "docs", "working", "idle", "fix the tokeniser"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list does not mention %q:\n%s", want, view)
		}
	}
}

// Colour and word together, never colour alone. A row of coloured dots is meaningless in a pasted
// bug report and invisible to somebody who has turned colour off.
func TestStateIsReadableWithoutColour(t *testing.T) {
	m := model(engine(
		status("blocked", core.AgentAwaitingPermission, ""),
		status("broken", core.AgentFailed, ""),
	))

	view := plain(m.Body())
	if !strings.Contains(view, "needs you") {
		t.Errorf("a blocked agent is not identifiable without colour:\n%s", view)
	}
	if !strings.Contains(view, "failed") {
		t.Errorf("a failed agent is not identifiable without colour:\n%s", view)
	}
}

// A blocked agent's most useful fact is what it is blocked on.
func TestABlockedAgentSaysWhatItIsWaitingFor(t *testing.T) {
	blocked := status("blocked", core.AgentAwaitingPermission, "some task")
	blocked.Waiting = `running "make deploy"`

	m := model(engine(blocked))
	if !strings.Contains(plain(m.Body()), "make deploy") {
		t.Errorf("the list does not say what it is waiting on:\n%s", plain(m.Body()))
	}
}

// The count of who needs a person was here, and is in the frame's header now, on every screen
// rather than on this one. This asserts it left rather than being written twice: the header counts
// every conversation waiting on somebody and this can only ever count agents, so two numbers a row
// apart would eventually disagree about the same question.
func TestTheAgentsContextCountsTheAgentsAndLeavesNeedsYouToTheHeader(t *testing.T) {
	m := model(engine(
		status("a", core.AgentIdle, ""),
		status("b", core.AgentAwaitingPermission, ""),
		status("c", core.AgentFailed, ""),
	))

	context := plain(m.Context())
	if !strings.Contains(context, "3 agents") {
		t.Errorf("context = %q, want the total", context)
	}
	if strings.Contains(context, "need you") {
		t.Errorf("context = %q, and the header says this from every screen now", context)
	}
}

// The ordering moves as agents change state, and a cursor holding an index would follow the
// position rather than the agent somebody was looking at.
func TestTheCursorFollowsTheAgentNotThePosition(t *testing.T) {
	e := engine(
		status("first", core.AgentIdle, ""),
		status("second", core.AgentIdle, ""),
		status("third", core.AgentIdle, ""),
	)
	m := model(e)

	m = key(m, "j") // onto "second"
	if selected, _ := m.Selected(); selected.Agent.Name != "second" {
		t.Fatalf("selected %q, want second", selected.Agent.Name)
	}

	// "third" starts needing attention and jumps to the top, as AgentStatuses orders it.
	e.statuses = []session.AgentStatus{
		status("third", core.AgentAwaitingPermission, ""),
		status("first", core.AgentIdle, ""),
		status("second", core.AgentIdle, ""),
	}
	m, _ = m.Update(struct{}{}) // any non key message refreshes

	if selected, _ := m.Selected(); selected.Agent.Name != "second" {
		t.Errorf("the cursor moved to %q when the list reordered, want it still on second",
			selected.Agent.Name)
	}
}

// The same index is now a different agent, and acting on it would act on the wrong one.
func TestTheCursorFallsBackWhenItsAgentDisappears(t *testing.T) {
	e := engine(status("a", core.AgentIdle, ""), status("b", core.AgentIdle, ""))
	m := model(e)

	m = key(m, "j")
	e.statuses = []session.AgentStatus{status("a", core.AgentIdle, "")}
	m, _ = m.Update(struct{}{})

	selected, ok := m.Selected()
	if !ok || selected.Agent.Name != "a" {
		t.Errorf("selected = %+v, want the remaining agent", selected)
	}
}

func TestSwitchingBetweenTheFourLayouts(t *testing.T) {
	m := model(engine(
		status("one", core.AgentWorking, "task one"),
		status("two", core.AgentIdle, "task two"),
	))

	if m.Mode() != agents.ModeList {
		t.Errorf("mode = %v, want list to be where you land", m.Mode())
	}

	// One key cycles the four, for people who would rather not remember them.
	m = key(m, "v")
	if m.Mode() != agents.ModeMosaic {
		t.Errorf("mode = %v, want mosaic", m.Mode())
	}
	// Both agents on screen at once, which is the whole point of the mosaic.
	view := plain(m.Body())
	if !strings.Contains(view, "one") || !strings.Contains(view, "two") {
		t.Errorf("the mosaic does not show both agents:\n%s", view)
	}

	m = key(m, "v")
	if m.Mode() != agents.ModeHero {
		t.Errorf("mode = %v, want hero", m.Mode())
	}

	m = key(m, "v")
	if m.Mode() != agents.ModeFocus {
		t.Errorf("mode = %v, want focus", m.Mode())
	}
	if !strings.Contains(plain(m.Body()), "a reply from one") {
		t.Errorf("focus does not show the conversation:\n%s", plain(m.Body()))
	}

	m = key(m, "v")
	if m.Mode() != agents.ModeList {
		t.Errorf("v from focus went to %v, want it to wrap round to list", m.Mode())
	}
}

// A pane squeezed below readability drops a column instead, so a narrow terminal gets a single
// column of panes rather than a torn grid.
func TestANarrowMosaicStacksInsteadOfTearing(t *testing.T) {
	m := agents.New(engine(
		status("one", core.AgentWorking, "task one"),
		status("two", core.AgentIdle, "task two"),
	))
	m.SetSize(40, 20)
	m = key(m, "v")

	for i, line := range strings.Split(m.Body(), "\n") {
		if got := len([]rune(plain(line))); got > 40 {
			t.Errorf("line %d is %d columns at width 40:\n%s", i, got, plain(line))
			break
		}
	}
}

// The frame is a fixed size, and a body wider than it corrupts the whole screen rather than just
// its own part of it.
func TestEveryLayoutFitsTheSpaceItWasGiven(t *testing.T) {
	e := engine(
		status("a-long-agent-name", core.AgentWorking, strings.Repeat("a long title ", 20)),
		status("another-one", core.AgentAwaitingPermission, strings.Repeat("more text ", 20)),
		status("third", core.AgentIdle, "short"),
	)
	e.sessions["s-a-long-agent-name"] = conversation(strings.Repeat("a very long reply ", 60))

	for _, size := range [][2]int{{60, 12}, {80, 24}, {200, 60}} {
		for presses := 0; presses < 4; presses++ {
			m := agents.New(e)
			m.SetSize(size[0], size[1])
			for i := 0; i < presses; i++ {
				m = key(m, "v")
			}

			for i, line := range strings.Split(m.Body(), "\n") {
				if got := len([]rune(plain(line))); got > size[0] {
					t.Errorf("%dx%d mode %v: line %d is %d columns:\n%s",
						size[0], size[1], m.Mode(), i, got, plain(line))
					break
				}
			}
		}
	}
}

// A title cut without a marker reads as the whole title, and somebody comparing two agents by their
// titles would be comparing two prefixes without knowing it.
func TestATruncatedTitleSaysItWasTruncated(t *testing.T) {
	m := agents.New(engine(status("agent", core.AgentIdle, strings.Repeat("very long title ", 20))))
	m.SetSize(60, 20)

	view := plain(m.Body())
	if !strings.Contains(view, "...") {
		t.Errorf("a truncated title carries no marker:\n%s", view)
	}
}

// The first thing somebody sees with nothing running should tell them what an agent is and how to
// start one.
func TestTheEmptyStateExplainsItself(t *testing.T) {
	view := plain(model(engine()).Body())

	if !strings.Contains(view, "No agents") {
		t.Errorf("the empty state does not say so:\n%s", view)
	}
	if !strings.Contains(view, "credential") {
		t.Errorf("the empty state does not say what an agent is:\n%s", view)
	}
}

func TestNavigationWraps(t *testing.T) {
	m := model(engine(
		status("one", core.AgentIdle, ""),
		status("two", core.AgentIdle, ""),
	))

	m = key(m, "k") // up from the top
	if selected, _ := m.Selected(); selected.Agent.Name != "two" {
		t.Errorf("moving up from the top gave %q, want it to wrap to the last",
			selected.Agent.Name)
	}
	m = key(m, "j")
	if selected, _ := m.Selected(); selected.Agent.Name != "one" {
		t.Errorf("moving down from the bottom gave %q, want it to wrap", selected.Agent.Name)
	}
}

// Which screen is showing belongs to the application, and a view that could change it would be one
// that can put the program somewhere the application never agreed to.
func TestOpeningAnAgentAsksRatherThanActs(t *testing.T) {
	m := model(engine(
		status("parser", core.AgentWorking, "task"),
		status("docs", core.AgentIdle, "task"),
	))
	m = key(m, "j")

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on an agent should ask to open it")
	}

	msg, ok := cmd().(agents.SwitchMsg)
	if !ok {
		t.Fatalf("enter produced %T, want a switch request", cmd())
	}
	if msg.AgentName != "docs" || msg.SessionID != "s-docs" {
		t.Errorf("switch = %+v, want the selected agent", msg)
	}
}

// The first thing somebody wants from a second agent is another of what they already have.
func TestCreatingAnAgent(t *testing.T) {
	e := engine(status("main", core.AgentIdle, "the first one"))
	m := model(e)
	m.SetDefaults("claude", "claude-opus-5", "/work/project")

	m = key(m, "n")
	if !m.Naming() {
		t.Fatal("n should start naming a new agent")
	}
	// Said plainly, because somebody naming their second agent has no other way to find out what it
	// will be using.
	if !strings.Contains(plain(m.Body()), "claude") {
		t.Errorf("the prompt does not say what the new agent will use:\n%s", plain(m.Body()))
	}

	for _, r := range "parser" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(e.added) != 0 {
		t.Fatal("enter on the name created a direct agent before showing its workspace warning")
	}
	confirmation := plain(m.Body())
	for _, visible := range []string{"Direct mode", "/work/project", "primary checkout", "not contained"} {
		if !strings.Contains(confirmation, visible) {
			t.Errorf("direct confirmation does not show %q:\n%s", visible, confirmation)
		}
	}

	m = key(m, "y")
	if len(e.added) != 1 {
		t.Fatalf("%d agents created, want 1", len(e.added))
	}
	if e.added[0].Name != "parser" {
		t.Errorf("created %q", e.added[0].Name)
	}
	if e.added[0].KeyName != "claude" || e.added[0].Model != "claude-opus-5" {
		t.Errorf("the new agent did not inherit the defaults: %+v", e.added[0])
	}
	if m.Naming() {
		t.Error("the naming prompt is still up after the agent was created")
	}
	// And the cursor lands on the agent that was just made, since that is what somebody is about to
	// do something with.
	if selected, _ := m.Selected(); selected.Agent.Name != "parser" {
		t.Errorf("selected %q after creating, want the new agent", selected.Agent.Name)
	}
}

// Both reasons this fails are things the person typing can fix in a keystroke, and clearing the box
// would make them retype a name they nearly had.
func TestAFailedCreationKeepsTheName(t *testing.T) {
	e := engine(status("main", core.AgentIdle, ""))
	e.addErr = errTaken{}
	m := model(e)

	m = key(m, "n")
	for _, r := range "main" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = key(m, "y")

	if !m.Naming() {
		t.Error("a failed creation closed the prompt")
	}
	view := plain(m.Body())
	if !strings.Contains(view, "already") {
		t.Errorf("the reason is not on screen:\n%s", view)
	}
	if !strings.Contains(view, "main") {
		t.Errorf("the typed name was lost:\n%s", view)
	}
}

func TestEscFromDirectConfirmationReturnsToTheName(t *testing.T) {
	e := engine(status("main", core.AgentIdle, ""))
	m := model(e)
	m.SetDefaults("claude", "claude-opus-5", "/work/project")

	m = key(m, "n")
	for _, r := range "parser" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = key(m, "enter")
	m = key(m, "esc")

	if len(e.added) != 0 {
		t.Fatal("going back from direct confirmation created an agent")
	}
	if !strings.Contains(plain(m.Body()), "parser") {
		t.Errorf("going back lost the name:\n%s", plain(m.Body()))
	}
}

type errTaken struct{}

func (errTaken) Error() string { return `there is already an agent called "main"` }

// An agent called "wesc" could never be typed if the navigation keys stayed live.
func TestNamingTakesTheKeyboard(t *testing.T) {
	m := model(engine(status("main", core.AgentIdle, "")))

	m = key(m, "n")
	for _, r := range "v2w" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if m.Mode() != agents.ModeList {
		t.Errorf("mode = %v, want the layout keys to have gone into the name", m.Mode())
	}
	if !strings.Contains(plain(m.Body()), "v2w") {
		t.Errorf("the keystrokes did not reach the name:\n%s", plain(m.Body()))
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.Naming() {
		t.Error("esc should cancel naming")
	}
}

// The agents view used to be built with no engine at all: nothing in the application supplied one,
// its list silently showed nothing, and creating an agent dereferenced a nil interface and took the
// whole program down with a panic.
func TestCreatingAnAgentWithoutAnEngineSaysSoRatherThanCrashing(t *testing.T) {
	var m agents.Model

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if !m.Naming() {
		t.Fatal("n did not open the name field")
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("worker")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(m.Body(), "no engine") {
		t.Errorf("the failure is not on screen:\n%s", m.Body())
	}
}

func TestAnAgentNeedsAName(t *testing.T) {
	m := agents.New(&fakeEngine{})

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !m.Naming() {
		t.Error("an empty name was accepted and the field closed")
	}
	if !strings.Contains(m.Body(), "needs a name") {
		t.Errorf("nothing says why:\n%s", m.Body())
	}
}
