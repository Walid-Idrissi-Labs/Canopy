package session

import (
	"fmt"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Forking a conversation: go back to a turn and try it the other way.
//
// The thing this replaces is bad in two familiar ways. Starting a fresh session throws away
// everything the agent had learned about the project. Arguing with an agent that has already
// committed to an approach means fighting its own context, which it will keep defending because
// the reasoning for the first approach is still in front of it.
//
// A fork gives you neither. The history up to the fork point is the same history, and what comes
// after diverges, with no trace of the branch you did not take. It maps onto how people already
// think in git, and at A5 a fork becomes a second agent on a second branch, which is where it earns
// its place.

// Fork creates a new session sharing this one's history up to and including a turn.
//
// The original is not modified. That is the property the whole feature rests on: somebody who forks
// to try something risky has to be able to come back and find the original exactly as they left it,
// or forking is just a slower way of losing work.
func (e *Engine) Fork(sessionID, throughTurnID string) (core.Session, error) {
	e.mu.Lock()

	source, ok := e.sessions[sessionID]
	if !ok {
		e.mu.Unlock()
		return core.Session{}, fmt.Errorf("no session %q", sessionID)
	}

	cut := -1
	for i, turn := range source.Turns {
		if turn.ID == throughTurnID {
			cut = i
			break
		}
	}
	if cut < 0 {
		e.mu.Unlock()
		return core.Session{}, fmt.Errorf("session %q has no turn %q", sessionID, throughTurnID)
	}
	if !source.Turns[cut].State.Terminal() {
		// Forking from a turn still in flight would copy an answer that is still arriving, and the
		// copy would stop growing while the original kept going. Two conversations that were meant
		// to be identical up to a point, differing at that point.
		e.mu.Unlock()
		return core.Session{}, fmt.Errorf(
			"turn %q has not finished yet, so there is nothing settled to fork from", throughTurnID)
	}

	e.nextID++
	now := e.events.Now()
	forked := &core.Session{
		ID:          fmt.Sprintf("session-%d", e.nextID),
		Title:       source.Title,
		WorkspaceID: source.WorkspaceID,
		KeyName:     source.KeyName,
		Model:       source.Model,
		CreatedAt:   now,
		UpdatedAt:   now,

		// Copied, not shared. A shared backing array would mean the next turn appended to either
		// session could land in the other one, which is the exact failure forking exists to avoid.
		Turns: append([]core.Turn(nil), source.Turns[:cut+1]...),

		// The fork point, recorded on both sides.
		ForkedFrom:  source.ID,
		ForkedAt:    throughTurnID,
		ForkedWhen:  now,
		Compactions: compactionsThrough(source.Compactions, cut+1),
	}

	source.Forks = append(source.Forks, core.ForkRef{
		SessionID: forked.ID,
		AtTurnID:  throughTurnID,
		At:        now,
	})
	source.UpdatedAt = now

	e.sessions[forked.ID] = forked
	e.order = append(e.order, forked.ID)

	out := copySession(*forked)
	origin := copySession(*source)
	e.mu.Unlock()

	// Both sides are written, because the fork point is visible from both and a record that only
	// one end knows about is one that disagrees with itself after a restart.
	e.persistSession(origin)
	e.persistSession(out)
	for i, turn := range out.Turns {
		e.persistTurn(out.ID, i, turn)
	}

	e.events.Publish(core.Event{Kind: core.EventSessionsChanged, SessionID: forked.ID})
	e.events.Publish(core.Event{Kind: core.EventSessionUpdated, SessionID: source.ID})
	return out, nil
}

// compactionsThrough keeps the compactions that still make sense in a shortened conversation.
//
// A compaction covering turns the fork does not contain would tell the model that turns which are
// not there have been summarised, and the summary would describe a conversation the fork never had.
func compactionsThrough(compactions []core.Compaction, turns int) []core.Compaction {
	var out []core.Compaction
	for _, compaction := range compactions {
		if compaction.Through <= turns {
			out = append(out, compaction)
		}
	}
	return out
}
