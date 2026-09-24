package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A repository's .git/config can name commands: core.fsmonitor runs on every status and hooks run
// on commit. Canopy computes a revision key every two seconds by calling git in that repository,
// so if those settings are honoured, a line written into .git/config is a program that runs with
// nobody having approved it. These tests plant both and require that nothing runs.
func TestTheRevisionKeyRunsNothingTheRepositoryConfigures(t *testing.T) {
	dir := repo(t)
	marker := filepath.Join(t.TempDir(), "fsmonitor-ran")
	script := filepath.Join(dir, "monitor.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "config", "core.fsmonitor", script)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config: %v %s", err, out)
	}

	if _, reason := NewRevisions(0).Key(context.Background(), dir); reason != "" {
		t.Fatalf("revision key: %s", reason)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's core.fsmonitor ran when Canopy computed a revision key; a line in .git/config is then code that runs every two seconds without approval")
	}
}

func TestACheckpointRunsNoRepositoryHook(t *testing.T) {
	dir := repo(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := filepath.Join(dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pre-commit", "post-commit", "commit-msg", "reference-transaction"} {
		body := []byte("#!/bin/sh\ntouch " + marker + "\n")
		if err := os.WriteFile(filepath.Join(hooks, name), body, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "changed.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taker := NewTaker(dir)
	if _, err := taker.Take(context.Background(), "t1", "turn"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repository hook ran during a checkpoint; Canopy's own bookkeeping must never execute repository code")
	}
}
