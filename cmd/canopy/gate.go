package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/verify"
)

// The concrete green gate, joining the verifier to the session engine.
//
// The behaviour is the engine's and is tested there against a stub. What lives here is the only part
// that needs a real test runner: start the project's own tests, wait for them, and read the roll-up
// the dashboard already reads. Deliberately thin, because everything in this file runs only against
// a real repository and is therefore the hardest part to test.

// gate runs the workspace's own tests and reports whether it verified.
type gate struct {
	verifier *verify.Verifier

	// agent is the name the verifier watches this workspace under. The conversation the engine asks
	// about is the main agent's, and the verifier knows workspaces rather than sessions.
	agent string
}

// gateTimeout bounds one check.
//
// Generous, because it is a whole test suite and the alternative to waiting is deciding a turn's
// fate on incomplete evidence. A check that times out is an error rather than a failure, so the turn
// is kept: see the engine's own note on why those two must not be confused.
const gateTimeout = 10 * time.Minute

// gatePoll is how often the runner is asked whether the tests have finished.
//
// The runner reports terminal state rather than offering a channel to wait on, so this polls. Slow
// enough to cost nothing over a run measured in minutes.
const gatePoll = 250 * time.Millisecond

func (g gate) Check(ctx context.Context, _ string) (bool, string, error) {
	if g.verifier == nil {
		return false, "", fmt.Errorf("nothing is verifying this workspace")
	}

	ctx, cancel := context.WithTimeout(ctx, gateTimeout)
	defer cancel()

	if err := g.verifier.Verify(ctx, g.agent); err != nil {
		return false, "", fmt.Errorf("running the checks: %w", err)
	}

	for {
		snapshot, ok := g.verifier.Snapshot(g.agent)
		if !ok {
			return false, "", fmt.Errorf("the workspace %s is no longer being watched", g.agent)
		}
		if settled(snapshot) {
			rollup := core.RollUp(snapshot)
			if rollup.Green {
				return true, "", nil
			}
			// The roll-up's own reason, which is the same sentence the dashboard shows. Two
			// explanations of one verdict is one explanation that goes stale.
			return false, rollup.Reason, nil
		}

		select {
		case <-ctx.Done():
			return false, "", fmt.Errorf("the checks did not finish within %s", gateTimeout)
		case <-time.After(gatePoll):
		}
	}
}

// settled reports whether every test has stopped moving.
//
// Reading the roll-up before they have would judge a turn on a suite that is still running, which
// reports whatever happens to have failed first as the verdict.
func settled(snapshot core.WorkspaceSnapshot) bool {
	for _, test := range snapshot.Tests {
		switch test.Explain(snapshot.Revision).State {
		case core.TestRunning, core.TestQueued:
			return false
		}
	}
	return true
}
