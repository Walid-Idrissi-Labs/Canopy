package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/git"
)

// Modes are a safety setting, so every way out of one is a way past the permission layer.
//
// These are the ways out that existed and were not obvious: forking a conversation, and quitting
// one. Both dropped the mode and resolved the level from configuration instead, which lands in
// build. Neither showed anything on screen, because from the interface's point of view nothing had
// gone wrong: the box read "build" and the agent was in build.

// A fork continues the same work down a second path, so it continues under the same rules.
func TestAForkKeepsTheModeItWasForkedFrom(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)

	source := answered(t, e, client, 2)
	plan, _ := core.ModeByName(core.ModePlan)
	if err := e.SetMode(source.ID, plan); err != nil {
		t.Fatalf("SetMode: %v", err)
	}

	forked, err := e.Fork(source.ID, source.Turns[0].ID)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	if got := e.Mode(forked.ID).Name; got != core.ModePlan {
		t.Errorf("the fork is in %q mode, want %q", got, core.ModePlan)
	}
	if got := e.Trust(forked.ID); got != core.TrustReadOnly {
		t.Errorf("the fork is trusted %q, want %q: forking widened what the agent may do",
			got, core.TrustReadOnly)
	}
}

// The harder half of the same bug. A conversation that nobody set a mode on is still in one,
// resolved from how its agent was configured, and the fork has no agent of its own at all. Copying
// only what the map held would leave the fork resolving against the engine's own level, so an agent
// deliberately confined to reading would produce a fork that can edit.
func TestAForkOfAConfinedAgentIsAlsoConfined(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)

	added, err := e.AddAgent(context.Background(), Agent{
		Name: "reader", KeyName: "claude", Model: "claude-opus-5", Trust: core.TrustReadOnly,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	turnID, err := e.Send(added.SessionID, "what does this do")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, added.SessionID, turnID)
	source, _ := e.Session(added.SessionID)

	forked, err := e.Fork(source.ID, source.Turns[0].ID)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}

	if got := e.Trust(forked.ID); got != core.TrustReadOnly {
		t.Errorf("the fork of a read-only agent is trusted %q, want %q", got, core.TrustReadOnly)
	}
}

// Quitting must not be a way to widen what an agent may do.
func TestTheModeSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	client := &scriptedClient{name: "claude", events: reply("answer")}

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	first := New(fixedResolver{client: client, id: anthropicID()})
	if err := first.WithStorage(storage, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}

	created := first.Create("claude", "claude-opus-5")
	plan, _ := core.ModeByName(core.ModePlan)
	if err := first.SetMode(created.ID, plan); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	first.Close()

	second := reopen(t, path, client)
	if got := second.Mode(created.ID).Name; got != core.ModePlan {
		t.Errorf("the conversation came back in %q mode, want %q", got, core.ModePlan)
	}
	if got := second.Trust(created.ID); got != core.TrustReadOnly {
		t.Errorf("the conversation came back trusted %q, want %q", got, core.TrustReadOnly)
	}
}

// Runway promises a turn is put back if it ends red. Reopened where nothing can check the
// workspace, that promise cannot be kept, and the mode it shares a trust level with is cruise, which
// keeps the turn instead. Resolving a stored mode by its level would therefore turn the safest of
// the two broad modes into the least safe, on a restart, silently.
func TestARestoredRunwayNeverBecomesCruise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	client := &scriptedClient{name: "claude", events: reply("answer")}

	created := storedWithMode(t, path, client, core.ModeRunway)

	second := reopen(t, path, client)
	got := second.Mode(created)
	if got.Name == core.ModeCruise {
		t.Fatal("a stored runway came back as cruise, which keeps the turns runway would revert")
	}
	if got.Trust.AtLeast(core.TrustBroad) {
		t.Errorf("a stored runway came back as %q, trusted %q: it kept broad trust with nothing to "+
			"check the workspace with", got.Name, got.Trust)
	}
}

// A mode name this build has never heard of means the conversation was left somewhere that no longer
// exists. Nothing can be assumed about it, so it comes back in the narrowest mode there is rather
// than in whatever the configuration would otherwise allow.
func TestAnUnknownStoredModeComesBackAsPlan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	client := &scriptedClient{name: "claude", events: reply("answer")}

	created := storedWithMode(t, path, client, "autopilot")

	second := reopen(t, path, client)
	if got := second.Mode(created).Name; got != core.ModePlan {
		t.Errorf("a conversation stored in an unknown mode came back as %q, want %q",
			got, core.ModePlan)
	}
}

