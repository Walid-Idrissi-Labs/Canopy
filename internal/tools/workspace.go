// Package tools is what an agent can actually do.
//
// Everything here is confined to one directory, and the confinement is enforced in one place. A
// tool that resolved its own paths would be one tool away from a bug that lets an agent write
// outside its worktree, and that bug is not recoverable: by the time anyone notices, the file is
// already gone.
package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideWorkspace is returned for a path that resolves outside the agent's directory.
//
// Its own error because callers respond differently: a permission layer records it as a refused
// call rather than a failure, and the model is told it may not go there rather than that something
// broke.
var ErrOutsideWorkspace = errors.New("that path is outside this agent's workspace")

// Workspace is a directory an agent may work inside, and nothing outside.
type Workspace struct {
	// root is the resolved, symlink free absolute path of the directory.
	//
	// Resolved once at construction rather than per call. If it were resolved per call, a symlink
	// swapped underneath between the check and the use would change what "inside" means, which is
	// the classic shape of this bug.
	root string
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
	return resolved, nil
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
