//go:build darwin

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A writable directory that only shares a name's beginning with a denied one is not inside it, so
// it is not opened again as if it were.
func TestASiblingThatSharesAPrefixIsNotInsideTheDeniedDirectory(t *testing.T) {
	profile := Policy{Writable: []string{"/x/canopy-other"}, DenyRead: []string{"/x/canopy"},
		Reopen: []string{"/x/canopy"}}.profile()
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

// Only Canopy's own data directories are opened again for a workspace inside them. A workspace
// inside ~/.ssh stays as denied as the rest of ~/.ssh.
func TestOnlyCanopysDataDirectoryIsOpenedAgain(t *testing.T) {
	requireSandbox(t)
	home := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	t.Setenv("HOME", home)
	inSSH := filepath.Join(home, ".ssh", "project")
	if err := os.MkdirAll(inSSH, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519"), []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := ForWorkspace(inSSH)
	if out, _ := run(t, p, "cat "+filepath.Join(home, ".ssh", "id_ed25519")+"; echo x > "+filepath.Join(inSSH, "f")); strings.Contains(out, "PRIVATE") {
		t.Fatal("a workspace inside ~/.ssh opened ~/.ssh")
	}
	if _, err := os.Stat(filepath.Join(inSSH, "f")); err == nil {
		t.Fatal("a workspace inside ~/.ssh was written")
	}
	worktree := filepath.Join(home, "Library", "Application Support", "canopy", "worktrees", "repo")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, ForWorkspace(worktree), "cd '"+worktree+"' && echo ok > made && cat made"); err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("a worktree in Canopy's data directory is not usable: %v %s", err, out)
	}
}

// With the network off, a name does not resolve either: a lookup goes through mDNSResponder, which
// would otherwise ask the network for the command, and a name can carry data out.
func TestNoNameResolvesWithTheNetworkOff(t *testing.T) {
	requireSandbox(t)
	lookup := "dscacheutil -q host -a name example.com"
	if out, err := exec.Command("/bin/sh", "-c", lookup).Output(); err != nil || !strings.Contains(string(out), "address") {
		t.Skip("example.com does not resolve here even outside the sandbox")
	}
	p := Policy{Writable: []string{t.TempDir()}, Network: NetworkNone}
	if out, _ := run(t, p, lookup); strings.Contains(out, "address") {
		t.Fatalf("a name resolved with the network off:\n%s", out)
	}
}

// With the network off, a URL cannot be handed to another application, which would fetch it
// outside the sandbox: LaunchServices does not answer. The scheme belongs to no application, so
// nothing is opened either way; what matters is whether LaunchServices was reached.
func TestNoURLIsHandedOnWithTheNetworkOff(t *testing.T) {
	requireSandbox(t)
	probe := "open -g 'x-canopy-probe-nonexistent://probe' 2>&1"
	if out, _ := exec.Command("/bin/sh", "-c", probe).CombinedOutput(); !strings.Contains(string(out), "-10814") {
		t.Skip("LaunchServices does not answer as expected here even outside the sandbox")
	}
	p := Policy{Writable: []string{t.TempDir()}, Network: NetworkNone}
	if out, _ := run(t, p, probe); strings.Contains(out, "-10814") {
		t.Fatalf("LaunchServices answered a command with the network off:\n%s", out)
	}
}
