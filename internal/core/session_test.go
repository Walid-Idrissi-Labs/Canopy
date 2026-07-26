package core

import (
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)

func done(id, text string, state TurnState) Turn {
	return Turn{
		ID:        id,
		State:     state,
		Request:   Message{Role: RoleUser, Text: "ask"},
		Text:      text,
		StartedAt: now,
		EndedAt:   now.Add(time.Second),
	}
}

// Every state that is not complete leaves text on screen that reads as an answer and is not one.
// Renderers ask Whole rather than checking for the absence of an error, so this is the assertion
// that keeps a partial reply from being presented as a finished one.
func TestOnlyCompleteIsWhole(t *testing.T) {
	for _, state := range AllTurnStates() {
		whole := state.Whole()
		if state == TurnComplete && !whole {
			t.Errorf("%s should be whole", state)
		}
		if state != TurnComplete && whole {
			t.Errorf("%s reports itself as a whole answer, and it is not", state)
		}
	}
}

// A terminal turn is what makes an event final, and a final event may never be coalesced away.
// Being wrong in the permissive direction means the last thing a user hears is "streaming".
func TestTerminalStatesAreExactlyTheOnesThatStop(t *testing.T) {
	stops := map[TurnState]bool{
		TurnComplete: true, TurnInterrupted: true, TurnRefused: true,
		TurnTruncated: true, TurnFailed: true,
	}
	for _, state := range AllTurnStates() {
		if got := state.Terminal(); got != stops[state] {
			t.Errorf("%s.Terminal() = %v, want %v", state, got, stops[state])
		}
	}
}

// The mapping is not the identity, which is the point of having it. A refusal and a truncation both
// arrive as successful responses carrying text somebody could mistake for a finished answer.
func TestStopReasonsBecomeTurnStates(t *testing.T) {
	cases := map[StopReason]TurnState{
		StopEndTurn:   TurnComplete,
		StopToolUse:   TurnAwaitingTools,
		StopRefusal:   TurnRefused,
		StopMaxTokens: TurnTruncated,
		StopCancelled: TurnInterrupted,
		StopError:     TurnFailed,
	}
	for reason, want := range cases {
		if got := TurnStateFromStopReason(reason); got != want {
			t.Errorf("%s became %s, want %s", reason, got, want)
		}
	}

	// The two that matter: neither may come out as a whole answer.
	for _, reason := range []StopReason{StopRefusal, StopMaxTokens} {
		if TurnStateFromStopReason(reason).Whole() {
			t.Errorf("%s produced a state that reads as a complete answer", reason)
		}
	}
}

// A finished turn with no end time reports a duration that grows forever, so it keeps counting up
// on screen as though it were still running.
func TestATerminalTurnMustSayWhenItEnded(t *testing.T) {
	turn := Turn{ID: "t1", State: TurnComplete, StartedAt: now}
	if err := turn.Validate(); err == nil {
		t.Error("a complete turn with no end time should be rejected")
	}

	turn.EndedAt = now.Add(time.Second)
	if err := turn.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if got := turn.Duration(now.Add(time.Hour)); got != time.Second {
		t.Errorf("a finished turn's duration is %v, want a second, not the wall clock", got)
	}

	// A running turn measures against now, which is what makes a spinner count up.
	running := Turn{ID: "t2", State: TurnStreaming, StartedAt: now}
	if got := running.Duration(now.Add(3 * time.Second)); got != 3*time.Second {
		t.Errorf("a running turn's duration = %v, want 3s", got)
	}
}

