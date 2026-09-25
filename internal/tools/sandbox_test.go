package tools

import (
	"context"
	"encoding/json"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
)

// A sandboxed shell command can write in its workspace, and on macOS cannot read where credentials
// live. Write confinement itself is tested in the sandbox package.
func TestShellCommandsAreConfined(t *testing.T) {
	if err := sandbox.Available(); err != nil {
		if os.Getenv("CANOPY_REQUIRE_SANDBOX") == "1" {
			t.Fatalf("CI requires a sandbox and there is none: %v", err)
		}
		t.Skipf("no sandbox here: %v", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_test"), []byte("PRIVATE-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := testWorkspace(t)
	// A repository already, as a workspace normally is, made outside the sandbox.
	if out, err := osexec.Command("git", "-C", w.Root(), "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	shell := ShellTool(w)
	run := func(command string) string {
		input, _ := json.Marshal(map[string]string{"command": command})
		result, err := shell.Run(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		return result.Content
	}

	run("echo inside > made-here.txt")
	if _, err := os.Stat(filepath.Join(w.Root(), "made-here.txt")); err != nil {
		t.Fatalf("a write inside the workspace was refused: %v", err)
	}
	if runtime.GOOS == "darwin" {
		if got := run("cat " + filepath.Join(home, ".ssh", "id_test")); strings.Contains(got, "PRIVATE-KEY") {
			t.Fatal("a sandboxed command read a private key")
		}
		// A command must not leave a program for the user's next ordinary command to run
		// unconfined: a git hook, or a binary in a toolchain directory on PATH.
		run("mkdir -p .git/hooks; echo evil > .git/hooks/pre-commit")
		if data, err := os.ReadFile(filepath.Join(w.Root(), ".git", "hooks", "pre-commit")); err == nil && strings.Contains(string(data), "evil") {
			t.Fatal("a sandboxed command wrote a git hook")
		}
		// Nor by moving the git directory out of the way and back.
		got := run("mv .git g && mkdir -p g/hooks && echo evil > g/hooks/pre-commit && mv g .git")
		if _, err := os.Stat(filepath.Join(w.Root(), ".git")); err != nil {
			t.Fatalf("the repository's .git was moved away: %v\n%s", err, got)
		}
		if data, err := os.ReadFile(filepath.Join(w.Root(), ".git", "hooks", "pre-commit")); err == nil && strings.Contains(string(data), "evil") {
			t.Fatal("a sandboxed command planted a hook by renaming .git")
		}
	}
}

// A command that ran without the sandbox says so in its result.
func TestAnUnsandboxedCommandSaysSo(t *testing.T) {
	t.Setenv(sandbox.DisableEnvVar, "off")
	input, _ := json.Marshal(map[string]string{"command": "echo hi"})
	result, err := ShellTool(testWorkspace(t)).Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "without a sandbox") {
		t.Fatalf("an unconfined command did not say so: %q", result.Content)
	}
}
