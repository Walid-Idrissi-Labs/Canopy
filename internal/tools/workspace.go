// Package tools is what an agent can actually do.
//
// Everything here is confined to one directory, and the confinement is enforced in one place. A
// tool that resolved its own paths would be one tool away from a bug that lets an agent write
// outside its worktree, and that bug is not recoverable: by the time anyone notices, the file is
// already gone.
package tools

import (
	"context"
	"errors"
	"fmt"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/gitsafe"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// ErrOutsideWorkspace is returned for a path that resolves outside the agent's directory.
//
// Its own error because callers respond differently: a permission layer records it as a refused
// call rather than a failure, and the model is told it may not go there rather than that something
// broke.
var ErrOutsideWorkspace = errors.New("that path is outside this agent's workspace")

// ErrGitDirectory is returned for any path inside a .git directory.
//
// Git's own directory holds configuration that git executes: core.fsmonitor runs on every status,
// hooks run on commit. A file tool that could write there would turn an ordinary, auto-approved
// edit into a shell command that runs the next time anything calls git, with no prompt and no
// entry in the audit trail. Reading is refused too, since remote URLs in .git/config routinely
// carry tokens, and the structured git tools already answer every legitimate question.
var ErrGitDirectory = errors.New("the .git directory is managed by git, not by file tools")

// Workspace is a directory an agent may work inside, and nothing outside.
type Workspace struct {
	// root is the resolved, symlink free absolute path of the directory.
	//
	// Resolved once at construction rather than per call. If it were resolved per call, a symlink
	// swapped underneath between the check and the use would change what "inside" means, which is
	// the classic shape of this bug.
	root string

	policyOnce sync.Once
	policy     sandbox.Policy

	// diagnoser, when set, checks each file the edit and write tools change and its report is
	// added to their result.
	diagnoser Diagnoser
}

// Diagnoser checks a file just written and says what is wrong with it, or "" when it has nothing
// to add. A language server behind it is the usual case.
type Diagnoser interface {
	Check(ctx context.Context, path, content string) string
}

// SetDiagnoser has the edit and write tools report problems in what they wrote.
func (w *Workspace) SetDiagnoser(d Diagnoser) { w.diagnoser = d }

// diagnose is what the diagnoser says about a file, set off from the tool's own result.
func (w *Workspace) diagnose(ctx context.Context, path, content string) string {
	if w.diagnoser == nil {
		return ""
	}
	if report := w.diagnoser.Check(ctx, path, content); report != "" {
		return "\n\n" + report
	}
	return ""
}

// OpenWorkspace resolves a directory and returns a workspace confined to it.
func OpenWorkspace(dir string) (*Workspace, error) {
	if dir == "" {
		return nil, errors.New("a workspace needs a directory")
	}

	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}

	// EvalSymlinks on the root too. On macOS the temporary directory is itself a symlink, so a
	// workspace opened at /var/folders/... has a root that never matches any path resolved through
	// it, and every single call is refused. Found the tedious way.
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", dir, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}

	return &Workspace{root: resolved}, nil
}

// Root is the directory this workspace is confined to.
func (w *Workspace) Root() string { return w.root }

// Resolve turns a path from a tool call into an absolute path inside the workspace.
//
// This is the only function in the package that turns a model's string into a path on disk, and
// everything else goes through it. One place to get right, one place to test, one place to read
// when somebody asks how confinement works.
//
// The check is on the resolved path, not the written one. `../../etc/passwd` is the obvious attack
// and the easy one to catch; a symlink inside the workspace pointing at somewhere outside it looks
// entirely innocent until it is followed.
func (w *Workspace) Resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("a path is required")
	}

	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(w.root, candidate)
	}
	candidate = filepath.Clean(candidate)

	// A path that does not exist yet still has to be confined, which is the case for every file an
	// agent creates. Its parent is what gets resolved, since the parent is what a symlink could
	// redirect.
	resolved, err := resolveExisting(candidate)
	if err != nil {
		return "", err
	}

	if !within(w.root, resolved) {
		// The error deliberately does not echo the resolved path. Telling a caller where their
		// traversal actually landed is a description of the filesystem outside their workspace,
		// which is the thing they were not allowed to learn.
		return "", fmt.Errorf("%q: %w", path, ErrOutsideWorkspace)
	}
	if insideGitDir(w.root, resolved) || insideGitDir(w.root, candidate) {
		return "", fmt.Errorf("%q: %w", path, ErrGitDirectory)
	}
	return resolved, nil
}

// insideGitDir reports whether any component of path below root is named .git.
//
// Compared without regard to case, because the default macOS filesystem treats .GIT and .git as
// the same directory and git will read a config written through either spelling.
func insideGitDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	return false
}

// resolveExisting resolves symlinks on the longest existing prefix of a path.
//
// filepath.EvalSymlinks fails outright on a path that does not exist, which would make it useless
// for the create case. Walking up to the nearest existing ancestor and resolving that gives the
// same guarantee: whatever symlinks exist have been followed, and what does not exist yet cannot be
// a symlink to anywhere.
func resolveExisting(path string) (string, error) {
	remaining := ""
	current := path

	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			if remaining == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, remaining), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolving %s: %w", path, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Walked all the way to the root without finding anything that exists, which on a real
			// filesystem cannot happen, so this is a guard against looping rather than a case.
			return "", fmt.Errorf("resolving %s: no part of this path exists", path)
		}
		remaining = filepath.Join(filepath.Base(current), remaining)
		current = parent
	}
}

// within reports whether path is root or is inside it.
//
// String prefixes are not enough on their own: `/work/project-secrets` has `/work/project` as a
// prefix and is a different directory. The separator check is what makes it a path comparison
// rather than a text one.
func within(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// Relative renders a resolved path the way it should appear to the model and in the transcript.
//
// Relative to the workspace, because an absolute path leaks where on the machine the work is
// happening into every tool result and every transcript, which is noise at best and somebody's home
// directory name at worst.
func (w *Workspace) Relative(path string) string {
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return path
	}
	return rel
}

// SandboxPolicy is the confinement for commands run in this workspace, worked out once: the
// workspace, the git directory it shares with its repository when it is a worktree, temporary
// directories and toolchain caches, with the git directories themselves kept unwritable.
func (w *Workspace) SandboxPolicy() sandbox.Policy {
	w.policyOnce.Do(func() {
		gitPath := func(flag string) string {
			cmd := osexec.Command("git", "rev-parse", "--path-format=absolute", flag)
			cmd.Dir = w.Root()
			cmd.Env = gitsafe.InheritedFor(w.Root())
			out, err := cmd.Output()
			if err != nil {
				return ""
			}
			return strings.TrimSpace(string(out))
		}
		gitDir, common := gitPath("--git-dir"), gitPath("--git-common-dir")
		var extra []string
		if common != "" {
			extra = append(extra, common)
		}
		// The workspace's own .git is named whether or not it exists yet, so a repository the agent
		// creates is covered from its first command.
		w.policy = sandbox.ForWorkspace(w.Root(), extra...).
			WithGitDirs(gitDir, common, filepath.Join(w.Root(), ".git"))
	})
	return w.policy
}

// Confinement is the sandbox for commands run in dir other than the agent's own, the project's tests
// first: nil where there is no sandbox or it was switched off, so the caller runs them as before.
func Confinement(dir string) *sandbox.Policy {
	if sandbox.Disabled() || sandbox.Available() != nil {
		return nil
	}
	w, err := OpenWorkspace(dir)
	if err != nil {
		return nil
	}
	policy := w.SandboxPolicy()
	return &policy
}
