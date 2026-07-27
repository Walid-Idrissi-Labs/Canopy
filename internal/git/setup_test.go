package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// prepared builds a repository with something ignored, something tracked, and a worktree to
// prepare. The ignored file holds a value that must never appear in anything this package reports.
func prepared(t *testing.T) (*Repo, string, core.WorkspaceSnapshot) {
	t.Helper()

	dir := repo_(t)
	write(t, dir, ".gitignore", ".env\nsecrets/\ncache/\n")
	git(t, dir, "add", ".gitignore")
	git(t, dir, "commit", "-m", "ignore the environment")

	write(t, dir, ".env", "API_KEY=sk-do-not-print-this\n")
	if err := os.Chmod(filepath.Join(dir, ".env"), 0o600); err != nil {
		t.Fatalf("Chmod: %v", err)
	}

	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	workspace, err := r.Create(context.Background(), "agent", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return r, dir, workspace
}

func yesToEverything() Confirm {
	return Confirm{
		Ignored: func(CopyRequest) bool { return true },
		Tracked: func(CopyRequest) bool { return true },
	}
}

// The whole reason this task exists: a worktree with no .env is a worktree where nothing runs.
func TestASecretIsCopiedIntoAFreshWorktree(t *testing.T) {
	r, _, workspace := prepared(t)

	result, err := r.Prepare(context.Background(), workspace,
		Environment{Copy: []string{".env"}}, yesToEverything())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(result.Copied) != 1 || result.Copied[0] != ".env" {
		t.Fatalf("copied %v, want [.env]. Skipped: %+v", result.Copied, result.Skipped)
	}

	info, err := os.Stat(filepath.Join(workspace.Path, ".env"))
	if err != nil {
		t.Fatalf("the copy is not there: %v", err)
	}
	// A .env is routinely 0600, and copying it out at 0644 hands the agent's credentials to every
	// account on the machine.
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the copy is mode %v, want 0600 as the original was", perm)
	}
}

// A caller with nowhere to ask copies nothing, rather than defaulting to yes.
func TestNothingIsCopiedWithoutConfirmation(t *testing.T) {
	r, _, workspace := prepared(t)

	result, err := r.Prepare(context.Background(), workspace,
		Environment{Copy: []string{".env"}}, Confirm{})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(result.Copied) != 0 {
		t.Fatalf("copied %v with no confirmation wired up", result.Copied)
	}
	if len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "confirmed") {
		t.Errorf("skips = %+v, want one that says nothing confirmed it", result.Skipped)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, ".env")); !os.IsNotExist(err) {
		t.Error("the file was copied anyway")
	}
}

func TestDecliningCopiesNothing(t *testing.T) {
	r, _, workspace := prepared(t)

	asked := 0
	result, err := r.Prepare(context.Background(), workspace, Environment{Copy: []string{".env"}},
		Confirm{Ignored: func(CopyRequest) bool { asked++; return false }})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if asked != 1 {
		t.Errorf("asked %d times, want exactly once", asked)
	}
	if len(result.Copied) != 0 {
		t.Errorf("copied %v after being declined", result.Copied)
	}
}

// The separate question, which is the point of Confirm having two fields rather than a flag. A
// caller that only wired up the ordinary case must not be able to answer the other one with it.
func TestATrackedFileNeedsItsOwnConfirmation(t *testing.T) {
	r, _, workspace := prepared(t)
	ctx := context.Background()
	env := Environment{Copy: []string{"tracked.go"}}

	ignoredOnly := Confirm{Ignored: func(CopyRequest) bool { return true }}
	result, err := r.Prepare(ctx, workspace, env, ignoredOnly)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(result.Copied) != 0 {
		t.Fatalf("a tracked file was copied on the strength of the ignored answer: %v", result.Copied)
	}
	if len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "tracks") {
		t.Errorf("skips = %+v, want one that says git tracks it", result.Skipped)
	}

	// And with the separate question answered, it goes through.
	answered := false
	result, err = r.Prepare(ctx, workspace, env,
		Confirm{Tracked: func(r CopyRequest) bool { answered = !r.Ignored; return true }})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !answered {
		t.Error("the tracked question was asked with Ignored set, so the two are not distinguishable")
	}
	if len(result.Copied) != 1 {
		t.Errorf("copied %v, want the tracked file once it was confirmed separately", result.Copied)
	}
}

