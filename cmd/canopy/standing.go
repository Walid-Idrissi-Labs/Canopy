package main

import (
	"fmt"
	"path/filepath"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// standingIn says how verification stands in the workspace at a directory, from the roll-up the
// review screen shows: green or not, at which revision, and why, stale included.
func standingIn(store core.SnapshotStore, root string) func(dir string) string {
	return func(dir string) string {
		if dir == "" {
			dir = root
		}
		want := cleanPath(dir)
		for _, workspace := range store.Snapshot().Workspaces {
			if cleanPath(workspace.Path) != want {
				continue
			}
			rollup := core.RollUp(workspace)
			verdict := "not verified"
			if rollup.Green {
				verdict = "green"
			}
			line := fmt.Sprintf("%s at %s, tests %s", verdict, workspace.Revision.Short(), rollup.Tests)
			if rollup.Reason != "" {
				line += ": " + rollup.Reason
			}
			return line
		}
		return "no verification recorded for its workspace"
	}
}

// cleanPath is a directory as it is on disk, symlinks resolved where they can be.
func cleanPath(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}
