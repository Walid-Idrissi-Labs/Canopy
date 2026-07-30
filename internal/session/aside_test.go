package session

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A btw is worth keeping, and keeping it must not change what the agent knows.
//
// Those two are in tension and the tension is the whole of this file: an aside is recorded beside
// the conversation and never inside it, so it survives a restart and still never reaches a model.

// asideEngine is an engine with a real history file and a provider that answers whatever is asked.
func asideEngine(t *testing.T, path string) (*Engine, *scriptedClient) {
	t.Helper()

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	client := &scriptedClient{name: "claude", events: reply("the parser lives in internal/config")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	if err := e.WithStorage(storage, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}
	return e, client
}

// Ask, quit, come back: the answer is still there. Before this the panel was the whole of the
// history, so closing the screen threw away an answer somebody had asked for twenty minutes earlier.
func TestAnAsideSurvivesTheProcessThatAskedIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	first, _ := asideEngine(t, path)
	s := first.Create("claude", "claude-opus-5")

	answer, err := first.Aside(context.Background(), s.ID, "which file holds the parser")
	if err != nil {
		t.Fatalf("Aside: %v", err)
	}
	if answer == "" {
		t.Fatal("the aside answered with nothing")
	}
	first.Close()

	second, _ := asideEngine(t, path)
	defer second.Close()

	kept := second.Asides(s.ID)
	if len(kept) != 1 {
		t.Fatalf("%d asides survived, want the one that was asked", len(kept))
	}
	if kept[0].Question != "which file holds the parser" || kept[0].Answer != answer {
		t.Errorf("the exchange came back as %+v", kept[0])
	}
	if kept[0].At.IsZero() {
		t.Error("the aside has no time on it, so nothing can put several of them in order")
	}
}

// An aside belongs to the conversation it was asked about. Showing one agent's side questions over
// another agent's work would be worse than showing none.
func TestAsidesBelongToOneConversation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	e, _ := asideEngine(t, path)
	defer e.Close()

	one := e.Create("claude", "claude-opus-5")
	two := e.Create("claude", "claude-opus-5")

	if _, err := e.Aside(context.Background(), one.ID, "why this library"); err != nil {
		t.Fatalf("Aside: %v", err)
	}

	if got := len(e.Asides(one.ID)); got != 1 {
		t.Errorf("the conversation that asked has %d asides", got)
	}
	if got := e.Asides(two.ID); len(got) != 0 {
		t.Errorf("a conversation that asked nothing has %+v", got)
	}
}

// Asked in order and read back in order, since the panel stacks them oldest first and a history that
// came back shuffled would put an answer under the wrong question.
func TestAsidesComeBackInTheOrderTheyWereAsked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	e, _ := asideEngine(t, path)
	defer e.Close()

	s := e.Create("claude", "claude-opus-5")
	for _, question := range []string{"first question", "second question", "third question"} {
		if _, err := e.Aside(context.Background(), s.ID, question); err != nil {
			t.Fatalf("Aside %q: %v", question, err)
		}
	}

	kept := e.Asides(s.ID)
	if len(kept) != 3 {
		t.Fatalf("%d asides kept", len(kept))
	}
	for i, want := range []string{"first question", "second question", "third question"} {
		if kept[i].Question != want {
			t.Errorf("aside %d is %q, want %q", i, kept[i].Question, want)
		}
	}
}

