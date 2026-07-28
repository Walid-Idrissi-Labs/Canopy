// Package git holds the version control operations Canopy performs on its own behalf, as opposed to
// the ones it offers an agent.
//
// The distinction matters. The tools in internal/tools/git.go are things a model asks for and the
// permission model decides about. What is here is Canopy's own bookkeeping, and it runs without
// asking, because a checkpoint taken only when somebody remembered to approve it is a checkpoint
// that does not exist when it is needed.
package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// Checkpoints, and why they are a hidden commit rather than a stash or a copy.
//
// A stash is a stack, and a stack is shared mutable state: two agents stashing in the same
// repository interleave, and `stash pop` gives one of them the other's work. A file copy is
// expensive on a large repository and gets the ignored files wrong in one direction or the other.
//
// A commit object written to a ref nobody looks at has neither problem. It is content addressed, so
// two agents writing checkpoints cannot collide; it is cheap, because git is already storing most of
// those blobs; and it captures exactly what git captures, which is the definition the rest of the
// tool already uses.

// refPrefix is where checkpoints live.
//
// Under `refs/canopy/` rather than `refs/heads/`, so they never appear in `git branch`, never get
// pushed by a plain `git push`, and never turn up in somebody's tab completion. A checkpoint is
// Canopy's bookkeeping and should be invisible until it is wanted.
const refPrefix = "refs/canopy/checkpoints/"

// Checkpoint is a saved state of a worktree.
type Checkpoint struct {
	// ID is the ref this checkpoint is stored under, without the prefix.
	ID string
	// Commit is the object holding the tree.
	Commit string
	// Label is what it was taken for, shown when offering to undo.
	Label string
	At    time.Time

	// Head is the commit the branch was on when this was taken, so undo can tell "the agent edited
	// files" apart from "the agent also committed".
	Head string
}

// Taker takes and restores checkpoints in one worktree.
type Taker struct {
	dir string
}

// NewTaker builds a checkpoint taker for a worktree.
func NewTaker(dir string) *Taker { return &Taker{dir: dir} }

// Take snapshots the worktree, including untracked files.
//
// Cheap enough to run every turn, which is the requirement: a checkpoint somebody has to ask for is
// one that was not taken before the turn that needed it.
func (t *Taker) Take(ctx context.Context, id, label string) (Checkpoint, error) {
	if id == "" {
		return Checkpoint{}, fmt.Errorf("a checkpoint needs an id")
	}

	head, err := t.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		// A repository with no commits yet has no HEAD, which is a legitimate state for a project
		// somebody just started. The checkpoint still works; it simply has no parent.
		head = ""
	}

	// A temporary index, so staging for the checkpoint does not disturb what the user has staged.
	// Without this, taking a checkpoint would silently stage every untracked file in their working
	// tree, and the next `git commit` would include things they never chose.
	index, err := t.indexPath(ctx)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("finding the index for the checkpoint: %w", err)
	}
	indexFile := fmt.Sprintf("%s.canopy-checkpoint-%s", index, id)
	env := append(environ(), "GIT_INDEX_FILE="+indexFile)
	// Removed with os rather than by shelling out, since it is a plain file and `git rm` means
	// something entirely different from removing a file off disk.
	defer func() { _ = os.Remove(indexFile) }()

	// Seed the temporary index from HEAD so unchanged files are already in it, then add everything.
	if head != "" {
		if _, err := t.runEnv(ctx, env, "read-tree", head); err != nil {
			return Checkpoint{}, fmt.Errorf("preparing the checkpoint index: %w", err)
		}
	}
	// Untracked files are included, because an agent that created a file and then made a mess is
	// the common case and an undo that left the new files behind would not be an undo.
	if _, err := t.runEnv(ctx, env, "add", "--all", "."); err != nil {
		return Checkpoint{}, fmt.Errorf("staging the checkpoint: %w", err)
	}

	tree, err := t.runEnv(ctx, env, "write-tree")
	if err != nil {
		return Checkpoint{}, fmt.Errorf("writing the checkpoint tree: %w", err)
	}

	args := []string{"commit-tree", tree, "-m", "canopy checkpoint: " + label}
	if head != "" {
		args = append(args, "-p", head)
	}
	commit, err := t.runEnv(ctx, env, args...)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("writing the checkpoint commit: %w", err)
	}

	if _, err := t.run(ctx, "update-ref", refPrefix+id, commit); err != nil {
		return Checkpoint{}, fmt.Errorf("recording the checkpoint: %w", err)
	}

	return Checkpoint{ID: id, Commit: commit, Label: label, Head: head, At: time.Now()}, nil
}

