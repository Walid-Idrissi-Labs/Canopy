package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// key is the revision of dir, failing the test if it could not be computed. Most of these tests
// care that two keys differ, so the reason only ever appears when something has gone wrong.
func key(t *testing.T, dir string) string {
	t.Helper()
	revision, reason := NewRevisions(0).Key(context.Background(), dir)
	if reason != "" {
		t.Fatalf("the revision of %s could not be computed: %s", dir, reason)
	}
	if !revision.Known() {
		t.Fatalf("the revision of %s is unknown with no reason given", dir)
	}
	return revision.String()
}

// The four things that must move the key, and the one that must not. This is the acceptance
// criterion for A6-01 read straight across, and the reason each case is here rather than folded
// into one table is that a table would hide which of them broke.
func TestWhatMovesTheRevisionKey(t *testing.T) {
	dir := repo_(t)

	t.Run("an edit to a tracked file", func(t *testing.T) {
		before := key(t, dir)
		write(t, dir, "tracked.go", "package main\n\nfunc main() {}\n")
		if after := key(t, dir); after == before {
			t.Error("editing a tracked file left the revision unchanged, so a passing result would " +
				"still look current against code that no longer exists")
		}
	})

	t.Run("staging that content", func(t *testing.T) {
		before := key(t, dir)
		git(t, dir, "add", "tracked.go")
		if after := key(t, dir); after == before {
			t.Error("staging left the revision unchanged, so the index and the working tree would be " +
				"indistinguishable to anything downstream")
		}
	})

	t.Run("a new untracked file", func(t *testing.T) {
		before := key(t, dir)
		write(t, dir, "brand-new.go", "package main\n")
		if after := key(t, dir); after == before {
			t.Error("a new untracked file left the revision unchanged, and a new source file is " +
				"exactly the kind of change that breaks a build")
		}
	})

	t.Run("a git ignored file, which must not", func(t *testing.T) {
		write(t, dir, ".gitignore", "secrets.env\n")
		git(t, dir, "add", ".gitignore")
		git(t, dir, "commit", "-m", "ignore secrets")

		before := key(t, dir)
		write(t, dir, "secrets.env", "TOKEN=one\n")
		if after := key(t, dir); after != before {
			t.Error("an ignored file moved the revision, which would turn every dotenv edit into a " +
				"stale result and teach the user to ignore staleness")
		}
	})
}

// A round trip through the same content has to land on the same key. Without this the digest is a
// change counter rather than a content fingerprint, and undoing an edit would leave a green result
// permanently stale against code identical to the code it was measured on.
func TestRevertingAnEditRestoresTheRevision(t *testing.T) {
	dir := repo_(t)

	clean := key(t, dir)
	write(t, dir, "tracked.go", "package main\n// changed\n")
	dirty := key(t, dir)
	write(t, dir, "tracked.go", "package main\n")
	back := key(t, dir)

	if dirty == clean {
		t.Fatal("the edit did not register at all")
	}
	if back != clean {
		t.Errorf("reverting the edit gave %s, want the original %s: the digest is counting events "+
			"rather than hashing content", back, clean)
	}
}

