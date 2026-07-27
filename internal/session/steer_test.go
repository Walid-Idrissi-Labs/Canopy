package session

import (
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// gated returns an engine whose provider holds each event back until released, so a test can steer
// a turn that is genuinely still in flight rather than one that has already quietly finished.
func gated(t *testing.T) (*Engine, *scriptedClient, chan struct{}) {
	t.Helper()

	gate := make(chan struct{}, 16)
	client := &scriptedClient{name: "claude", gate: gate, events: reply("working on it")}
	return New(fixedResolver{client: client, id: anthropicID()}), client, gate
}

// The acceptance criterion, and the reason this task is two mechanisms rather than one: steering
// must not cancel anything.
func TestSteeringDoesNotStopTheTurnInFlight(t *testing.T) {
	e, _, gate := gated(t)
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	turnID, err := e.Send(session.ID, "start the refactor")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if err := e.Steer(session.ID, "use the existing helper rather than a new one"); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if queued := e.Steering(session.ID); len(queued) != 1 {
		t.Fatalf("%d pieces of guidance queued, want 1", len(queued))
	}

	// The turn is released only now. If steering had cancelled it, it would already be terminal and
	// its text would be empty.
	for range 2 {
		gate <- struct{}{}
	}
	turn := waitForTurn(t, e, session.ID, turnID)

	if turn.State != core.TurnComplete {
		t.Errorf("the steered turn finished as %q, want complete: steering is not cancelling",
			turn.State)
	}
	if turn.Text != "working on it" {
		t.Errorf("the turn kept %q, so work in progress was lost", turn.Text)
	}
}

// The guidance has to actually arrive, as part of the conversation, at the next boundary.
func TestGuidanceIsDeliveredAtTheNextTurnBoundary(t *testing.T) {
	e, client, gate := gated(t)
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	first, err := e.Send(session.ID, "start the refactor")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := e.Steer(session.ID, "use the existing helper"); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	// Four releases: two events for the first turn, two for the steered one it starts.
	for range 4 {
		gate <- struct{}{}
	}
	waitForTurn(t, e, session.ID, first)

	deadline := time.After(3 * time.Second)
	for {
		s, _ := e.Session(session.ID)
		if len(s.Turns) >= 2 {
			if s.Turns[1].Request.Text != "use the existing helper" {
				t.Fatalf("the steered turn asked %q", s.Turns[1].Request.Text)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("the queued guidance never became a turn, so a correction typed mid answer is lost")
		case <-time.After(2 * time.Millisecond):
		}
	}

	if queued := e.Steering(session.ID); len(queued) != 0 {
		t.Errorf("guidance is still queued after delivery: %v", queued)
	}

	// Visibly part of the next turn's context, which is the other half of the acceptance criterion.
	// A correction the model never sees is a correction that did not happen.
	var carried bool
	for _, message := range client.History() {
		if strings.Contains(message.Text, "use the existing helper") {
			carried = true
		}
	}
	if !carried {
		t.Errorf("the guidance is not in what was sent to the provider: %+v", client.History())
	}
}

// Three corrections typed while one answer streamed are three parts of one thought. Sent as three
// turns, the agent would answer the first before it had read the third.
func TestSeveralCorrectionsArriveAsOneMessage(t *testing.T) {
	e, _, gate := gated(t)
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	if _, err := e.Send(session.ID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	for _, guidance := range []string{"not that file", "the other one", "and keep the tests passing"} {
		if err := e.Steer(session.ID, guidance); err != nil {
			t.Fatalf("Steer: %v", err)
		}
	}

	for range 4 {
		gate <- struct{}{}
	}

	deadline := time.After(3 * time.Second)
	for {
		s, _ := e.Session(session.ID)
		if len(s.Turns) >= 2 {
			text := s.Turns[1].Request.Text
			for _, want := range []string{"not that file", "the other one", "keep the tests passing"} {
				if !strings.Contains(text, want) {
					t.Errorf("the delivered message is missing %q: %q", want, text)
				}
			}
			if len(s.Turns) > 2 {
				t.Errorf("%d turns, want the three corrections delivered as one", len(s.Turns))
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("the guidance was never delivered")
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// Steering an idle session sends immediately. Holding it back waiting for a turn that is not coming
// would look exactly like the input being swallowed.
func TestSteeringAnIdleSessionJustSends(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("understood")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	if err := e.Steer(session.ID, "do the thing"); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if queued := e.Steering(session.ID); len(queued) != 0 {
		t.Errorf("guidance was queued on an idle session: %v", queued)
	}

	deadline := time.After(3 * time.Second)
	for {
		s, _ := e.Session(session.ID)
		if len(s.Turns) == 1 && s.Turns[0].Request.Text == "do the thing" {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("nothing was sent: %+v", s.Turns)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// Guidance somebody typed and then cancelled is still something they wrote. Losing it to a keystroke
// is the kind of small theft that makes a tool feel unsafe.
func TestCancellingSteeringHandsTheTextBack(t *testing.T) {
	e, _, gate := gated(t)
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	if _, err := e.Send(session.ID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := e.Steer(session.ID, "actually, wait"); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	dropped := e.ClearSteering(session.ID)
	if len(dropped) != 1 || dropped[0] != "actually, wait" {
		t.Errorf("clearing returned %v, so the interface has nothing to put back in the box", dropped)
	}
	if queued := e.Steering(session.ID); len(queued) != 0 {
		t.Errorf("guidance survived being cleared: %v", queued)
	}

	for range 2 {
		gate <- struct{}{}
	}
	time.Sleep(50 * time.Millisecond)

	s, _ := e.Session(session.ID)
	if len(s.Turns) != 1 {
		t.Errorf("%d turns, want just the one: cleared guidance was delivered anyway", len(s.Turns))
	}
}

// Both mechanisms reach the right agent and only that agent.
func TestSteeringOneAgentDoesNotReachAnother(t *testing.T) {
	e, _, gate := gated(t)
	defer e.Close()

	one := e.Create("claude", "claude-opus-5")
	two := e.Create("claude", "claude-opus-5")

	if _, err := e.Send(one.ID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := e.Send(two.ID, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := e.Steer(one.ID, "for the first one only"); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	if queued := e.Steering(two.ID); len(queued) != 0 {
		t.Errorf("guidance for one agent is queued on another: %v", queued)
	}

	for range 6 {
		gate <- struct{}{}
	}
	time.Sleep(100 * time.Millisecond)

	second, _ := e.Session(two.ID)
	for _, turn := range second.Turns {
		if strings.Contains(turn.Request.Text, "first one only") {
			t.Errorf("the other agent received the guidance: %q", turn.Request.Text)
		}
	}
}

// Interrupt is the other mechanism and stays what it was: it stops the turn and keeps what arrived.
// Asserted next to steering rather than only in the cancellation tests, because the thing that
// would break is the two becoming one.
func TestInterruptStillStopsAndKeepsWhatArrived(t *testing.T) {
	gate := make(chan struct{}, 8)
	client := &scriptedClient{name: "claude", gate: gate, events: []core.StreamEvent{
		{Kind: core.EventText, Text: "partial"},
		{Kind: core.EventText, Text: " answer"},
		{Kind: core.EventDone, StopReason: core.StopEndTurn},
	}}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	turnID, err := e.Send(session.ID, "start")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	gate <- struct{}{}
	deadline := time.After(3 * time.Second)
	for {
		s, _ := e.Session(session.ID)
		if len(s.Turns) > 0 && s.Turns[0].Text != "" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("nothing streamed before the interrupt")
		case <-time.After(2 * time.Millisecond):
		}
	}

	e.Cancel(session.ID)
	turn := waitForTurn(t, e, session.ID, turnID)

	if turn.State != core.TurnInterrupted {
		t.Errorf("an interrupted turn finished as %q", turn.State)
	}
	if turn.Text == "" {
		t.Error("the partial answer was thrown away, which is what steering exists to avoid")
	}
	if queued := e.Steering(session.ID); len(queued) != 0 {
		t.Errorf("interrupting queued something: %v", queued)
	}
}

func TestSteeringNothingIsRefused(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	if err := e.Steer(session.ID, "   "); err == nil {
		t.Error("empty guidance was accepted")
	}
	if err := e.Steer("session-does-not-exist", "hello"); err == nil {
		t.Error("guidance for a session that does not exist was accepted")
	}
}
