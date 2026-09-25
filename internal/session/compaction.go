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
	`somebody else. Be specific: a summary that says "discussed the API" is worse than no summary.

Do not call any tools. Answer with the summary only.`

// summaryMaxTokens bounds what a summary may spend. A compaction exists to save money, and without a
// bound it inherited the default for an agent turn, 32 thousand output tokens, for text that has to
// fit in a few thousand to be worth having.
const summaryMaxTokens = 8000

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
	older, kept := splitAfter(s.Turns, compactedThrough(s))
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
	tools, _ := e.toolsForLocked(sessionID)
	e.mu.Unlock()

	return e.summarise(ctx, session, tools)
}

// summarise produces the compaction of a conversation without applying it. The caller has checked
// the conversation may be compacted; an automatic compaction during a turn relies on the in-flight
// turn being among the ones kept verbatim.
func (e *Engine) summarise(
	ctx context.Context, session core.Session, tools *core.ToolRegistry,
) (CompactionResult, error) {
	older, kept := splitAfter(session.Turns, compactedThrough(session))
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
	// The earlier summary and the turns since it, not the whole conversation again: a second
	// compaction that resent everything from the first turn would overflow the very window it is
	// meant to make room in.
	history := core.Session{Turns: older, Compactions: session.Compactions}.History()
	history = append(history, core.Message{Role: core.RoleUser, Text: compactionPrompt})

	// The same system prompt and tool list as the conversation, so the request starts with exactly
	// the prefix the conversation already cached and reads the older turns back at the cache rate.
	// A summary request with neither, as before, wrote the whole conversation to cache again and
	// sent tool calls with no tools defined.
	request := core.Request{
		Model:     session.Model,
		System:    e.systemPrompt(),
		Messages:  history,
		MaxTokens: summaryMaxTokens,
		WebSearch: e.webSearchOn(),
	}
	if tools != nil {
		request.Tools = tools.Definitions()
	}
	stream, err := client.Stream(ctx, request)
	if err != nil {
		return CompactionResult{}, err
	}
	// Load-bearing on a route whose client holds a session, which is more than closing a stream used
	// to mean. A compaction resolves without a conversation, so what it gets back is a client that
	// ends when its turn does, and this is what ends it. See copilot.Clients.
	defer func() { _ = stream.Close() }()

	var summary strings.Builder
	for stream.Next() {
		event := stream.Event()
		switch event.Kind {
		case core.EventText:
			summary.WriteString(event.Text)
		case core.EventDone:
			if event.StopReason != core.StopEndTurn {
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
		for _, step := range turn.Steps {
			for _, result := range step.ToolResults {
				bytes += len(result.Content)
			}
			for _, call := range step.ToolCalls {
				bytes += len(call.Input)
			}
		}
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
	return splitAfter(turns, 0)
}

// splitAfter is splitForCompaction for a conversation already compacted through `after` turns:
// there is something new to summarise only if the cut falls past it.
func splitAfter(turns []core.Turn, after int) (older, kept []core.Turn) {
	if len(turns) <= keepRecentTurns {
		return nil, turns
	}
	cut := len(turns) - keepRecentTurns
	for cut > 0 && !turns[cut-1].State.Terminal() {
		cut--
	}
	if cut <= after {
		return nil, turns
	}
	return turns[:cut], turns[cut:]
}

// compactedThrough is how many turns the conversation's latest compaction covers.
func compactedThrough(s core.Session) int {
	if c, ok := s.Compacted(); ok {
		return c.Through
	}
	return 0
}

// autoCompact compacts a conversation that has grown past its budget, and says so in the transcript
// like any other compaction. Best effort: a conversation that is busy, too short, or whose summary
// fails is left as it was, and the manual command still works.
func (e *Engine) autoCompact(ctx context.Context, sessionID string) bool {
	return e.compactPast(ctx, sessionID, false)
}

// compactPast compacts when the conversation is past its budget, or regardless when force is set,
// which is the answer to a provider saying the conversation no longer fits at all.
func (e *Engine) compactPast(ctx context.Context, sessionID string, force bool) bool {
	e.mu.Lock()
	if e.autoCompactOff && !force {
		e.mu.Unlock()
		return false
	}
	s, ok := e.sessions[sessionID]
	if !ok {
		e.mu.Unlock()
		return false
	}
	session := copySession(*s)
	tools, _ := e.toolsForLocked(sessionID)
	e.mu.Unlock()

	if !force && session.ContextUse().Tokens < core.AutoCompactTokens(core.WindowFor(session.Model)) {
		return false
	}
	if !PlanCompaction(session).Possible() {
		return false
	}
	result, err := e.summarise(ctx, session, tools)
	if err != nil {
		return false
	}
	return e.Apply(sessionID, result) == nil
}

// SetAutoCompact turns automatic compaction on or off; it is on unless configured otherwise.
func (e *Engine) SetAutoCompact(on bool) {
	e.mu.Lock()
	e.autoCompactOff = !on
	e.mu.Unlock()
}
