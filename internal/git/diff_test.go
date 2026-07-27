package git

import (
	"context"
	"strings"
	"testing"
)

// branched makes a repository with a worktree on its own branch, which is the shape every diff
// question in A6 and A7 is asked about.
func branched(t *testing.T) (*Repo, string) {
	t.Helper()

	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	workspace, err := r.Create(context.Background(), "worker", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return r, workspace.Path
}

func TestAnAgentsChangesIncludeWorkItHasNotCommitted(t *testing.T) {
	r, work := branched(t)
	ctx := context.Background()

	write(t, work, "tracked.go", "package main\n\nfunc main() {}\n")

	stat, err := r.Diff(ctx, work, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if stat.Empty() {
		t.Fatal("an uncommitted edit measured as no work at all, so an agent that has written a fix " +
			"and not committed it would rank last")
	}
	if stat.FilesChanged != 1 {
		t.Errorf("%d files changed, want 1: %s", stat.FilesChanged, stat.Summary())
	}
}

func TestANewFileCountsEvenBeforeItIsAdded(t *testing.T) {
	r, work := branched(t)

	write(t, work, "feature.go", "package main\n\nfunc feature() {}\n")

	changes, err := r.Changes(context.Background(), work, "main")
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}

	var found bool
	for _, change := range changes {
		if change.Path == "feature.go" {
			found = true
			if change.Status != 'A' {
				t.Errorf("the new file is %q, want A for added", string(change.Status))
			}
			if change.Insertions == 0 {
				t.Error("the new file measured as zero lines")
			}
		}
	}
	if !found {
		t.Error("an untracked new file is missing from the change list, so a fresh implementation " +
			"in its own file would look like no work")
	}
}

// Commits landing on the base after an agent branched are not the agent's work. Getting this wrong
// is how a branch that is a day behind main reports a huge diff and loses every ranking on size.
func TestCommitsOnTheBaseAreNotCountedAsTheAgentsWork(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	workspace, err := r.Create(ctx, "worker", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	write(t, workspace.Path, "agent.go", "package main\n")

	// Somebody else pushes to main while the agent works.
	write(t, dir, "unrelated.go", strings.Repeat("// noise\n", 200))
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "unrelated work on main")

	stat, err := r.Diff(ctx, workspace.Path, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if stat.FilesChanged != 1 {
		t.Errorf("%d files changed, want just the agent's one: %s", stat.FilesChanged, stat.Summary())
	}
	if stat.Lines() > 10 {
		t.Errorf("%d lines of churn, which is the other branch's work being blamed on this agent",
			stat.Lines())
	}
}

func TestABinaryFileIsCountedRatherThanMeasured(t *testing.T) {
	r, work := branched(t)

	write(t, work, "asset.bin", "head\x00\x01\x02tail")

	stat, err := r.Diff(context.Background(), work, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if stat.Binary != 1 {
		t.Errorf("%d binary files, want 1: a replaced asset reported as zero lines would make the "+
			"least reviewable change look like the smallest", stat.Binary)
	}
	if !strings.Contains(stat.Summary(), "binary") {
		t.Errorf("the summary %q does not mention the binary file", stat.Summary())
	}
}

func TestNoChangesReadsAsNoChanges(t *testing.T) {
	r, work := branched(t)

	stat, err := r.Diff(context.Background(), work, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !stat.Empty() {
		t.Errorf("a fresh worktree reports %s", stat.Summary())
	}
	if stat.Summary() != "no changes" {
		t.Errorf("the summary is %q", stat.Summary())
	}
}

func TestAPatchIsAvailablePerFile(t *testing.T) {
	r, work := branched(t)
	ctx := context.Background()

	write(t, work, "tracked.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")

	patch, err := r.Patch(ctx, work, "main", "tracked.go")
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !strings.Contains(patch, "hello") {
		t.Errorf("the patch does not contain the change:\n%s", patch)
	}
	if !strings.Contains(patch, "@@") {
		t.Errorf("the patch has no hunk header, so it is not a diff:\n%s", patch)
	}
}

func TestAPatchIsAvailableForAFileGitHasNeverSeen(t *testing.T) {
	r, work := branched(t)

	write(t, work, "brand-new.go", "package main\n\n// the whole point\n")

	patch, err := r.Patch(context.Background(), work, "main", "brand-new.go")
	if err != nil {
		t.Fatalf("Patch: %v", err)
	}
	if !strings.Contains(patch, "the whole point") {
		t.Errorf("an untracked file produced no reviewable patch:\n%s", patch)
	}
}

func TestARenameIsOneChangeThatRemembersItsOldName(t *testing.T) {
	r, work := branched(t)
	ctx := context.Background()

	git(t, work, "mv", "tracked.go", "renamed.go")
	git(t, work, "commit", "-m", "rename it")

	changes, err := r.Changes(ctx, work, "main")
	if err != nil {
		t.Fatalf("Changes: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("%d changes for one rename: %+v", len(changes), changes)
	}
	if changes[0].Path != "renamed.go" {
		t.Errorf("the change names %q, want the new path", changes[0].Path)
	}
	if changes[0].Status != 'R' {
		t.Errorf("the change is %q, want R", string(changes[0].Status))
	}
	if changes[0].Old != "tracked.go" {
		t.Errorf("the old path is %q, want tracked.go: a review that cannot say what a file used to "+
			"be called shows a delete and an add", changes[0].Old)
	}
}

func TestABaseThatDoesNotExistSaysSo(t *testing.T) {
	r, work := branched(t)

	if _, err := r.Diff(context.Background(), work, "no-such-branch"); err == nil {
		t.Error("diffing against a branch that does not exist was accepted")
	}
	if _, err := r.Diff(context.Background(), work, ""); err == nil {
		t.Error("diffing against nothing was accepted")
	}
}
