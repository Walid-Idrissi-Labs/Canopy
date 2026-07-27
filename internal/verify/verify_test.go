package verify

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	canopyexec "github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/git"
)

// The whole package is exercised against a real repository, real worktrees and real shell commands.
// A fake runner would let every test here pass while the thing that matters, a result staying bound
// to the code it was measured on, was broken.

func repository(t *testing.T) string {
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
	writeFile(t, dir, "main.go", "package main\n")

	// The marker file the configured test looks for is scaffolding rather than an agent's work, so
	// it is ignored. Left tracked it would count as a change in every diff and, worse, it would move
	// the revision, which would make the act of making a test pass invalidate its own result.
	writeFile(t, dir, ".gitignore", "pass\n")

	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "first")
	return dir
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// harness builds a verifier over a repository with the named agents, each in its own worktree.
//
// The configured test is a shell command reading a file in the worktree, so an agent "passes" by
// writing the right thing into its own tree. That is as close to the real shape as a test can get
// without a compiler in the loop.
func harness(t *testing.T, agents ...string) (*Verifier, *git.Repo, map[string]Subject) {
	t.Helper()

	dir := repository(t)
	repo, err := git.OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}

	verifier := New(repo, "main", []canopyexec.Test{
		{Name: "unit", Command: "test -f pass", Required: true},
	}, nil)

	subjects := make(map[string]Subject)
	var watched []Subject
	for _, name := range agents {
		workspace, err := repo.Create(context.Background(), name, "")
		if err != nil {
			t.Fatalf("Create(%s): %v", name, err)
		}
		subject := Subject{
			Agent:       name,
			WorkspaceID: workspace.ID,
			Dir:         workspace.Path,
			Branch:      workspace.Branch,
		}
		subjects[name] = subject
		watched = append(watched, subject)
	}
	verifier.Watch(watched)
	return verifier, repo, subjects
}

// look tells the verifier what is in each worktree now, which is what the poller does in production.
func look(t *testing.T, v *Verifier, subjects map[string]Subject) {
	t.Helper()
	ctx := context.Background()

	for _, subject := range subjects {
		key, reason := v.repo.Revision(ctx, subject.Dir)
		v.Observe(ctx, git.Change{WorkspaceID: subject.WorkspaceID, Path: subject.Dir, To: key, Reason: reason})
	}
}