// An allow list is an allow list. Something ignored but unlisted stays where it is.
func TestOnlyAllowListedPathsAreCopied(t *testing.T) {
	r, dir, workspace := prepared(t)
	write(t, dir, "secrets/token.txt", "another one\n")

	result, err := r.Prepare(context.Background(), workspace,
		Environment{Copy: []string{".env"}}, yesToEverything())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(result.Copied) != 1 {
		t.Fatalf("copied %v, want only the listed path", result.Copied)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "secrets")); !os.IsNotExist(err) {
		t.Error("an ignored directory that was not on the allow list was copied anyway")
	}
}

// A worktree where setup failed looks exactly like a healthy one from the outside. If that is not
// said out loud, an agent is dispatched into it and produces failures nobody can attribute.
func TestSetupFailureIsAVisibleState(t *testing.T) {
	r, _, workspace := prepared(t)

	result, err := r.Prepare(context.Background(), workspace,
		Environment{Setup: "echo installing; exit 3"}, yesToEverything())
	if err != nil {
		// A failing setup command is a state, not an error. The worktree still exists and still has
		// the code in it, and somebody may want to go and fix it by hand.
		t.Fatalf("Prepare returned an error for a command that ran and failed: %v", err)
	}
	if result.OK() {
		t.Error("a setup command that exited 3 reports OK")
	}
	if result.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", result.ExitCode)
	}
	if !strings.Contains(result.Summary(), "not ready") {
		t.Errorf("summary = %q, want it to say the worktree is not ready", result.Summary())
	}
	if !strings.Contains(result.Output, "installing") {
		t.Errorf("output = %q, want the command's output captured", result.Output)
	}
}

