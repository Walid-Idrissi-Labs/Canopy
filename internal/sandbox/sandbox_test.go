package sandbox

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// A repository nested in the workspace is as dangerous as the workspace's own: added as a gitlink,
// the user's git status runs a git inside it that obeys its config. None can be made, and an
// existing one's config and hooks cannot be written, while ordinary files stay writable.
func TestNestedRepositoriesCannotBeArmed(t *testing.T) {
	requireSandbox(t)
	if runtime.GOOS != "darwin" {
		t.Skip("carving paths out of a writable tree is enforced on macOS")
	}
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	for _, d := range []string{"old/.git/hooks", "old/.git/modules/m/hooks"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := Policy{Writable: []string{ws}, Network: NetworkOpen}.WithGitDirs(filepath.Join(ws, ".git"))
	for _, attempt := range []string{
		"mkdir new && mkdir new/.git",
		"echo 'gitdir: /tmp/x' > .git",
		"echo x > old/.git/config",
		"echo x > old/.git/config.worktree",
		"echo x > old/.git/hooks/pre-commit",
		"echo x > old/.git/modules/m/config",
		"mv old/.git old/moved",
	} {
		_, _ = run(t, p, "cd "+ws+" && "+attempt)
	}
	for _, armed := range []string{"new/.git", ".git", "old/.git/config", "old/.git/config.worktree",
		"old/.git/hooks/pre-commit", "old/.git/modules/m/config", "old/moved"} {
		if _, err := os.Lstat(filepath.Join(ws, armed)); err == nil {
			t.Errorf("%s was written from inside the sandbox", armed)
		}
	}
	if out, err := run(t, p, "cd "+ws+" && echo ok > notes.git && echo ok > old/.git/HEAD && mkdir -p a/b"); err != nil {
		t.Fatalf("an ordinary write was refused: %v %s", err, out)
	}
}

// Anchored to the workspace: a repository made in the temporary area, the way a git dependency is
// fetched, is left alone, while one inside the workspace, even under a path with spaces and
// brackets, is still refused.
func TestOnlyTheWorkspaceIsGuardedAgainstNestedRepositories(t *testing.T) {
	requireSandbox(t)
	if runtime.GOOS != "darwin" {
		t.Skip("carving paths out of a writable tree is enforced on macOS")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	ws := filepath.Join(base, "my project (v1.2)")
	elsewhere := filepath.Join(base, "cache")
	for _, d := range []string{ws, elsewhere} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := Policy{Workspace: ws, Writable: []string{ws, elsewhere}, Network: NetworkOpen}.
		WithGitDirs(filepath.Join(ws, ".git"))
	if out, err := run(t, p, "mkdir -p '"+elsewhere+"/dep/.git/hooks' && echo x > '"+elsewhere+"/dep/.git/config'"); err != nil {
		t.Fatalf("a repository outside the workspace was refused: %v %s", err, out)
	}
	_, _ = run(t, p, "mkdir -p '"+ws+"/sub/.git'")
	if _, err := os.Stat(filepath.Join(ws, "sub", ".git")); err == nil {
		t.Fatal("a nested repository was made inside the workspace")
	}
}

// A worktree's shared git directory lies outside the workspace, and its submodules' and worktrees'
// config and hooks are obeyed all the same, so they stay unwritable when the rules are anchored.
func TestASharedGitDirectoryOutsideTheWorkspaceIsGuarded(t *testing.T) {
	requireSandbox(t)
	if runtime.GOOS != "darwin" {
		t.Skip("carving paths out of a writable tree is enforced on macOS")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	ws, common := filepath.Join(base, "ws"), filepath.Join(base, "repo.git")
	for _, d := range []string{ws, filepath.Join(common, "modules", "m"), filepath.Join(common, "worktrees", "w")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := Policy{Workspace: ws, Writable: []string{ws, common}, Network: NetworkOpen}.WithGitDirs(common)
	for _, target := range []string{"modules/m/config", "modules/m/hooks", "worktrees/w/config.worktree"} {
		_, _ = run(t, p, "echo x > '"+filepath.Join(common, target)+"' || mkdir '"+filepath.Join(common, target)+"'")
		if _, err := os.Stat(filepath.Join(common, target)); err == nil {
			t.Errorf("%s was written in the shared git directory", target)
		}
	}
	if out, err := run(t, p, "echo ok > '"+filepath.Join(common, "worktrees", "w", "HEAD")+"'"); err != nil {
		t.Fatalf("an ordinary write in the shared git directory was refused: %v %s", err, out)
	}
}

// In proxy mode a command can reach the loopback address, where the proxy and a test's own servers
// are, and nothing else. The outside attempt is a UDP datagram to a documentation address, which
// needs no answer: unconfined it is sent at once, confined the sandbox refuses the send.
func TestProxyModeReachesOnlyTheLoopback(t *testing.T) {
	requireSandbox(t)
	if runtime.GOOS != "darwin" {
		t.Skip("Landlock names ports, not addresses; covered on macOS")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 sends the probe")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("HTTP/1.0 200 OK\r\nContent-Length: 2\r\n\r\nok"))
			_ = conn.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	open := Policy{Writable: []string{ws}, Network: NetworkOpen}
	proxied := Policy{Writable: []string{ws}, Network: NetworkProxy, ProxyPort: port}
	probe := `python3 -c "import socket; socket.socket(socket.AF_INET, socket.SOCK_DGRAM).sendto(b'x', ('192.0.2.1', 9))"`
	if out, err := run(t, open, probe); err != nil {
		t.Skipf("a datagram cannot be sent even unconfined: %v %s", err, out)
	}
	if out, err := run(t, proxied, fmt.Sprintf("curl -s --noproxy '*' --max-time 3 http://127.0.0.1:%d/", port)); err != nil || out != "ok" {
		t.Fatalf("the loopback was not reachable in proxy mode: %v %q", err, out)
	}
	if out, err := run(t, proxied, probe); err == nil {
		t.Fatalf("a datagram left the machine in proxy mode: %s", out)
	}
}

// A workspace inside a directory the sandbox denies, as an agent's worktree sits inside Canopy's
// data directory, is still the command's to read and write; the rest of that directory stays
// closed, and a guard inside the workspace still holds.
func TestAWorkspaceInsideADeniedDirectoryIsStillUsable(t *testing.T) {
	requireSandbox(t)
	data := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(data); err == nil {
		data = resolved
	}
	workspace := filepath.Join(data, "worktrees", "repo")
	hooks := filepath.Join(workspace, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(data, "trust.json")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := Policy{Writable: []string{workspace, "/dev"}, DenyRead: []string{data}, DenyWrite: []string{data, hooks},
		Network: NetworkOpen}
	out, err := run(t, p, "cd "+workspace+" && pwd && ls >/dev/null && echo ok > made && cat made")
	if err != nil || !strings.Contains(out, "ok") {
		t.Fatalf("the workspace could not be used: %v\n%s", err, out)
	}
	_, _ = run(t, p, "echo armed > "+filepath.Join(hooks, "pre-commit"))
	if _, err := os.Stat(filepath.Join(hooks, "pre-commit")); err == nil {
		t.Fatal("a guard inside the workspace gave way when the workspace was allowed again")
	}
	if runtime.GOOS == "darwin" {
		if out, _ := run(t, p, "cat "+secret); strings.Contains(out, "SECRET") {
			t.Fatal("the rest of the denied directory became readable")
		}
		if out, _ := run(t, p, "ls "+data); strings.Contains(out, "trust.json") {
			t.Fatal("the denied directory can be listed on the way to the workspace")
		}
	}
}
