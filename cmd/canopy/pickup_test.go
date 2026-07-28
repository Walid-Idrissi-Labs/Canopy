package main

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// The pickup code is a small number somebody reads off a terminal and retypes, so `canopy pickup 7`
// in the wrong window is a matter of time rather than a matter of luck. Nothing downstream would
// notice: the agent is given this directory's files and that conversation's history, so the model is
// told it was halfway through editing files that are not here, under a plan for a repository it
// cannot see, and it acts on the difference in a workspace nobody meant it to touch.
func TestAConversationFromAnotherProjectWillNotOpenHere(t *testing.T) {
	engine := session.New(nil)
	t.Cleanup(engine.Close)

	engine.SetProjectID("project-a")
	created := engine.Create("claude", "claude-opus-5")

	err := belongsHere(engine, created.ID, "project-b")
	if err == nil {
		t.Fatal("a conversation from another project was opened here")
	}
	// The code rather than the session ID, because the code is what the person has in front of them.
	if code := session.Code(created.ID); !strings.Contains(err.Error(), code) {
		t.Errorf("the refusal does not name the conversation as %q: %v", code, err)
	}
}

// Its own project is the ordinary case and has to keep working, or the guard has cost the feature.
func TestAConversationOpensInTheProjectItBelongsTo(t *testing.T) {
	engine := session.New(nil)
	t.Cleanup(engine.Close)

	engine.SetProjectID("project-a")
	created := engine.Create("claude", "claude-opus-5")

	if err := belongsHere(engine, created.ID, "project-a"); err != nil {
		t.Errorf("a conversation would not open in its own project: %v", err)
	}
}

// Both unknowns are allowed through, deliberately. A conversation recorded before history was scoped
// by project has no association at all, and a directory that is not a repository produces no project
// to compare against. Refusing either would break resuming for people who have done nothing wrong,
// to guard a case where there is nothing to tell apart.
func TestAnUnscopedConversationStillOpens(t *testing.T) {
	engine := session.New(nil)
	t.Cleanup(engine.Close)

	// No SetProjectID, so this conversation predates project scoping as far as the engine knows.
	created := engine.Create("claude", "claude-opus-5")

	if err := belongsHere(engine, created.ID, "project-b"); err != nil {
		t.Errorf("a conversation with no recorded project was refused: %v", err)
	}

	engine.SetProjectID("project-a")
	scoped := engine.Create("claude", "claude-opus-5")
	if err := belongsHere(engine, scoped.ID, ""); err != nil {
		t.Errorf("a conversation was refused outside a repository, where there is nothing to "+
			"compare it against: %v", err)
	}
}
