package verify

// Ranking agents by outcome, and the queue of work that is ready to look at.
//
// This is the strategic argument for the whole project in about two hundred lines. Several tools
// will fan a task out across agents. None of them appear to use test truth to say which result won,
// which means the human still opens every branch and reads every diff, which is the work the fan out
// was supposed to save.
//
// The thing that makes it honest rather than a leaderboard is the refusal. An agent whose tests
// passed an hour ago and whose worktree has moved since is not in fourth place, it is not placed at
// all, and the reason says so. Ranking it on the strength of a result that no longer describes its
// code would be exactly the false green the truth contract is built to prevent, dressed up as a
// recommendation.

import (
	"fmt"
	"sort"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Rank compares every agent being verified.
//
// Passing first, then diff size as the tiebreak. Smaller wins, on the grounds that between two
// changes that both make the tests pass, the one with less code in it is the one with less to review
// and less to be wrong. That is a judgement rather than a measurement, and it only ever decides ties.
func (v *Verifier) Rank() core.Ranking {
	v.mu.Lock()
	names := append([]string(nil), v.order...)
	v.mu.Unlock()

	var ranking core.Ranking
	for _, name := range names {
		placement, rankable := v.placementFor(name)
		if rankable {
			ranking.Ranked = append(ranking.Ranked, placement)
		} else {
			ranking.Unranked = append(ranking.Unranked, placement)
		}
	}

	sort.SliceStable(ranking.Ranked, func(i, j int) bool {
		a, b := ranking.Ranked[i], ranking.Ranked[j]

		// More required tests passing beats fewer, always. This is the only comparison that is
		// evidence rather than preference, so nothing below it gets to overturn it.
		if a.Passing != b.Passing {
			return a.Passing > b.Passing
		}
		if a.Diff.Lines() != b.Diff.Lines() {
			return a.Diff.Lines() < b.Diff.Lines()
		}
		if a.Diff.FilesChanged != b.Diff.FilesChanged {
			return a.Diff.FilesChanged < b.Diff.FilesChanged
		}
		// Name last, so the order is stable across calls. Two agents that are genuinely tied should
		// not swap places every time the screen redraws.
		return a.Agent < b.Agent
	})

	for i := range ranking.Ranked {
		ranking.Ranked[i].Rank = i + 1
		ranking.Ranked[i].Reason = rankReason(ranking.Ranked[i])
	}
	return ranking
}

// placementFor builds one agent's placement and says whether it can be ranked at all.
func (v *Verifier) placementFor(agent string) (core.Placement, bool) {
	v.mu.Lock()
	snapshot, known := v.snapshotLocked(agent)
	stat := v.diffs[agent]
	v.mu.Unlock()

	if !known {
		return core.Placement{Agent: agent, Reason: "this agent is no longer being verified"}, false
	}

	placement := core.Placement{
		Agent:    agent,
		Diff:     stat,
		Revision: snapshot.Revision,
		Tests:    core.RollUp(snapshot).Tests,
	}

	if !snapshot.Revision.Known() {
		placement.Reason = "not ranked: the revision of this worktree could not be determined, so " +
			"no result can be tied to it"
		if snapshot.RevisionError != "" {
			placement.Reason = "not ranked: " + snapshot.RevisionError
		}
		return placement, false
	}

	// Every required test has to have a verdict about the code that is in the worktree right now.
	// One stale or unfinished result is enough to withhold a placement, because a ranking built from
	// a mixture of current and out of date evidence is not a ranking of anything.
	var required, passing int
	for _, test := range snapshot.Tests {
		if !test.Required {
			continue
		}
		required++

		verdict := test.Explain(snapshot.Revision)
		switch verdict.State {
		case core.TestPassing:
			passing++
		case core.TestFailing:
			// A current failure is a real result and places below a current pass. Refusing to rank a
			// failing agent would leave the loser off the screen, which reads as it having vanished.
		default:
			placement.Passing, placement.Required = passing, required
			placement.Reason = fmt.Sprintf("not ranked: %s is %s, %s", test.Name, verdict.State, verdict.Reason)
			return placement, false
		}
	}

	placement.Passing, placement.Required = passing, required
	if required == 0 {
		placement.Reason = "not ranked: nothing is marked required, so there is no evidence that has to hold"
		return placement, false
	}
	return placement, true
}

func rankReason(p core.Placement) string {
	switch {
	case p.Passing == p.Required:
		return fmt.Sprintf("all %d required %s pass for revision %s, %s",
			p.Required, plural(p.Required, "test", "tests"), p.Revision.Short(), p.Diff.Summary())
	case p.Passing == 0:
		return fmt.Sprintf("no required test passes for revision %s, %s",
			p.Revision.Short(), p.Diff.Summary())
	default:
		return fmt.Sprintf("%d of %d required tests pass for revision %s, %s",
			p.Passing, p.Required, p.Revision.Short(), p.Diff.Summary())
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ReadyToReview lists agents that are green for the code currently in their worktree and have
// something to show for it, easiest review first.
//
// Two exclusions carry the whole feature. An agent whose result went stale leaves immediately,
// which happens for free because the queue is derived on every call rather than maintained: there is
// no cached membership to forget to invalidate. And an agent that is green with an empty diff never
// enters, because a passing test suite over no changes is the state every repository starts in and
// putting it at the top of a work queue would bury the actual work.
func (v *Verifier) ReadyToReview() []core.ReadyForReview {
	v.mu.Lock()
	names := append([]string(nil), v.order...)
	v.mu.Unlock()

	var queue []core.ReadyForReview
	for _, name := range names {
		v.mu.Lock()
		snapshot, known := v.snapshotLocked(name)
		stat := v.diffs[name]
		v.mu.Unlock()

		if !known {
			continue
		}
		rollup := core.RollUp(snapshot)
		if !rollup.Green || stat.Empty() {
			continue
		}

		why := rollup.Reason
		if rollup.Caveat != "" {
			// A caveat is the reason this field exists: an optional test that has been failing for
			// weeks must not be invisible just because it cannot block the green.
			why += ", but " + rollup.Caveat
		}
		queue = append(queue, core.ReadyForReview{
			Agent:  name,
			Branch: snapshot.Branch,
			Diff:   stat,
			Why:    fmt.Sprintf("%s, %s", why, stat.Summary()),
		})
	}

	sort.SliceStable(queue, func(i, j int) bool {
		if queue[i].Diff.Lines() != queue[j].Diff.Lines() {
			return queue[i].Diff.Lines() < queue[j].Diff.Lines()
		}
		if queue[i].Diff.FilesChanged != queue[j].Diff.FilesChanged {
			return queue[i].Diff.FilesChanged < queue[j].Diff.FilesChanged
		}
		return queue[i].Agent < queue[j].Agent
	})
	return queue
}