func TestAFailedTurnMustSayWhy(t *testing.T) {
	turn := Turn{ID: "t1", State: TurnFailed, StartedAt: now, EndedAt: now}
	if err := turn.Validate(); err == nil {
		t.Error("a failed turn with no explanation is not something a user can act on")
	}
	turn.Error = "the credential was rejected"
	if err := turn.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// A turn left streaming behind a newer one was abandoned without being closed out, and it would
// show as running forever.
func TestOnlyTheLastTurnMayBeInFlight(t *testing.T) {
	session := Session{ID: "s1", Turns: []Turn{
		{ID: "t1", State: TurnStreaming, StartedAt: now},
		done("t2", "second", TurnComplete),
	}}
	err := session.Validate()
	if err == nil {
		t.Fatal("an unfinished turn before a finished one should be rejected")
	}
	if !strings.Contains(err.Error(), "t1") {
		t.Errorf("the error should name the abandoned turn, got %q", err)
	}

	// The same session with the in flight turn last is fine, since that is a conversation in
	// progress.
	ok := Session{ID: "s1", Turns: []Turn{
		done("t1", "first", TurnComplete),
		{ID: "t2", State: TurnStreaming, StartedAt: now},
	}}
	if err := ok.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestDuplicateTurnIDsAreRejected(t *testing.T) {
	session := Session{ID: "s1", Turns: []Turn{
		done("t1", "a", TurnComplete),
		done("t1", "b", TurnComplete),
	}}
	if err := session.Validate(); err == nil {
		t.Error("two turns with the same ID make every lookup ambiguous")
	}
}

func TestActiveTurn(t *testing.T) {
	settled := Session{ID: "s1", Turns: []Turn{done("t1", "a", TurnComplete)}}
	if _, running := settled.Active(); running {
		t.Error("a session whose last turn finished has nothing in flight")
	}

	live := Session{ID: "s1", Turns: []Turn{
		done("t1", "a", TurnComplete),
		{ID: "t2", State: TurnStreaming, StartedAt: now},
	}}
	turn, running := live.Active()
	if !running || turn.ID != "t2" {
		t.Errorf("active = %+v, %v, want t2", turn, running)
	}

	if _, running := (Session{ID: "s1"}).Active(); running {
		t.Error("a session with no turns has nothing in flight")
	}
}

// The agents view in A5 shows several sessions at once and needs one word per row that means the
// same thing for every one of them.
func TestSessionCollapsesToOneAgentState(t *testing.T) {
	cases := []struct {
		name  string
		turns []Turn
		want  AgentState
	}{
		{"empty", nil, AgentIdle},
		{"streaming", []Turn{{ID: "t", State: TurnStreaming, StartedAt: now}}, AgentWorking},
		{"awaiting tools", []Turn{{ID: "t", State: TurnAwaitingTools, StartedAt: now}}, AgentWorking},
		{"answered", []Turn{done("t", "a", TurnComplete)}, AgentIdle},
		{"stopped", []Turn{done("t", "a", TurnInterrupted)}, AgentStopped},
		{"failed", []Turn{
			{ID: "t", State: TurnFailed, Error: "no", StartedAt: now, EndedAt: now},
		}, AgentFailed},
	}
	for _, tc := range cases {
		got := Session{ID: "s", Turns: tc.turns}.AgentState()
		if got != tc.want {
			t.Errorf("%s: agent state = %s, want %s", tc.name, got, tc.want)
		}
	}
}

// With eight agents running, the useful sort is by which ones have stopped and cannot start again
// without a person.
func TestNeedsAttentionIsOnlyTheOnesThatStopped(t *testing.T) {
	wants := map[AgentState]bool{AgentAwaitingPermission: true, AgentFailed: true}
	for _, state := range AllAgentStates() {
		if got := state.NeedsAttention(); got != wants[state] {
			t.Errorf("%s.NeedsAttention() = %v, want %v", state, got, wants[state])
		}
	}

	snapshot := ProjectSnapshot{Sessions: []Session{
		{ID: "a", Turns: []Turn{done("t", "x", TurnComplete)}},
		{ID: "b", Turns: []Turn{{ID: "t", State: TurnFailed, Error: "x", StartedAt: now, EndedAt: now}}},
		{ID: "c", Turns: []Turn{{ID: "t", State: TurnStreaming, StartedAt: now}}},
	}}
	if got := snapshot.AgentsNeedingAttention(); got != 1 {
		t.Errorf("%d agents need attention, want 1", got)
	}
}

// Reconstructing a conversation for the provider must be a copy, not a translation, because a
// translation is where a tool result quietly loses its error flag.
func TestHistoryRoundTripsToolResults(t *testing.T) {
	session := Session{ID: "s1", Turns: []Turn{
		{
			ID:      "t1",
			State:   TurnComplete,
			Request: Message{Role: RoleUser, Text: "read the file"},
			Text:    "let me look",
			ToolCalls: []ToolCall{
				{ID: "c1", Name: "read", Input: []byte(`{"path":"x"}`)},
			},
			ToolResults: []ToolResult{
				{CallID: "c1", Content: "permission denied", IsError: true},
			},
			StartedAt: now, EndedAt: now,
		},
	}}

	history := session.History()
	if len(history) != 3 {
		t.Fatalf("%d messages, want the request, the reply and the tool results: %+v",
			len(history), history)
	}
	if history[0].Role != RoleUser || history[0].Text != "read the file" {
		t.Errorf("first message = %+v", history[0])
	}
	if history[1].Role != RoleAssistant || len(history[1].ToolCalls) != 1 {
		t.Errorf("second message = %+v", history[1])
	}
	results := history[2].ToolResults
	if len(results) != 1 || !results[0].IsError {
		t.Errorf("the tool result lost its error flag on the way through: %+v", results)
	}
}

// An empty assistant message is rejected by the API, and a turn that failed before the model said
// anything has nothing to contribute to the context anyway.
func TestHistorySkipsTurnsThatProducedNothing(t *testing.T) {
	session := Session{ID: "s1", Turns: []Turn{
		{
			ID: "t1", State: TurnFailed, Error: "overloaded",
			Request:   Message{Role: RoleUser, Text: "hello"},
			StartedAt: now, EndedAt: now,
		},
	}}
	history := session.History()
	if len(history) != 1 {
		t.Fatalf("%d messages, want just the request: %+v", len(history), history)
	}
	for _, msg := range history {
		if msg.Role == RoleAssistant {
			t.Error("an empty assistant message would be rejected by the API")
		}
	}
}

// Folding from a zero Usage would report a fully priced session as unpriced, since an empty running
// total and a turn nobody could price are the same value.
func TestSessionUsageSumsWithoutPoisoningTheCost(t *testing.T) {
	session := Session{ID: "s1", Turns: []Turn{
		{
			ID: "t1", State: TurnComplete, StartedAt: now, EndedAt: now,
			Usage: Usage{InputTokens: 100, OutputTokens: 10, CostUSD: 0.01, CostKnown: true},
		},
		{
			ID: "t2", State: TurnComplete, StartedAt: now, EndedAt: now,
			Usage: Usage{InputTokens: 200, OutputTokens: 20, CostUSD: 0.02, CostKnown: true},
		},
	}}

	usage := session.Usage()
	if !usage.CostKnown {
		t.Error("every turn was priced, so the session is priced")
	}
	if usage.InputTokens != 300 || usage.OutputTokens != 30 {
		t.Errorf("usage = %+v", usage)
	}

	if (Session{ID: "s1"}).Usage().CostKnown {
		t.Error("a session with no turns has spent nothing knowable, not a priced zero")
	}
}

// Two agents streaming at once must not collapse into each other. Coalescing a burst of tokens from
// one turn loses nothing; coalescing across turns loses the fact that a different turn moved.
func TestTurnEventsCoalescePerTurnNotPerSession(t *testing.T) {
	a := Event{Kind: EventTurnUpdated, SessionID: "s1", TurnID: "t1"}
	b := Event{Kind: EventTurnUpdated, SessionID: "s1", TurnID: "t2"}
	c := Event{Kind: EventTurnUpdated, SessionID: "s2", TurnID: "t1"}

	if a.CoalesceKey() == b.CoalesceKey() {
		t.Error("two turns in one session share a coalescing key, so one would swallow the other")
	}
	if a.CoalesceKey() == c.CoalesceKey() {
		t.Error("two sessions share a coalescing key")
	}

	again := Event{Kind: EventTurnUpdated, SessionID: "s1", TurnID: "t1"}
	if a.CoalesceKey() != again.CoalesceKey() {
		t.Error("a burst of tokens from one turn should coalesce, which is the whole point")
	}

	// The last word about a turn may never be swallowed, whatever the load.
	final := Event{Kind: EventTurnUpdated, SessionID: "s1", TurnID: "t1", Final: true}
	if final.Coalescable() {
		t.Error("a final turn event was coalescable, so the last thing a user hears about a turn " +
			"could be that it was streaming")
	}
}