// D-09: the link target is hashed and the link is not followed. Retargeting a symlink is a real
// change to what the code does, and following one is how a fingerprint leaves the worktree.
func TestASymlinkHashesItsTargetRatherThanFollowingIt(t *testing.T) {
	dir := repo_(t)

	write(t, dir, "real.go", "package main\n")
	link := filepath.Join(dir, "link.go")
	if err := os.Symlink("real.go", link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	before := key(t, dir)
	if err := os.Remove(link); err != nil {
		t.Fatalf("removing the link: %v", err)
	}
	if err := os.Symlink("other.go", link); err != nil {
		t.Fatalf("re-pointing the link: %v", err)
	}
	if after := key(t, dir); after == before {
		t.Error("re-pointing a symlink left the revision unchanged")
	}

	// And the target being unreadable is not an error, since the whole point is that it is never
	// opened. A dangling link is a legitimate thing to have in a worktree.
	if _, reason := NewRevisions(0).Key(context.Background(), dir); reason != "" {
		t.Errorf("a dangling symlink made the revision unknown: %s", reason)
	}
}

// D-09: a submodule contributes its HEAD and nothing else. Canopy does not recurse in 0.1, which is
// a documented limitation rather than a silent one, so what is asserted here is that the SHA counts
// and that an uninitialised submodule does not make the whole revision unknown.
func TestASubmoduleContributesItsHead(t *testing.T) {
	outer := repo_(t)
	inner := repo_(t)

	// -c protocol.file.allow=always because git refuses file transports for submodules by default.
	git(t, outer, "-c", "protocol.file.allow=always", "submodule", "add", inner, "vendored")

	before := key(t, outer)

	write(t, inner, "tracked.go", "package main\n// moved on\n")
	git(t, inner, "add", ".")
	git(t, inner, "commit", "-m", "second")
	git(t, filepath.Join(outer, "vendored"), "fetch", "origin")
	git(t, filepath.Join(outer, "vendored"), "checkout", strings.TrimSpace(git(t, inner, "rev-parse", "HEAD")))

	if after := key(t, outer); after == before {
		t.Error("moving a submodule's HEAD left the outer revision unchanged, so a dependency bump " +
			"would not invalidate a passing result")
	}
}

// D-09 in full: over the limit the revision is unknown, and the reason names the file. Skipping the
// file and carrying on is the tempting choice and the wrong one, because a large fixture could then
// change without disturbing a green result.
func TestAnOversizedFileMakesTheRevisionUnknown(t *testing.T) {
	dir := repo_(t)

	write(t, dir, "fixture.bin", strings.Repeat("x", 4096))

	revisions := NewRevisions(1024)
	revision, reason := revisions.Key(context.Background(), dir)

	if revision.Known() {
		t.Error("an unfingerprintable file left the revision known, which is the false green this " +
			"whole mechanism exists to prevent")
	}
	if !strings.Contains(reason, "fixture.bin") {
		t.Errorf("the reason is %q, which does not name the file the user has to do something about", reason)
	}
	if !strings.Contains(reason, "MB") {
		t.Errorf("the reason is %q, which does not say how big the file is or what the limit is", reason)
	}
}

// An unknown revision is never equal to another unknown revision, so nothing computed while a
// worktree was unreadable can survive as current. Asserted here as well as in core because this is
// the caller that produces the unknown keys in practice.
func TestTwoUnknownRevisionsAreNotTheSameRevision(t *testing.T) {
	dir := repo_(t)
	write(t, dir, "fixture.bin", strings.Repeat("x", 4096))

	revisions := NewRevisions(1024)
	first, _ := revisions.Key(context.Background(), dir)
	second, _ := revisions.Key(context.Background(), dir)

	if first.Equal(second) {
		t.Error("two unknown revisions compared equal, which would let evidence gathered during an " +
			"outage keep looking current forever")
	}
}

// A repository with no commits has no HEAD. That is an ordinary state for a directory somebody just
// ran git init in, and it has to read as unknown with an explanation rather than as a crash.
func TestARepositoryWithNoCommitsHasNoRevision(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")

	revision, reason := NewRevisions(0).Key(context.Background(), dir)
	if revision.Known() {
		t.Error("a repository with no commits reported a known revision")
	}
	if reason == "" {
		t.Error("no reason was given for the unknown revision, so the dashboard would have nothing to show")
	}
}

// Executable bit only, same content. Git tracks this and so must the digest, or a chmod that fixes
// a broken CI script would leave the previous failing result looking current.
func TestChangingOnlyThePermissionsMovesTheRevision(t *testing.T) {
	dir := repo_(t)
	write(t, dir, "script.sh", "#!/bin/sh\necho hello\n")

	before := key(t, dir)
	if err := os.Chmod(filepath.Join(dir, "script.sh"), 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if after := key(t, dir); after == before {
		t.Error("making a file executable left the revision unchanged")
	}
}

// The rename is the case the old hand rolled status parser got wrong, and it got it wrong quietly:
// the counts were only ever slightly too high. Both readings of the status output now come from one
// parser, so this asserts the counts directly.
func TestARenameCountsOnce(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}

	git(t, dir, "mv", "tracked.go", "renamed.go")

	dirty, err := r.DirtyState(context.Background(), dir)
	if err != nil {
		t.Fatalf("DirtyState: %v", err)
	}
	if dirty.Staged != 1 || dirty.Unstaged != 0 || dirty.Untracked != 0 {
		t.Errorf("a rename counted as %+v, want exactly one staged change: with -z the old path "+
			"arrives as its own field and was being read as a second entry", dirty)
	}
}

// The cache is a performance device and must never become a correctness one. Same size, new
// content, new modification time is the ordinary case an editor produces, and it has to be seen.
func TestTheHashCacheStillSeesASameLengthEdit(t *testing.T) {
	dir := repo_(t)
	revisions := NewRevisions(0)
	ctx := context.Background()

	write(t, dir, "note.txt", "aaaa")
	before, _ := revisions.Key(ctx, dir)

	write(t, dir, "note.txt", "bbbb")
	after, _ := revisions.Key(ctx, dir)

	if before.Equal(after) {
		t.Error("an edit of the same length was hidden by the content hash cache")
	}
}

func TestForgettingAWorktreeDropsItsCachedHashes(t *testing.T) {
	dir := repo_(t)
	revisions := NewRevisions(0)

	write(t, dir, "note.txt", "hello")
	if _, reason := revisions.Key(context.Background(), dir); reason != "" {
		t.Fatalf("Key: %s", reason)
	}

	revisions.mu.Lock()
	held := len(revisions.cached)
	revisions.mu.Unlock()
	if held == 0 {
		t.Fatal("nothing was cached, so this test is not testing anything")
	}

	revisions.Forget(dir)

	revisions.mu.Lock()
	defer revisions.mu.Unlock()
	if len(revisions.cached) != 0 {
		t.Errorf("%d hashes survived forgetting the worktree, so a long session leaks one entry per "+
			"file per agent it ever ran", len(revisions.cached))
	}
}
