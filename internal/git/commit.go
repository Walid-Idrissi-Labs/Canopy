package git

// Turning an agent's work into history, without leaving the tool.
//
// Two rules govern everything here and both come from the acceptance criteria, which are right.
// Nothing is staged, committed or pushed without somebody asking for it by name, and the drafted
// message is a draft: it is put in front of a person to edit, never used as written.
//
// The draft deliberately does not invent a subject. A diff says which files changed and says
// nothing about why, and a generated subject like "update auth.go" is worse than a blank one,
// because it is plausible enough to commit by accident and it tells a future reader nothing. So the
// type and the scope are derived, since those are readable from the files, and the subject is left
// as a placeholder that is obviously a placeholder.

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Draft writes everything of a conventional commit message except the subject.
func Draft(changes []core.FileChange) core.CommitDraft {
	if len(changes) == 0 {
		return core.CommitDraft{}
	}

	prefix := commitType(changes)
	if scope := commitScope(changes); scope != "" {
		prefix += "(" + scope + ")"
	}

	var body []string
	for _, change := range changes {
		switch change.Status {
		case 'A':
			body = append(body, "add "+change.Path)
		case 'D':
			body = append(body, "remove "+change.Path)
		case 'R':
			body = append(body, fmt.Sprintf("rename %s to %s", change.Old, change.Path))
		default:
			body = append(body, "change "+change.Path)
		}
	}
	sort.Strings(body)

	// Bounded, because a two hundred file change produces a body nobody reads and a commit message
	// longer than the diff summary it is meant to summarise.
	const most = 12
	if len(body) > most {
		remaining := len(body) - most
		body = append(body[:most], fmt.Sprintf("and %d %s", remaining, plural(remaining, "other file", "other files")))
	}

	return core.CommitDraft{Prefix: prefix, Body: "- " + strings.Join(body, "\n- ")}
}

// commitType picks a conventional commit type from what the files are.
//
// Only from evidence the files actually carry. Tests, documentation and tooling are readable from a
// path; the difference between a feature and a bug fix is not, so a change that adds nothing new
// falls back to the neutral type rather than claiming to fix something.
func commitType(changes []core.FileChange) string {
	var tests, docs, tooling, added, other int

	for _, change := range changes {
		name := path.Base(change.Path)
		switch {
		case strings.HasSuffix(name, "_test.go"), strings.Contains(change.Path, "/test/"),
			strings.HasPrefix(change.Path, "test/"), strings.HasSuffix(name, ".test.ts"),
			strings.HasSuffix(name, ".spec.ts"):
			tests++
		case strings.HasSuffix(name, ".md"), strings.HasPrefix(change.Path, "docs/"):
			docs++
		case strings.HasPrefix(change.Path, ".github/"), name == "Makefile",
			strings.HasSuffix(name, ".yml"), strings.HasSuffix(name, ".yaml"):
			tooling++
		default:
			other++
			if change.Status == 'A' {
				added++
			}
		}
	}

	switch {
	case other == 0 && tests > 0 && docs == 0 && tooling == 0:
		return "test"
	case other == 0 && docs > 0 && tests == 0 && tooling == 0:
		return "docs"
	case other == 0 && tooling > 0 && tests == 0 && docs == 0:
		return "ci"
	case added > 0:
		return "feat"
	default:
		return "chore"
	}
}

// commitScope is the deepest directory every changed file shares.
//
// Empty when they share nothing, rather than falling back to something like "repo". A scope that is
// always present carries no information, and one that is sometimes absent tells a reader the change
// spans the project.
func commitScope(changes []core.FileChange) string {
	var common []string
	for i, change := range changes {
		parts := strings.Split(path.Dir(change.Path), "/")
		if parts[0] == "." {
			return ""
		}
		if i == 0 {
			common = parts
			continue
		}
		for j := range common {
			if j >= len(parts) || common[j] != parts[j] {
				common = common[:j]
				break
			}
		}
		if len(common) == 0 {
			return ""
		}
	}
	if len(common) == 0 {
		return ""
	}
	// The last segment rather than the whole path, since "internal/tui/chat" as a scope is longer
	// than most of the subjects it would prefix.
	return common[len(common)-1]
}

// Stage adds paths to the index. An empty list stages everything that has changed.
func (r *Repo) Stage(ctx context.Context, dir string, paths []string) error {
	worktree := &Repo{dir: dir}

	args := []string{"add", "--"}
	if len(paths) == 0 {
		args = append(args, ".")
	} else {
		args = append(args, paths...)
	}
	if _, err := worktree.run(ctx, args...); err != nil {
		return fmt.Errorf("staging in %s: %w", dir, err)
	}
	return nil
}

// Commit records the staged content and returns the new commit SHA.
func (r *Repo) Commit(ctx context.Context, dir, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		// Empty is what CommitDraft.Message returns for an empty subject, so this is also the check
		// that stops a body of file names being committed with nothing summarising it.
		return "", fmt.Errorf("a commit needs a message")
	}
	worktree := &Repo{dir: dir}

	// --cleanup=verbatim so a message somebody wrote is the message that is recorded. The default
	// strips lines beginning with a hash, which silently eats a subject mentioning an issue number.
	if _, err := worktree.run(ctx, "commit", "--cleanup=verbatim", "-m", message); err != nil {
		return "", fmt.Errorf("committing in %s: %w", dir, err)
	}
	return worktree.run(ctx, "rev-parse", "HEAD")
}

// Push sends a branch to a remote, setting upstream the first time.
//
// Reached only from an explicit confirmation, and separately from committing. Committing is local
// and undoable; pushing is neither, and putting them behind one keystroke would make the reversible
// half of the operation carry the irreversible one.
func (r *Repo) Push(ctx context.Context, dir, remote, branch string) error {
	switch {
	case remote == "":
		remote = "origin"
	case branch == "":
		return fmt.Errorf("a branch to push is required")
	}
	worktree := &Repo{dir: dir}

	if _, err := worktree.run(ctx, "push", "--set-upstream", remote, branch); err != nil {
		return fmt.Errorf("pushing %s to %s: %w", branch, remote, err)
	}
	return nil
}

// HasRemote reports whether a remote is configured, so a push can be refused before it is offered
// rather than after it fails.
func (r *Repo) HasRemote(ctx context.Context, dir, remote string) bool {
	if remote == "" {
		remote = "origin"
	}
	worktree := &Repo{dir: dir}

	out, err := worktree.run(ctx, "remote")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == remote {
			return true
		}
	}
	return false
}