// verified runs the configured tests for one agent and waits for them to finish.
func verified(t *testing.T, v *Verifier, agent string) {
	t.Helper()

	if err := v.Verify(context.Background(), agent); err != nil {
		t.Fatalf("Verify(%s): %v", agent, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, _ := v.Snapshot(agent)
		settled := true
		for _, test := range snapshot.Tests {
			if test.Latest == nil || test.Latest.State.IsPending() {
				settled = false
			}
		}
		if settled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the tests for %s never finished", agent)
}

// A6-04, the whole task in one test: an agent that edits its worktree turns stale, and re-running
// clears it. Nothing marks anything stale here, which is the point. Staleness is derived from the
// revision on the run against the revision in the tree.
func TestEditingAWorktreeTurnsItsResultStaleAndRerunningClearsIt(t *testing.T) {
	verifier, _, subjects := harness(t, "one")
	agent := subjects["one"]

	writeFile(t, agent.Dir, "pass", "")
	look(t, verifier, subjects)
	verified(t, verifier, "one")

	rollup, ok := verifier.Rollup("one")
	if !ok {
		t.Fatal("no roll-up for a watched agent")
	}
	if !rollup.Green {
		t.Fatalf("the agent is not green after passing: %s", rollup.Reason)
	}

	// An edit that has nothing to do with the test still moves the revision, per D-16. That is the
	// decided behaviour, coarse and whole worktree, and it is what a user sees.
	writeFile(t, agent.Dir, "README.md", "notes\n")
	look(t, verifier, subjects)

	rollup, _ = verifier.Rollup("one")
	if rollup.Green {
		t.Error("an edited worktree stayed green, so a result would outlive the code it describes")
	}
	if rollup.Tests != core.TestStale {
		t.Errorf("the aggregate is %q, want stale: the result is not wrong, it is out of date", rollup.Tests)
	}
	if !strings.Contains(rollup.Reason, "stale") {
		t.Errorf("the reason is %q, which does not say the evidence is out of date", rollup.Reason)
	}

	verified(t, verifier, "one")
	rollup, _ = verifier.Rollup("one")
	if !rollup.Green {
		t.Errorf("re-running did not clear the staleness: %s", rollup.Reason)
	}
}

func TestAFailingAgentIsNotGreen(t *testing.T) {
	verifier, _, subjects := harness(t, "one")

	look(t, verifier, subjects)
	verified(t, verifier, "one")

	rollup, _ := verifier.Rollup("one")
	if rollup.Green {
		t.Error("an agent whose test failed is green")
	}
	if rollup.Tests != core.TestFailing {
		t.Errorf("the aggregate is %q, want failing", rollup.Tests)
	}
}

// A6-05. Two agents pass, one fails; the passing pair are ordered by diff size and the failing one
// still gets a place, because an agent missing from the screen reads as an agent that vanished.
func TestRankingPutsThePassingAgentsFirstAndBreaksTiesOnSize(t *testing.T) {
	verifier, _, subjects := harness(t, "small", "large", "broken")

	writeFile(t, subjects["small"].Dir, "pass", "")
	writeFile(t, subjects["small"].Dir, "fix.go", "package main\n\nfunc fix() {}\n")

	writeFile(t, subjects["large"].Dir, "pass", "")
	writeFile(t, subjects["large"].Dir, "fix.go", strings.Repeat("// a great deal of code\n", 400))

	writeFile(t, subjects["broken"].Dir, "fix.go", "package main\n")

	look(t, verifier, subjects)
	for name := range subjects {
		verified(t, verifier, name)
	}

	ranking := verifier.Rank()
	if len(ranking.Unranked) != 0 {
		t.Fatalf("agents were refused a place with current evidence: %+v", ranking.Unranked)
	}
	if len(ranking.Ranked) != 3 {
		t.Fatalf("%d agents ranked, want 3", len(ranking.Ranked))
	}

	order := []string{ranking.Ranked[0].Agent, ranking.Ranked[1].Agent, ranking.Ranked[2].Agent}
	if order[0] != "small" || order[1] != "large" || order[2] != "broken" {
		t.Errorf("the order is %v, want small then large then broken: passing beats failing, and "+
			"between two passes the smaller diff wins", order)
	}

	best, ok := ranking.Best()
	if !ok || best.Agent != "small" {
		t.Errorf("the winner is %+v", best)
	}
	for _, placement := range ranking.Ranked {
		if placement.Reason == "" {
			t.Errorf("%s was placed with no reason given", placement.Agent)
		}
		if placement.Rank == 0 {
			t.Errorf("%s has no rank number", placement.Agent)
		}
	}
	if !strings.Contains(ranking.Ranked[0].Reason, "pass") {
		t.Errorf("the winner's reason is %q, which does not say what it won on", ranking.Ranked[0].Reason)
	}
}

// The acceptance criterion that makes ranking honest rather than a leaderboard.
func TestRankingRefusesAnAgentWhoseEvidenceWentStale(t *testing.T) {
	verifier, _, subjects := harness(t, "one", "two")

	for _, name := range []string{"one", "two"} {
		writeFile(t, subjects[name].Dir, "pass", "")
	}
	look(t, verifier, subjects)
	for _, name := range []string{"one", "two"} {
		verified(t, verifier, name)
	}

	// One agent keeps working after its tests ran, which is the ordinary case and the reason this
	// matters: the branch that looked best is usually the one still being edited.
	writeFile(t, subjects["two"].Dir, "more.go", "package main\n")
	look(t, verifier, subjects)

	ranking := verifier.Rank()
	if len(ranking.Ranked) != 1 || ranking.Ranked[0].Agent != "one" {
		t.Errorf("ranked %+v, want only the agent whose evidence still describes its code", ranking.Ranked)
	}
	if len(ranking.Unranked) != 1 || ranking.Unranked[0].Agent != "two" {
		t.Fatalf("unranked %+v, want the edited agent", ranking.Unranked)
	}
	refusal := ranking.Unranked[0]
	if refusal.Rank != 0 {
		t.Errorf("an unranked agent carries rank %d", refusal.Rank)
	}
	if !strings.Contains(refusal.Reason, "not ranked") || !strings.Contains(refusal.Reason, "stale") {
		t.Errorf("the refusal reads %q, which does not say why no placement was made", refusal.Reason)
	}
}

func TestRankingRefusesAnAgentWithNoResultAtAll(t *testing.T) {
	verifier, _, subjects := harness(t, "one")
	look(t, verifier, subjects)

	ranking := verifier.Rank()
	if len(ranking.Ranked) != 0 {
		t.Errorf("an agent that has never run a test was ranked: %+v", ranking.Ranked)
	}
	if len(ranking.Unranked) != 1 {
		t.Fatalf("%d unranked, want 1", len(ranking.Unranked))
	}
	if !strings.Contains(ranking.Unranked[0].Reason, "never been run") {
		t.Errorf("the refusal reads %q", ranking.Unranked[0].Reason)
	}
}

// A6-06. Green plus a real diff, easiest first.
func TestTheReviewQueueIsOrderedEasiestFirst(t *testing.T) {
	verifier, _, subjects := harness(t, "small", "large")

	writeFile(t, subjects["small"].Dir, "pass", "")
	writeFile(t, subjects["small"].Dir, "fix.go", "package main\n")

	writeFile(t, subjects["large"].Dir, "pass", "")
	writeFile(t, subjects["large"].Dir, "fix.go", strings.Repeat("// more\n", 300))

	look(t, verifier, subjects)
	verified(t, verifier, "small")
	verified(t, verifier, "large")

	queue := verifier.ReadyToReview()
	if len(queue) != 2 {
		t.Fatalf("%d agents ready, want 2: %+v", len(queue), queue)
	}
	if queue[0].Agent != "small" {
		t.Errorf("the queue leads with %q, want the smaller change: with six agents running, the "+
			"question is which one to look at next", queue[0].Agent)
	}
	if queue[0].Branch == "" {
		t.Error("the queue entry does not say which branch to look at")
	}
	if !strings.Contains(queue[0].Why, "file") {
		t.Errorf("the entry says %q, which does not say how much there is to read", queue[0].Why)
	}
}

// The two exclusions, which are what stop the queue being just a list of agents.
func TestTheReviewQueueExcludesStaleResultsAndEmptyDiffs(t *testing.T) {
	verifier, _, subjects := harness(t, "edited", "nothing-to-show")

	writeFile(t, subjects["edited"].Dir, "pass", "")
	writeFile(t, subjects["edited"].Dir, "fix.go", "package main\n")

	// Green with nothing to review. Passing over no changes is the state every repository starts in.
	writeFile(t, subjects["nothing-to-show"].Dir, "pass", "")

	look(t, verifier, subjects)
	verified(t, verifier, "edited")
	verified(t, verifier, "nothing-to-show")

	if queue := verifier.ReadyToReview(); len(queue) != 1 || queue[0].Agent != "edited" {
		t.Fatalf("the queue is %+v, want only the agent with something to show", queue)
	}

	writeFile(t, subjects["edited"].Dir, "another.go", "package main\n")
	look(t, verifier, subjects)

	if queue := verifier.ReadyToReview(); len(queue) != 0 {
		t.Errorf("an agent whose result went stale is still queued for review: %+v", queue)
	}
}

// An agent that has ended takes its evidence with it. A green row for a directory that no longer
// exists is worse than no row, because somebody will try to review it.
func TestEndingAnAgentRemovesItsEvidence(t *testing.T) {
	verifier, _, subjects := harness(t, "one", "two")

	for name := range subjects {
		writeFile(t, subjects[name].Dir, "pass", "")
		writeFile(t, subjects[name].Dir, "fix.go", "package main\n")
	}
	look(t, verifier, subjects)
	for name := range subjects {
		verified(t, verifier, name)
	}
	if len(verifier.ReadyToReview()) != 2 {
		t.Fatal("both agents should be ready before one of them ends")
	}

	verifier.Watch([]Subject{subjects["one"]})

	queue := verifier.ReadyToReview()
	if len(queue) != 1 || queue[0].Agent != "one" {
		t.Errorf("the queue is %+v after an agent ended", queue)
	}
	if _, ok := verifier.Snapshot("two"); ok {
		t.Error("an agent that ended still has a snapshot")
	}
	ranking := verifier.Rank()
	for _, placement := range append(ranking.Ranked, ranking.Unranked...) {
		if placement.Agent == "two" {
			t.Error("an agent that ended is still in the ranking")
		}
	}
}

// Nothing configured is not the same as nothing wrong, and it must not produce a placement either.
func TestAnAgentWithNoConfiguredTestsIsNotRanked(t *testing.T) {
	dir := repository(t)
	repo, err := git.OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	verifier := New(repo, "main", nil, nil)

	workspace, err := repo.Create(context.Background(), "one", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	subjects := map[string]Subject{"one": {
		Agent: "one", WorkspaceID: workspace.ID, Dir: workspace.Path, Branch: workspace.Branch,
	}}
	verifier.Watch([]Subject{subjects["one"]})
	look(t, verifier, subjects)

	rollup, _ := verifier.Rollup("one")
	if rollup.Green {
		t.Error("an agent with no configured tests is green, which is the product's central lie")
	}

	ranking := verifier.Rank()
	if len(ranking.Ranked) != 0 {
		t.Errorf("an agent with no evidence was ranked: %+v", ranking.Ranked)
	}
	if err := verifier.Verify(context.Background(), "one"); err == nil {
		t.Error("verifying with nothing configured was accepted silently")
	}
}

// An optional test that has been broken for weeks must not be invisible just because it cannot
// block the green. That is what Rollup.Caveat is for and this is the queue honouring it.
func TestAFailingOptionalTestIsCarriedIntoTheQueueEntry(t *testing.T) {
	dir := repository(t)
	repo, err := git.OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	verifier := New(repo, "main", []canopyexec.Test{
		{Name: "unit", Command: "exit 0", Required: true},
		{Name: "lint", Command: "exit 1"},
	}, nil)

	workspace, err := repo.Create(context.Background(), "one", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	subjects := map[string]Subject{"one": {
		Agent: "one", WorkspaceID: workspace.ID, Dir: workspace.Path, Branch: workspace.Branch,
	}}
	verifier.Watch([]Subject{subjects["one"]})

	writeFile(t, workspace.Path, "fix.go", "package main\n")
	look(t, verifier, subjects)
	verified(t, verifier, "one")

	queue := verifier.ReadyToReview()
	if len(queue) != 1 {
		t.Fatalf("%d ready, want 1: an optional failure must not withhold the green", len(queue))
	}
	if !strings.Contains(queue[0].Why, "lint") {
		t.Errorf("the entry says %q and never mentions the broken optional test", queue[0].Why)
	}
}

// An unknown revision is not a low score. There is no code identity to attach a result to, so there
// is nothing to place.
func TestAnUnknownRevisionIsRefusedRatherThanRankedLast(t *testing.T) {
	verifier, _, subjects := harness(t, "one")
	agent := subjects["one"]

	writeFile(t, agent.Dir, "pass", "")
	look(t, verifier, subjects)
	verified(t, verifier, "one")

	verifier.Observe(context.Background(), git.Change{
		WorkspaceID: agent.WorkspaceID,
		Path:        agent.Dir,
		To:          core.RevisionKey{},
		Reason:      "fixture.bin is 40.0 MB, over the 25.0 MB Canopy will fingerprint",
	})

	ranking := verifier.Rank()
	if len(ranking.Ranked) != 0 {
		t.Errorf("an agent with an unknown revision was ranked: %+v", ranking.Ranked)
	}
	if len(ranking.Unranked) != 1 {
		t.Fatalf("%d unranked, want 1", len(ranking.Unranked))
	}
	if !strings.Contains(ranking.Unranked[0].Reason, "fixture.bin") {
		t.Errorf("the refusal reads %q and does not carry the reason the revision is unknown",
			ranking.Unranked[0].Reason)
	}
	if len(verifier.ReadyToReview()) != 0 {
		t.Error("an agent whose revision is unknown is queued for review")
	}
}
