package git

// The one failure this product cannot have.
//
// Canopy makes exactly one claim: a result stops reading as current the moment the code it was
// gathered against changes. Everything else is presentation. These tests exist because that claim
// was false for any worktree whose status listing exceeded the general output bound, and it was
// false silently, which is the worst way for it to be false.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// enoughFilesToOverflow is well past the general 32 KB bound at roughly 45 bytes per status entry,
// and small enough that the test stays quick.
const enoughFilesToOverflow = 4000

func floodWithUntrackedFiles(t *testing.T, dir string) {
	t.Helper()
	for i := range enoughFilesToOverflow {
		write(t, dir, fmt.Sprintf("file-%04d-with-a-reasonably-long-name.txt", i), "original\n")
	}
}

// The regression itself, written as the user's experience rather than as an assertion about bytes.
//
// Before the fix this passed the first check and failed the second: the key called itself known and
// then did not move when a file changed, because that file's status entry was in the middle of the
// output, and the middle is what the general bound discards.
func TestARevisionIsNeverComputedFromATruncatedStatus(t *testing.T) {
	dir := repo_(t)
	ctx := context.Background()

	floodWithUntrackedFiles(t, dir)

	// A small bound stands in for a repository large enough to exceed the real one. The mechanism
	// is what is under test, and generating sixty four megabytes of status output to reach it would
	// make the test guarding the product's central claim too slow to keep.
	revisions := NewRevisions(0)
	revisions.outputLimit = 4096

	before, reason := revisions.Key(ctx, dir)
	if before.Known() {
		t.Fatalf("the revision of a worktree Canopy could not fully read reports itself known: %+v", before)
	}
	// Unknown is only acceptable when it says why. "Unknown" on its own is the same shrug the
	// dashboard exists to replace.
	if reason == "" {
		t.Error("the revision is unknown and nothing says why")
	}
	if !strings.Contains(reason, "more changes than Canopy can read") {
		t.Errorf("the reason does not explain what happened: %q", reason)
	}

	// And the property that actually matters, stated directly: whatever this key is, editing a file
	// must not leave it comparing equal to what it was.
	write(t, dir, "file-2000-with-a-reasonably-long-name.txt", "CHANGED\n")
	after, _ := revisions.Key(ctx, dir)
	if before.Equal(after) {
		t.Error("the revision did not move after an edit, so a result recorded before it still reads as current")
	}
}

// The bound belongs at the boundary rather than in each caller, so this asserts the boundary itself.
// A caller that forgets is the failure mode, and there are thirty three of them.
func TestTruncatedGitOutputIsAnErrorRatherThanAFragment(t *testing.T) {
	dir := repo_(t)
	floodWithUntrackedFiles(t, dir)

	small := &Repo{dir: dir, outputLimit: 4096}
	out, err := small.run(context.Background(), "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err == nil {
		t.Fatalf("a truncated status came back as %d bytes of usable output", len(out))
	}
	if !errors.Is(err, ErrOutputTruncated) {
		t.Errorf("error = %v, want it to identify itself as truncation", err)
	}
	if out != "" {
		t.Errorf("a fragment was returned alongside the error: %d bytes", len(out))
	}
}

// A repository big enough to be interesting must still work. A fix that made every large worktree
// permanently unknown would trade a lie for a product that does nothing.
func TestAWorktreeWithManyChangesStillGetsARevision(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	floodWithUntrackedFiles(t, dir)

	before, reason := r.Revision(ctx, dir)
	if !before.Known() {
		t.Fatalf("a worktree with %d changed files has no revision: %s", enoughFilesToOverflow, reason)
	}

	write(t, dir, "file-2000-with-a-reasonably-long-name.txt", "CHANGED\n")
	after, _ := r.Revision(ctx, dir)
	if before.Equal(after) {
		t.Error("the revision did not move after editing a file in the middle of the listing")
	}
}

// Checkpoints in a linked worktree, where .git is a file rather than a directory.
//
// This failed with "Not a directory" for every isolated agent, and the engine reported it through
// onStorageError and carried on, so undo quietly did nothing for exactly the agents most likely to
// need it.
func TestACheckpointCanBeTakenInALinkedWorktree(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	worktree, err := r.Create(ctx, "isolated", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The thing that makes this case different, asserted rather than assumed, so the test still
	// means something if git ever changes how linked worktrees are laid out.
	info, err := os.Stat(filepath.Join(worktree.Path, ".git"))
	if err != nil {
		t.Fatalf("stat .git: %v", err)
	}
	if info.IsDir() {
		t.Skip("this git lays linked worktrees out with a .git directory, so the case does not arise")
	}

	write(t, worktree.Path, "work.go", "package main\n")

	taker := NewTaker(worktree.Path)
	checkpoint, err := taker.Take(ctx, "turn-1", "before the first turn")
	if err != nil {
		t.Fatalf("Take in a linked worktree: %v", err)
	}
	if checkpoint.Commit == "" {
		t.Fatal("the checkpoint has no commit")
	}

	// And it has to actually restore, or it is a checkpoint in name only.
	write(t, worktree.Path, "work.go", "package main // ruined\n")
	write(t, worktree.Path, "junk.go", "package junk\n")

	if err := taker.Restore(ctx, checkpoint); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := read(t, worktree.Path, "work.go"); got != "package main\n" {
		t.Errorf("work.go = %q after restore", got)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, "junk.go")); !os.IsNotExist(err) {
		t.Error("a file created after the checkpoint survived the restore")
	}
}
