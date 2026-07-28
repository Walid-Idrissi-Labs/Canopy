package session

import (
	"context"
	"fmt"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The green gate, which is what makes runway more than cruise with a better prompt.
//
// Every other tool asks what an agent may touch. This asks what state it may leave you in. The agent
// runs whatever it likes and a turn is only kept if the workspace still verifies afterwards; where it
// does not, the checkpoint taken before the turn is restored and the model is told what broke.
//
// Canopy can ask that question because it already has all three pieces: a checkpoint before every
// turn, a verifier that runs the project's own tests, and staleness detection that knows whether a
// green result still applies to this revision. A classifier guesses at intent. This checks an
// outcome.

// Gate answers whether a workspace is in a state worth keeping.
type Gate interface {
	// Check runs the workspace's own tests, waits for them, and says whether it verified.
	//
	// The three way answer is the whole reason this is not a bool. Green is a pass. Not green with a
	// reason is a fail and the turn goes back. An error is "the question could not be asked", which
	// is emphatically not a fail: rolling a turn back because the test runner itself fell over would
	// destroy somebody's work to punish an infrastructure problem, and the honest response to not
	// knowing is to say so and keep the work.
	Check(ctx context.Context, sessionID string) (green bool, reason string, err error)
}

// WithGate attaches the check that runway mode runs after every turn.
//
// Optional, and its absence is what stops runway being offered at all rather than something that
// makes it quietly behave like cruise. A mode that silently became the more dangerous one below it
// would be the worst possible failure of a safety setting.
func (e *Engine) WithGate(gate Gate) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.gate = gate
}

// keepGreen puts the workspace back when a turn in a mode that promised to leave it verified did not.
//
// Runs after the turn is terminal rather than during it, because the promise is about the state you
// are left in and a turn that breaks the build in step two and fixes it in step nine has kept it.
// Checking mid turn would roll back work that was on its way to being correct.
func (e *Engine) keepGreen(ctx context.Context, sessionID, turnID string) {
	// Released on every path out of here, including the ones that return early, because the
	// conversation was closed to new messages before the turn was marked terminal and a hold that is
	// not released is a conversation nobody can type into again.
	defer e.releaseConversation(sessionID)

	e.mu.Lock()
	// Through the resolver, not straight out of the map. A conversation reopened in runway has its
	// mode waiting to be restored from history rather than sitting in sessionMode, and reading the
	// map alone would find nothing, decide the mode does not keep the workspace green, and skip the
	// check entirely. Runway would then look exactly like runway and revert nothing, which is the
	// failure this whole file exists to prevent.
	applies := e.gateAppliesLocked(sessionID)
	gate := e.gate
	e.mu.Unlock()

	if !applies {
		return
	}

	green, reason, err := gate.Check(ctx, sessionID)
	switch {
	case err != nil:
		// Said on the turn and nothing is reverted. "The checks could not be run" and "the checks
		// failed" are different facts, and treating the first as the second is how a mode meant to
		// protect work becomes the thing that destroys it.
		e.update(sessionID, turnID, func(t *core.Turn) {
			t.RolledBack = fmt.Sprintf(
				"kept, because the checks could not be run to decide otherwise: %v", err)
		})
		e.publishSession(sessionID)
		return

	case green:
		return
	}

	// Why it failed leads, whatever happens next. The restore is what Canopy did about it and the
	// reason is what the reader has to act on, and an earlier version of this dropped the reason
	// entirely on the path where the restore also failed, which is the one moment somebody most
	// needs to know what went wrong.
	broke := "the workspace did not verify after this turn"
	if reason != "" {
		broke += ": " + reason
	}

	note := "rolled back, " + broke
	if err := e.Undo(ctx, sessionID, turnID); err != nil {
		// The check said no and the restore failed, which is the worst of the three outcomes and the
		// one that must never be quiet: the workspace is red and nothing put it back.
		note = fmt.Sprintf("%s, and it could not be put back: %v", broke, err)
	}

	e.update(sessionID, turnID, func(t *core.Turn) { t.RolledBack = note })
	e.publishSession(sessionID)
}

// publishSession tells the interface something about the conversation moved.
func (e *Engine) publishSession(sessionID string) {
	e.events.Publish(core.Event{Kind: core.EventSessionsChanged, SessionID: sessionID})
}

// Whether a mode can be entered at all is decided in SetMode, next to the trust ceiling, so that
// every reason a mode can be refused is in one place. Both of the bottom two need a way back: cruise
// runs commands that discard work without asking and outside a git repository there is no checkpoint
// to undo them with, and runway needs the gate as well, since without it runway is cruise wearing a
// promise it cannot keep.
