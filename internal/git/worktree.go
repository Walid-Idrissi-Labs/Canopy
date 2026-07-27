package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// Worktree discovery, and the one rule that governs all of it.
//
// **Canopy discovers worktrees; it does not assume it created them.** Somebody may already have
// three worktrees for reasons of their own, and those are theirs. The primary checkout is theirs
// twice over. So discovery reads and never writes, ownership is recorded rather than inferred, and
// the removal path refuses anything Canopy did not make.
//
// The whole set is returned on every discovery rather than a delta. A worktree removed outside
// Canopy simply stops appearing, and callers replace their view rather than reconciling, which is
// what makes an externally deleted worktree disappear safely instead of lingering as a row nobody
// can explain.

// Repo is a git repository Canopy can inspect.
type Repo struct {
	dir string

	// revisions carries the content hash cache across polls. It is nil on the throwaway handles this
	// package builds to run git inside a particular worktree, and a nil one simply means every hash
	// is recomputed, which is correct and only slower.
	revisions *Revisions
}

// OpenRepo returns a repository handle for a directory inside a working tree.
func OpenRepo(dir string) (*Repo, error) {
	repo := &Repo{dir: dir, revisions: NewRevisions(0)}

	inside, err := repo.run(context.Background(), "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository: %w", dir, err)
	}
	if strings.TrimSpace(inside) != "true" {
		return nil, fmt.Errorf("%s is not inside a git working tree", dir)
	}
	return repo, nil
}

// Dir is the directory this handle runs git in.
func (r *Repo) Dir() string { return r.dir }

// Worktrees returns every worktree of this repository, primary first.
func (r *Repo) Worktrees(ctx context.Context) ([]core.WorkspaceSnapshot, error) {
	out, err := r.run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("listing worktrees: %w", err)
	}

	// The porcelain format is a blank line separated set of records, each starting with `worktree`.
	// Parsed by field name rather than by position, because the optional lines, `bare`, `detached`,
	// `locked`, `prunable`, appear only when they apply.
	var found []core.WorkspaceSnapshot
	for i, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		snapshot, ok := parseWorktreeBlock(block)
		if !ok {
			continue
		}
		// Git lists the primary first, always. Relying on that rather than comparing paths against
		// the common directory, which is subtly different for a bare repository.
		if i == 0 {
			snapshot.Ownership = core.OwnershipPrimary
		} else {
			snapshot.Ownership = ownershipOf(snapshot.Path)
		}
		found = append(found, snapshot)
	}
	return found, nil
}

// parseWorktreeBlock reads one record of `git worktree list --porcelain`.
func parseWorktreeBlock(block string) (core.WorkspaceSnapshot, bool) {
	var snapshot core.WorkspaceSnapshot

	for _, line := range strings.Split(block, "\n") {
		field, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch field {
		case "worktree":
			snapshot.Path = value
		case "HEAD":
			snapshot.Revision = core.RevisionKey{HeadSHA: value}
		case "branch":
			// Reported as a full ref, and everything downstream wants the short name.
			snapshot.Branch = strings.TrimPrefix(value, "refs/heads/")
		case "detached":
			snapshot.Detached = true
		case "bare":
			// A bare repository has no working tree, so there is nothing for an agent to work in
			// and nothing to report a dirty state for.
			return core.WorkspaceSnapshot{}, false
		}
	}

	if snapshot.Path == "" {
		return core.WorkspaceSnapshot{}, false
	}
	snapshot.ID = WorkspaceID(snapshot.Path)
	snapshot.Name = filepath.Base(snapshot.Path)
	return snapshot, true
}

// WorkspaceID is a stable identifier for a worktree.
//
// Derived from the path rather than assigned, so the same worktree keeps its ID across restarts and
// a caller holding an ID from one run can still resolve it in the next. Hashed rather than used
// directly because the ID appears in events, transcripts and audit entries, and an absolute path
// carries somebody's home directory name into all of them.
func WorkspaceID(path string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	return "ws-" + hex.EncodeToString(sum[:6])
}

// canopyMarker is the file that says Canopy created a worktree.
//
// A marker file rather than a naming convention, because a naming convention is something a user can
// accidentally satisfy. Somebody whose own worktree happens to be called `canopy-feature` should not
// find Canopy willing to delete it.
const canopyMarker = "canopy-created"

// markerPath is where the marker lives for a given worktree.
//
// **Inside the worktree's git directory, not inside the worktree.** In a linked worktree `.git` is a
// file containing a pointer, not a directory, so there is nothing to write into there. Resolving the
// real git directory also puts the marker outside the working tree, which means it never appears in
// `git status` as an untracked file and never has to be explained to somebody wondering what Canopy
// left in their repository.
func markerPath(ctx context.Context, worktree string) (string, error) {
	dir, err := (&Repo{dir: worktree}).run(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, canopyMarker), nil
}

