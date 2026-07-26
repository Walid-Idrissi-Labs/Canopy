// Package git discovers worktrees and computes revision identity by shelling out to git and
// parsing its porcelain output.
//
// Worktree plumbing is far more reliable through the real git binary than through a
// reimplementation, so this package parses "git worktree list --porcelain", "git diff --binary
// HEAD" and "git ls-files --others --exclude-standard -z" instead of reading the object store.
//
// In v0.1 this package is strictly read only. It never creates, removes, prunes, resets or cleans
// anything. The primary checkout is identified and protected, and every other worktree is
// observed as external-read-only.
//
// Filled in by P2-01 through P2-04.
package git
