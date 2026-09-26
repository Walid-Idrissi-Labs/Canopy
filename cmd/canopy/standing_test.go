package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

type snapshotOnly struct{ snap core.ProjectSnapshot }

func (s snapshotOnly) Snapshot() core.ProjectSnapshot  { return s.snap }
func (s snapshotOnly) Events(uint64) <-chan core.Event { return nil }

// An agent's standing is the roll-up of the workspace it works in, found by its directory however
// the path is spelled, and an unknown directory says there is nothing recorded rather than green.
func TestAnAgentsStandingIsItsWorkspacesRollUp(t *testing.T) {
	root, agent := t.TempDir(), t.TempDir()
	store := snapshotOnly{core.ProjectSnapshot{Workspaces: []core.WorkspaceSnapshot{
		{Path: root, Name: "main", Revision: core.RevisionKey{HeadSHA: "1111111111"}},
		{Path: agent, Name: "agent", Revision: core.RevisionKey{HeadSHA: "abcdef1234"},
			Tests: []core.TestSnapshot{{Name: "unit", Required: true}}},
	}}}
	standing := standingIn(store, root)
	resolved, _ := filepath.EvalSymlinks(agent)
	got := standing(resolved + "/")
	if !strings.HasPrefix(got, "not verified at abcdef1") {
		t.Fatalf("the agent's workspace reads %q", got)
	}
	if got := standing(""); !strings.Contains(got, "at 1111111") {
		t.Fatalf("an agent working in the repository reads %q", got)
	}
	if got := standing(t.TempDir()); got != "no verification recorded for its workspace" {
		t.Fatalf("an unknown directory reads %q", got)
	}
}
