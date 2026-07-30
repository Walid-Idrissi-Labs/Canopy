package session

import (
	"context"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Compaction shortens what gets sent, and never shortens what is kept.
//
// The distinction is the whole design. Every turn stays in the session and in storage, searchable
// and readable, exactly as it was. What changes is the history handed to the provider on the next
// request: the older turns are replaced by a summary of them.
//
// It is also never silent. An agent that quietly forgets half of what it was told and carries on
// answering is the same class of problem as a test result that says passing about code it never
// ran: confident, wrong, and undetectable from outside. So a compaction adds a visible marker to
// the transcript saying what was summarised and how far back it goes.

// keepRecentTurns is how many exchanges survive compaction verbatim.
//
// The recent ones are where the actual work is: what file we are editing, what just failed, what
// the user corrected a moment ago. Summarising those is how an agent loses the thread mid task and
// starts re proposing something it was already told not to do.
const keepRecentTurns = 4

// compactionPrompt asks for the summary.
//
// Written to preserve decisions and constraints rather than to be readable prose, because the only
// consumer is the model on the next turn. Asking for something a person would enjoy reading
// produces something that drops the file paths.
const compactionPrompt = `Summarise the conversation above so it can replace those messages as ` +
	`context for continuing the work.

Keep, in this order of priority:
  - decisions made and the reasoning behind them
  - constraints, preferences and corrections the user gave
  - files, paths, commands and identifiers that came up
  - what has been done so far, and what was about to happen next
  - anything that was tried and did not work, so it is not tried again

Leave out pleasantries and restatements. Write it as notes for yourself, not as a report for ` +
	`somebody else. Be specific: a summary that says "discussed the API" is worse than no summary.`

// CompactionResult describes what a compaction did.
type CompactionResult struct {
	// Summary replaces the turns before Through.
	Summary string
	// Through is the number of turns that were summarised.
	Through int
	// TokensBefore and TokensAfter are what the context measured either side of it.
	TokensBefore int
	TokensAfter  int
}

// CompactionPlan is what compacting a conversation now would do, worked out without sending
// anything.
//
// It exists because the key that starts a compaction has to say what it is about to spend before it
// spends it, and no unconfirmed keystroke may start a paid call. A screen working this out for
// itself would be a second opinion about how much of a conversation survives, free to drift from
// the one Compact actually acts on, and the sentence somebody agreed to would stop describing what
// happened.
type CompactionPlan struct {
	// Turns is how many exchanges would be summarised and Kept how many stay verbatim.
	Turns int
	Kept  int

	// Tokens is roughly what would be sent, by the same estimate the result reports afterwards.
	// Rough is the honest word: see bytesPerToken.
	Tokens int
}

// Possible reports whether there is anything to compact. False on a conversation short enough that
// everything in it is inside the window kept verbatim.
func (p CompactionPlan) Possible() bool { return p.Turns > 0 }

// PlanCompaction is what compacting this conversation now would cover.
func PlanCompaction(s core.Session) CompactionPlan {
	older, kept := splitForCompaction(s.Turns)
	return CompactionPlan{Turns: len(older), Kept: len(kept), Tokens: estimateTokensOf(older)}
}

// Compact summarises the older part of a session so the next turn has room.
//
// Returns the result rather than applying it, so the caller decides whether to announce it, store
// it, or reject it. Compaction that applied itself would be a function that quietly changes what an
// agent knows, which is exactly what this design is trying to make impossible.
func (e *Engine) Compact(ctx context.Context, sessionID string) (CompactionResult, error) {
	e.mu.Lock()
	s, ok := e.sessions[sessionID]
	if !ok {
		e.mu.Unlock()
		return CompactionResult{}, fmt.Errorf("no session %q", sessionID)
	}
	if _, running := s.Active(); running {
		e.mu.Unlock()
		return CompactionResult{}, ErrBusy
	}
	session := copySession(*s)
	e.mu.Unlock()

	older, kept := splitForCompaction(session.Turns)
	if len(older) == 0 {
		return CompactionResult{}, fmt.Errorf(
			"there is not enough history to compact yet, %d turns and the last %d are always kept",
			len(session.Turns), keepRecentTurns)
	}

	client, _, err := e.resolver.Resolve(session.KeyName, session.Model)
	if err != nil {
		return CompactionResult{}, err
	}

	// The summary is asked for as an ordinary turn against the same provider, so it costs what it
	// costs and shows up in the usage like anything else. Hiding the price of compaction would make
	// a session's total quietly wrong.
	history := core.Session{Turns: older}.History()
	history = append(history, core.Message{Role: core.RoleUser, Text: compactionPrompt})

	stream, err := client.Stream(ctx, core.Request{
		Model:    session.Model,
		Messages: history,
	})
	if err != nil {
		return CompactionResult{}, err
	}
	defer func() { _ = stream.Close() }()

	var summary strings.Builder
	for stream.Next() {
		event := stream.Event()
		switch event.Kind {
		case core.EventText:
			summary.WriteString(event.Text)
		case core.EventDone:
			if !event.StopReason.Complete() {
				// A truncated or refused summary is worse than no summary, because it would replace
				// real history with a partial account of it and nothing downstream could tell.
				return CompactionResult{}, fmt.Errorf(
					"the summary did not finish (%s), so nothing was compacted", event.StopReason)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return CompactionResult{}, err
	}

	text := strings.TrimSpace(summary.String())
	if text == "" {
		return CompactionResult{}, fmt.Errorf("the summary came back empty, so nothing was compacted")
	}

	// Measured either side so the transcript can say what compacting actually bought, rather than
	// only that it happened. "Compacted" on its own tells nobody whether it was worth the call.
	//
	// **Both figures are estimates, deliberately.** The tempting version uses the provider's
	// reported input count for "before", since that one is a fact. It is a fact about a different
	// question: the size of the whole request as sent, including the system prompt and tool schemas
	// the model never sees in this list. Comparing it against an estimate of the kept turns
	// produced a compaction that appeared to make the conversation larger, which a test caught.
	// Two estimates measured the same way answer the question actually being asked, which is how
	// much of the conversation went away.
	before := estimateTokensOf(session.Turns)
	after := estimateTokensOf(kept) + estimateText(text)

	return CompactionResult{
		Summary:      text,
		Through:      len(older),
		TokensBefore: before,
		TokensAfter:  after,
	}, nil
}

// estimateTokensOf sizes a run of turns from their text.
func estimateTokensOf(turns []core.Turn) int {
	var bytes int
	for _, turn := range turns {
		bytes += len(turn.Request.Text) + len(turn.Text) + len(turn.Thinking)
	}
	return bytes / bytesPerToken
}

// bytesPerToken matches the estimate in core, and is repeated rather than exported from there
// because exporting it would invite call sites to do their own token maths, which is how two parts
// of one program end up disagreeing about how full a context is.
const bytesPerToken = 4

func estimateText(s string) int { return len(s) / bytesPerToken }

// Apply records a compaction on a session, so later turns send the summary instead of the turns it
// covers.
//
// Separate from Compact because producing a summary and deciding to use it are different decisions,
// and a function that did both would be one that quietly changes what an agent knows.
func (e *Engine) Apply(sessionID string, result CompactionResult) error {
	e.mu.Lock()
	s, ok := e.sessions[sessionID]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("no session %q", sessionID)
	}

	now := e.events.Now()
	s.Compactions = append(s.Compactions, core.Compaction{
		Summary:      result.Summary,
		Through:      result.Through,
		At:           now,
		TokensBefore: result.TokensBefore,
		TokensAfter:  result.TokensAfter,
	})
	s.UpdatedAt = now
	saved := copySession(*s)
	e.mu.Unlock()

	e.persistSession(saved)
	e.events.Publish(core.Event{Kind: core.EventSessionUpdated, SessionID: sessionID})
	return nil
}

// splitForCompaction divides a conversation into the part to summarise and the part to keep.
//
// Only terminal turns can be summarised. A turn still in flight has an answer arriving into it, and
// folding that into a summary would produce a summary of something that had not happened yet.
func splitForCompaction(turns []core.Turn) (older, kept []core.Turn) {
	if len(turns) <= keepRecentTurns {
		return nil, turns
	}
	cut := len(turns) - keepRecentTurns
	for cut > 0 && !turns[cut-1].State.Terminal() {
		cut--
	}
	if cut == 0 {
		return nil, turns
	}
	return turns[:cut], turns[cut:]
}