func ownershipOf(path string) core.WorkspaceOwnership {
	marker, err := markerPath(context.Background(), path)
	if err != nil {
		// Unable to tell means unable to claim it. Reporting it as external is the conservative
		// reading, and the consequence of being wrong is a worktree Canopy will not remove, which is
		// a far better outcome than one it removes and should not have.
		return core.OwnershipExternalReadOnly
	}
	if _, err := os.Stat(marker); err == nil {
		return core.OwnershipManaged
	}
	return core.OwnershipExternalReadOnly
}

// Create makes a worktree and a branch for an agent.
//
// A failed creation leaves nothing behind, which is why the marker is written last and why the
// worktree is removed if writing it fails. A half created worktree is worse than none: it is a
// directory that looks usable, is not registered as Canopy's, and will not be cleaned up.
func (r *Repo) Create(ctx context.Context, name, branch string) (core.WorkspaceSnapshot, error) {
	if err := validateWorktreeName(name); err != nil {
		return core.WorkspaceSnapshot{}, err
	}
	if branch == "" {
		branch = name
	}
	if err := validateBranchName(branch); err != nil {
		return core.WorkspaceSnapshot{}, err
	}

	// Beside the repository rather than inside it. A worktree nested in the primary checkout appears
	// in every glob, every grep and every build, and the first thing anybody notices is their test
	// suite running twice.
	root, err := r.run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return core.WorkspaceSnapshot{}, fmt.Errorf("finding the repository root: %w", err)
	}
	path := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+name)

	if _, err := os.Stat(path); err == nil {
		return core.WorkspaceSnapshot{}, fmt.Errorf(
			"%s already exists, so a worktree there would be working on top of something else",
			filepath.Base(path))
	}

	if _, err := r.run(ctx, "worktree", "add", "-b", branch, path); err != nil {
		return core.WorkspaceSnapshot{}, fmt.Errorf("creating the worktree: %w", err)
	}

	// The marker goes last, and its failure undoes the creation. Better to have made nothing than
	// to have made something nothing will clean up.
	undo := func(err error) (core.WorkspaceSnapshot, error) {
		_, _ = r.run(context.WithoutCancel(ctx), "worktree", "remove", "--force", path)
		return core.WorkspaceSnapshot{}, fmt.Errorf("marking the worktree as Canopy's: %w", err)
	}
	marker, err := markerPath(ctx, path)
	if err != nil {
		return undo(err)
	}
	if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		return undo(err)
	}

	// Git reports the resolved path, and on macOS the temporary directory is a symlink, so a
	// worktree created at one path is listed at another. Resolving here keeps the ID and the path
	// this returns identical to what discovery will report for the same worktree.
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = resolved
	}

	return core.WorkspaceSnapshot{
		ID:        WorkspaceID(path),
		Name:      filepath.Base(path),
		Path:      path,
		Branch:    branch,
		Ownership: core.OwnershipManaged,
	}, nil
}

// ErrNotOurs is returned when asked to remove a worktree Canopy did not create.
var ErrNotOurs = errors.New("this worktree was not created by Canopy, so it will not be removed")

// ErrDirty is returned when a worktree has uncommitted work.
var ErrDirty = errors.New("this worktree has uncommitted changes")

// Remove deletes a worktree Canopy created.
//
// Three refusals, in this order, and each of them is the answer to a way somebody loses work:
//
//  1. **Never the primary.** It is the user's own checkout and it is not Canopy's to remove, at any
//     level of confirmation.
//  2. **Never one Canopy did not create.** Discovery finds worktrees somebody made for their own
//     reasons, and finding them is not the same as owning them.
//  3. **Never a dirty one without saying so.** Uncommitted work is work, and an agent's abandoned
//     experiment is sometimes the only copy of an idea.
func (r *Repo) Remove(ctx context.Context, workspace core.WorkspaceSnapshot, force bool) error {
	switch {
	case workspace.Ownership == core.OwnershipPrimary:
		return errors.New("the primary checkout is yours and is never removed")
	case workspace.Ownership != core.OwnershipManaged:
		return fmt.Errorf("%s: %w", workspace.Name, ErrNotOurs)
	case workspace.Path == "":
		return errors.New("that worktree has no path")
	}

	if !force {
		dirty, err := r.DirtyState(ctx, workspace.Path)
		if err != nil {
			// Unable to tell means unable to promise it is safe, and the safe reading of "I do not
			// know whether there is work here" is that there might be.
			return fmt.Errorf("could not check whether %s has uncommitted work: %w",
				workspace.Name, err)
		}
		if dirty.IsDirty() {
			return fmt.Errorf("%s has %d changed and %d untracked files: %w",
				workspace.Name, dirty.Staged+dirty.Unstaged, dirty.Untracked, ErrDirty)
		}
	}

	args := []string{"worktree", "remove", workspace.Path}
	if force {
		args = append(args, "--force")
	}
	if _, err := r.run(ctx, args...); err != nil {
		return fmt.Errorf("removing %s: %w", workspace.Name, err)
	}
	return nil
}

