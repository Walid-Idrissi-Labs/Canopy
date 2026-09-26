package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// standingSource is the part of the verifier an agent's standing is read from.
type standingSource interface {
	Snapshot(agent string) (core.WorkspaceSnapshot, bool)
	Head(ctx context.Context, agent string) (core.RevisionKey, bool)
}

// standingOf says how verification stands for an agent: green or not, at which revision, and why.
// The revision is read from git now rather than taken from the last poll, since a report is made
// the moment a turn ends and the poll can still hold the revision from before its last edits; a
// green run of that older revision would read as green for code nobody tested.
func standingOf(source standingSource) func(agent string) string {
	return func(agent string) string {
		snapshot, ok := source.Snapshot(agent)
		if !ok {
			return "no verification recorded for it"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		head, ok := source.Head(ctx, agent)
		if !ok {
			return "not verified: its revision could not be read just now"
		}
		snapshot.Revision = head
		rollup := core.RollUp(snapshot)
		verdict := "not verified"
		if rollup.Green {
			verdict = "green"
		}
		line := fmt.Sprintf("%s at %s, tests %s", verdict, head.Short(), rollup.Tests)
		if rollup.Reason != "" {
			line += ": " + rollup.Reason
		}
		return line
	}
}
