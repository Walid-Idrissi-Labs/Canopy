package session_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// Live tests, which talk to a real provider and are skipped unless asked for.
//
// They exist because everything else in this repository is proved against a scripted stream, and a
// scripted stream is written from the same understanding of the API as the code it tests. If that
// understanding is wrong, both are wrong together and every test still passes. This is the only
// thing in the suite that can catch that.
//
// Run them with a stored credential:
//
//	CANOPY_LIVE_KEY=nim CANOPY_LIVE_MODEL=minimaxai/minimax-m2.7 go test ./internal/session/ -run Live -v
//
// The credential is read from the key store by name. No secret is ever passed on a command line or
// written into this repository.
func liveConfig(t *testing.T) (keyName, model string) {
	t.Helper()

	keyName = os.Getenv("CANOPY_LIVE_KEY")
	if keyName == "" {
		t.Skip("set CANOPY_LIVE_KEY to the name of a stored credential to run live tests")
	}
	return keyName, os.Getenv("CANOPY_LIVE_MODEL")
}

func liveEngine(t *testing.T) (*session.Engine, string, string) {
	t.Helper()

	keyName, model := liveConfig(t)

	store, err := keys.Open()
	if err != nil {
		t.Fatalf("opening the key store: %v", err)
	}

	engine := session.New(session.NewKeyResolver(store, "test"))
	t.Cleanup(engine.Close)
	return engine, keyName, model
}

func waitForLiveTurn(t *testing.T, e *session.Engine, sessionID, turnID string) core.Turn {
	t.Helper()

	deadline := time.After(3 * time.Minute)
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
			t.Fatal("the turn never finished, which on a live provider usually means the request " +
				"was accepted and then abandoned")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// The whole path in one test: a stored credential, a provider client chosen from it, a streamed
// reply, a turn state and a usage record.
func TestLiveTurnCompletes(t *testing.T) {
	engine, keyName, model := liveEngine(t)

	s := engine.Create(keyName, model)
	turnID, err := engine.Send(s.ID, "Reply with exactly the two words: canopy works")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	turn := waitForLiveTurn(t, engine, s.ID, turnID)

	if turn.State != core.TurnComplete {
		t.Fatalf("turn ended as %s: %s", turn.State, turn.Error)
	}
	if turn.Text == "" {
		t.Error("a completed turn with no text means the stream was parsed wrongly")
	}
	if !strings.Contains(strings.ToLower(turn.Text), "canopy") {
		t.Errorf("reply = %q, which does not look like an answer to the question", turn.Text)
	}

	// The reason to check this against a live provider rather than a script: usage only arrives if
	// the request asked for it, and a scripted stream cannot tell us whether it did.
	if turn.Usage.InputTokens == 0 || turn.Usage.OutputTokens == 0 {
		t.Errorf("usage = %+v, want real counts. A turn with no usage cannot be costed or budgeted",
			turn.Usage)
	}
	if turn.Provider == "" {
		t.Error("the turn should record which credential answered")
	}
	if turn.EndedAt.IsZero() {
		t.Error("a finished turn with no end time counts up forever on screen")
	}
}

// Each turn has to carry the conversation so far, or the model answers every message as though it
// were the first. Worth checking live because it is the one thing a scripted stream cannot fail at.
func TestLiveConversationRemembers(t *testing.T) {
	engine, keyName, model := liveEngine(t)

	s := engine.Create(keyName, model)

	turnID, err := engine.Send(s.ID, "My favourite colour is heliotrope. Reply with just: noted")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	first := waitForLiveTurn(t, engine, s.ID, turnID)
	if first.State != core.TurnComplete {
		t.Fatalf("first turn ended as %s: %s", first.State, first.Error)
	}

	turnID, err = engine.Send(s.ID, "What is my favourite colour? Reply with just the colour.")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	second := waitForLiveTurn(t, engine, s.ID, turnID)
	if second.State != core.TurnComplete {
		t.Fatalf("second turn ended as %s: %s", second.State, second.Error)
	}

	if !strings.Contains(strings.ToLower(second.Text), "heliotrope") {
		t.Errorf("the model did not remember the first turn, so history is not being sent.\n"+
			"reply = %q", second.Text)
	}
}

// Cancelling has to actually close the connection, not just stop reading it.
func TestLiveCancelStopsTheTurn(t *testing.T) {
	engine, keyName, model := liveEngine(t)

	s := engine.Create(keyName, model)
	turnID, err := engine.Send(s.ID, "Count slowly from one to two hundred, one number per line.")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Long enough that the reply is genuinely in flight, short enough that it cannot have finished.
	time.Sleep(2 * time.Second)
	engine.Cancel(s.ID)

	turn := waitForLiveTurn(t, engine, s.ID, turnID)
	if turn.State != core.TurnInterrupted {
		t.Errorf("state = %s, want interrupted. A cancelled turn presented as complete is the "+
			"chat form of a stale green", turn.State)
	}
	if turn.State.Whole() {
		t.Error("a cancelled turn must never read as a whole answer")
	}
}

// The whole persistence path with a real provider: ask, quit, come back, and find it again.
//
// Worth doing live rather than only with a scripted stream, because the text a real model returns
// is what actually goes through the search index, and a model that replies with punctuation or
// code fences is the case a hand written fixture never covers.
func TestLiveHistorySurvivesARestart(t *testing.T) {
	keyName, model := liveConfig(t)

	path := filepath.Join(t.TempDir(), "history.db")

	store, err := keys.Open()
	if err != nil {
		t.Fatalf("opening the key store: %v", err)
	}

	storage, err := session.OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	engine := session.New(session.NewKeyResolver(store, "test"))
	if err := engine.WithStorage(storage, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}

	s := engine.Create(keyName, model)
	turnID, err := engine.Send(s.ID,
		"Reply with one sentence that contains the word chrysanthemum.")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	turn := waitForLiveTurn(t, engine, s.ID, turnID)
	if turn.State != core.TurnComplete {
		t.Fatalf("turn ended as %s: %s", turn.State, turn.Error)
	}
	engine.Close()

	// A second run, as though the program had been quit and started again.
	reopened, err := session.OpenStorage(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = reopened.Close() }()

	loaded, err := reopened.Load(s.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Turns) != 1 || loaded.Turns[0].Text != turn.Text {
		t.Errorf("the conversation did not come back intact:\n got %+v\nwant %q",
			loaded.Turns, turn.Text)
	}

	hits, err := reopened.Search("chrysanthemum", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Errorf("a real reply was saved but is not findable. Reply was:\n%s", turn.Text)
	}
}
