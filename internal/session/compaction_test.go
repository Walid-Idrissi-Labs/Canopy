package session

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// answered builds a session that has been talking long enough to be worth compacting.
func answered(t *testing.T, e *Engine, client *scriptedClient, n int) core.Session {
	t.Helper()

	s := e.Create("claude", "claude-opus-5")
	for i := 0; i < n; i++ {
		client.events = reply("answer")
		turnID, err := e.Send(s.ID, "question")
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		waitForTurn(t, e, s.ID, turnID)
	}
	got, _ := e.Session(s.ID)
	return got
}

// The promise compaction makes: what gets sent gets shorter, what is kept does not.
func TestCompactionShortensTheRequestAndKeepsTheTranscript(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := answered(t, e, client, 10)
	before := len(session.History())

	client.events = reply("Decided to use bcrypt. Files touched: auth.go, session.go.")
	result, err := e.Compact(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.Summary == "" {
		t.Fatal("a compaction with no summary has nothing to replace the history with")
	}
	if result.Through != 10-keepRecentTurns {
		t.Errorf("summarised %d turns, want %d", result.Through, 10-keepRecentTurns)
	}

	if err := e.Apply(session.ID, result); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	after, _ := e.Session(session.ID)

	// What gets sent is shorter.
	if len(after.History()) >= before {
		t.Errorf("history is %d messages after compacting, was %d. Compaction that does not "+
			"shorten the request has cost a model call for nothing",
			len(after.History()), before)
	}
	if !strings.Contains(after.History()[0].Text, "bcrypt") {
		t.Errorf("the summary is not leading the history: %+v", after.History()[0])
	}

	// What is kept is not. Every turn is still there, in full, in the order it happened.
	if len(after.Turns) != 10 {
		t.Errorf("%d turns kept, want all 10. Compaction shortens what is sent, never what is kept",
			len(after.Turns))
	}
	for _, turn := range after.Turns {
		if turn.Text != "answer" {
			t.Errorf("a kept turn was altered: %+v", turn)
		}
	}
}

// An agent that quietly forgets half of what it was told and carries on answering is the same
// class of problem as a test result that says passing about code it never ran.
func TestACompactionIsRecordedWhereItCanBeSeen(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := answered(t, e, client, 10)
	client.events = reply("a summary")
	result, err := e.Compact(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if err := e.Apply(session.ID, result); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	after, _ := e.Session(session.ID)
	compaction, ok := after.Compacted()
	if !ok {
		t.Fatal("nothing on the session says a compaction happened")
	}
	if compaction.At.IsZero() {
		t.Error("a compaction with no time cannot be placed in the transcript")
	}
	// "Compacted" on its own tells nobody whether it was worth the call.
	if compaction.TokensBefore <= compaction.TokensAfter {
		t.Errorf("before %d, after %d: a compaction that did not shrink anything should say so",
			compaction.TokensBefore, compaction.TokensAfter)
	}
}

// The recent turns are where the actual work is: what file we are editing, what just failed, what
// the user corrected a moment ago. Summarising those is how an agent loses the thread mid task.
func TestTheRecentTurnsAreNeverSummarised(t *testing.T) {
	turns := make([]core.Turn, 10)
	for i := range turns {
		turns[i] = core.Turn{ID: string(rune('a' + i)), State: core.TurnComplete}
	}

	older, kept := splitForCompaction(turns)
	if len(kept) != keepRecentTurns {
		t.Errorf("%d turns kept verbatim, want %d", len(kept), keepRecentTurns)
	}
	if len(older) != 10-keepRecentTurns {
		t.Errorf("%d turns summarised, want %d", len(older), 10-keepRecentTurns)
	}
	if kept[len(kept)-1].ID != turns[len(turns)-1].ID {
		t.Error("the newest turn must be one of the ones kept")
	}
}

// A turn still in flight has an answer arriving into it, and folding that into a summary would
// summarise something that has not happened yet.
func TestAnInFlightTurnIsNeverSummarised(t *testing.T) {
	turns := make([]core.Turn, 10)
	for i := range turns {
		turns[i] = core.Turn{ID: string(rune('a' + i)), State: core.TurnComplete}
	}
	// The turn just before the cut is still running.
	turns[10-keepRecentTurns-1].State = core.TurnStreaming

	older, _ := splitForCompaction(turns)
	for _, turn := range older {
		if !turn.State.Terminal() {
			t.Errorf("turn %s is %s and was going to be summarised", turn.ID, turn.State)
		}
	}
}

// The confirmation on the key that starts a compaction has to say what it is about to spend before
// it spends it, and it has to be describing the compaction that would actually happen. Worked out
// from the same split, so the sentence somebody agrees to cannot drift from what they get.
func TestACompactionCanBeDescribedBeforeAnythingIsSent(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	conversation := answered(t, e, client, 10)
	current, _ := e.Session(conversation.ID)

	plan := PlanCompaction(current)
	if !plan.Possible() {
		t.Fatal("a ten turn conversation has something to compact")
	}
	if plan.Turns+plan.Kept != len(current.Turns) {
		t.Errorf("the plan accounts for %d of %d turns", plan.Turns+plan.Kept, len(current.Turns))
	}
	if plan.Kept != keepRecentTurns {
		t.Errorf("the plan keeps %d turns, and compaction keeps %d", plan.Kept, keepRecentTurns)
	}
	if plan.Tokens <= 0 {
		t.Error("the plan says compacting would send nothing, so the offer names no cost")
	}

	// And it agrees with what the compaction then does, which is the whole reason it is worked out
	// here rather than by the screen that asks.
	result, err := e.Compact(context.Background(), conversation.ID)
	if err != nil {
		t.Fatalf("compacting: %v", err)
	}
	if result.Through != plan.Turns {
		t.Errorf("the offer said %d turns and %d were summarised", plan.Turns, result.Through)
	}
}

// A conversation short enough that everything in it is inside the window kept verbatim has nothing
// to offer, and offering to summarise none of it and then refusing would be worse than saying so.
func TestAShortConversationHasNoCompactionToOffer(t *testing.T) {
	short := core.Session{Turns: []core.Turn{
		{ID: "t1", State: core.TurnComplete},
		{ID: "t2", State: core.TurnComplete},
	}}
	if PlanCompaction(short).Possible() {
		t.Error("a two turn conversation was offered a compaction")
	}
}

func TestThereIsNothingToCompactInAShortConversation(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := answered(t, e, client, 2)
	_, err := e.Compact(context.Background(), session.ID)
	if err == nil {
		t.Fatal("compacting a two turn conversation should be refused rather than done")
	}
	if !strings.Contains(err.Error(), "not enough history") {
		t.Errorf("the error should explain, got %q", err)
	}
}

// A truncated or refused summary would replace real history with a partial account of it, and
// nothing downstream could tell the difference.
func TestAnIncompleteSummaryIsRejected(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := answered(t, e, client, 10)

	for _, reason := range []core.StopReason{core.StopMaxTokens, core.StopRefusal} {
		client.events = []core.StreamEvent{
			{Kind: core.EventText, Text: "half a summ"},
			{Kind: core.EventDone, StopReason: reason},
		}
		if _, err := e.Compact(context.Background(), session.ID); err == nil {
			t.Errorf("a summary that ended as %s was accepted", reason)
		}
	}

	// And the session is untouched by the attempt.
	after, _ := e.Session(session.ID)
	if _, ok := after.Compacted(); ok {
		t.Error("a rejected compaction was recorded anyway")
	}
}

// The summary is Canopy speaking about the conversation, not the model recalling it. A model that
// reads its own summary as something it said will defend it.
func TestTheSummaryIsSentAsAUserMessage(t *testing.T) {
	session := core.Session{
		Turns: []core.Turn{
			{ID: "t1", State: core.TurnComplete,
				Request: core.Message{Role: core.RoleUser, Text: "old"}, Text: "old answer"},
			{ID: "t2", State: core.TurnComplete,
				Request: core.Message{Role: core.RoleUser, Text: "new"}, Text: "new answer"},
		},
		Compactions: []core.Compaction{{Summary: "we agreed on bcrypt", Through: 1}},
	}

	history := session.History()
	if history[0].Role != core.RoleUser {
		t.Errorf("the summary is sent as %q, want a user message", history[0].Role)
	}
	if !strings.Contains(history[0].Text, "bcrypt") {
		t.Errorf("first message = %+v", history[0])
	}
	// And the turn it replaced is gone from what gets sent, which is the point.
	for _, msg := range history {
		if msg.Text == "old answer" {
			t.Error("a summarised turn is still being sent, so compacting saved nothing")
		}
	}
	if history[len(history)-1].Text != "new answer" {
		t.Errorf("the kept turns are missing from the history: %+v", history)
	}
}

// Compaction is part of the conversation's history, so losing it on restart would mean an agent
// silently regaining context it had been told to forget, and a transcript missing the marker that
// explains why it had.
func TestCompactionsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}

	client := &scriptedClient{name: "claude", events: reply("answer")}
	first := New(fixedResolver{client: client, id: anthropicID()})
	if err := first.WithStorage(storage, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}

	session := answered(t, first, client, 10)
	client.events = reply("a durable summary")
	result, err := first.Compact(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if err := first.Apply(session.ID, result); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	first.Close()

	reopened, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	second := New(fixedResolver{client: client, id: anthropicID()})
	defer second.Close()
	if err := second.WithStorage(reopened, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}

	loaded, ok := second.Session(session.ID)
	if !ok {
		t.Fatal("the session did not come back")
	}
	compaction, ok := loaded.Compacted()
	if !ok {
		t.Fatal("the compaction was lost, so the agent silently regained context it had forgotten")
	}
	if !strings.Contains(compaction.Summary, "durable summary") {
		t.Errorf("summary = %q", compaction.Summary)
	}
	if compaction.Through != result.Through {
		t.Errorf("through = %d, want %d", compaction.Through, result.Through)
	}
	if len(loaded.Turns) != 10 {
		t.Errorf("%d turns after restart, want all 10 still kept", len(loaded.Turns))
	}
}
