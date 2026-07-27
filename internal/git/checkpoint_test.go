package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repo(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed here")
	}

	dir := t.TempDir()
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

	write(t, dir, "tracked.go", "package main\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "first")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(content)
}

// The whole promise: tracked changes, new files and deletions all come back.
func TestUndoRestoresEverythingAnAgentDid(t *testing.T) {
	dir := repo(t)
	taker := NewTaker(dir)
	ctx := context.Background()

	write(t, dir, "untracked.txt", "here before the checkpoint\n")

	checkpoint, err := taker.Take(ctx, "turn-1", "before the agent ran")
	if err != nil {
		t.Fatalf("Take: %v", err)
	}

	// An agent then does all three destructive things.
	write(t, dir, "tracked.go", "package main // ruined\n")
	write(t, dir, "invented.go", "package invented\n")
	if err := os.Remove(filepath.Join(dir, "untracked.txt")); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if err := taker.Restore(ctx, checkpoint); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	if got := read(t, dir, "tracked.go"); got != "package main\n" {
		t.Errorf("the edited file was not restored: %q", got)
	}
	// A restore that left the agent's new files lying around would leave somebody cleaning up by
	// hand, which is what they were trying to avoid.
	if read(t, dir, "invented.go") != "" {
		t.Error("a file the agent created survived the undo")
	}
	if got := read(t, dir, "untracked.txt"); got != "here before the checkpoint\n" {
		t.Errorf("a deleted untracked file was not brought back: %q", got)
	}
}

// Without a temporary index, taking a checkpoint would silently stage every untracked file in the
// user's working tree, and their next commit would include things they never chose.
func TestTakingACheckpointDoesNotDisturbWhatIsStaged(t *testing.T) {
	dir := repo(t)
	taker := NewTaker(dir)

	write(t, dir, "deliberately-staged.go", "package staged\n")
	git(t, dir, "add", "deliberately-staged.go")
	write(t, dir, "deliberately-not-staged.go", "package unstaged\n")

	before := git(t, dir, "status", "--porcelain")

	if _, err := taker.Take(context.Background(), "turn-1", "before"); err != nil {
		t.Fatalf("Take: %v", err)
	}

	after := git(t, dir, "status", "--porcelain")
	if before != after {
		t.Errorf("taking a checkpoint changed what was staged:\nbefore:\n%s\nafter:\n%s",
			before, after)
	}
}

// A checkpoint is Canopy's bookkeeping and should be invisible until it is wanted.
func TestCheckpointsDoNotAppearAsBranches(t *testing.T) {
	dir := repo(t)
	taker := NewTaker(dir)

	if _, err := taker.Take(context.Background(), "turn-1", "before"); err != nil {
		t.Fatalf("Take: %v", err)
	}

	branches := git(t, dir, "branch", "--list")
	if strings.Contains(branches, "checkpoint") || strings.Contains(branches, "turn-1") {
		t.Errorf("a checkpoint showed up in git branch:\n%s", branches)
	}
	// And nothing about the user's own history changed.
	if log := git(t, dir, "log", "--oneline"); strings.Contains(log, "canopy checkpoint") {
		t.Errorf("a checkpoint is in the branch history:\n%s", log)
	}
}

// Two agents writing checkpoints must not collide, which is why these are content addressed commits
// rather than entries on the stash stack.
func TestCheckpointsInTwoWorktreesAreIndependent(t *testing.T) {
	first, second := repo(t), repo(t)
	ctx := context.Background()

	firstTaker, secondTaker := NewTaker(first), NewTaker(second)

	firstPoint, err := firstTaker.Take(ctx, "turn-1", "first agent")
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if _, err := secondTaker.Take(ctx, "turn-1", "second agent"); err != nil {
		t.Fatalf("Take: %v", err)
	}

	write(t, second, "tracked.go", "package main // the second agent's work\n")

	// Restoring the first must not reach into the second.
	if err := firstTaker.Restore(ctx, firstPoint); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := read(t, second, "tracked.go"); !strings.Contains(got, "second agent's work") {
		t.Errorf("restoring one worktree touched another: %q", got)
	}
}

func TestListingAndForgetting(t *testing.T) {
	dir := repo(t)
	taker := NewTaker(dir)
	ctx := context.Background()

	for _, id := range []string{"turn-1", "turn-2", "turn-3"} {
		if _, err := taker.Take(ctx, id, "label for "+id); err != nil {
			t.Fatalf("Take: %v", err)
		}
	}

	checkpoints, err := taker.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(checkpoints) != 3 {
		t.Fatalf("%d checkpoints, want 3: %+v", len(checkpoints), checkpoints)
	}
	// The label has to survive, or the list is three identical hashes and nobody can choose.
	if !strings.Contains(checkpoints[0].Label, "turn-") {
		t.Errorf("the label was lost: %+v", checkpoints[0])
	}

	if err := taker.Forget(ctx, "turn-2"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	checkpoints, _ = taker.List(ctx)
	if len(checkpoints) != 2 {
		t.Errorf("%d checkpoints after forgetting one, want 2", len(checkpoints))
	}
}

// A checkpoint per turn on a long session is thousands of refs, and every one pins the objects it
// references so nothing can be garbage collected.
func TestPruningKeepsTheMostRecent(t *testing.T) {
	dir := repo(t)
	taker := NewTaker(dir)
	ctx := context.Background()

	for _, id := range []string{"turn-1", "turn-2", "turn-3", "turn-4", "turn-5"} {
		write(t, dir, "tracked.go", "package main // "+id+"\n")
		if _, err := taker.Take(ctx, id, id); err != nil {
			t.Fatalf("Take: %v", err)
		}
	}

	if err := taker.Prune(ctx, 2); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	checkpoints, _ := taker.List(ctx)
	if len(checkpoints) != 2 {
		t.Fatalf("%d checkpoints after pruning to 2", len(checkpoints))
	}
}

// A project somebody just started has no HEAD, which is a legitimate state and not a reason to
// refuse to protect their work.
func TestCheckpointingARepositoryWithNoCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed here")
	}

	dir := t.TempDir()
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
	write(t, dir, "brand-new.go", "package main\n")

	taker := NewTaker(dir)
	ctx := context.Background()

	checkpoint, err := taker.Take(ctx, "turn-1", "before")
	if err != nil {
		t.Fatalf("Take on a repository with no commits: %v", err)
	}

	write(t, dir, "brand-new.go", "package main // ruined\n")
	if err := taker.Restore(ctx, checkpoint); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got := read(t, dir, "brand-new.go"); got != "package main\n" {
		t.Errorf("restore on a repository with no commits gave %q", got)
	}
}

func TestRestoringAnEmptyCheckpointIsRefused(t *testing.T) {
	if err := NewTaker(t.TempDir()).Restore(context.Background(), Checkpoint{}); err == nil {
		t.Error("restoring a checkpoint with no commit should be refused rather than attempted")
	}
}
