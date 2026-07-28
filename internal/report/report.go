package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Run is everything known about one agent's work, gathered by the caller.
//
// A plain struct rather than an interface over the verifier, because this package must not be able
// to go and ask a second question. Everything it says has to come from evidence somebody already
// gathered and already stands behind, and a struct makes that visible: if a field is empty, the
// report says the evidence is missing rather than quietly fetching it from somewhere else.
type Run struct {
	Agent  string
	Branch string
	Base   string

	// Rollup is the verdict. Zero value means no verification was configured at all, which is a
	// different thing from failing and is reported differently.
	Rollup core.Rollup

	// Ranked is the agent's placement when several were compared. Rank zero means the ranking
	// refused to place it, and Reason says why.
	Ranked    core.Placement
	WasRanked bool

	Diff  core.DiffStat
	Files []core.FileChange

	// Usage is the whole conversation's cost.
	Usage core.Usage

	// Turns is how many turns it took, for a reader judging whether the cost was reasonable.
	Turns int
}

// Markdown renders the report.
//
// Ordered so the first thing a reviewer reads is whether they can trust the rest. A summary that
// leads with the diff and buries the verification state at the bottom is one people skim the top of.
func Markdown(run Run) string {
	var b strings.Builder

	title := run.Agent
	if run.Branch != "" {
		title += " on " + run.Branch
	}
	fmt.Fprintf(&b, "## %s\n\n", title)

	b.WriteString(verdict(run))
	b.WriteString("\n")
	b.WriteString(changes(run))
	b.WriteString("\n")
	b.WriteString(spend(run))

	// Named, because a report pasted into a pull request should say what produced it and what that
	// thing does and does not check. The alternative is a reader assuming it means more than it does.
	b.WriteString("\nProduced by Canopy from the evidence it had at the time. " +
		"Test results describe the revision named above and nothing after it.\n")
	return b.String()
}

// verdict is the part that must never overstate.
func verdict(run Run) string {
	var b strings.Builder
	b.WriteString("### Verification\n\n")

	r := run.Rollup
	switch {
	case r.TestsTotal == 0 && r.ServicesTotal == 0:
		// Not a pass and not a failure. Saying "no tests configured" plainly is the whole point:
		// this is the state most easily mistaken for a clean run.
		b.WriteString("**Not verified.** Nothing is configured to check this project, " +
			"so no evidence was gathered either way.\n")
		return b.String()

	case r.Green:
		fmt.Fprintf(&b, "**Verified.** %s\n", sentence(r.Reason))
		if r.Caveat != "" {
			// A green with a failing optional test is the failure the roll-up's caveat field exists
			// for, and a report that dropped it would be the one place that hole reopened.
			fmt.Fprintf(&b, "\nWorth knowing anyway: %s\n", sentence(r.Caveat))
		}

	default:
		fmt.Fprintf(&b, "**Not verified.** %s\n", sentence(r.Reason))
	}

	if r.TestsTotal > 0 {
		fmt.Fprintf(&b, "\n- Tests: %s, %d of %d required passing\n",
			r.Tests, r.TestsPassing, r.RequiredTests)
	}
	if r.ServicesTotal > 0 {
		fmt.Fprintf(&b, "- Services: %s, %d of %d up\n", r.Services, r.ServicesUp, r.ServicesTotal)
	}

	b.WriteString(placement(run))
	return b.String()
}

// placement reports where this agent came, or that it could not be placed.
//
// The refusal is reported rather than omitted. An agent left out of a ranking looks like an agent
// nobody compared, when in fact it was compared and its evidence was not good enough to place.
func placement(run Run) string {
	if !run.WasRanked {
		return ""
	}
	if run.Ranked.Rank == 0 {
		return fmt.Sprintf("\n**Not ranked.** %s\n", sentence(run.Ranked.Reason))
	}
	return fmt.Sprintf("\n**Ranked %s.** %s\n",
		ordinal(run.Ranked.Rank), sentence(run.Ranked.Reason))
}

// changes is what was actually done.
func changes(run Run) string {
	var b strings.Builder
	b.WriteString("### Changes\n\n")

	if run.Diff.Empty() {
		b.WriteString("No files changed.\n")
		return b.String()
	}

	fmt.Fprintf(&b, "%s", run.Diff.Summary())
	if run.Base != "" {
		fmt.Fprintf(&b, ", against `%s`", run.Base)
	}
	b.WriteString("\n")

	if len(run.Files) > 0 {
		b.WriteString("\n")
		shown := run.Files
		// Bounded, because a hundred file list pasted into a pull request body buries the part a
		// reviewer needs. The count of what was left out is stated rather than the list silently
		// stopping, which would read as the whole change.
		const most = 25
		truncated := 0
		if len(shown) > most {
			truncated = len(shown) - most
			shown = shown[:most]
		}
		for _, file := range shown {
			fmt.Fprintf(&b, "- `%s` (%s)\n", file.Path, changeWord(file.Status))
		}
		if truncated > 0 {
			fmt.Fprintf(&b, "- and %d more\n", truncated)
		}
	}
	return b.String()
}

// spend reports cost without ever presenting an unknown one as a number.
func spend(run Run) string {
	var b strings.Builder
	b.WriteString("### Cost\n\n")

	fmt.Fprintf(&b, "%d turns, %d tokens in and %d out.\n",
		run.Turns, run.Usage.InputTokens, run.Usage.OutputTokens)

	switch {
	case run.Usage.CostKnown:
		fmt.Fprintf(&b, "\nCost: $%.4f\n", run.Usage.CostUSD)
	case run.Usage.CostUSD > 0:
		// A partial sum. Reported as a floor rather than as a figure, because a number on the page
		// is read as the answer and this one is not: it is the priced part of a bill that also
		// contains turns nobody could price.
		fmt.Fprintf(&b, "\nCost: at least $%.4f. Some turns could not be priced, "+
			"so the real figure is higher.\n", run.Usage.CostUSD)
	default:
		// Not zero. Zero would read as free, which is a claim, and this is the absence of one.
		b.WriteString("\nCost: not known. No pricing was available for the endpoint this used.\n")
	}
	return b.String()
}

// changeWord turns git's status letter into something a person reads.
func changeWord(status byte) string {
	switch status {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'M':
		return "modified"
	case 'C':
		return "copied"
	default:
		// An unrecognised code is reported as itself rather than guessed at or dropped, so a status
		// nobody has handled shows up as strange instead of as a modification.
		return fmt.Sprintf("status %q", string(status))
	}
}

func ordinal(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return fmt.Sprintf("%dth", n)
	case n%10 == 1:
		return fmt.Sprintf("%dst", n)
	case n%10 == 2:
		return fmt.Sprintf("%dnd", n)
	case n%10 == 3:
		return fmt.Sprintf("%drd", n)
	default:
		return fmt.Sprintf("%dth", n)
	}
}

// sentence makes a reason read as prose in a document rather than as a fragment in a table.
func sentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "No reason was recorded, which is itself worth knowing."
	}
	if r := []rune(text); r[0] >= 'a' && r[0] <= 'z' {
		text = strings.ToUpper(string(r[0])) + string(r[1:])
	}
	if !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "!") {
		text += "."
	}
	return text
}

// Sorted returns file changes in a stable order, so two reports of the same work read the same.
func Sorted(files []core.FileChange) []core.FileChange {
	out := append([]core.FileChange(nil), files...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
