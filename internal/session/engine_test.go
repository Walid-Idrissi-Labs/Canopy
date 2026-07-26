package session

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
)

// scriptedClient replays events with a controllable pace, which is how a streaming turn gets tested
// without a network or a credential.
type scriptedClient struct {
	name   string
	events []core.StreamEvent
	// gate, when set, holds each event back until it receives, so a test can observe a turn while
	// it is still in flight.
	gate    chan struct{}
	openErr error
	history []core.Message
}

func (c *scriptedClient) Name() string { return c.name }

func (c *scriptedClient) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	c.history = req.Messages
	if c.openErr != nil {
		return nil, c.openErr
	}
	return &scriptedStream{events: c.events, gate: c.gate, ctx: ctx}, nil
}

type scriptedStream struct {
	events  []core.StreamEvent
	gate    chan struct{}
	ctx     context.Context
	current core.StreamEvent
}

func (s *scriptedStream) Next() bool {
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-s.ctx.Done():
			s.current = core.StreamEvent{Kind: core.EventDone, StopReason: core.StopCancelled}
			s.events = nil
			return true
		}
	}
	if len(s.events) == 0 {
		return false
	}
	s.current, s.events = s.events[0], s.events[1:]
	return true
}

func (s *scriptedStream) Event() core.StreamEvent { return s.current }
func (s *scriptedStream) Err() error              { return nil }
func (s *scriptedStream) Close() error            { return nil }

type fixedResolver struct {
	client core.ProviderClient
	id     pricing.ModelID
	err    error
}

func (r fixedResolver) Resolve(string, string) (core.ProviderClient, pricing.ModelID, error) {
	return r.client, r.id, r.err
}

func anthropicID() pricing.ModelID {
	return pricing.ModelID{Provider: core.ProviderAnthropic, Model: "claude-opus-5"}
}

