package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
