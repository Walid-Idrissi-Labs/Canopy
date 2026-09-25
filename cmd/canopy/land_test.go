package main

import (
	"bytes"
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
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	config := `{"tests":[{"name":"has-ok","command":{"argv":["test","-f","ok.txt"]},"required":true}]}`
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
	return dir
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
	if err := runLand([]string{"good"}, strings.NewReader(""), &out); err == nil {
		t.Fatal("landed over uncommitted work")
	}
}
