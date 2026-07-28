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

// Agent names are labels, not evidence identities. Reusing a name for another worktree must start
// with no result even when both worktrees happen to be at the same revision. Otherwise a brand-new
// agent can inherit a green run it never performed.
func TestMovingAnAgentToAnotherWorkspaceClearsItsEvidence(t *testing.T) {
	verifier, repo, subjects := harness(t, "one")
	first := subjects["one"]

	writeFile(t, first.Dir, "pass", "")
	look(t, verifier, subjects)
	verified(t, verifier, "one")
	if rollup, _ := verifier.Rollup("one"); !rollup.Green {
		t.Fatalf("the first workspace never became green: %s", rollup.Reason)
	}

	secondWorkspace, err := repo.Create(context.Background(), "replacement", "")
	if err != nil {
		t.Fatalf("Create replacement: %v", err)
	}
	second := Subject{
		Agent:       "one",
		WorkspaceID: secondWorkspace.ID,
		Dir:         secondWorkspace.Path,
		Branch:      secondWorkspace.Branch,
	}
	// The marker is ignored, so both worktrees still have the exact same RevisionKey. That is what
	// makes inherited evidence dangerous instead of merely stale.
	writeFile(t, second.Dir, "pass", "")
	verifier.Watch([]Subject{second})
	secondKey, reason := repo.Revision(context.Background(), second.Dir)
	verifier.Observe(context.Background(), git.Change{
		WorkspaceID: second.WorkspaceID,
		Path:        second.Dir,
		To:          secondKey,
		Reason:      reason,
	})

	rollup, ok := verifier.Rollup("one")
	if !ok {
		t.Fatal("the replacement agent is not being watched")
	}
	if rollup.Green {
		t.Fatal("the replacement workspace inherited green evidence from another workspace")
	}
	if rollup.Tests != core.TestUnknown {
		t.Errorf("the replacement workspace reports %q, want unknown until it runs its own test", rollup.Tests)
	}
}

func TestAgentsSharingAWorkspaceAreRefusedVerificationAttribution(t *testing.T) {
	dir := repository(t)
	repo, err := git.OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	verifier := New(repo, "main", []canopyexec.Test{
		{Name: "unit", Command: "exit 0", Required: true},
	}, nil)
	workspaceID := git.WorkspaceID(dir)
	subjects := []Subject{
		{Agent: "one", WorkspaceID: workspaceID, Dir: dir, Branch: "main"},
		{Agent: "two", WorkspaceID: workspaceID, Dir: dir, Branch: "main"},
	}
	verifier.Watch(subjects)

	key, reason := repo.Revision(context.Background(), dir)
	verifier.Observe(context.Background(), git.Change{
		WorkspaceID: workspaceID, Path: dir, To: key, Reason: reason,
	})

	for _, name := range []string{"one", "two"} {
		err := verifier.Verify(context.Background(), name)
		if err == nil || !strings.Contains(err.Error(), "shared") {
			t.Errorf("Verify(%q) = %v, want an explicit shared-workspace refusal", name, err)
		}
	}
	ranking := verifier.Rank()
	if len(ranking.Ranked) != 0 || len(ranking.Unranked) != 2 {
		t.Fatalf("shared agents were attributed a ranking: %+v", ranking)
	}
	for _, placement := range ranking.Unranked {
		if !strings.Contains(placement.Reason, "isolate") {
			t.Errorf("%s refusal does not say how to make attribution possible: %q",
				placement.Agent, placement.Reason)
		}
	}
}

