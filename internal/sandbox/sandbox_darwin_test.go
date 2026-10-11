//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A writable directory that only shares a name's beginning with a denied one is not inside it, so
// it is not opened again as if it were.
func TestASiblingThatSharesAPrefixIsNotInsideTheDeniedDirectory(t *testing.T) {
	profile := Policy{Writable: []string{"/x/canopy-other"}, DenyRead: []string{"/x/canopy"}}.profile()
	if strings.Contains(profile, "(allow file-read*\n  (subpath \"/x/canopy-other\")") {
		t.Fatalf("a sibling was treated as inside the denied directory:\n%s", profile)
	}
}

// A credential location inside the workspace stays unwritable: canopy run in the home directory
// makes the home directory the workspace.
func TestACredentialInsideTheWorkspaceStaysUnwritable(t *testing.T) {
	requireSandbox(t)
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	t.Setenv("HOME", home)
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	p := ForWorkspace(home)
	_, _ = run(t, p, "echo planted > "+filepath.Join(ssh, "authorized_keys"))
	if _, err := os.Stat(filepath.Join(ssh, "authorized_keys")); err == nil {
		t.Fatal("a command in the home directory wrote ~/.ssh/authorized_keys")
	}
	if out, err := run(t, p, "echo ok > "+filepath.Join(home, "notes.txt")); err != nil {
		t.Fatalf("the rest of the workspace is not writable: %v %s", err, out)
	}
}
