package verify

// The conflict radar: which files several agents have all touched.
//
// This preempts a pain that exists only because you are running agents in parallel, which is what
// makes it worth building rather than table stakes. Merge the first agent's work and the second one
// stops merging cleanly, and by then the second agent has spent an hour on it. Knowing an hour
// earlier that two of them are both rewriting the same file is the whole feature.
//
// It is deliberately not a merge simulation. Two agents editing different functions in one file
// merge cleanly most of the time, and running a real three way merge per pair per poll to find out
// would cost more than it saves. What is reported is overlap, not conflict, and the wording says
// so: these are the files to look at first, not a prediction that git will fail.

import (
	"context"
	"fmt"
	"sort"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/git"
)

// Overlaps lists every file that more than one agent has changed, worst first.
//
// Worst means most agents, then a deletion against an edit, then the path, so the order is stable
// and the top of the list is the thing to look at. An empty result is the common and good case.
func (v *Verifier) Overlaps() ([]core.Overlap, error) {
	v.mu.Lock()
	names := append([]string(nil), v.order...)
	v.mu.Unlock()

	touched := make(map[string][]string)
	removed := make(map[string][]string)

	for _, name := range names {
		changes, err := v.Changes(name)
		if err != nil {
			// One agent whose worktree cannot be read must not hide the overlaps among the others.
			// The alternative, failing the whole call, would turn a removed worktree into a blank
			// screen at exactly the moment somebody is deciding what to merge.
			continue
		}
		for _, change := range changes {
			touched[change.Path] = append(touched[change.Path], name)
			if change.Status == 'D' {
				removed[change.Path] = append(removed[change.Path], name)
			}
			// A rename is a change to both names. Left out, one agent renaming a file and another
			// editing it under its old name would not show as overlap at all, which is the version of
			// this that ends in the edit being silently lost.
			if change.Old != "" {
				touched[change.Old] = append(touched[change.Old], name)
			}
		}
	}

	var overlaps []core.Overlap
	for path, agents := range touched {
		agents = unique(agents)
		if len(agents) < 2 {
			continue
		}
		overlaps = append(overlaps, core.Overlap{Path: path, Agents: agents, Deleted: unique(removed[path])})
	}

	sort.SliceStable(overlaps, func(i, j int) bool {
		a, b := overlaps[i], overlaps[j]
		if len(a.Agents) != len(b.Agents) {
			return len(a.Agents) > len(b.Agents)
		}
		if a.Contested() != b.Contested() {
			return a.Contested()
		}
		return a.Path < b.Path
	})
	return overlaps, nil
}

func unique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

// Draft returns the generated half of a commit message for an agent's work.
func (v *Verifier) Draft(agent string) (core.CommitDraft, error) {
	changes, err := v.Changes(agent)
	if err != nil {
		return core.CommitDraft{}, err
	}
	return git.Draft(changes), nil
}

// Commit stages everything in an agent's worktree and records it.
//
// Staging everything rather than a chosen subset, for now. Partial staging is a real workflow and
// it needs a way to select hunks, which is its own screen; offering a half version that stages whole
// files silently would be worse than not offering it, because the difference only shows up in what
// was committed.
func (v *Verifier) Commit(agent, message string) error {
	v.mu.Lock()
	subject, ok := v.subjects[agent]
	v.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, agent)
	}

	ctx := context.Background()
	if err := v.repo.Stage(ctx, subject.Dir, nil); err != nil {
		return err
	}
	if _, err := v.repo.Commit(ctx, subject.Dir, message); err != nil {
		return err
	}
	return nil
}

// Push sends an agent's branch to a remote.
//
// Separate from Commit and reached from its own confirmation. Committing is local and undoable;
// pushing is neither, and one keystroke doing both would make the reversible half carry the
// irreversible one.
func (v *Verifier) Push(agent, remote string) error {
	v.mu.Lock()
	subject, ok := v.subjects[agent]
	v.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, agent)
	}
	if subject.Branch == "" {
		return fmt.Errorf("%s is not on a branch, so there is nothing to push", agent)
	}
	if !v.repo.HasRemote(context.Background(), subject.Dir, remote) {
		return fmt.Errorf("this repository has no remote to push to")
	}
	return v.repo.Push(context.Background(), subject.Dir, remote, subject.Branch)
}