// The line the whole feature is balanced on: recording is storage, not context. A request built
// after an aside must contain nothing of it, or the aside has quietly become part of the
// conversation and the thing it was invented to avoid has happened anyway.
func TestTheRequestAfterAnAsideContainsNothingOfIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	e, client := asideEngine(t, path)
	defer e.Close()

	s := e.Create("claude", "claude-opus-5")
	turnID, err := e.Send(s.ID, "build the parser")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, s.ID, turnID)

	if _, err := e.Aside(context.Background(), s.ID, "which file holds the parser"); err != nil {
		t.Fatalf("Aside: %v", err)
	}

	turnID, err = e.Send(s.ID, "now write a test for it")
	if err != nil {
		t.Fatalf("Send after the aside: %v", err)
	}
	waitForTurn(t, e, s.ID, turnID)

	history := client.History()
	for _, message := range history {
		if strings.Contains(message.Text, "which file holds the parser") {
			t.Errorf("the question reached the model in %+v", message)
		}
	}
	// Counted as well as searched. The provider answers the aside with the same words it answers a
	// turn with, so an aside folded into the context would be an extra exchange rather than a
	// recognisable string: two turns is three messages, and an aside in the middle would make five.
	if len(history) != 3 {
		t.Errorf("the request carried %d messages, want the two turns that were sent: %+v",
			len(history), history)
	}

	// And the conversation itself is two turns, not three: an aside creates none.
	if current, _ := e.Session(s.ID); len(current.Turns) != 2 {
		t.Errorf("the conversation has %d turns, want the two that were sent", len(current.Turns))
	}
}

// Nothing is recorded for a question that was never answered. A row with an empty answer would be
// drawn as a question the agent ignored, which is a worse account than no row at all.
func TestAFailedAsideIsNotRecorded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	e, _ := asideEngine(t, path)
	defer e.Close()

	s := e.Create("claude", "claude-opus-5")
	if _, err := e.Aside(context.Background(), s.ID, "   "); err == nil {
		t.Fatal("an empty question was accepted")
	}
	if got := e.Asides(s.ID); len(got) != 0 {
		t.Errorf("a refused aside was recorded anyway: %+v", got)
	}
}

// An engine with nowhere to write still answers asides. Storage is what makes them last, not what
// makes them work, and a conversation running without a history file is a supported state.
func TestAsidesWorkWithNoStorageAttached(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("it is in internal/config")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	s := e.Create("claude", "claude-opus-5")
	if _, err := e.Aside(context.Background(), s.ID, "where is the parser"); err != nil {
		t.Fatalf("Aside: %v", err)
	}
	if got := e.Asides(s.ID); len(got) != 0 {
		t.Errorf("an engine with no storage remembered %+v", got)
	}
}

// A failure to write must not cost the answer.
//
// The person asked a question and got one; losing that because the history file could not be written
// would be trading the thing they wanted for the thing that makes it convenient later. The failure
// still has to reach somebody, which is what the storage error hook is for, so this holds both
// halves at once.
func TestAFailedRecordingDoesNotCostTheAnswer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}

	var reported []error
	client := &scriptedClient{name: "claude", events: reply("the parser lives in internal/config")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()
	if err := e.WithStorage(storage, func(err error) { reported = append(reported, err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}

	s := e.Create("claude", "claude-opus-5")

	// The table taken out from under a live engine, which is the cheapest honest way to make one
	// write fail while everything around it goes on working.
	if _, err := storage.db.Exec(`DROP TABLE asides`); err != nil {
		t.Fatalf("dropping the table: %v", err)
	}

	answer, err := e.Aside(context.Background(), s.ID, "which file holds the parser")
	if err != nil {
		t.Fatalf("Aside reported %v, want the answer regardless of the write", err)
	}
	if answer == "" {
		t.Fatal("the answer was lost with the write")
	}

	if len(reported) == 0 {
		t.Fatal("the failed write reached nobody, so it is invisible as well as harmless")
	}
	if !strings.Contains(reported[0].Error(), "aside") {
		t.Errorf("the reported error does not say what failed: %v", reported[0])
	}

	// And reading them back fails the same way: reported, and answered with nothing rather than
	// with an error that would stop a conversation opening.
	reported = nil
	if kept := e.Asides(s.ID); len(kept) != 0 {
		t.Errorf("asides came back from a table that is not there: %+v", kept)
	}
	if len(reported) == 0 {
		t.Error("a failed read reached nobody")
	}
}
