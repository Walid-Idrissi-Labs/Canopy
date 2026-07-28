package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// A report is pasted into a pull request and read by somebody who cannot see the screen it came
// from, usually quickly and usually trusting it. The acceptance criterion is that it never claims a
// verification state the evidence does not support, and a worktree that moves while the suite is
// running is the case that criterion did not survive.
//
// Against a real repository and a real shell command, because the failure lives in the gap between
// when the checks start and when they finish. A fake runner returns instantly and there is no gap.

func reportRepo(t *testing.T, test string) string {
	t.Helper()

	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main", ".")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")

	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatalf("writing the file: %v", err)
	}
	// The shell form, opted into explicitly, because these checks are shell scripts with markers and
	// sleeps in them rather than a program with arguments.
	config, err := json.Marshal(map[string]any{
		"tests": []map[string]any{{
			"name":     "slow",
			"command":  map[string]any{"shell": test, "allow_shell": true},
			"required": true,
		}},
	})
	if err != nil {
		t.Fatalf("encoding the config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "canopy.json"), config, 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")

	// History goes to a temporary file, or the test would read and write the real one on this
	// machine. The report only reads it for cost, and a missing one is a normal outcome there.
	t.Setenv(session.PathEnvVar, filepath.Join(t.TempDir(), "history.db"))
	return dir
}

// The whole failure in one test. The worktree is edited while the suite is running, so the results
// describe a revision the worktree has moved on from.
//
// Both halves of the answer were wrong and wrong in the same flattering direction: the runs were
// still attributed to the revision they started against, so nothing looked stale and the verdict
// read "Verified", while the diff was the one measured before the tests began and said nothing had
// changed. A reader was told the work was finished and verified, and that there was no work.
func TestAReportDoesNotClaimAVerdictForAWorktreeThatMoved(t *testing.T) {
	started := filepath.Join(t.TempDir(), "check-started")
	t.Setenv("CANOPY_REPORT_TEST_STARTED", started)
	dir := reportRepo(t, `touch "$CANOPY_REPORT_TEST_STARTED"; sleep 3`)
	t.Chdir(dir)

	edited := make(chan error, 1)
	go func() {
		// The runner captures the revision before it starts the command. Wait for the command's
		// marker rather than guessing that startup takes less than 750 ms: under a loaded full suite
		// the old delay could expire first, making the edit part of the revision the test correctly
		// verified and failing this test for the wrong reason.
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(started); err == nil {
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				edited <- fmt.Errorf("reading the check marker: %w", err)
				return
			}
			if time.Now().After(deadline) {
				edited <- errors.New("the check command never wrote its start marker")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}

		f, err := os.OpenFile(filepath.Join(dir, "file.txt"), os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			edited <- err
			return
		}
		if _, err := f.WriteString("edited while the checks were running\n"); err != nil {
			_ = f.Close()
			edited <- err
			return
		}
		edited <- f.Close()
	}()

	var out bytes.Buffer
	if err := runReport(context.Background(), &out); err != nil {
		t.Fatalf("runReport: %v", err)
	}
	if err := <-edited; err != nil {
		t.Fatalf("editing while the checks ran: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "**Verified.**") {
		t.Errorf("a worktree that moved during the run was reported as verified:\n%s", got)
	}
	if strings.Contains(got, "No files changed") {
		t.Errorf("an edit made during the run was reported as no change at all:\n%s", got)
	}
	// And it says which of the two revisions it is talking about, or the reader cannot act on it.
	if !strings.Contains(got, "stale") {
		t.Errorf("the report does not say the evidence went stale:\n%s", got)
	}
}

// The ordinary case still has to work, or the fix above would have been bought by making every
// report say it could not be trusted.
func TestAReportOfAStillWorktreeIsVerified(t *testing.T) {
	dir := reportRepo(t, "true")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runReport(context.Background(), &out); err != nil {
		t.Fatalf("runReport: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "**Verified.**") {
		t.Errorf("a passing suite over an unchanged worktree was not reported as verified:\n%s", got)
	}
}
