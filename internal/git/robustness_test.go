package git

// The robustness sweep's share of this package: worktrees that go away without asking, and paths
// that have spaces in them.
//
// Both are ordinary rather than exotic. Somebody tidying up with `rm -rf` is the normal way a
// worktree disappears, and a repository under "~/Side Projects" is where a lot of people keep their
// work. Neither is a hypothetical worth a defensive comment; both are worth a test.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// spacedRepo builds a repository whose own path contains a space, so that every path derived from
// it does too: the worktrees are created beside it and inherit the parent directory.
func spacedRepo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed here")
	}

	dir := filepath.Join(t.TempDir(), "my side projects", "the project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	write(t, dir, "a source file.go", "package main\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "first")
	return dir
}

// Nothing in this package builds a shell command, so this holds by construction. That is precisely
// why it is asserted: construction is a thing people change, and the symptom of getting it wrong is
// git reporting something about a file nobody named.
func TestAWorktreeUnderAPathWithSpacesIsFullyUsable(t *testing.T) {
	dir := spacedRepo(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	// A worktree name with a space in it too, since it becomes a directory name. The branch is
	// separate because git will not have a space in a ref, which the test below covers.
	workspace, err := r.Create(ctx, "feature one", "feature-one")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(workspace.Path, " ") {
		t.Fatalf("the worktree path %q has no space in it, so this test is not testing anything",
			workspace.Path)
	}

	found, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("%d worktrees, want the primary plus one: %+v", len(found), found)
	}
	// Parsed out of `git worktree list --porcelain`, which does not quote a path and separates the
	// field from its value with the same character the path contains.
	var made core.WorkspaceSnapshot
	for _, w := range found {
		if w.Ownership == core.OwnershipManaged {
			made = w
		}
	}
	if filepath.Base(made.Path) != filepath.Base(workspace.Path) {
		t.Fatalf("the worktree came back as %q, want %q", made.Path, workspace.Path)
	}
	if made.Branch != "feature-one" {
		t.Errorf("branch = %q, want feature-one", made.Branch)
	}

	// A file with a space in its name, inside a directory with a space in its name, because the
	// status parser and the fingerprinter each read a path out of git's own output.
	write(t, workspace.Path, "some notes/another file.md", "hello\n")

	dirty, err := r.DirtyState(ctx, workspace.Path)
	if err != nil {
		t.Fatalf("DirtyState: %v", err)
	}
	if dirty.Untracked != 1 {
		t.Errorf("dirty = %+v, want one untracked file", dirty)
	}

	before, reason := r.Revision(ctx, workspace.Path)
	if !before.Known() {
		t.Fatalf("the revision of a worktree with spaces in its path is unknown: %s", reason)
	}

	write(t, workspace.Path, "some notes/another file.md", "hello again\n")
	after, reason := r.Revision(ctx, workspace.Path)
	if !after.Known() {
		t.Fatalf("the revision went unknown after an edit: %s", reason)
	}
	if before.Equal(after) {
		t.Error("editing a file whose name contains a space did not move the revision, so a green " +
			"result would survive the edit that broke it")
	}

	described, err := r.Describe(ctx, made)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if described.Dirty.Untracked != 1 || described.Branch != "feature-one" {
		t.Errorf("described = %+v", described)
	}

	if err := r.Remove(ctx, made, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(workspace.Path); !os.IsNotExist(err) {
		t.Error("the worktree with spaces in its path was not removed")
	}
}

// Git has no branch name with a space in it. There is nothing to make work, so the requirement is
// that Canopy says so before building a command out of it rather than handing it over and reporting
// whatever git says back.
func TestABranchNameWithASpaceIsRefusedBeforeGitEverSeesIt(t *testing.T) {
	dir := spacedRepo(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	// Established rather than assumed, so this test documents why the refusal is right instead of
	// enshrining a restriction nobody checked.
	cmd := exec.Command("git", "check-ref-format", "--branch", "my branch")
	cmd.Dir = dir
	if err := cmd.Run(); err == nil {
		t.Fatal("git accepted a branch name with a space in it, so refusing one is now wrong")
	}

	// Refused by the check that exists for it, not by git reporting back. The difference is what the
	// name of this test claims: a name that reaches git has already been built into a command, and
	// the message that comes back is git's rather than one written for somebody who typed it.
	if err := validateBranchName("my branch"); err == nil {
		t.Error("the branch name check accepted a space, so the refusal below is git's rather " +
			"than Canopy's")
	}

	_, err = r.Create(ctx, "feature", "my branch")
	if err == nil {
		t.Fatal("a branch name git cannot have was accepted")
	}
	if !strings.Contains(err.Error(), "my branch") {
		t.Errorf("the refusal does not name what was refused: %v", err)
	}
	// Refused before anything was made, not after. A worktree left behind by a refusal is one
	// nothing will ever clean up, because nothing knows it is there.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-feature")); err == nil {
		t.Error("a worktree was created for a branch name that was then refused")
	}
}

// Somebody deleting a worktree with `rm -rf` is the ordinary way one goes away. Git keeps listing
// the registration until `git worktree prune` runs, which is right for git and wrong for a
// discovery pass that is supposed to say what exists.
func TestAWorktreeRemovedOutsideCanopyStopsBeingListed(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	staying, err := r.Create(ctx, "staying", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	going, err := r.Create(ctx, "going", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := os.RemoveAll(going.Path); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	found, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatalf("Worktrees after an external removal: %v", err)
	}

	for _, w := range found {
		if w.Name == filepath.Base(going.Path) {
			t.Errorf("a worktree deleted outside Canopy is still listed as %+v. Everything "+
				"downstream then has to explain a row about a directory that is not there: its "+
				"revision is unknown for a reason that reads as a git failure, its state cannot be "+
				"read at all, and it comes back as somebody else's worktree, which is the one "+
				"answer that stops Canopy tidying it up", w)
		}
	}

	// The rest of the set is untouched, which is the other half of disappearing safely. A discovery
	// pass that dropped everything because one entry was stale would be worse than one that kept it.
	if len(found) != 2 {
		t.Fatalf("%d worktrees, want the primary and the one still there: %+v", len(found), found)
	}
	if found[0].Ownership != core.OwnershipPrimary {
		t.Errorf("the primary is %q", found[0].Ownership)
	}
	if found[1].Name != filepath.Base(staying.Path) {
		t.Errorf("the surviving worktree is %q, want %q", found[1].Name, filepath.Base(staying.Path))
	}
}

// The poller keeps running across the removal, because a worktree going away while Canopy is
// watching it is the normal case rather than a shutdown. It has to report an unknown revision with
// a reason and then stop repeating itself, not fail, hang or claim the worktree changed on every
// tick from then on.
func TestPollingAWorktreeThatWasRemovedOutsideCanopyIsSafe(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	workspace, err := r.Create(ctx, "doomed", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	found, err := r.Worktrees(ctx)
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}

	seen := newCollector()
	poller := NewPoller(r, time.Hour, seen.record)
	poller.Watch(found)

	if changes := poller.Poll(ctx); len(changes) != len(found) {
		t.Fatalf("the first poll reported %d changes, want %d", len(changes), len(found))
	}

	if err := os.RemoveAll(workspace.Path); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	changes := poller.Poll(ctx)
	if len(changes) != 1 {
		t.Fatalf("%d changes after the worktree was deleted, want one: %+v", len(changes), changes)
	}
	if changes[0].To.Known() {
		t.Errorf("the revision of a deleted worktree came back known: %+v", changes[0])
	}
	if changes[0].Reason == "" {
		t.Error("an unknown revision with no reason leaves the screen saying nothing at all")
	}

	// The same unknown with the same explanation is not news. Without this, a deleted worktree
	// redraws the interface every two seconds forever.
	if repeat := poller.Poll(ctx); len(repeat) != 0 {
		t.Errorf("the deleted worktree reported %d further changes: %+v", len(repeat), repeat)
	}

	key, reason, ok := poller.Revision(workspace.ID)
	if !ok || key.Known() || reason == "" {
		t.Errorf("Revision = %+v, %q, %v, want an unknown revision with an explanation",
			key, reason, ok)
	}
}
