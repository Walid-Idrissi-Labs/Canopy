package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tools"
)

// isolatingEngine builds an engine over a real repository, with real tools.
//
// Real tools rather than fakes, because the guarantee under test is that an isolated agent cannot
// reach outside its worktree, and that is a property of `tools.Workspace` refusing to resolve the
// path. A fake registry would prove only that the test agrees with itself.
func isolatingEngine(t *testing.T) (*Engine, string) {
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
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "first"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Resolved because on macOS the temporary directory is a symlink, and every path comparison in
	// here would otherwise be comparing two spellings of the same place.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}

	repo, err := git.OpenRepo(dir)
	if err != nil {
		t.Fatalf("OpenRepo: %v", err)
	}

	e := New(fixedResolver{client: &scriptedClient{name: "claude", events: reply("ok")}, id: anthropicID()})
	t.Cleanup(e.Close)

	if err := e.WithIsolation(Isolation{Repo: repo, Tools: fileToolsFor}); err != nil {
		t.Fatalf("WithIsolation: %v", err)
	}
	return e, dir
}

// fileToolsFor is the factory an isolated agent's registry is built with.
func fileToolsFor(dir string) (*core.ToolRegistry, error) {
	workspace, err := tools.OpenWorkspace(dir)
	if err != nil {
		return nil, err
	}
	registry := core.NewToolRegistry()
	for _, tool := range tools.FileTools(workspace) {
		if err := registry.Register(tool); err != nil {
			return nil, err
		}
	}
	if err := registry.Register(tools.ShellTool(workspace)); err != nil {
		return nil, err
	}
	return registry, nil
}

// writeThrough asks an agent's own tools to write a file, and reports what they said.
//
// Going through the registry rather than the filesystem is the point: this is exactly the path a
// model's tool call takes, so a refusal here is the refusal an agent would actually get.
func writeThrough(t *testing.T, e *Engine, sessionID, path, content string) error {
	t.Helper()

	e.mu.Lock()
	registry, _ := e.toolsForLocked(sessionID)
	e.mu.Unlock()

	if registry == nil {
		t.Fatal("that session has no tools at all")
	}
	tool, ok := registry.Get("write_file")
	if !ok {
		t.Fatal("there is no write_file tool")
	}

	input, err := json.Marshal(map[string]string{"path": path, "content": content})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Run(context.Background(), input)
	if err != nil {
		return err
	}
	if result.IsError {
		return errors.New(result.Content)
	}
	return nil
}

