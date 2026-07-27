package session

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Somebody who forks to try something risky has to be able to come back and find the original
// exactly as they left it, or forking is just a slower way of losing work.
func TestForkingDoesNotTouchTheOriginal(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	source := answered(t, e, client, 5)
	forkPoint := source.Turns[2].ID

	forked, err := e.Fork(source.ID, forkPoint)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	if len(forked.Turns) != 3 {
		t.Errorf("%d turns in the fork, want everything up to and including the fork point", len(forked.Turns))
	}
	if forked.ID == source.ID {
		t.Error("a fork must be its own session")
	}

	after, _ := e.Session(source.ID)
	if len(after.Turns) != 5 {
		t.Errorf("the original has %d turns after forking, want its original 5", len(after.Turns))
	}

	// And they must genuinely be separate afterwards. A shared backing array would mean the next
	// turn on either lands in the other, which is the exact failure forking exists to avoid.
	client.events = reply("only in the fork")
	turnID, err := e.Send(forked.ID, "a different question")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, forked.ID, turnID)

	after, _ = e.Session(source.ID)
	if len(after.Turns) != 5 {
		t.Errorf("a turn sent to the fork landed in the original: %d turns", len(after.Turns))
	}
	for _, turn := range after.Turns {
		if turn.Text == "only in the fork" {
			t.Error("the fork's answer appeared in the original")
		}
	}
}

// The question gets asked from both directions: "where did this come from" when reading a fork, and
// "what did I try from here" when reading the original.
func TestTheForkPointIsRecordedOnBothSides(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	source := answered(t, e, client, 5)
	forkPoint := source.Turns[2].ID

	forked, err := e.Fork(source.ID, forkPoint)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	if forked.ForkedFrom != source.ID || forked.ForkedAt != forkPoint {
		t.Errorf("the fork does not know where it came from: from=%q at=%q",
			forked.ForkedFrom, forked.ForkedAt)
	}
	if forked.ForkedWhen.IsZero() {
		t.Error("a fork with no time cannot be placed in a list")
	}

	after, _ := e.Session(source.ID)
	if len(after.Forks) != 1 {
		t.Fatalf("the original records %d forks, want 1", len(after.Forks))
	}
	if after.Forks[0].SessionID != forked.ID || after.Forks[0].AtTurnID != forkPoint {
		t.Errorf("fork record = %+v", after.Forks[0])
	}
}

// Forking from a turn still in flight would copy an answer that is still arriving, and the copy
// would stop growing while the original kept going.
func TestForkingFromAnUnfinishedTurnIsRefused(t *testing.T) {
	gate := make(chan struct{})
	client := &scriptedClient{name: "claude", gate: gate, events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	s := e.Create("claude", "m")
	turnID, err := e.Send(s.ID, "question")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, err = e.Fork(s.ID, turnID)
	if err == nil {
		t.Error("forking from a turn that has not settled should be refused")
	}
	if err != nil && !strings.Contains(err.Error(), "not finished") {
		t.Errorf("the error should say why, got %q", err)
	}

	close(gate)
}

func TestForkingFromSomethingThatDoesNotExist(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	source := answered(t, e, client, 2)

	if _, err := e.Fork("nope", source.Turns[0].ID); err == nil {
		t.Error("forking a session that does not exist should be an error")
	}
	if _, err := e.Fork(source.ID, "no-such-turn"); err == nil {
		t.Error("forking at a turn that does not exist should be an error")
	}
}

// Both sessions have to be independently resumable, and a fork record only one end knows about is
// one that disagrees with itself after a restart.
func TestForksSurviveARestart(t *testing.T) {
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

	source := answered(t, first, client, 5)
	forked, err := first.Fork(source.ID, source.Turns[2].ID)
	if err != nil {
		t.Fatalf("Fork: %v", err)
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

	loadedFork, ok := second.Session(forked.ID)
	if !ok {
		t.Fatal("the fork did not come back")
	}
	if len(loadedFork.Turns) != 3 {
		t.Errorf("%d turns in the resumed fork, want 3", len(loadedFork.Turns))
	}
	if loadedFork.ForkedFrom != source.ID {
		t.Errorf("the resumed fork does not know where it came from: %q", loadedFork.ForkedFrom)
	}

	loadedSource, ok := second.Session(source.ID)
	if !ok {
		t.Fatal("the original did not come back")
	}
	if len(loadedSource.Turns) != 5 {
		t.Errorf("%d turns in the resumed original, want 5", len(loadedSource.Turns))
	}
	if len(loadedSource.Forks) != 1 || loadedSource.Forks[0].SessionID != forked.ID {
		t.Errorf("the original lost its record of the fork: %+v", loadedSource.Forks)
	}
}

// A compaction covering turns the fork does not contain would tell the model that turns which are
// not there have been summarised, and the summary would describe a conversation the fork never had.
func TestAForkDropsCompactionsItDoesNotCover(t *testing.T) {
	compactions := []core.Compaction{
		{Summary: "covers the first two", Through: 2},
		{Summary: "covers the first six", Through: 6},
	}

	kept := compactionsThrough(compactions, 3)
	if len(kept) != 1 {
		t.Fatalf("%d compactions kept, want only the one the fork covers: %+v", len(kept), kept)
	}
	if kept[0].Through != 2 {
		t.Errorf("kept = %+v", kept[0])
	}
}
