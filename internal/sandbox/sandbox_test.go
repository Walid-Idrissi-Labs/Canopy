package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == TrampolineArg {
		if err := RunTrampoline(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(126)
		}
		return
	}
	os.Exit(m.Run())
}

func run(t *testing.T, p Policy, script string) (string, error) {
	t.Helper()
	name, args, err := p.Wrap("/bin/sh", []string{"-c", script})
	if err != nil {
		t.Fatalf("wrapping: %v", err)
	}
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// Writes land only beneath the writable directories.
func TestWritesAreConfined(t *testing.T) {
	requireSandbox(t)
	allowed, other := t.TempDir(), t.TempDir()
	p := Policy{Writable: clean([]string{allowed, "/dev"}), Network: NetworkOpen}
	if out, err := run(t, p, "echo ok > "+filepath.Join(allowed, "a")); err != nil {
		t.Fatalf("a write inside the writable directory failed: %v %s", err, out)
	}
	_, _ = run(t, p, "echo no > "+filepath.Join(other, "b"))
	if _, err := os.Stat(filepath.Join(other, "b")); err == nil {
		t.Fatal("a sandboxed command wrote outside its writable directories")
	}
}

// The default policy writes the workspace and caches and denies credential paths.
func TestTheDefaultPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws := t.TempDir()
	p := ForWorkspace(ws)
	joined := strings.Join(p.Writable, "\n")
	resolved, _ := filepath.EvalSymlinks(ws)
	if !strings.Contains(joined, resolved) {
		t.Errorf("the workspace is not writable: %v", p.Writable)
	}
	resolvedHome, _ := filepath.EvalSymlinks(home)
	// Caches only: a toolchain directory on PATH, or one whose scripts every build runs, is a place
	// to leave a program for the user's next command.
	for _, planted := range []string{"go", "go/bin", ".cargo", ".cargo/bin", ".gradle", ".gradle/init.d", ".m2"} {
		for _, w := range p.Writable {
			if w == filepath.Join(resolvedHome, planted) || w == filepath.Join(home, planted) {
				t.Errorf("%s is writable", planted)
			}
		}
	}
	if !strings.Contains(strings.Join(p.DenyRead, "\n"), filepath.Join(resolvedHome, ".ssh")) &&
		!strings.Contains(strings.Join(p.DenyRead, "\n"), filepath.Join(home, ".ssh")) {
		t.Errorf("~/.ssh is readable: %v", p.DenyRead)
	}
}

// requireSandbox skips where there is no sandbox, except in CI, where CANOPY_REQUIRE_SANDBOX makes
// a missing sandbox a failure so the platform path is known to have run.
func requireSandbox(t *testing.T) {
	t.Helper()
	if err := Available(); err != nil {
		if os.Getenv("CANOPY_REQUIRE_SANDBOX") == "1" {
			t.Fatalf("CI requires a sandbox and there is none: %v", err)
		}
		t.Skip(err)
	}
}

// A repository nested in the workspace is as dangerous as the workspace's own: added as a gitlink,
// the user's git status runs a git inside it that obeys its config. None can be made, and an
// existing one's config and hooks cannot be written, while ordinary files stay writable.
func TestNestedRepositoriesCannotBeArmed(t *testing.T) {
	requireSandbox(t)
	if runtime.GOOS != "darwin" {
		t.Skip("carving paths out of a writable tree is enforced on macOS")
	}
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	for _, d := range []string{"old/.git/hooks", "old/.git/modules/m/hooks"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := Policy{Writable: []string{ws}, Network: NetworkOpen}.WithGitDirs(filepath.Join(ws, ".git"))
	for _, attempt := range []string{
		"mkdir new && mkdir new/.git",
		"echo 'gitdir: /tmp/x' > .git",
		"echo x > old/.git/config",
		"echo x > old/.git/config.worktree",
		"echo x > old/.git/hooks/pre-commit",
		"echo x > old/.git/modules/m/config",
		"mv old/.git old/moved",
	} {
		_, _ = run(t, p, "cd "+ws+" && "+attempt)
	}
	for _, armed := range []string{"new/.git", ".git", "old/.git/config", "old/.git/config.worktree",
		"old/.git/hooks/pre-commit", "old/.git/modules/m/config", "old/moved"} {
		if _, err := os.Lstat(filepath.Join(ws, armed)); err == nil {
			t.Errorf("%s was written from inside the sandbox", armed)
		}
	}
	if out, err := run(t, p, "cd "+ws+" && echo ok > notes.git && echo ok > old/.git/HEAD && mkdir -p a/b"); err != nil {
		t.Fatalf("an ordinary write was refused: %v %s", err, out)
	}
}

// Anchored to the workspace: a repository made in the temporary area, the way a git dependency is
// fetched, is left alone, while one inside the workspace, even under a path with spaces and
// brackets, is still refused.
func TestOnlyTheWorkspaceIsGuardedAgainstNestedRepositories(t *testing.T) {
	requireSandbox(t)
	if runtime.GOOS != "darwin" {
		t.Skip("carving paths out of a writable tree is enforced on macOS")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	ws := filepath.Join(base, "my project (v1.2)")
	elsewhere := filepath.Join(base, "cache")
	for _, d := range []string{ws, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := Policy{Workspace: ws, Writable: []string{ws, elsewhere}, Network: NetworkOpen}.
		WithGitDirs(filepath.Join(ws, ".git"))
	if out, err := run(t, p, "mkdir -p '"+elsewhere+"/dep/.git/hooks' && echo x > '"+elsewhere+"/dep/.git/config'"); err != nil {
		t.Fatalf("a repository outside the workspace was refused: %v %s", err, out)
	}
	_, _ = run(t, p, "mkdir -p '"+ws+"/sub/.git'")
	if _, err := os.Stat(filepath.Join(ws, "sub", ".git")); err == nil {
		t.Fatal("a nested repository was made inside the workspace")
	}
}
