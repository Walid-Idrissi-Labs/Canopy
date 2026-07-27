package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func gitWorkspace(t *testing.T) (*Workspace, map[string]core.Tool) {
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

	w, err := OpenWorkspace(dir)
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}

	tools := map[string]core.Tool{}
	for _, tool := range GitTools(w) {
		tools[tool.Name()] = tool
	}
	return w, tools
}

// The whole cycle an agent needs: see what changed, stage it, commit it, confirm.
func TestAnAgentCanInspectAndCommitItsChanges(t *testing.T) {
	_, tools := gitWorkspace(t)

	status := call(t, tools["git_status"], map[string]any{})
	if status.IsError {
		t.Fatalf("status failed: %s", status.Content)
	}
	if !strings.Contains(status.Content, "main.go") {
		t.Errorf("status does not mention the new file:\n%s", status.Content)
	}

	if r := call(t, tools["git_add"], map[string]any{"path": "."}); r.IsError {
		t.Fatalf("add failed: %s", r.Content)
	}

	staged := call(t, tools["git_diff"], map[string]any{"staged": true})
	if staged.IsError {
		t.Fatalf("diff failed: %s", staged.Content)
	}
	if !strings.Contains(staged.Content, "package main") {
		t.Errorf("the staged diff is missing the change:\n%s", staged.Content)
	}

	if r := call(t, tools["git_commit"], map[string]any{"message": "add main"}); r.IsError {
		t.Fatalf("commit failed: %s", r.Content)
	}

	log := call(t, tools["git_log"], map[string]any{})
	if !strings.Contains(log.Content, "add main") {
		t.Errorf("the commit is not in the log:\n%s", log.Content)
	}
}

// Git is famously silent on success, and a model handed an empty string cannot tell that apart from
// a failure it did not notice.
func TestSilentSuccessSaysSomething(t *testing.T) {
	_, tools := gitWorkspace(t)

	if r := call(t, tools["git_add"], map[string]any{"path": "."}); r.IsError {
		t.Fatalf("add: %s", r.Content)
	}
	call(t, tools["git_commit"], map[string]any{"message": "first"})

	// Nothing has changed since, so the diff is empty.
	result := call(t, tools["git_diff"], map[string]any{})
	if result.IsError {
		t.Fatalf("diff: %s", result.Content)
	}
	if result.Content == "" {
		t.Error("an empty result cannot be told apart from a failure nobody noticed")
	}
}

// Git reads a leading dash as an option wherever it appears, so a path called -f becomes a flag.
func TestAPathThatLooksLikeAFlagIsRefused(t *testing.T) {
	_, tools := gitWorkspace(t)

	for _, tool := range []string{"git_add", "git_diff"} {
		result := call(t, tools[tool], map[string]any{"path": "--exec=rm -rf /"})
		if !result.IsError {
			t.Errorf("%s accepted a path that git would read as an option", tool)
		}
		if !strings.Contains(result.Content, "dash") {
			t.Errorf("%s: the refusal should say why, got %q", tool, result.Content)
		}
	}
}

// Git itself often refuses paths outside the worktree, but delegating the boundary to Git leaves
// behavior dependent on repository layout and submodules. Structured tools validate their own path
// arguments before starting the process.
func TestGitPathArgumentsCannotEscapeTheWorkspace(t *testing.T) {
	_, tools := gitWorkspace(t)

	for _, name := range []string{"git_add", "git_diff"} {
		result := call(t, tools[name], map[string]any{"path": "../outside.txt"})
		if !result.IsError || !result.Refused {
			t.Errorf("%s returned %+v, want a confinement refusal", name, result)
		}
	}
}

// Git's own error for a bad ref name is written for somebody who has read the ref format
// documentation, and a model reading it tries something adjacent rather than something correct.
func TestABadBranchNameIsRefusedWithAReadableReason(t *testing.T) {
	_, tools := gitWorkspace(t)

	for _, name := range []string{"-force", "has spaces", "two..dots", "trailing/", "star*"} {
		result := call(t, tools["git_branch"], map[string]any{"create": name})
		if !result.IsError {
			t.Errorf("%q was accepted as a branch name", name)
		}
	}
}

func TestCreatingABranch(t *testing.T) {
	_, tools := gitWorkspace(t)

	call(t, tools["git_add"], map[string]any{"path": "."})
	call(t, tools["git_commit"], map[string]any{"message": "first"})

	if r := call(t, tools["git_branch"], map[string]any{"create": "feature/thing"}); r.IsError {
		t.Fatalf("branch failed: %s", r.Content)
	}

	status := call(t, tools["git_status"], map[string]any{})
	if !strings.Contains(status.Content, "feature/thing") {
		t.Errorf("the new branch is not current:\n%s", status.Content)
	}
}

// A shell tool hands the permission model an opaque string. A named tool with fields is what makes
// the destructive operations separable from the ordinary ones.
func TestGitToolsAreClassifiedForThePermissionModel(t *testing.T) {
	w := testWorkspace(t)

	kinds := map[string]core.ToolKind{}
	for _, tool := range GitTools(w) {
		kinds[tool.Name()] = tool.Kind()

		if tool.Description() == "" {
			t.Errorf("%s has no description", tool.Name())
		}
		if !tool.Kind().Valid() {
			t.Errorf("%s has kind %q, which no permission rule covers", tool.Name(), tool.Kind())
		}
	}

	// Reading is a read. Staging, committing and branch creation are writes. The branch tool can
	// also list, but its maximum capability is creating and switching branches, so classifying the
	// mixed operation as read or ordinary git would let a read-only agent change Git state.
	for name, want := range map[string]core.ToolKind{
		"git_status": core.ToolRead,
		"git_diff":   core.ToolRead,
		"git_log":    core.ToolRead,
		"git_add":    core.ToolWrite,
		"git_commit": core.ToolWrite,
		"git_branch": core.ToolWrite,
	} {
		if kinds[name] != want {
			t.Errorf("%s is %q, want %q", name, kinds[name], want)
		}
	}
}

// Amending rewrites a commit that may already have been pushed, and -a stages files the model never
// looked at. Both are reachable through the shell tool, where they are visible and approved as what
// they are.
func TestCommitTakesNoDestructiveOptions(t *testing.T) {
	args, err := buildCommit([]byte(`{"message":"a message"}`))
	if err != nil {
		t.Fatalf("buildCommit: %v", err)
	}
	for _, arg := range args {
		if arg == "--amend" || arg == "-a" || arg == "--all" {
			t.Errorf("commit passes %q", arg)
		}
	}

	if _, err := buildCommit([]byte(`{"message":"   "}`)); err == nil {
		t.Error("an empty commit message should be refused")
	}
}

// Switching to an existing branch can discard uncommitted work, which belongs behind the permission
// model's destructive gate rather than inside a tool called "branch".
func TestBranchCanOnlyCreate(t *testing.T) {
	args, err := buildBranch([]byte(`{"create":"new-thing"}`))
	if err != nil {
		t.Fatalf("buildBranch: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-b") {
		t.Errorf("branch creation should use checkout -b, got %q", joined)
	}
}

func TestLogCountIsBounded(t *testing.T) {
	args, err := buildLog([]byte(`{"count":100000}`))
	if err != nil {
		t.Fatalf("buildLog: %v", err)
	}
	if strings.Contains(strings.Join(args, " "), "100000") {
		t.Error("an unbounded log would put a whole repository history into the context")
	}
}
