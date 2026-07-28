package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
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
