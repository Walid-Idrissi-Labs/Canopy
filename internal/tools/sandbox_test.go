package tools

import (
	"context"
	"encoding/json"
	"os"
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
	}
}
