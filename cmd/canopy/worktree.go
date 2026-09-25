package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	gitpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/git"
)

// runWorktree lists and reclaims the worktrees Canopy made for agents in this repository.
//
// gc removes a Canopy-owned worktree only when it has no uncommitted work, and never deletes its
// branch, so whatever an agent committed is still there to merge or look at. The primary checkout
// and worktrees Canopy did not make are never touched.
func runWorktree(args []string, out io.Writer) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := gitpkg.OpenRepo(dir)
	if err != nil {
		return fmt.Errorf("this directory is not in a git repository: %w", err)
	}
	ctx := context.Background()
	found, err := repo.Worktrees(ctx)
	if err != nil {
		return err
	}
	sub := "list"
	if len(args) > 0 {
		sub = args[0]
	}
	dryRun := len(args) > 1 && args[1] == "--dry-run"

	switch sub {
	case "list":
		home, _ := gitpkg.WorktreeHome(repoRoot(ctx, dir))
		_, _ = fmt.Fprintf(out, "new agent worktrees go under %s\n", home)
		for _, w := range found {
			if w.Ownership != core.OwnershipManaged {
				continue
			}
			state := "clean"
			if dirty, err := repo.DirtyState(ctx, w.Path); err == nil && dirty.IsDirty() {
				state = fmt.Sprintf("%d changed, %d untracked", dirty.Staged+dirty.Unstaged, dirty.Untracked)
			}
			_, _ = fmt.Fprintf(out, "  %-24s %-28s %s\n", w.Branch, state, w.Path)
		}
		return nil
	case "gc":
		removed, kept := 0, 0
		for _, w := range found {
			if w.Ownership != core.OwnershipManaged {
				continue
			}
			if why := keepReason(ctx, repo, w); why != "" {
				kept++
				_, _ = fmt.Fprintf(out, "kept %s: %s\n", w.Path, why)
				continue
			}
			if dryRun {
				_, _ = fmt.Fprintf(out, "would remove %s (branch %s kept)\n", w.Path, w.Branch)
				continue
			}
			if err := repo.Remove(ctx, w, false); err != nil {
				kept++
				_, _ = fmt.Fprintf(out, "kept %s: %v\n", w.Path, err)
				continue
			}
			removed++
			_, _ = fmt.Fprintf(out, "removed %s (branch %s kept)\n", w.Path, w.Branch)
		}
		if !dryRun {
			_, _ = fmt.Fprintf(out, "%d removed, %d kept\n", removed, kept)
		}
		return nil
	default:
		return errors.New("usage: canopy worktree [list | gc [--dry-run]]")
	}
}

// keepReason says why gc must leave a worktree alone, or "" when removing it loses nothing.
func keepReason(ctx context.Context, repo *gitpkg.Repo, w core.WorkspaceSnapshot) string {
	if w.Locked != "" {
		pid := gitpkg.LockHolder(w.Locked)
		if pid == 0 || processAlive(pid) {
			return "locked (" + w.Locked + ")"
		}
	}
	if dirty, err := repo.DirtyState(ctx, w.Path); err != nil {
		return "its state could not be read: " + err.Error()
	} else if dirty.IsDirty() {
		return "it has uncommitted work"
	}
	if w.Detached && !repo.Reachable(ctx, w.Revision.HeadSHA) {
		return "its commits are on no branch, so removing it would lose them"
	}
	return ""
}

func repoRoot(ctx context.Context, dir string) string {
	if repo, err := gitpkg.OpenRepo(dir); err == nil {
		if root, err := repo.Toplevel(ctx); err == nil {
			return root
		}
	}
	return dir
}

// processAlive reports whether a process exists, by signalling it with nothing.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