func reply(text string) []core.StreamEvent {
	return []core.StreamEvent{
		{Kind: core.EventText, Text: text},
		{Kind: core.EventDone, StopReason: core.StopEndTurn,
			Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}
}

// waitForTurn blocks until a turn reaches a terminal state, failing fast rather than hanging if it
// never does.
func waitForTurn(t *testing.T, e *Engine, sessionID, turnID string) core.Turn {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		s, ok := e.Session(sessionID)
		if ok {
			for _, turn := range s.Turns {
				if turn.ID == turnID && turn.State.Terminal() {
					return turn
				}
			}
		}
		select {
		case <-deadline:
			t.Fatalf("turn %s never reached a terminal state", turnID)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func TestATurnStreamsIntoTheSnapshot(t *testing.T) {
	client := &scriptedClient{name: "claude", events: []core.StreamEvent{
		{Kind: core.EventText, Text: "Hello"},
		{Kind: core.EventText, Text: ", world"},
		{Kind: core.EventDone, StopReason: core.StopEndTurn,
			Usage: core.Usage{InputTokens: 100, OutputTokens: 20}},
	}}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	turnID, err := e.Send(session.ID, "hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	turn := waitForTurn(t, e, session.ID, turnID)
	if turn.Text != "Hello, world" {
		t.Errorf("text = %q", turn.Text)
	}
	if turn.State != core.TurnComplete {
		t.Errorf("state = %s, want complete", turn.State)
	}
	if turn.Provider != "claude" {
		t.Errorf("provider = %q, want the credential that answered", turn.Provider)
	}
	// Priced here, since only the engine knows which credential answered.
	if !turn.Usage.CostKnown || turn.Usage.CostUSD <= 0 {
		t.Errorf("usage = %+v, want a real cost", turn.Usage)
	}
	if turn.EndedAt.IsZero() {
		t.Error("a terminal turn with no end time counts up forever on screen")
	}
}

// The whole reason events carry no payload: a reader who sees one notification where three were
// sent still finds every token, because the reply grows in the snapshot.
func TestTheReplyIsReadableWhileItIsStillArriving(t *testing.T) {
	gate := make(chan struct{})
	client := &scriptedClient{name: "claude", gate: gate, events: []core.StreamEvent{
		{Kind: core.EventText, Text: "partial"},
		{Kind: core.EventDone, StopReason: core.StopEndTurn},
	}}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	turnID, err := e.Send(session.ID, "hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	gate <- struct{}{} // let the text through, hold the done event back

	deadline := time.After(3 * time.Second)
	for {
		s, _ := e.Session(session.ID)
		turn := s.Turns[len(s.Turns)-1]
		if turn.Text == "partial" {
			if turn.State.Whole() {
				t.Error("a turn still streaming reported itself as a whole answer")
			}
			if _, running := s.Active(); !running {
				t.Error("a turn still streaming should read as in flight")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("the partial reply never appeared in the snapshot")
		case <-time.After(2 * time.Millisecond):
		}
	}

	gate <- struct{}{}
	waitForTurn(t, e, session.ID, turnID)
}

// A second message while the first is still streaming is a person typing ahead, not a failure.
func TestASessionRunsOneTurnAtATime(t *testing.T) {
	gate := make(chan struct{})
	client := &scriptedClient{name: "claude", gate: gate, events: reply("ok")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	if _, err := e.Send(session.ID, "first"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, err := e.Send(session.ID, "second")
	if !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want ErrBusy so the interface can queue rather than show a failure", err)
	}

	close(gate)
}

// A partial answer presented as complete is the chat equivalent of a stale green.
func TestCancellingKeepsThePartialAndMarksIt(t *testing.T) {
	gate := make(chan struct{})
	client := &scriptedClient{name: "claude", gate: gate, events: []core.StreamEvent{
		{Kind: core.EventText, Text: "half an ans"},
		{Kind: core.EventText, Text: "wer"},
		{Kind: core.EventDone, StopReason: core.StopEndTurn},
	}}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	turnID, err := e.Send(session.ID, "hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	gate <- struct{}{} // one chunk through
	e.Cancel(session.ID)

	turn := waitForTurn(t, e, session.ID, turnID)
	if turn.State != core.TurnInterrupted {
		t.Errorf("state = %s, want interrupted", turn.State)
	}
	if turn.State.Whole() {
		t.Error("an interrupted turn must never read as a whole answer")
	}
	if turn.Text != "half an ans" {
		t.Errorf("text = %q, want what actually arrived kept", turn.Text)
	}
}

func TestAFailureToReachTheProviderIsAFailedTurn(t *testing.T) {
	client := &scriptedClient{
		name: "claude",
		openErr: &core.ProviderError{
			Kind: core.ErrAuthentication, Provider: "claude", Message: "the credential was rejected",
		},
	}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	turnID, err := e.Send(session.ID, "hi")
	if err != nil {
		t.Fatalf("Send registers the turn even when it will fail: %v", err)
	}

	turn := waitForTurn(t, e, session.ID, turnID)
	if turn.State != core.TurnFailed {
		t.Errorf("state = %s, want failed", turn.State)
	}
	if !strings.Contains(turn.Error, "rejected") {
		t.Errorf("error = %q, want something a user can act on", turn.Error)
	}
}

// A stream that stops without saying how it ended is a bug in a provider adapter, not an answer.
func TestAStreamThatJustStopsIsAFailure(t *testing.T) {
	client := &scriptedClient{name: "claude", events: []core.StreamEvent{
		{Kind: core.EventText, Text: "started saying something"},
	}}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	turnID, _ := e.Send(session.ID, "hi")

	turn := waitForTurn(t, e, session.ID, turnID)
	if turn.State != core.TurnFailed {
		t.Errorf("state = %s, want failed rather than an answer nobody can vouch for", turn.State)
	}
	if turn.Error == "" {
		t.Error("a failed turn must say why")
	}
}

// Both arrive on a successful response carrying text that reads like a finished answer.
func TestRefusalAndTruncationAreTheirOwnStates(t *testing.T) {
	cases := map[core.StopReason]core.TurnState{
		core.StopRefusal:   core.TurnRefused,
		core.StopMaxTokens: core.TurnTruncated,
	}
	for reason, want := range cases {
		client := &scriptedClient{name: "claude", events: []core.StreamEvent{
			{Kind: core.EventText, Text: "looks like an answer"},
			{Kind: core.EventDone, StopReason: reason},
		}}
		e := New(fixedResolver{client: client, id: anthropicID()})

		session := e.Create("claude", "m")
		turnID, _ := e.Send(session.ID, "hi")
		turn := waitForTurn(t, e, session.ID, turnID)

		if turn.State != want {
			t.Errorf("%s became %s, want %s", reason, turn.State, want)
		}
		if turn.State.Whole() {
			t.Errorf("%s produced a turn that reads as a complete answer", reason)
		}
		e.Close()
	}
}

// Each turn has to carry the conversation so far, or the model answers every message as though it
// were the first.
func TestHistoryIsSentWithEachTurn(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("first answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	turnID, _ := e.Send(session.ID, "first question")
	waitForTurn(t, e, session.ID, turnID)

	client.events = reply("second answer")
	turnID, _ = e.Send(session.ID, "second question")
	waitForTurn(t, e, session.ID, turnID)

	if len(client.history) != 3 {
		t.Fatalf("%d messages sent, want the first exchange plus the new question: %+v",
			len(client.history), client.history)
	}
	if client.history[0].Text != "first question" || client.history[1].Text != "first answer" {
		t.Errorf("history = %+v", client.history)
	}
	if client.history[2].Text != "second question" {
		t.Errorf("the newest question is not last: %+v", client.history)
	}
}

// The last thing anyone hears about a turn must be how it ended, at any load.
func TestTheFinalEventIsMarkedFinal(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("done")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	events := e.Events(0)
	turnID, _ := e.Send(session.ID, "hi")
	waitForTurn(t, e, session.ID, turnID)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Kind == core.EventTurnUpdated && ev.Final {
				if ev.TurnID != turnID {
					t.Errorf("final event names turn %q, want %q", ev.TurnID, turnID)
				}
				return
			}
		case <-deadline:
			t.Fatal("no final event arrived, so the last thing a user hears about this turn is " +
				"that it was streaming")
		}
	}
}

// A session is recognised in a list by what was first said to it.
func TestTheFirstMessageNamesTheSession(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("ok")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	turnID, _ := e.Send(session.ID, "add a login form to the settings page")
	waitForTurn(t, e, session.ID, turnID)

	got, _ := e.Session(session.ID)
	if got.Title != "add a login form to the settings page" {
		t.Errorf("title = %q", got.Title)
	}

	// And is never rewritten, so a session does not rename itself out from under someone.
	turnID, _ = e.Send(session.ID, "actually never mind, do something else")
	waitForTurn(t, e, session.ID, turnID)
	got, _ = e.Session(session.ID)
	if got.Title != "add a login form to the settings page" {
		t.Errorf("title changed to %q on the second message", got.Title)
	}
}

func TestLongTitlesAreCutAtAWordBoundary(t *testing.T) {
	long := "please refactor the entire authentication subsystem including the session store"
	got := summarise(long)

	if len(got) > 52 {
		t.Errorf("title is %d characters, which is a paragraph rather than a label: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("a truncated title should say so, got %q", got)
	}
	// Cut mid word it looks like a rendering fault rather than a deliberate truncation.
	if strings.Contains(got, "  ") || strings.HasSuffix(got, " ...") {
		t.Errorf("title = %q", got)
	}
}

func TestSnapshotsAreIsolatedFromLaterTurns(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("first")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	turnID, _ := e.Send(session.ID, "one")
	waitForTurn(t, e, session.ID, turnID)

	held, _ := e.Session(session.ID)
	before := len(held.Turns)

	client.events = reply("second")
	turnID, _ = e.Send(session.ID, "two")
	waitForTurn(t, e, session.ID, turnID)

	if len(held.Turns) != before {
		t.Error("a held snapshot grew when a later turn arrived, so the interface could render " +
			"half of one update and half of the next")
	}
}

func TestEmptyMessagesAreRefused(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	if _, err := e.Send(session.ID, "   "); err == nil {
		t.Error("an empty message has nothing to answer and should not spend a turn")
	}
	if _, err := e.Send("nope", "hello"); err == nil {
		t.Error("sending to a session that does not exist should be an error")
	}
}

// A provider can take several seconds to send its first byte. Somebody who presses escape in that
// window has stopped the turn, not hit a fault, and reporting a failure would put an error on
// screen for something they did on purpose.
//
// Found by a live test against a real provider: the cancel landed while the request was still
// waiting for a response, so it never reached the stream at all.
func TestCancellingBeforeTheFirstByteIsStillAnInterruption(t *testing.T) {
	client := &blockingClient{opened: make(chan struct{})}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "m")
	turnID, err := e.Send(session.ID, "hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	<-client.opened
	e.Cancel(session.ID)

	turn := waitForTurn(t, e, session.ID, turnID)
	if turn.State != core.TurnInterrupted {
		t.Errorf("state = %s, want interrupted", turn.State)
	}
}

// blockingClient never answers, so a cancel always lands before the stream exists.
type blockingClient struct {
	opened chan struct{}
	once   sync.Once
}

func (c *blockingClient) Name() string { return "blocking" }

func (c *blockingClient) Stream(ctx context.Context, _ core.Request) (core.Stream, error) {
	c.once.Do(func() { close(c.opened) })
	<-ctx.Done()
	return nil, &core.ProviderError{
		Kind: core.ErrNetwork, Provider: "blocking", Message: "context canceled", Err: ctx.Err(),
	}
}

// "Nothing leaks" is the half of cancellation that is invisible when it is broken. A goroutine
// still reading an abandoned response body holds a connection open, and eight agents doing it is a
// program that slowly stops working for reasons nobody can see.
func TestCancellingLeavesNothingRunning(t *testing.T) {
	before := runtime.NumGoroutine()

	for i := 0; i < 20; i++ {
		gate := make(chan struct{})
		client := &scriptedClient{name: "claude", gate: gate, events: []core.StreamEvent{
			{Kind: core.EventText, Text: "started"},
			{Kind: core.EventDone, StopReason: core.StopEndTurn},
		}}
		e := New(fixedResolver{client: client, id: anthropicID()})

		s := e.Create("claude", "m")
		turnID, err := e.Send(s.ID, "hi")
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		gate <- struct{}{}
		e.Cancel(s.ID)
		waitForTurn(t, e, s.ID, turnID)
		e.Close()
	}

	// Goroutines wind down asynchronously, so this allows for that rather than demanding the count
	// be exact the instant the last Close returns.
	deadline := time.Now().Add(3 * time.Second)
	for {
		after := runtime.NumGoroutine()
		if after <= before+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("%d goroutines still running after 20 cancelled turns, started with %d",
				after, before)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Closing the engine has to close out every turn still in flight, or quitting during a reply loses
// the partial that cancelling went to the trouble of keeping.
func TestClosingTheEngineFinishesRunningTurns(t *testing.T) {
	gate := make(chan struct{})
	client := &scriptedClient{name: "claude", gate: gate, events: []core.StreamEvent{
		{Kind: core.EventText, Text: "half written"},
		{Kind: core.EventDone, StopReason: core.StopEndTurn},
	}}
	e := New(fixedResolver{client: client, id: anthropicID()})

	s := e.Create("claude", "m")
	turnID, err := e.Send(s.ID, "hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	gate <- struct{}{}

	// Wait for the text to land, so there is genuinely a partial to keep.
	deadline := time.After(3 * time.Second)
	for {
		got, _ := e.Session(s.ID)
		if got.Turns[0].Text == "half written" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the partial never arrived")
		case <-time.After(2 * time.Millisecond):
		}
	}

	e.Close()

	got, _ := e.Session(s.ID)
	turn := got.Turns[0]
	if turn.ID != turnID {
		t.Fatalf("turn = %q", turn.ID)
	}
	if !turn.State.Terminal() {
		t.Errorf("state = %s, want a turn that is closed out", turn.State)
	}
	if turn.Text != "half written" {
		t.Errorf("text = %q, want the partial kept", turn.Text)
	}
}

// Somebody who thinks they can undo and cannot is worse off than somebody who knows they cannot.
func TestUndoWithoutACheckpointSaysSoRatherThanFailingQuietly(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("done")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	s := e.Create("claude", "m")
	turnID, err := e.Send(s.ID, "change something")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, s.ID, turnID)

	err = e.Undo(context.Background(), s.ID, turnID)
	if err == nil {
		t.Fatal("undoing with no checkpoint should be refused rather than silently doing nothing")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Errorf("the reason should be actionable, got %q", err)
	}

	if err := e.Undo(context.Background(), "nope", turnID); err == nil {
		t.Error("undoing a session that does not exist should be an error")
	}
}
