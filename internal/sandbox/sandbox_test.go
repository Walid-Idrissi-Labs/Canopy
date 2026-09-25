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
		t.Skipf("no sandbox here: %v", err)
	}
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// Writes land only beneath the writable directories.
func TestWritesAreConfined(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
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
	if !strings.Contains(strings.Join(p.DenyRead, "\n"), filepath.Join(resolvedHome, ".ssh")) &&
		!strings.Contains(strings.Join(p.DenyRead, "\n"), filepath.Join(home, ".ssh")) {
		t.Errorf("~/.ssh is readable: %v", p.DenyRead)
	}
}