func TestAnIsolatedAgentGetsItsOwnWorktreeAndBranch(t *testing.T) {
	e, dir := isolatingEngine(t)

	agent, err := e.AddAgent(context.Background(), Agent{
		Name: "parser", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	if agent.Dir == dir || agent.Dir == "" {
		t.Fatalf("dir = %q, want a worktree of its own rather than the repository", agent.Dir)
	}
	if agent.Branch != "parser" {
		t.Errorf("branch = %q, want the agent's own name so git branch reads as a list of who did what",
			agent.Branch)
	}
	if agent.WorkspaceID == "" {
		t.Error("an isolated agent has no workspace ID, so nothing can refer to its worktree later")
	}
	if _, err := os.Stat(agent.Dir); err != nil {
		t.Errorf("the worktree is not on disk: %v", err)
	}
}

// The acceptance criterion, and the reason isolation is a tool registry rather than an instruction.
func TestAnIsolatedAgentCannotReachOutsideItsWorktree(t *testing.T) {
	e, dir := isolatingEngine(t)

	agent, err := e.AddAgent(context.Background(), Agent{
		Name: "parser", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	// Inside its own worktree: allowed, or the isolation would be useless rather than safe.
	if err := writeThrough(t, e, agent.SessionID, "mine.txt", "work\n"); err != nil {
		t.Fatalf("an isolated agent cannot write in its own worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(agent.Dir, "mine.txt")); err != nil {
		t.Errorf("the file did not land in the worktree: %v", err)
	}

	// The primary checkout, reached by traversal and by absolute path.
	for _, path := range []string{
		filepath.Join("..", filepath.Base(dir), "main.go"),
		filepath.Join(dir, "escaped.txt"),
	} {
		if err := writeThrough(t, e, agent.SessionID, path, "not allowed\n"); err == nil {
			t.Errorf("an isolated agent wrote to %q, which is outside its worktree", path)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); !os.IsNotExist(err) {
		t.Error("a file appeared in the primary checkout")
	}
}

// Ranking three agents on one task is meaningless if they are all editing the same tree, and it is
// worse than meaningless if one can edit another's.
func TestOneIsolatedAgentCannotReachAnother(t *testing.T) {
	e, _ := isolatingEngine(t)
	ctx := context.Background()

	first, err := e.AddAgent(ctx, Agent{
		Name: "first", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	})
	if err != nil {
		t.Fatalf("AddAgent(first): %v", err)
	}
	second, err := e.AddAgent(ctx, Agent{
		Name: "second", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	})
	if err != nil {
		t.Fatalf("AddAgent(second): %v", err)
	}

	if first.Dir == second.Dir {
		t.Fatal("two isolated agents share a worktree")
	}

	target := filepath.Join(second.Dir, "theirs.txt")
	if err := writeThrough(t, e, first.SessionID, target, "reaching over\n"); err == nil {
		t.Error("one isolated agent wrote into another's worktree")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Error("the file landed in the other agent's worktree")
	}
}

// The common case has to stay ordinary. An agent is not a branch.
func TestANonIsolatedAgentWorksInTheRepository(t *testing.T) {
	e, dir := isolatingEngine(t)

	// The engine's own tools, which is what a normal run attaches.
	registry, err := fileToolsFor(dir)
	if err != nil {
		t.Fatalf("building tools: %v", err)
	}
	e.WithTools(registry, core.TrustStandard, nil)

	agent, err := e.AddAgent(context.Background(), Agent{
		Name: "main", KeyName: "claude", Model: "claude-opus-5", Dir: dir,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	if agent.Isolated || agent.WorkspaceID != "" {
		t.Errorf("an agent that did not ask for isolation got it: %+v", agent)
	}
	if err := writeThrough(t, e, agent.SessionID, "notes.txt", "ordinary work\n"); err != nil {
		t.Fatalf("a normal agent cannot write in the repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Errorf("the file did not land in the repository: %v", err)
	}
}

// An abandoned experiment is sometimes the only copy of an idea.
func TestEndingAnAgentNeverRemovesADirtyWorktreeSilently(t *testing.T) {
	e, _ := isolatingEngine(t)
	ctx := context.Background()

	agent, err := e.AddAgent(ctx, Agent{
		Name: "parser", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}
	if err := writeThrough(t, e, agent.SessionID, "unfinished.txt", "half an idea\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	err = e.EndAgent(ctx, "parser", RemoveWorktree)
	if !errors.Is(err, git.ErrDirty) {
		t.Fatalf("EndAgent on a dirty worktree returned %v, want a refusal naming the uncommitted work", err)
	}
	if _, statErr := os.Stat(agent.Dir); statErr != nil {
		t.Error("the worktree was removed despite the refusal")
	}

	// And the agent is still registered, which is what leads back to the work. Forgetting it here
	// would leave a directory on disk with uncommitted changes and nothing referring to it.
	if _, ok := e.Agent("parser"); !ok {
		t.Fatal("the agent was forgotten even though its worktree could not be removed")
	}

	// Discard is the second, explicit answer. Only then does the work go.
	if err := e.EndAgent(ctx, "parser", DiscardWorktree); err != nil {
		t.Fatalf("EndAgent(discard): %v", err)
	}
	if _, statErr := os.Stat(agent.Dir); !os.IsNotExist(statErr) {
		t.Error("discard left the worktree behind")
	}
	if _, ok := e.Agent("parser"); ok {
		t.Error("the agent is still registered after being ended")
	}
}

func TestKeepingAWorktreeLeavesItAlone(t *testing.T) {
	e, _ := isolatingEngine(t)
	ctx := context.Background()

	agent, err := e.AddAgent(ctx, Agent{
		Name: "docs", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	if err := e.EndAgent(ctx, "docs", KeepWorktree); err != nil {
		t.Fatalf("EndAgent(keep): %v", err)
	}
	if _, err := os.Stat(agent.Dir); err != nil {
		t.Errorf("keep removed the worktree anyway: %v", err)
	}

	// The conversation survives the worker, as RemoveAgent already promises.
	if _, ok := e.Session(agent.SessionID); !ok {
		t.Error("ending an agent burned its transcript")
	}
}

// A clean worktree removes without ceremony, or keeping the tree tidy would be a chore.
func TestRemovingACleanWorktreeJustWorks(t *testing.T) {
	e, _ := isolatingEngine(t)
	ctx := context.Background()

	agent, err := e.AddAgent(ctx, Agent{
		Name: "docs", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}
	if err := e.EndAgent(ctx, "docs", RemoveWorktree); err != nil {
		t.Fatalf("EndAgent(remove) on a clean worktree: %v", err)
	}
	if _, err := os.Stat(agent.Dir); !os.IsNotExist(err) {
		t.Error("the clean worktree is still there")
	}
}

// A5 stored a trust level on every agent and nothing read it, so an agent configured as read only
// ran with whatever the engine was set to.
func TestAnAgentRunsAtItsOwnTrustLevel(t *testing.T) {
	e, dir := isolatingEngine(t)
	registry, err := fileToolsFor(dir)
	if err != nil {
		t.Fatalf("building tools: %v", err)
	}
	e.WithTools(registry, core.TrustStandard, nil)

	cautious, err := e.AddAgent(context.Background(), Agent{
		Name: "cautious", KeyName: "claude", Model: "claude-opus-5", Trust: core.TrustReadOnly,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}
	ordinary, err := e.AddAgent(context.Background(), Agent{
		Name: "ordinary", KeyName: "claude", Model: "claude-opus-5",
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	e.mu.Lock()
	_, cautiousTrust := e.toolsForLocked(cautious.SessionID)
	_, ordinaryTrust := e.toolsForLocked(ordinary.SessionID)
	e.mu.Unlock()

	if cautiousTrust != core.TrustReadOnly {
		t.Errorf("the read only agent runs at %q, so its configured trust is being ignored", cautiousTrust)
	}
	if ordinaryTrust != core.TrustStandard {
		t.Errorf("the agent with no trust of its own runs at %q, want the engine's", ordinaryTrust)
	}
}

// Asking for isolation from an engine that cannot provide it has to say so rather than quietly
// producing an agent that shares the repository with everybody else.
func TestIsolationIsRefusedWhenThereIsNoRepository(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	t.Cleanup(e.Close)

	_, err := e.AddAgent(context.Background(), Agent{
		Name: "parser", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	})
	if err == nil {
		t.Fatal("an isolated agent was created by an engine with nowhere to make a worktree")
	}
	if !strings.Contains(err.Error(), "worktree") {
		t.Errorf("error = %q, want it to name what was missing", err)
	}
	if _, ok := e.Agent("parser"); ok {
		t.Error("the failed agent was registered anyway")
	}
}

func TestWithIsolationNeedsBothHalves(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	t.Cleanup(e.Close)

	if err := e.WithIsolation(Isolation{Tools: fileToolsFor}); err == nil {
		t.Error("isolation was accepted with no repository")
	}
	if err := e.WithIsolation(Isolation{Repo: &git.Repo{}}); err == nil {
		t.Error("isolation was accepted with no way to build tools")
	}
}

func TestPreparingANonIsolatedAgentSaysWhyThereIsNothingToDo(t *testing.T) {
	e, dir := isolatingEngine(t)

	if _, err := e.AddAgent(context.Background(), Agent{
		Name: "main", KeyName: "claude", Model: "claude-opus-5", Dir: dir,
	}); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	_, err := e.PrepareAgent(context.Background(), "main")
	if err == nil {
		t.Fatal("preparing an agent with no worktree was allowed")
	}
	if !strings.Contains(err.Error(), "nothing to prepare") {
		t.Errorf("error = %q, want it to say there is nothing to prepare", err)
	}
}

func TestPreparingAnIsolatedAgentRunsInItsWorktree(t *testing.T) {
	e, dir := isolatingEngine(t)

	e.mu.Lock()
	e.isolation.Environment = git.Environment{Setup: "touch prepared-here"}
	e.mu.Unlock()

	agent, err := e.AddAgent(context.Background(), Agent{
		Name: "parser", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	result, err := e.PrepareAgent(context.Background(), "parser")
	if err != nil {
		t.Fatalf("PrepareAgent: %v", err)
	}
	if !result.OK() {
		t.Fatalf("setup failed: %s", result.Output)
	}
	if _, err := os.Stat(filepath.Join(agent.Dir, "prepared-here")); err != nil {
		t.Errorf("setup did not run in the agent's worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "prepared-here")); !os.IsNotExist(err) {
		t.Error("setup ran in the primary checkout")
	}
}

func TestAnUnknownDispositionIsRefused(t *testing.T) {
	e, _ := isolatingEngine(t)
	ctx := context.Background()

	if _, err := e.AddAgent(ctx, Agent{
		Name: "parser", KeyName: "claude", Model: "claude-opus-5", Isolated: true,
	}); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	if err := e.EndAgent(ctx, "parser", Disposition("delete everything")); err == nil {
		t.Fatal("an unknown disposition was accepted")
	}
	if _, ok := e.Agent("parser"); !ok {
		t.Error("the agent was ended anyway")
	}
}
