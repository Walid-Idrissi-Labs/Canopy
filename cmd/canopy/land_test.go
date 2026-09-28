package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// landRepo is a repository on main whose test requires ok.txt, with an agent branch that adds it
// and one that does not.
func landRepo(t *testing.T) string {
	t.Helper()
	return landRepoWith(t, `{"tests":[{"name":"has-ok","command":{"argv":["test","-f","ok.txt"]},"required":true}]}`)
}

// landRepoWith is landRepo with its own canopy.json.
func landRepoWith(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	landIn(t, dir, config)
	return dir
}

// landIn builds the landing repository in dir.
func landIn(t *testing.T, dir, config string) {
	t.Helper()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	// In the repository, not only on these commands: landing makes its own merge commit, and a CI
	// machine has no identity of its own for git to fall back on.
	git("config", "user.email", "a@b")
	git("config", "user.name", "a")
	if err := os.WriteFile(filepath.Join(dir, "canopy.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	git("switch", "-q", "-c", "good")
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "good")
	git("switch", "-q", "-c", "bad", "main")
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "bad")
	git("switch", "-q", "main")

	t.Setenv("CANOPY_TRUST_FILE", filepath.Join(t.TempDir(), "trust.json"))
	trustForTest(t, dir)
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
}

func head(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// A branch whose merged result passes lands; one whose result fails leaves main where it was.
func TestLandingMovesTheBranchOnlyWhenTheMergedResultPasses(t *testing.T) {
	dir := landRepo(t)
	start := head(t, dir)

	var out bytes.Buffer
	if err := runLand([]string{"bad"}, strings.NewReader(""), &out); err == nil {
		t.Fatalf("a failing result landed:\n%s", out.String())
	}
	if head(t, dir) != start {
		t.Fatal("main moved although the merged result failed its tests")
	}

	out.Reset()
	if err := runLand([]string{"good"}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("a passing result did not land: %v\n%s", err, out.String())
	}
	if head(t, dir) == start {
		t.Fatal("main did not move")
	}
	if _, err := os.Stat(filepath.Join(dir, "ok.txt")); err != nil {
		t.Fatal("the landed work is not in the checkout")
	}
	if branches, _ := exec.Command("git", "-C", dir, "branch", "--list", "good").Output(); !strings.Contains(string(branches), "good") {
		t.Fatal("landing deleted the agent's branch")
	}
}

// Uncommitted work in the checkout is never mixed into a landing.
func TestLandingRefusesADirtyCheckout(t *testing.T) {
	dir := landRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := runLand([]string{"good"}, strings.NewReader(""), &out)
	// Refused up front, before any merge is made or any test run.
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") || strings.Contains(out.String(), "merging") {
		t.Fatalf("landed over uncommitted work, or found out too late: %v\n%s", err, out.String())
	}
}

// The scratch worktree is prepared like an agent's: a project whose tests need its setup's output
// lands, rather than failing for want of it.
func TestLandingRunsTheProjectSetupFirst(t *testing.T) {
	landRepoWith(t, `{"setup":"mkdir -p deps && touch deps/lib",`+
		`"tests":[{"name":"deps","command":{"argv":["test","-f","deps/lib"]},"required":true}]}`)
	var out bytes.Buffer
	if err := runLand([]string{"good"}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("the setup did not run before the tests: %v\n%s", err, out.String())
	}
}

// A checkout that moves while the tests run is not overwritten with a result tested against what
// it was before.
func TestLandingRefusesACheckoutThatMovedDuringTheTests(t *testing.T) {
	dir := t.TempDir()
	commit := fmt.Sprintf("git -C %q -c user.name=a -c user.email=a@b commit -q --allow-empty -m moved", dir)
	config, _ := json.Marshal(map[string]any{"tests": []any{map[string]any{"name": "moves", "required": true,
		"command": map[string]any{"argv": []string{"sh", "-c", commit}}}}})
	landIn(t, dir, string(config))
	before := head(t, dir)
	var out bytes.Buffer
	err := runLand([]string{"good"}, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "changed while the tests ran") {
		t.Fatalf("landed over a checkout that moved: %v\n%s", err, out.String())
	}
	if moved := head(t, dir); moved == before {
		t.Fatal("the test command did not move the checkout, so this proves nothing")
	}
}

// A setup that fails stops the landing: tests run on an unprepared tree would decide nothing.
func TestAFailingSetupStopsTheLanding(t *testing.T) {
	dir := landRepoWith(t, `{"setup":"exit 7",`+
		`"tests":[{"name":"has-ok","command":{"argv":["test","-f","ok.txt"]},"required":true}]}`)
	start := head(t, dir)
	var out bytes.Buffer
	err := runLand([]string{"good"}, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "setup failed") {
		t.Fatalf("a failed setup did not stop the landing: %v\n%s", err, out.String())
	}
	if head(t, dir) != start {
		t.Fatal("main moved after a failed setup")
	}
}
