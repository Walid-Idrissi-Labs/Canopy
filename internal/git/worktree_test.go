package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func openRepo(t *testing.T) *Repo {
	t.Helper()

	repo, err := OpenRepo(repo_(t))
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	return repo
}

// repo_ is repo from checkpoint_test.go, renamed here to avoid shadowing the Repo type.
func repo_(t *testing.T) string { return repo(t) }

func TestDiscoveringWorktrees(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()

	for _, name := range []string{"one", "two", "three"} {
		if _, err := r.Create(ctx, name, ""); err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
	}

	found, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(found) != 4 {
		t.Fatalf("%d worktrees, want the primary plus three: %+v", len(found), found)
	}

	// Git lists the primary first, always, and everything downstream depends on it being marked.
	if found[0].Ownership != core.OwnershipPrimary {
		t.Errorf("the first worktree is %q, want primary", found[0].Ownership)
	}
	for _, w := range found[1:] {
		if w.Ownership != core.OwnershipManaged {
			t.Errorf("%s is %q, want managed since Canopy created it", w.Name, w.Ownership)
		}
		if w.ID == "" || w.Path == "" || w.Branch == "" {
			t.Errorf("incomplete worktree: %+v", w)
		}
	}
}

// A naming convention is something a user can accidentally satisfy. Somebody whose own worktree
// happens to be called canopy-feature should not find Canopy willing to delete it.
func TestAWorktreeCanopyDidNotCreateIsNotOurs(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	// Made by hand, exactly as somebody would, and named to look like Canopy's.
	external := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-canopy-feature")
	git(t, dir, "worktree", "add", "-b", "their-branch", external)

	found, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}

	// Compared by name rather than by path, because git reports the resolved path and on macOS the
	// temporary directory is a symlink, so the two differ.
	var theirs core.WorkspaceSnapshot
	for _, w := range found {
		if w.Name == filepath.Base(external) {
			theirs = w
		}
	}
	if theirs.Ownership != core.OwnershipExternalReadOnly {
		t.Fatalf("their worktree is %q, want external", theirs.Ownership)
	}

	if err := r.Remove(ctx, theirs, false); !errors.Is(err, ErrNotOurs) {
		t.Errorf("Canopy was willing to remove a worktree it did not create: %v", err)
	}
	// Even with force, which is the case somebody reaches for when they are in a hurry.
	if err := r.Remove(ctx, theirs, true); !errors.Is(err, ErrNotOurs) {
		t.Errorf("force removed somebody else's worktree: %v", err)
	}
	if _, err := os.Stat(external); err != nil {
		t.Error("their worktree was removed")
	}
}

// It is the user's own checkout and it is not Canopy's to remove, at any level of confirmation.
func TestThePrimaryCheckoutIsNeverRemoved(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()

	found, _ := r.Worktrees(ctx)
	primary := found[0]

	for _, force := range []bool{false, true} {
		if err := r.Remove(ctx, primary, force); err == nil {
			t.Errorf("the primary checkout was removed with force=%v", force)
		}
	}
	if _, err := os.Stat(primary.Path); err != nil {
		t.Fatal("the primary checkout is gone")
	}
}

