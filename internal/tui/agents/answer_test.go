package agents_test

// Answering from the grid. D-50: a waiting pane may be answered where it is seen, once and never
// remembered, and the keys that answer are enter and backspace on the selected pane only.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/agents"
)

// stuck is an agent parked on a permission question, with the request a person would be shown.
func stuck(name, subject string) session.AgentStatus {
	s := status(name, core.AgentAwaitingPermission, "working on "+name)
	s.Waiting = subject
	return s
}

func press(m agents.Model, key tea.KeyType) (agents.Model, tea.Cmd) {
	return m.Update(tea.KeyMsg{Type: key})
}

// Enter on a selected waiting agent answers yes, once. Never remembered: the pane shows a summary,
// and a standing approval must come from the full canonical prompt.
func TestEnterApprovesTheSelectedWaitingAgentOnce(t *testing.T) {
	e := engine(stuck("blocked", "run_command: npm test"), status("docs", core.AgentIdle, ""))
	m := model(e)

	m, cmd := press(m, tea.KeyEnter)

	if len(e.answered) != 1 {
		t.Fatalf("enter gave %d answers, want one", len(e.answered))
	}
	answer := e.answered[0]
	if answer.session != "s-blocked" || !answer.approved || answer.remember {
		t.Errorf("enter gave %+v, want s-blocked approved once", answer)
	}
	if cmd != nil {
		if _, switched := cmd().(agents.SwitchMsg); switched {
			t.Error("enter both answered the question and opened the conversation")
		}
	}

	// With nothing waiting any more, enter goes back to meaning open, on the same keystroke a
	// person is already resting on.
	m, cmd = press(m, tea.KeyEnter)
	_ = m
	if cmd == nil {
		t.Fatal("enter did not open the agent once its question was answered")
	}
	if msg, ok := cmd().(agents.SwitchMsg); !ok || msg.AgentName != "blocked" {
		t.Errorf("enter opened %+v, want the selected agent", cmd())
	}
	if len(e.answered) != 1 {
		t.Errorf("the second enter answered again: %+v", e.answered)
	}
}

// Backspace is the other half of the popup: it declines, which costs the agent a retry and nothing
// else, and it does nothing at all when the selection is not waiting.
func TestBackspaceDeclinesTheSelectedWaitingAgent(t *testing.T) {
	e := engine(stuck("blocked", "run_command: rm -rf build"))
	m := model(e)

	m, _ = press(m, tea.KeyBackspace)

	if len(e.answered) != 1 {
		t.Fatalf("backspace gave %d answers, want one", len(e.answered))
	}
	if answer := e.answered[0]; answer.session != "s-blocked" || answer.approved {
		t.Errorf("backspace gave %+v, want s-blocked declined", answer)
	}

	// On an agent with no question, backspace decides nothing.
	if _, _ = press(m, tea.KeyBackspace); len(e.answered) != 1 {
		t.Errorf("backspace answered an agent that asked nothing: %+v", e.answered)
	}
}

// A pane can become stale between drawing and input. Answer reports that race as false, and the grid
// must not silently consume the key as a successful approval or refusal.
func TestASelectedAgentThatStoppedWaitingIsNotClaimedAsAnswered(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyType
	}{
		{name: "approve", key: tea.KeyEnter},
		{name: "decline", key: tea.KeyBackspace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine(stuck("blocked", "run_command: npm test"))
			e.staleAnswer = true
			m := model(e)

			m, _ = press(m, tc.key)

			if len(e.answered) != 0 {
				t.Fatalf("a stale request was recorded as answered: %+v", e.answered)
			}
			if !strings.Contains(m.Notice(), "no longer waiting") {
				t.Errorf("the grid does not explain that the request disappeared: %q", m.Notice())
			}
		})
	}
}

// The answer goes to the agent under the cursor, never to whoever happens to be waiting loudest.
func TestTheAnswerFollowsTheCursorNotTheQueue(t *testing.T) {
	e := engine(
		stuck("first", "run_command: npm test"),
		stuck("second", "run_command: make deploy"),
	)
	m := model(e)
	m = key(m, "j")

	if _, _ = press(m, tea.KeyEnter); len(e.answered) != 1 {
		t.Fatal("enter did not answer the selected agent")
	}
	if e.answered[0].session != "s-second" {
		t.Errorf("the answer went to %q, want the agent under the cursor", e.answered[0].session)
	}
}

// Every waiting pane pins its question, so the grid says who is stuck without being walked, and
// only the selected pane names the keys, because they answer for the selection alone.
func TestAWaitingPaneShowsItsQuestionAndTheSelectedOneNamesTheKeys(t *testing.T) {
	e := engine(
		stuck("blocked", "run_command: npm test"),
		stuck("also", "run_command: make deploy"),
	)
	m := mosaic(e, 100, 24)

	view := plain(m.Body())
	for _, want := range []string{"npm test", "make deploy", "needs you"} {
		if !strings.Contains(view, want) {
			t.Errorf("the mosaic does not show %q:\n%s", want, view)
		}
	}
	if got := strings.Count(view, "approve once"); got != 1 {
		t.Errorf("the answer keys are named on %d panes, want the selected one only:\n%s", got, view)
	}
	if !strings.Contains(view, "backspace") {
		t.Errorf("the selected pane does not name the decline key:\n%s", view)
	}
}

// The list layout answers with the same keys as the panes: one screen, one meaning per key.
func TestTheListAnswersWithTheSameKeys(t *testing.T) {
	e := engine(stuck("blocked", "run_command: npm test"))
	m := model(e)
	if m.Mode() != agents.ModeList {
		t.Fatalf("mode = %v, want the list", m.Mode())
	}

	if _, _ = press(m, tea.KeyEnter); len(e.answered) != 1 || !e.answered[0].approved {
		t.Errorf("enter on the list did not approve: %+v", e.answered)
	}
}

// SelectedAwaiting is what the frame reads to relabel its footer, so it has to say yes exactly
// when the keys answer.
func TestSelectedAwaitingFollowsTheCursor(t *testing.T) {
	e := engine(stuck("blocked", "run_command: npm test"), status("docs", core.AgentIdle, ""))
	m := model(e)

	if !m.SelectedAwaiting() {
		t.Error("the selection is a waiting agent and the model says otherwise")
	}
	m = key(m, "j")
	if m.SelectedAwaiting() {
		t.Error("the selection moved to an idle agent and the model still says waiting")
	}
}