func TestTheSetupCommandRunsInTheWorktree(t *testing.T) {
	r, dir, workspace := prepared(t)

	result, err := r.Prepare(context.Background(), workspace,
		Environment{Setup: "touch built-here"}, yesToEverything())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !result.OK() {
		t.Fatalf("setup failed: %s", result.Output)
	}

	if _, err := os.Stat(filepath.Join(workspace.Path, "built-here")); err != nil {
		t.Errorf("the setup command did not run in the worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "built-here")); !os.IsNotExist(err) {
		t.Error("the setup command ran in the primary checkout, which is somebody else's")
	}
}

// Half an hour of nothing looks like a build that is working.
func TestASetupCommandThatHangsIsNotWaitedFor(t *testing.T) {
	r, _, workspace := prepared(t)

	result, err := r.Prepare(context.Background(), workspace,
		Environment{Setup: "sleep 30", SetupTimeout: 150 * time.Millisecond}, yesToEverything())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !result.TimedOut {
		t.Fatal("a command that slept through its timeout is not reported as timed out")
	}
	if result.OK() {
		t.Error("a timed out setup reports OK")
	}
	if !strings.Contains(result.Summary(), "timed out") {
		t.Errorf("summary = %q, want it to say it timed out", result.Summary())
	}
}

// The one thing in this package that must never be printed is the contents of what it copies.
func TestSecretContentsAreNeverReported(t *testing.T) {
	r, _, workspace := prepared(t)

	result, err := r.Prepare(context.Background(), workspace,
		Environment{Copy: []string{".env"}}, yesToEverything())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	// Everything a caller could reasonably render, in one string.
	rendered := fmt.Sprintf("%+v %s", result, result.Summary())
	if strings.Contains(rendered, "sk-do-not-print-this") {
		t.Errorf("the contents of the copied secret appear in the result: %s", rendered)
	}
}

// The allow list is configuration rather than model output, but it is still what decides which
// files leave a checkout.
func TestAPathOutsideTheRepositoryIsRefused(t *testing.T) {
	r, dir, workspace := prepared(t)

	outside := filepath.Join(filepath.Dir(dir), "not-yours.txt")
	if err := os.WriteFile(outside, []byte("someone else's\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, path := range []string{"../not-yours.txt", outside, "..", "."} {
		result, err := r.Prepare(context.Background(), workspace,
			Environment{Copy: []string{path}}, yesToEverything())
		if err != nil {
			t.Fatalf("Prepare(%q): %v", path, err)
		}
		if len(result.Copied) != 0 {
			t.Errorf("%q was copied: %v", path, result.Copied)
		}
		if len(result.Skipped) != 1 {
			t.Errorf("%q produced skips %+v, want exactly one refusal", path, result.Skipped)
		}
	}
}

// Materialising a symlink inside an isolated worktree is a route back out of it that nobody chose.
func TestASymlinkIsNotCopied(t *testing.T) {
	r, dir, workspace := prepared(t)

	if err := os.Symlink(filepath.Join(dir, ".env"), filepath.Join(dir, "cache")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	result, err := r.Prepare(context.Background(), workspace,
		Environment{Copy: []string{"cache"}}, yesToEverything())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(result.Copied) != 0 {
		t.Fatalf("a symlink was copied into the worktree: %v", result.Copied)
	}
	if len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "symlink") {
		t.Errorf("skips = %+v, want one that says it is a symlink", result.Skipped)
	}
}

// Answering yes to node_modules without being told it is four hundred megabytes is not an answer.
func TestTheQuestionCarriesTheSize(t *testing.T) {
	r, dir, workspace := prepared(t)
	write(t, dir, "cache/one.bin", strings.Repeat("x", 4096))
	write(t, dir, "cache/two.bin", strings.Repeat("x", 2048))

	var asked CopyRequest
	if _, err := r.Prepare(context.Background(), workspace, Environment{Copy: []string{"cache"}},
		Confirm{Ignored: func(r CopyRequest) bool { asked = r; return true }}); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if !asked.Dir {
		t.Error("a directory was not reported as one")
	}
	if asked.Files != 2 {
		t.Errorf("files = %d, want 2", asked.Files)
	}
	if asked.Bytes != 6144 {
		t.Errorf("bytes = %d, want 6144", asked.Bytes)
	}
	if asked.Large() {
		t.Error("six kilobytes is reported as large")
	}
}

// An allow list is written once and applies to every worktree forever, so it naturally names files
// that are not there. That is not a failure of the worktree.
func TestAMissingPathIsASkipRatherThanAFailure(t *testing.T) {
	r, _, workspace := prepared(t)

	result, err := r.Prepare(context.Background(), workspace,
		Environment{Copy: []string{".env", "never-existed"}}, yesToEverything())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(result.Copied) != 1 {
		t.Errorf("copied %v, want the one that exists", result.Copied)
	}
	if len(result.Skipped) != 1 || result.Skipped[0].Path != "never-existed" {
		t.Errorf("skips = %+v, want the missing one reported", result.Skipped)
	}
	if !result.OK() {
		t.Error("a missing allow list entry made the whole preparation not OK")
	}
}

func TestPreparingThePrimaryCheckoutIsRefused(t *testing.T) {
	r, dir, _ := prepared(t)

	primary := core.WorkspaceSnapshot{Path: dir, Ownership: core.OwnershipPrimary}
	_, err := r.Prepare(context.Background(), primary,
		Environment{Setup: "touch should-not-happen"}, yesToEverything())
	if err == nil {
		t.Fatal("preparing the primary checkout was allowed")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "should-not-happen")); !os.IsNotExist(statErr) {
		t.Error("a setup command ran in the user's own checkout")
	}
}

// The zero Environment is the common case: most repositories need no preparation at all.
func TestAnEmptyEnvironmentDoesNothingAndSaysSo(t *testing.T) {
	r, _, workspace := prepared(t)

	result, err := r.Prepare(context.Background(), workspace, Environment{}, yesToEverything())
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if result.Ran {
		t.Error("a command ran when none was configured")
	}
	if !result.OK() || result.Summary() != "ready" {
		t.Errorf("summary = %q, OK = %v, want a plain ready", result.Summary(), result.OK())
	}
}