// Uncommitted work is work, and an agent's abandoned experiment is sometimes the only copy of an
// idea.
func TestADirtyWorktreeIsNotRemovedSilently(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()

	created, err := r.Create(ctx, "scratch", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	write(t, created.Path, "work-in-progress.go", "package wip\n")

	err = r.Remove(ctx, created, false)
	if !errors.Is(err, ErrDirty) {
		t.Fatalf("a dirty worktree was removed without saying so: %v", err)
	}
	// The message has to say what would be lost, or "it is dirty" is not a decision anybody can make.
	if !strings.Contains(err.Error(), "untracked") {
		t.Errorf("the refusal should say what is there, got %q", err)
	}
	if _, statErr := os.Stat(created.Path); statErr != nil {
		t.Fatal("the worktree was removed anyway")
	}

	// And with explicit confirmation it goes.
	if err := r.Remove(ctx, created, true); err != nil {
		t.Fatalf("Remove with force: %v", err)
	}
	if _, statErr := os.Stat(created.Path); statErr == nil {
		t.Error("the worktree survived a forced removal")
	}
}

func TestRemovingACleanWorktree(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()

	created, err := r.Create(ctx, "clean", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.Remove(ctx, created, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	found, _ := r.Worktrees(ctx)
	if len(found) != 1 {
		t.Errorf("%d worktrees after removing the one that was added", len(found))
	}
}

// A half created worktree is worse than none: a directory that looks usable, is not registered as
// Canopy's, and will not be cleaned up.
func TestAFailedCreationLeavesNothingBehind(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	// A path that is already occupied, which is the realistic collision.
	occupied := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-taken")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if _, err := r.Create(ctx, "taken", ""); err == nil {
		t.Fatal("creating a worktree over an existing directory should be refused")
	}

	found, _ := r.Worktrees(ctx)
	if len(found) != 1 {
		t.Errorf("a failed creation registered a worktree anyway: %+v", found)
	}
}

func TestBadNamesAreRefused(t *testing.T) {
	r := openRepo(t)
	ctx := context.Background()

	for _, name := range []string{"", ".", "..", ".hidden", "-dash", "has/slash", "has:colon"} {
		if _, err := r.Create(ctx, name, ""); err == nil {
			t.Errorf("%q was accepted as a worktree name", name)
		}
	}
	if _, err := r.Create(ctx, "fine", "bad..branch"); err == nil {
		t.Error("a branch name git would reject was accepted")
	}
}

// A worktree nested in the primary checkout appears in every glob, every grep and every build, and
// the first thing anybody notices is their test suite running twice.
func TestAWorktreeIsCreatedBesideTheRepositoryNotInsideIt(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}

	created, err := r.Create(context.Background(), "beside", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Both resolved, because git reports the resolved path and on macOS the temporary directory is
	// a symlink, so comparing one against the other fails for a reason that has nothing to do with
	// where the worktree actually is.
	resolvedRepo, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}

	if strings.HasPrefix(created.Path, resolvedRepo+string(filepath.Separator)) {
		t.Errorf("the worktree was created inside the repository at %s", created.Path)
	}
	if filepath.Dir(created.Path) != filepath.Dir(resolvedRepo) {
		t.Errorf("the worktree is at %s, want it beside %s", created.Path, resolvedRepo)
	}
}

func TestDirtyStateCountsEachKind(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	clean, err := r.DirtyState(ctx, dir)
	if err != nil {
		t.Fatalf("DirtyState: %v", err)
	}
	if clean.IsDirty() {
		t.Errorf("a fresh checkout reads as dirty: %+v", clean)
	}

	write(t, dir, "tracked.go", "package main // changed\n")
	write(t, dir, "brand-new.go", "package new\n")
	git(t, dir, "add", "brand-new.go")
	write(t, dir, "never-added.txt", "loose\n")

	dirty, err := r.DirtyState(ctx, dir)
	if err != nil {
		t.Fatalf("DirtyState: %v", err)
	}
	if dirty.Staged == 0 {
		t.Errorf("staged = %d, want the added file counted", dirty.Staged)
	}
	if dirty.Unstaged == 0 {
		t.Errorf("unstaged = %d, want the edited file counted", dirty.Unstaged)
	}
	if dirty.Untracked == 0 {
		t.Errorf("untracked = %d, want the loose file counted", dirty.Untracked)
	}
}

// The digest is what turns a green result stale, so it has to move when the working tree moves and
// not only when a commit happens.
func TestTheDirtyDigestChangesWithTheWorkingTree(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	found, _ := r.Worktrees(ctx)
	clean, err := r.Describe(ctx, found[0])
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if clean.Revision.DirtyDigest != "" {
		t.Errorf("a clean tree has a dirty digest of %q, want none", clean.Revision.DirtyDigest)
	}
	if !clean.Revision.Known() {
		t.Error("a checkout with a commit should have a known revision")
	}

	write(t, dir, "tracked.go", "package main // edited\n")
	dirty, err := r.Describe(ctx, found[0])
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if dirty.Revision.DirtyDigest == "" {
		t.Error("an edited tree has no dirty digest, so a stale result would still read as fresh")
	}
	if clean.Revision.Equal(dirty.Revision) {
		t.Error("the revision did not change when the working tree did")
	}
}

// The ID appears in events, transcripts and audit entries, and an absolute path carries somebody's
// home directory name into all of them.
func TestWorkspaceIDsAreStableAndCarryNoPath(t *testing.T) {
	first := WorkspaceID("/Users/somebody/projects/thing")
	again := WorkspaceID("/Users/somebody/projects/thing")
	other := WorkspaceID("/Users/somebody/projects/other")

	if first != again {
		t.Error("the same path produced two different IDs, so nothing can be resolved across runs")
	}
	if first == other {
		t.Error("two paths collided")
	}
	if strings.Contains(first, "somebody") || strings.Contains(first, "/") {
		t.Errorf("the ID carries the path: %q", first)
	}
}

func TestOpeningSomethingThatIsNotARepository(t *testing.T) {
	if _, err := OpenRepo(t.TempDir()); err == nil {
		t.Error("a directory with no repository should be refused")
	}
}
