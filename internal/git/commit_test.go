package git

import (
	"context"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The draft never invents a subject. A generated one like "update auth.go" is worse than a blank
// one: plausible enough to commit by accident, and useless to whoever reads the history later.
func TestTheDraftLeavesTheSubjectForAPerson(t *testing.T) {
	draft := Draft([]core.FileChange{
		{Path: "internal/auth/token.go", Status: 'M', Insertions: 12, Deletions: 3},
	})

	if draft.Message("") != "" {
		t.Errorf("a draft with no subject produced a message: %q", draft.Message(""))
	}
	if !strings.Contains(draft.Body, "token.go") {
		t.Errorf("the body does not say what changed: %q", draft.Body)
	}

	written := draft.Message("stop refreshing an expired token")
	if !strings.HasPrefix(written, draft.Prefix+": stop refreshing") {
		t.Errorf("the message assembled as %q", written)
	}
}

func TestTheTypeComesFromWhatTheFilesAre(t *testing.T) {
	cases := []struct {
		name    string
		changes []core.FileChange
		want    string
	}{
		{"only tests", []core.FileChange{{Path: "internal/git/diff_test.go", Status: 'M'}}, "test"},
		{"only documentation", []core.FileChange{{Path: "README.md", Status: 'M'}}, "docs"},
		{"only tooling", []core.FileChange{{Path: ".github/workflows/ci.yml", Status: 'M'}}, "ci"},
		{"a new source file", []core.FileChange{{Path: "internal/git/commit.go", Status: 'A'}}, "feat"},
		{"an edit to an existing one", []core.FileChange{{Path: "internal/git/diff.go", Status: 'M'}}, "chore"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			draft := Draft(c.changes)
			if !strings.HasPrefix(draft.Prefix, c.want) {
				t.Errorf("the draft is prefixed %q, want the type %q", draft.Prefix, c.want)
			}
		})
	}
}

// An edit that adds nothing must not claim to fix anything. The difference between a feature and a
// bug fix is not readable from a diff, so the neutral type is the honest one.
func TestAnOrdinaryEditDoesNotClaimToBeAFix(t *testing.T) {
	draft := Draft([]core.FileChange{{Path: "internal/git/diff.go", Status: 'M'}})
	if strings.HasPrefix(draft.Prefix, "fix") {
		t.Errorf("an edit was drafted as a fix: %q", draft.Prefix)
	}
}

func TestTheScopeIsWhatTheFilesShare(t *testing.T) {
	shared := Draft([]core.FileChange{
		{Path: "internal/tui/chat/model.go", Status: 'M'},
		{Path: "internal/tui/chat/input.go", Status: 'M'},
	})
	if !strings.Contains(shared.Prefix, "(chat)") {
		t.Errorf("the scope is missing: %q", shared.Prefix)
	}

	// A change that spans the project has no scope, rather than a filler one. A scope that is always
	// there carries no information.
	spread := Draft([]core.FileChange{
		{Path: "internal/tui/chat/model.go", Status: 'M'},
		{Path: "cmd/canopy/main.go", Status: 'M'},
	})
	if strings.Contains(spread.Prefix, "(") {
		t.Errorf("a change across the project was given a scope: %q", spread.Prefix)
	}
}

func TestALargeChangeDoesNotProduceAnEndlessMessage(t *testing.T) {
	var changes []core.FileChange
	for i := range 200 {
		changes = append(changes, core.FileChange{
			Path: "internal/pkg/file" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + ".go", Status: 'M',
		})
	}

	draft := Draft(changes)
	if lines := strings.Count(draft.Body, "\n"); lines > 20 {
		t.Errorf("a 200 file change produced a %d line body", lines)
	}
	if !strings.Contains(draft.Body, "other files") {
		t.Errorf("the body does not say how much it left out:\n%s", draft.Body)
	}
}

func TestNothingIsCommittedWithoutASubject(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	write(t, dir, "new.go", "package main\n")
	if err := r.Stage(ctx, dir, nil); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	draft := Draft([]core.FileChange{{Path: "new.go", Status: 'A'}})
	if _, err := r.Commit(ctx, dir, draft.Message("")); err == nil {
		t.Fatal("a draft with no subject was committed, so a list of file names would go into the " +
			"history with nothing summarising it")
	}
	if _, err := r.Commit(ctx, dir, "  "); err == nil {
		t.Error("an empty message was committed")
	}

	sha, err := r.Commit(ctx, dir, "feat: add the thing\n")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if len(sha) < 7 {
		t.Errorf("the commit returned %q rather than a SHA", sha)
	}
}

// The default cleanup strips lines beginning with a hash, which silently eats a subject that
// mentions an issue number.
func TestAMessageIsRecordedAsItWasWritten(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	write(t, dir, "new.go", "package main\n")
	if err := r.Stage(ctx, dir, nil); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := r.Commit(ctx, dir, "fix(auth): close #412 properly\n"); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	recorded := git(t, dir, "log", "-1", "--format=%B")
	if !strings.Contains(recorded, "#412") {
		t.Errorf("the issue number was stripped from the message: %q", recorded)
	}
}

func TestStagingIsExplicitAboutWhat(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	write(t, dir, "wanted.go", "package main\n")
	write(t, dir, "unwanted.go", "package main\n")

	if err := r.Stage(ctx, dir, []string{"wanted.go"}); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	staged := git(t, dir, "diff", "--cached", "--name-only")
	if !strings.Contains(staged, "wanted.go") {
		t.Errorf("the named file was not staged: %q", staged)
	}
	if strings.Contains(staged, "unwanted.go") {
		t.Errorf("staging one file staged another: %q", staged)
	}
}

func TestPushingWithNoRemoteIsRefusedRatherThanAttempted(t *testing.T) {
	dir := repo_(t)
	r, err := OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	ctx := context.Background()

	if r.HasRemote(ctx, dir, "origin") {
		t.Fatal("a fresh repository reported having a remote")
	}
	if err := r.Push(ctx, dir, "origin", ""); err == nil {
		t.Error("pushing with no branch named was accepted")
	}
}