// Restore puts the worktree back to a checkpoint.
//
// Tracked changes, new files and deletions all come back, which is the whole promise. A restore that
// left an agent's new files lying around would leave somebody cleaning up by hand, which is what
// they were trying to avoid.
func (t *Taker) Restore(ctx context.Context, checkpoint Checkpoint) error {
	if checkpoint.Commit == "" {
		return fmt.Errorf("that checkpoint has no commit to restore from")
	}

	// The tree goes into the real index and the worktree together. `checkout-index` alone would
	// restore file contents and leave files the agent created still present, since nothing removes
	// what is not in the tree.
	if _, err := t.run(ctx, "read-tree", "-u", "--reset", checkpoint.Commit); err != nil {
		return fmt.Errorf("restoring the checkpoint: %w", err)
	}

	// Anything still untracked after that was created after the checkpoint and is not in its tree.
	// Removing it is what makes an undo complete rather than approximate.
	if _, err := t.run(ctx, "clean", "-fd"); err != nil {
		return fmt.Errorf("removing files created after the checkpoint: %w", err)
	}
	return nil
}

// List returns every checkpoint in this worktree, newest first.
func (t *Taker) List(ctx context.Context) ([]Checkpoint, error) {
	out, err := t.run(ctx, "for-each-ref", "--sort=-creatordate",
		"--format=%(refname:short)%09%(objectname)%09%(creatordate:iso-strict)%09%(subject)",
		refPrefix)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}

	var checkpoints []Checkpoint
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, fields[2])
		checkpoints = append(checkpoints, Checkpoint{
			ID:     strings.TrimPrefix(fields[0], "canopy/checkpoints/"),
			Commit: fields[1],
			At:     at,
			Label:  strings.TrimPrefix(fields[3], "canopy checkpoint: "),
		})
	}
	return checkpoints, nil
}

// Forget removes a checkpoint's ref.
//
// The commit object stays until git garbage collects it, which is correct: an undo somebody
// regretted is still recoverable through the reflog for a while, and that is a better failure mode
// than a checkpoint that is genuinely gone the instant it is dropped.
func (t *Taker) Forget(ctx context.Context, id string) error {
	_, err := t.run(ctx, "update-ref", "-d", refPrefix+id)
	return err
}

// Prune keeps the most recent checkpoints and drops the rest.
//
// Bounded because a checkpoint per turn on a long session is thousands of refs, and every one of
// them pins the objects it references so nothing can be garbage collected.
func (t *Taker) Prune(ctx context.Context, keep int) error {
	if keep < 1 {
		keep = 1
	}
	checkpoints, err := t.List(ctx)
	if err != nil {
		return err
	}
	for _, checkpoint := range checkpoints[min(keep, len(checkpoints)):] {
		if err := t.Forget(ctx, checkpoint.ID); err != nil {
			return err
		}
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// indexPath is where this worktree's index actually lives.
//
// Asked of git rather than assembled from the directory, because `.git` is a directory in an
// ordinary checkout and a *file* in a linked worktree, holding a line that points at the real git
// directory under the main checkout. Building the path by hand therefore produced `.git/index`
// underneath a file, which fails with "Not a directory". markerPath in worktree.go had already
// worked this out for the marker file; this one had not.
//
// The consequence was worse than the bug. Taking a checkpoint failed for every isolated agent, the
// engine reported it through onStorageError and carried on, and undo silently did nothing for
// exactly the agents most likely to need it. Found by the A9-01 sweep.
func (t *Taker) indexPath(ctx context.Context) (string, error) {
	out, err := t.run(ctx, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}

	path := strings.TrimSpace(out)
	if path == "" {
		return "", fmt.Errorf("git did not say where the index is")
	}
	if !filepath.IsAbs(path) {
		// git answers relative to the worktree it was run in.
		path = filepath.Join(t.dir, path)
	}
	return path, nil
}

func (t *Taker) run(ctx context.Context, args ...string) (string, error) {
	return t.runEnv(ctx, environ(), args...)
}

func (t *Taker) runEnv(ctx context.Context, env []string, args ...string) (string, error) {
	result, err := exec.Run(ctx, "git", args, exec.Options{
		Dir:     t.dir,
		Env:     env,
		Timeout: 60 * time.Second,
	})
	if err != nil {
		return "", err
	}
	if !result.Ran {
		return "", fmt.Errorf("git could not be run: %s", strings.TrimSpace(result.Output))
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(result.Output))
	}
	return strings.TrimSpace(result.Output), nil
}

// environ is the environment git runs in for checkpoint work.
//
// Deliberately minimal rather than inherited. A user's `GIT_INDEX_FILE`, `GIT_DIR` or
// `GIT_WORK_TREE` would redirect Canopy's bookkeeping somewhere unexpected, and the failure would be
// a checkpoint silently taken of the wrong thing.
func environ() []string {
	return []string{
		"PATH=" + pathEnv(),
		"HOME=" + homeEnv(),
		// Git refuses to write a commit without these, and a checkpoint is not authored by the user.
		// Naming Canopy is also what makes a stray checkpoint commit identifiable later.
		"GIT_AUTHOR_NAME=Canopy",
		"GIT_AUTHOR_EMAIL=canopy@localhost",
		"GIT_COMMITTER_NAME=Canopy",
		"GIT_COMMITTER_EMAIL=canopy@localhost",
	}
}