// storedWithMode writes a conversation to a fresh database with a mode recorded against it.
//
// Written through storage rather than through SetMode, because the modes worth testing here are the
// ones SetMode would refuse to set in a bare engine. What is under test is what happens on the way
// back in, and that has to be reachable without first arranging a repository and a test runner.
func storedWithMode(t *testing.T, path string, client *scriptedClient, mode string) string {
	t.Helper()

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	e := New(fixedResolver{client: client, id: anthropicID()})
	if err := e.WithStorage(storage, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}
	created := e.Create("claude", "claude-opus-5")
	// Before the engine is closed, since closing it closes the database underneath.
	if err := storage.SaveSessionMode(created.ID, mode); err != nil {
		t.Fatalf("SaveSessionMode: %v", err)
	}
	e.Close()
	return created.ID
}

func reopen(t *testing.T, path string, client *scriptedClient) *Engine {
	t.Helper()

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)
	if err := e.WithStorage(storage, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}
	return e
}

// A conversation reopened in runway is still checked after every turn.
//
// End to end, because nothing else covered the restored path reaching the gate at all. It does not
// discriminate on how keepGreen reads the mode, and that is worth saying rather than leaving somebody
// to discover it by mutating: a turn resolves the mode twice on its way past, once for the tool list
// and once for the system prompt, so reading the map directly and reading it through the resolver
// give the same answer by the time the gate is asked. The resolver is used there anyway, because
// two unrelated pieces of code happening to run in the right order is not a safety property.
func TestARestoredRunwayStillRunsTheGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	client := &scriptedClient{name: "claude", events: reply("done")}

	created := storedWithMode(t, path, client, core.ModeRunway)

	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	gate := &stubGate{green: false, reason: "2 of 14 tests failed"}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)
	e.WithGate(gate)
	// Runway needs somewhere to put the workspace back to as well as something to check it with, or
	// it is refused and this test would pass by never restoring runway at all. The taker is pointed
	// at an empty directory: what is under test is whether the gate is asked, and the existing
	// runway tests already tolerate a restore that cannot succeed.
	e.WithCheckpoints(git.NewTaker(t.TempDir()))
	// Errors are ignored rather than failed on, because the same callback carries the checkpoint
	// failure this test deliberately arranges by pointing the taker at a directory that is not a
	// repository.
	if err := e.WithStorage(storage, func(error) {}); err != nil {
		t.Fatalf("WithStorage: %v", err)
	}

	// Asserted before anything else touches the conversation, because what made this work by
	// accident was a turn resolving the mode on its way past for the system prompt.
	if got := e.Mode(created).Name; got != core.ModeRunway {
		t.Fatalf("the conversation came back in %q, so this is not testing what it means to", got)
	}

	turnID, err := e.Send(created, "refactor the parser")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, created, turnID)
	waitForCheck(t, e, created)

	if gate.ran() == 0 {
		t.Error("the gate was never asked, so a restored runway kept a turn it should have reverted")
	}
}

// blockingGate holds the check open until a test lets it go.
//
// The window this exists to observe is real and short: a turn goes terminal, its checks run, and the
// workspace may still be put back. Holding the check open makes that window wide enough to send a
// message into, which is exactly what the interface would do on its own.
type blockingGate struct {
	entered chan struct{}
	release chan struct{}
}

func newBlockingGate() *blockingGate {
	return &blockingGate{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (g *blockingGate) Check(context.Context, string) (bool, string, error) {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.release
	return false, "the tests failed", nil
}

// A terminal turn is not a finished one, and the difference is somebody's work.
//
// The turn goes terminal when the model stops talking, and in runway the checks run after that and
// may roll the workspace back. If the conversation reopens at the terminal transition, the interface
// accepts the next message, that turn starts editing, and then the first turn's rollback restores a
// checkpoint over what the second one just did. A safety feature destroying work is the worst
// outcome available here, worse than not checking at all.
func TestAConversationStaysClosedUntilItsChecksHaveFinished(t *testing.T) {
	gate := newBlockingGate()
	e, _ := runwayEngine(t, gate)
	e.WithCheckpoints(git.NewTaker(t.TempDir()))

	created := e.Create("claude", "claude-opus-5")
	runway, _ := core.ModeByName(core.ModeRunway)
	e.mu.Lock()
	e.sessionMode[created.ID] = runway
	e.mu.Unlock()

	turnID, err := e.Send(created.ID, "refactor the parser")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, created.ID, turnID)

	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the gate was never entered")
	}

	// The turn is terminal and the checks are running. This is the moment the interface would offer
	// the message box back.
	if _, err := e.Send(created.ID, "and now rename the fields"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Send while the checks were running = %v, want ErrBusy: a second turn would have "+
			"started editing under a rollback that was already coming", err)
	}

	close(gate.release)
	waitForCheck(t, e, created.ID)

	// And it reopens afterwards, or the conversation would be closed forever.
	if _, err := e.Send(created.ID, "and now rename the fields"); err != nil {
		t.Errorf("Send after the checks finished = %v, want the conversation to reopen", err)
	}
}
