package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testWorkspace(t *testing.T) *Workspace {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "core"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w, err := OpenWorkspace(dir)
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	return w
}

func TestPathsInsideTheWorkspaceResolve(t *testing.T) {
	w := testWorkspace(t)

	for _, path := range []string{
		"main.go",
		"./main.go",
		"internal/core",
		"internal/../main.go",
		"newfile.go",                // does not exist yet, which is every file an agent creates
		"internal/core/new/deep.go", // nor does its parent
	} {
		resolved, err := w.Resolve(path)
		if err != nil {
			t.Errorf("Resolve(%q) refused a path inside the workspace: %v", path, err)
			continue
		}
		if !filepath.IsAbs(resolved) {
			t.Errorf("Resolve(%q) = %q, want an absolute path", path, resolved)
		}
	}
}

// The obvious attack, and the easy one to catch.
func TestTraversalIsRefused(t *testing.T) {
	w := testWorkspace(t)

	for _, path := range []string{
		"../outside.go",
		"../../etc/passwd",
		"internal/../../outside.go",
		"/etc/passwd",
		"/",
	} {
		_, err := w.Resolve(path)
		if err == nil {
			t.Errorf("Resolve(%q) allowed a path outside the workspace", path)
			continue
		}
		if !errors.Is(err, ErrOutsideWorkspace) {
			t.Errorf("Resolve(%q) failed with %v, want ErrOutsideWorkspace so a permission layer "+
				"can tell a refusal from a fault", path, err)
		}
	}
}

// The one that looks entirely innocent until it is followed. A symlink inside the workspace
// pointing outside it passes every string check there is.
func TestASymlinkOutOfTheWorkspaceIsRefused(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("not for the agent"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w := testWorkspace(t)

	link := filepath.Join(w.Root(), "innocent.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	if _, err := w.Resolve("innocent.txt"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Errorf("a symlink pointing out of the workspace was followed: %v", err)
	}

	// And a linked directory, which is the version that lets an agent write rather than only read.
	dirLink := filepath.Join(w.Root(), "elsewhere")
	if err := os.Symlink(outside, dirLink); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	if _, err := w.Resolve("elsewhere/newfile.txt"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Errorf("a file could be created through a symlinked directory: %v", err)
	}
}

// A symlink that stays inside is legitimate and refusing it would break real repositories.
func TestASymlinkInsideTheWorkspaceIsAllowed(t *testing.T) {
	w := testWorkspace(t)

	link := filepath.Join(w.Root(), "alias.go")
	if err := os.Symlink(filepath.Join(w.Root(), "main.go"), link); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	resolved, err := w.Resolve("alias.go")
	if err != nil {
		t.Fatalf("a symlink within the workspace was refused: %v", err)
	}
	if filepath.Base(resolved) != "main.go" {
		t.Errorf("resolved to %q, want the symlink followed to its target", resolved)
	}
}

// `/work/project-secrets` has `/work/project` as a string prefix and is a different directory.
func TestASiblingDirectoryWithASharedPrefixIsRefused(t *testing.T) {
	parent := t.TempDir()
	inside := filepath.Join(parent, "project")
	sibling := filepath.Join(parent, "project-secrets")
	for _, dir := range []string{inside, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(sibling, "keys.txt"), []byte("no"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	w, err := OpenWorkspace(inside)
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}

	if _, err := w.Resolve("../project-secrets/keys.txt"); !errors.Is(err, ErrOutsideWorkspace) {
		t.Errorf("a sibling directory sharing a name prefix was treated as inside: %v", err)
	}
}

// Telling a caller where their path actually landed is a description of the filesystem outside
// their workspace, which is the thing they were not allowed to learn.
//
// Echoing back what they asked for is fine, and useful: they supplied it. The leak would be
// disclosing what it resolved to, which is only visible in the symlink case, where the two differ.
func TestARefusalDoesNotDescribeWhereItResolvedTo(t *testing.T) {
	outside := t.TempDir()
	target := filepath.Join(outside, "secret-directory-name")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	w := testWorkspace(t)
	if err := os.Symlink(target, filepath.Join(w.Root(), "innocent")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	_, err := w.Resolve("innocent/file.txt")
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), "secret-directory-name") {
		t.Errorf("the refusal discloses where the symlink pointed: %q", err)
	}
	// And it still names what was asked for, or the model has nothing to correct.
	if !strings.Contains(err.Error(), "innocent") {
		t.Errorf("the refusal should name the path that was asked for: %q", err)
	}
}

func TestTheWorkspaceRootItselfResolves(t *testing.T) {
	w := testWorkspace(t)

	resolved, err := w.Resolve(".")
	if err != nil {
		t.Fatalf("the workspace root should resolve: %v", err)
	}
	if resolved != w.Root() {
		t.Errorf("resolved %q, want the root %q", resolved, w.Root())
	}
}

// An absolute path in every tool result and transcript is noise at best and somebody's home
// directory name at worst.
func TestPathsAreShownRelativeToTheWorkspace(t *testing.T) {
	w := testWorkspace(t)

	resolved, err := w.Resolve("internal/core")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := w.Relative(resolved); got != filepath.Join("internal", "core") {
		t.Errorf("Relative = %q, want a path relative to the workspace", got)
	}
}

func TestOpeningSomethingThatIsNotADirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := OpenWorkspace(file); err == nil {
		t.Error("a workspace rooted at a file makes no sense and should be refused")
	}
	if _, err := OpenWorkspace(filepath.Join(dir, "nope")); err == nil {
		t.Error("a workspace rooted at nothing should be refused")
	}
	if _, err := OpenWorkspace(""); err == nil {
		t.Error("an empty directory should be refused")
	}
}

func TestAnEmptyPathIsRefused(t *testing.T) {
	w := testWorkspace(t)
	if _, err := w.Resolve(""); err == nil {
		t.Error("an empty path should be refused rather than resolving to the root")
	}
}