// DirtyState counts what has changed in a worktree.
func (r *Repo) DirtyState(ctx context.Context, path string) (core.DirtyState, error) {
	worktree := &Repo{dir: path}

	// -z so filenames containing newlines, which are legal and do exist, do not become two entries.
	out, err := worktree.run(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return core.DirtyState{}, err
	}

	var state core.DirtyState
	for _, entry := range parseStatus(out) {
		switch {
		case entry.Untracked():
			state.Untracked++
		default:
			if entry.X != ' ' && entry.X != '?' {
				state.Staged++
			}
			if entry.Y != ' ' && entry.Y != '?' {
				state.Unstaged++
			}
		}
	}
	return state, nil
}

// Describe fills in the details of one worktree: branch, head, dirty state and last activity.
func (r *Repo) Describe(ctx context.Context, snapshot core.WorkspaceSnapshot) (core.WorkspaceSnapshot, error) {
	worktree := &Repo{dir: snapshot.Path}

	snapshot.Revision, snapshot.RevisionError = r.Revision(ctx, snapshot.Path)

	if branch, err := worktree.run(ctx, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if branch == "HEAD" {
			snapshot.Detached = true
		} else {
			snapshot.Branch = branch
			snapshot.Detached = false
		}
	}

	dirty, err := r.DirtyState(ctx, snapshot.Path)
	if err != nil {
		return snapshot, fmt.Errorf("reading the state of %s: %w", snapshot.Name, err)
	}
	snapshot.Dirty = dirty

	if when, err := worktree.run(ctx, "log", "-1", "--format=%cI"); err == nil && when != "" {
		if at, parseErr := time.Parse(time.RFC3339, when); parseErr == nil {
			snapshot.LastActivity = at
		}
	}
	return snapshot, nil
}

// Revision computes the revision key of a worktree, and the reason it is unknown when it is.
//
// A thin forward to Revisions.Key, kept on Repo so callers that already hold a repository do not
// have to know that a cache exists.
func (r *Repo) Revision(ctx context.Context, path string) (core.RevisionKey, string) {
	revisions := r.revisions
	if revisions == nil {
		revisions = NewRevisions(0)
	}
	return revisions.Key(ctx, path)
}

// Revisions exposes the shared hash cache, so a poller can drop a worktree it has stopped watching.
func (r *Repo) Revisions() *Revisions { return r.revisions }

// validateBranchName refuses names git would reject or misread.
//
// Shares its reasoning with the branch check in the git tools, and is kept separate rather than
// shared because these two answer to different callers: that one refuses a name a model chose, this
// one refuses a name Canopy is about to build a directory from.
func validateBranchName(name string) error {
	switch {
	case name == "":
		return errors.New("a branch name is required")
	case strings.HasPrefix(name, "-"):
		return errors.New("a branch name cannot start with a dash")
	case strings.ContainsAny(name, " ~^:?*[\\"):
		return fmt.Errorf("%q contains a character git does not allow in a branch name", name)
	case strings.Contains(name, ".."):
		return errors.New("a branch name cannot contain two dots")
	}
	return nil
}

// validateWorktreeName refuses names that would produce a confusing or unsafe directory.
func validateWorktreeName(name string) error {
	switch {
	case name == "":
		return errors.New("a worktree needs a name")
	case strings.ContainsAny(name, `/\:*?"<>|`):
		return fmt.Errorf("%q contains a character that is not allowed in a directory name", name)
	case strings.HasPrefix(name, "."), strings.HasPrefix(name, "-"):
		return fmt.Errorf("a worktree name cannot start with %q", name[:1])
	case name == "." || name == "..":
		return fmt.Errorf("%q is not a name", name)
	case len(name) > 64:
		return errors.New("that worktree name is too long")
	}
	return nil
}

func (r *Repo) run(ctx context.Context, args ...string) (string, error) {
	result, err := exec.Run(ctx, "git", args, exec.Options{
		Dir:     r.dir,
		Env:     environ(),
		Timeout: 60 * time.Second,
	})
	if err != nil {
		return "", err
	}
	if !result.Ran {
		return "", fmt.Errorf("git could not be run: %s", strings.TrimSpace(result.Output))
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("git %s exited %s: %s",
			args[0], strconv.Itoa(result.ExitCode), strings.TrimSpace(result.Output))
	}
	return strings.TrimSpace(result.Output), nil
}