// A rerun supersedes the previous run when it starts, not when it finishes. If an older slow pass
// can overwrite a newer run-in-progress, the screen flashes green while the current evidence is
// still unknown and may later fail.
func TestAnOlderRunCannotOverwriteANewerRunInProgress(t *testing.T) {
	verifier, _, subjects := harness(t, "one")
	subject := subjects["one"]
	start := time.Now()

	older := core.TestRun{
		ID: "run-1", WorkspaceID: subject.WorkspaceID, TestName: "unit",
		StartedAt: start, State: core.TestRunning, Revision: core.RevisionKey{HeadSHA: "same"},
	}
	newer := core.TestRun{
		ID: "run-2", WorkspaceID: subject.WorkspaceID, TestName: "unit",
		StartedAt: start.Add(time.Millisecond), State: core.TestRunning,
		Revision: core.RevisionKey{HeadSHA: "same"},
	}
	verifier.record(older)
	verifier.record(newer)

	finished := start.Add(2 * time.Millisecond)
	older.State = core.TestPassing
	older.FinishedAt = &finished
	verifier.record(older)

	snapshot, _ := verifier.Snapshot("one")
	if len(snapshot.Tests) != 1 || snapshot.Tests[0].Latest == nil {
		t.Fatalf("the current run is missing from the snapshot: %+v", snapshot.Tests)
	}
	if got := snapshot.Tests[0].Latest.ID; got != newer.ID {
		t.Fatalf("the older run %q replaced the newer run %q", got, newer.ID)
	}
	if got := snapshot.Tests[0].Latest.State; got != core.TestRunning {
		t.Errorf("the current run is shown as %q, want running", got)
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

// Diff size is the declared tiebreak. Treating an unmeasurable diff as zero would put the agent
// with missing evidence first and describe that as the smallest change. Refusal is the only honest
// result until the comparison can actually be made.
func TestAnUnmeasurableDiffIsRefusedRatherThanRankedAsEmpty(t *testing.T) {
	dir := repository(t)
	repo, err := git.OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	verifier := New(repo, "branch-that-does-not-exist", []canopyexec.Test{
		{Name: "unit", Command: "exit 0", Required: true},
	}, nil)

	workspace, err := repo.Create(context.Background(), "one", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	subject := Subject{
		Agent: "one", WorkspaceID: workspace.ID, Dir: workspace.Path, Branch: workspace.Branch,
	}
	verifier.Watch([]Subject{subject})
	key, reason := repo.Revision(context.Background(), subject.Dir)
	verifier.Observe(context.Background(), git.Change{
		WorkspaceID: subject.WorkspaceID, Path: subject.Dir, To: key, Reason: reason,
	})
	verified(t, verifier, "one")

	ranking := verifier.Rank()
	if len(ranking.Ranked) != 0 {
		t.Fatalf("an agent with no measurable diff was ranked as %+v", ranking.Ranked)
	}
	if len(ranking.Unranked) != 1 {
		t.Fatalf("%d unranked agents, want one", len(ranking.Unranked))
	}
	if !strings.Contains(ranking.Unranked[0].Reason, "diff could not be measured") {
		t.Errorf("the refusal does not name the missing evidence: %q", ranking.Unranked[0].Reason)
	}
	if queue := verifier.ReadyToReview(); len(queue) != 0 {
		t.Errorf("an agent with an unmeasurable diff entered the review queue: %+v", queue)
	}
}

// A trailing separator on either side of a path comparison must not make a workspace invisible.
//
// The two paths reach Observe by different routes: a subject's directory is configured, and a
// change's path comes back through the poller's watch list. If one of them gains a separator the
// match fails, no revision is ever recorded, and every agent stays unknown. That reads as a
// workspace where nothing has changed rather than as anything going wrong, which is the worst
// direction for this to fail in.
func TestATrailingSeparatorDoesNotHideAWorkspace(t *testing.T) {
	dir := repository(t)
	repo, err := git.OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	verifier := New(repo, "main", []canopyexec.Test{
		{Name: "unit", Command: "exit 0", Required: true},
	}, nil)

	workspaceID := git.WorkspaceID(dir)
	verifier.Watch([]Subject{
		{Agent: "one", WorkspaceID: workspaceID, Dir: dir + string(filepath.Separator), Branch: "main"},
	})

	key, reason := repo.Revision(context.Background(), dir)
	verifier.Observe(context.Background(), git.Change{
		WorkspaceID: workspaceID, Path: dir, To: key, Reason: reason,
	})

	snapshot, ok := verifier.Snapshot("one")
	if !ok {
		t.Fatal("the agent is not being watched")
	}
	if !snapshot.Revision.Known() {
		t.Errorf("the revision was never recorded, so the worktree reads as unchanged forever")
	}
}

// And the same path written two ways is still one workspace, so two agents in it are still sharing.
func TestSharingIsDetectedThroughAnUncleanPath(t *testing.T) {
	dir := repository(t)
	repo, err := git.OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	verifier := New(repo, "main", []canopyexec.Test{
		{Name: "unit", Command: "exit 0", Required: true},
	}, nil)

	// Different WorkspaceIDs deliberately, so the only thing that can catch this is the path.
	verifier.Watch([]Subject{
		{Agent: "one", WorkspaceID: "a", Dir: dir, Branch: "main"},
		{Agent: "two", WorkspaceID: "b", Dir: dir + string(filepath.Separator) + ".", Branch: "main"},
	})

	for _, name := range []string{"one", "two"} {
		if err := verifier.Verify(context.Background(), name); err == nil ||
			!strings.Contains(err.Error(), "shared") {
			t.Errorf("Verify(%q) = %v, want a shared-workspace refusal", name, err)
		}
	}
}

// Refusing to rank an agent and then offering it for review are two answers to one question.
//
// A queue entry is a claim that this agent's work is finished and verified, which is exactly the
// claim a shared workspace makes unattributable. Unreachable today, because evidence is cleared when
// a workspace becomes shared and none is recorded while it stays that way, so the roll-up is never
// green. Asserted anyway: it holds for a reason that lives in two other functions.
func TestTheReviewQueueExcludesSharedWorkspaces(t *testing.T) {
	dir := repository(t)
	repo, err := git.OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}
	verifier := New(repo, "main", []canopyexec.Test{
		{Name: "unit", Command: "exit 0", Required: true},
	}, nil)

	workspaceID := git.WorkspaceID(dir)
	verifier.Watch([]Subject{
		{Agent: "one", WorkspaceID: workspaceID, Dir: dir, Branch: "main"},
		{Agent: "two", WorkspaceID: workspaceID, Dir: dir, Branch: "main"},
	})

	key, reason := repo.Revision(context.Background(), dir)
	verifier.Observe(context.Background(), git.Change{
		WorkspaceID: workspaceID, Path: dir, To: key, Reason: reason,
	})

	if queue := verifier.ReadyToReview(); len(queue) != 0 {
		t.Errorf("a shared workspace was offered for review: %+v", queue)
	}
}
