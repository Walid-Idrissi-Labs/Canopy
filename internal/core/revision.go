package core

import "fmt"

// RevisionKey identifies the exact content of a worktree at a moment in time.
//
// HeadSHA is the current commit. DirtyDigest covers staged, unstaged and non-ignored untracked
// content, so that a result computed against uncommitted work is never confused with a result
// computed against the commit alone.
//
// The zero value means "not known". That is deliberate: a revision we failed to compute must
// never be mistaken for a revision that happens to be clean.
type RevisionKey struct {
	HeadSHA     string
	DirtyDigest string
}

// Known reports whether this key was actually computed.
//
// A key with no HeadSHA is unknown. Evidence bound to an unknown revision can never be shown as
// green, because there is nothing to compare it against.
func (r RevisionKey) Known() bool {
	return r.HeadSHA != ""
}

// Equal reports whether two revisions are the same code.
//
// Two unknown revisions are never equal, and an unknown revision is never equal to a known one.
// This is the single most important line in the package. If unknown compared equal to unknown,
// then a test result captured while the revision was uncomputable would keep looking current
// forever, which is exactly the false green the product exists to prevent. Prefer a spurious
// stale over a spurious pass.
func (r RevisionKey) Equal(other RevisionKey) bool {
	if !r.Known() || !other.Known() {
		return false
	}
	return r.HeadSHA == other.HeadSHA && r.DirtyDigest == other.DirtyDigest
}

// Clean reports whether the worktree had no staged, unstaged or untracked changes when this key
// was computed. An unknown revision is not clean.
func (r RevisionKey) Clean() bool {
	return r.Known() && r.DirtyDigest == ""
}

// Short returns an abbreviated form for display, marking dirty worktrees so a user never mistakes
// a dirty revision for the commit it sits on.
func (r RevisionKey) Short() string {
	if !r.Known() {
		return "unknown"
	}
	sha := r.HeadSHA
	if len(sha) > 7 {
		sha = sha[:7]
	}
	if r.DirtyDigest == "" {
		return sha
	}
	digest := r.DirtyDigest
	if len(digest) > 4 {
		digest = digest[:4]
	}
	return fmt.Sprintf("%s+%s", sha, digest)
}

func (r RevisionKey) String() string {
	if !r.Known() {
		return "revision:unknown"
	}
	return fmt.Sprintf("revision:%s/%s", r.HeadSHA, r.DirtyDigest)
}
