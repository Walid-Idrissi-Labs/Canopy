package agents_test

import (
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
}

func (e *fakeEngine) AgentStatuses() []session.AgentStatus { return e.statuses }

func (e *fakeEngine) Session(id string) (core.Session, bool) {
	s, ok := e.sessions[id]
	return s, ok
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

// The header is the reason somebody opens this screen at all.
func TestTheHeaderLeadsWithWhatNeedsYou(t *testing.T) {
	m := model(engine(
		status("a", core.AgentIdle, ""),
		status("b", core.AgentAwaitingPermission, ""),
		status("c", core.AgentFailed, ""),
	))

	context := plain(m.Context())
	if !strings.Contains(context, "2 need you") {
		t.Errorf("context = %q, want the count of agents needing a person first", context)
	}
	if !strings.Contains(context, "3 agents") {
		t.Errorf("context = %q, want the total too", context)
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

func TestSwitchingBetweenTheThreeLayouts(t *testing.T) {
	m := model(engine(
		status("one", core.AgentWorking, "task one"),
		status("two", core.AgentIdle, "task two"),
	))

	if m.Mode() != agents.ModeList {
		t.Errorf("mode = %v, want list to be where you land", m.Mode())
	}

	m = key(m, "2")
	if m.Mode() != agents.ModeSplit {
		t.Errorf("mode = %v, want split", m.Mode())
	}
	// Both agents on screen at once, which is the whole point of the split.
	view := plain(m.Body())
	if !strings.Contains(view, "one") || !strings.Contains(view, "two") {
		t.Errorf("the split does not show both agents:\n%s", view)
	}

	m = key(m, "3")
	if m.Mode() != agents.ModeFocus {
		t.Errorf("mode = %v, want focus", m.Mode())
	}
	if !strings.Contains(plain(m.Body()), "a reply from one") {
		t.Errorf("focus does not show the conversation:\n%s", plain(m.Body()))
	}

	// And one key that cycles, for people who would rather not remember three.
	m = key(m, "v")
	if m.Mode() != agents.ModeList {
		t.Errorf("v from focus went to %v, want it to wrap round to list", m.Mode())
	}
}

// Twenty columns of a code discussion is not readable, so falling back is better than drawing
// something torn.
func TestASplitTooNarrowToReadFallsBackToOne(t *testing.T) {
	m := agents.New(engine(
		status("one", core.AgentWorking, "task one"),
		status("two", core.AgentIdle, "task two"),
	))
	m.SetSize(40, 20)
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})

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
		for _, mode := range []string{"1", "2", "3"} {
			m := agents.New(e)
			m.SetSize(size[0], size[1])
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(mode)})

			for i, line := range strings.Split(m.Body(), "\n") {
				if got := len([]rune(plain(line))); got > size[0] {
					t.Errorf("%dx%d mode %s: line %d is %d columns:\n%s",
						size[0], size[1], mode, i, got, plain(line))
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
