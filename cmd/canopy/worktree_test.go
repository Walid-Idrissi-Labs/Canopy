package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/git"
)

// gc reclaims clean agent worktrees, keeps any with uncommitted work, and keeps every branch.
func TestWorktreeGCKeepsWorkAndBranches(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "i")
	repo, err := gitpkg.OpenRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := repo.Create(context.Background(), "clean", "")
	if err != nil {
		t.Fatal(err)
	}
	busy, err := repo.Create(context.Background(), "busy", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(busy.Path, "work.txt"), []byte("unsaved"), 0o644); err != nil {
		t.Fatal(err)
	}

	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runWorktree([]string{"gc"}, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clean.Path); !os.IsNotExist(err) {
		t.Errorf("the clean worktree is still there:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(busy.Path, "work.txt")); err != nil {
		t.Errorf("a worktree with uncommitted work was removed:\n%s", out.String())
	}
	branches, _ := exec.Command("git", "-C", dir, "branch", "--list", "clean").Output()
	if !strings.Contains(string(branches), "clean") {
		t.Error("gc deleted the branch of the worktree it removed")
	}
}
