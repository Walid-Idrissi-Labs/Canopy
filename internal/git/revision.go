package git

// Computing the revision key, which is the load bearing measurement in the whole product.
//
// Everything Canopy claims about an agent reduces to one comparison: is the revision this evidence
// was gathered against still the revision in the worktree? So the only failure that really matters
// here is a digest that stays the same while the code changes, because that is a green result
// surviving an edit that broke it. Every choice below leans the other way. Content is hashed rather
// than summarised, anything that cannot be read makes the whole revision unknown rather than being
// skipped, and a state we do not recognise is treated as a change.
//
// The rules on symlinks, submodules, ignored files and the size limit are D-09, decided rather than
// invented here.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// DefaultHashLimit is the largest file Canopy will fingerprint, per D-09.
//
// D-09 fixes it at 25 MB and words it for untracked files, which is where an oversized file
// realistically comes from: a fixture, a downloaded archive, a database dump. The limit is applied
// to any content read from the worktree rather than untracked content alone, because a modified
// tracked binary of the same size poses the identical problem and the identical honest answer.
const DefaultHashLimit int64 = 25 << 20

// Revisions computes revision keys, remembering content hashes between calls.
//
// The cache exists because A6-02 polls this every couple of seconds across every worktree, and
// re-reading a 20 MB fixture at that rate would saturate a core on its own. A file whose size and
// modification time are both unchanged is taken to be unchanged, which is the same bet every build
// system makes. It is worth naming as a bet: two writes to one path, same length, within the same
// nanosecond, would be missed. Editors and agents write through a tool call at a time, and the
// poll interval is seconds, so the window does not realistically exist.
//
// The zero value is not usable. Use NewRevisions.
type Revisions struct {
	limit int64

	// outputLimit is passed to the throwaway repository handles this builds. Zero means the package
	// default, and it is set only by the test that proves a truncated status never becomes a key.
	outputLimit int

	mu     sync.Mutex
	cached map[string]cachedHash
}

type cachedHash struct {
	size    int64
	modTime time.Time
	digest  string
}

// NewRevisions returns a revision calculator. A limit of zero means DefaultHashLimit.
func NewRevisions(limit int64) *Revisions {
	if limit <= 0 {
		limit = DefaultHashLimit
	}
	return &Revisions{limit: limit, cached: make(map[string]cachedHash)}
}

// Key computes the revision of the worktree at path.
//
// The second return value is the reason the revision is unknown, and is empty whenever the key is
// known. Returning a reason rather than an error is the point: an unknown revision is a legitimate
// observation about the world, not a failure of the call, and WorkspaceSnapshot has a field for it
// precisely so the dashboard can say which file it could not read instead of shrugging.
func (v *Revisions) Key(ctx context.Context, path string) (core.RevisionKey, string) {
	worktree := &Repo{dir: path, outputLimit: v.outputLimit}

	head, err := worktree.run(ctx, "rev-parse", "HEAD")
	if err != nil {
		return core.RevisionKey{}, headFailure(ctx, err)
	}

	status, err := worktree.run(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		if errors.Is(err, ErrOutputTruncated) {
			// Unknown, deliberately, and this is the case worth being careful about. A truncated
			// status still parses and still digests, so the tempting behaviour is to carry on with
			// what arrived. That produces a key that does not move when a file in the dropped part
			// changes, which means a result recorded before the edit keeps reading as current. An
			// honest unknown is a nuisance; that is a lie.
			return core.RevisionKey{}, "this worktree reports more changes than Canopy can read " +
				"in one pass, so what it contains cannot be established. Usually a large untracked " +
				"directory that belongs in .gitignore"
		}
		return core.RevisionKey{}, fmt.Sprintf("the state of this worktree could not be read: %v", err)
	}
	entries := parseStatus(status)
	if len(entries) == 0 {
		// Clean. An empty digest rather than the hash of an empty string, so that a clean worktree at
		// a given commit compares equal to itself across restarts and across machines.
		return core.RevisionKey{HeadSHA: head}, ""
	}

	digest := sha256.New()

	// The staged side is taken from git rather than from disk, because the index holds content that
	// exists nowhere in the working tree: stage a file, edit it again, and the staged version is
	// only recoverable through the object it points at. The raw format carries both blob hashes and
	// the mode, so a chmod and a rename both move the digest.
	staged, err := worktree.run(ctx, "diff", "--cached", "--raw", "-z", "--abbrev=40")
	if err != nil {
		return core.RevisionKey{}, fmt.Sprintf("the staged changes here could not be read: %v", err)
	}
	_, _ = io.WriteString(digest, "staged\x00"+staged+"\x00")

	for _, entry := range entries {
		// The status codes go in as well as the content. They are what records a deletion, which has
		// no content to hash, and they distinguish "staged and then reverted in the tree" from an
		// ordinary modification.
		_, _ = fmt.Fprintf(digest, "entry\x00%c%c\x00%s\x00%s\x00", entry.X, entry.Y, entry.Path, entry.Orig)

		if reason := v.fingerprint(ctx, digest, path, entry.Path); reason != "" {
			return core.RevisionKey{}, reason
		}
	}

	return core.RevisionKey{
		HeadSHA:     head,
		DirtyDigest: hex.EncodeToString(digest.Sum(nil)[:8]),
	}, ""
}

// fingerprint writes one worktree path into the digest, returning a reason if it cannot.
//
// Every branch here writes something, including the ones that read no content. A branch that wrote
// nothing would make two different states hash the same, which is the one outcome this file exists
// to prevent.
func (v *Revisions) fingerprint(ctx context.Context, digest hash.Hash, root, name string) string {
	full := filepath.Join(root, name)

	info, err := os.Lstat(full)
	switch {
	case os.IsNotExist(err):
		// Deleted. The status code above already said so; this keeps the two readings consistent.
		_, _ = io.WriteString(digest, "absent\x00")
		return ""
	case err != nil:
		return fmt.Sprintf("%s could not be examined, so the revision here cannot be trusted: %v", name, err)
	}

	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		// Not followed, per D-09. The target string is the content of a symlink as far as the
		// revision is concerned, and following one is how a fingerprint walks out of the worktree or
		// into a loop.
		target, readErr := os.Readlink(full)
		if readErr != nil {
			return fmt.Sprintf("the symlink %s could not be read, so the revision here cannot be trusted: %v",
				name, readErr)
		}
		_, _ = io.WriteString(digest, "symlink\x00"+target+"\x00")
		return ""

	case mode.IsDir():
		// A directory inside the status output is a submodule. Its HEAD SHA contributes, and its
		// contents deliberately do not: recursing into submodules is out of scope for 0.1 and is
		// written down in LIMITATIONS.md rather than quietly handled.
		sub := &Repo{dir: full}
		if head, headErr := sub.run(ctx, "rev-parse", "HEAD"); headErr == nil {
			_, _ = io.WriteString(digest, "submodule\x00"+head+"\x00")
		} else {
			// An uninitialised submodule is an empty directory and a normal state. It has no content
			// to change, so a marker is honest rather than a hole.
			_, _ = io.WriteString(digest, "submodule\x00uninitialised\x00")
		}
		return ""

	case !mode.IsRegular():
		// A socket, device or fifo has no content worth hashing and reading one can block forever.
		// The type and name still go in, so one appearing or disappearing moves the digest.
		_, _ = fmt.Fprintf(digest, "special\x00%s\x00", mode.Type())
		return ""
	}

	if info.Size() > v.limit {
		return fmt.Sprintf(
			"%s is %s, over the %s Canopy will fingerprint, so it cannot tell whether this worktree changed",
			name, megabytes(info.Size()), megabytes(v.limit))
	}

	content, reason := v.content(full, info)
	if reason != "" {
		return reason
	}
	// The permission bits go in separately from the content, so chmod +x on an unchanged script is
	// a change. Only the executable bit is meaningful to git, but hashing the whole mode costs
	// nothing and avoids a second rule to remember.
	_, _ = fmt.Fprintf(digest, "file\x00%s\x00%04o\x00", content, mode.Perm())
	return ""
}

// content returns the hash of a regular file, reusing the last one if size and mtime are unchanged.
func (v *Revisions) content(full string, info os.FileInfo) (string, string) {
	v.mu.Lock()
	hit, ok := v.cached[full]
	v.mu.Unlock()
	if ok && hit.size == info.Size() && hit.modTime.Equal(info.ModTime()) {
		return hit.digest, ""
	}

	file, err := os.Open(full)
	if err != nil {
		return "", fmt.Sprintf("%s could not be read, so the revision here cannot be trusted: %v", full, err)
	}
	defer func() { _ = file.Close() }()

	sum := sha256.New()
	if _, err := io.Copy(sum, io.LimitReader(file, v.limit+1)); err != nil {
		return "", fmt.Sprintf("%s could not be read, so the revision here cannot be trusted: %v", full, err)
	}
	digest := hex.EncodeToString(sum.Sum(nil))

	v.mu.Lock()
	v.cached[full] = cachedHash{size: info.Size(), modTime: info.ModTime(), digest: digest}
	v.mu.Unlock()
	return digest, ""
}

// Forget drops cached hashes for a worktree that has gone away.
//
// Without it a long session that spawns and ends fifty agents keeps every file it ever hashed.
func (v *Revisions) Forget(root string) {
	prefix := root + string(os.PathSeparator)

	v.mu.Lock()
	defer v.mu.Unlock()
	for path := range v.cached {
		if strings.HasPrefix(path, prefix) {
			delete(v.cached, path)
		}
	}
}

// headFailure explains why HEAD could not be read.
//
// The three causes need different words and only one of them is ordinary. A branch with no commits
// is the state a fresh repository sits in and reads as unknown by design. A cancelled poll is
// Canopy shutting down and says so, because reporting it as an empty repository was a small lie
// that a cancelled poll produced every single time. Anything else carries git's own complaint,
// since guessing at it would be inventing a cause.
func headFailure(ctx context.Context, err error) string {
	if ctx.Err() != nil {
		return "reading this worktree was interrupted before its revision could be computed"
	}
	text := err.Error()
	if strings.Contains(text, "ambiguous argument") || strings.Contains(text, "unknown revision") {
		return "this branch has no commits yet, so there is no revision to compare against"
	}
	return fmt.Sprintf("the head of this worktree could not be read: %v", err)
}

func megabytes(n int64) string {
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// statusEntry is one line of git status --porcelain=v1.
type statusEntry struct {
	// X is the index status and Y the worktree status, in git's own terms.
	X, Y byte

	Path string

	// Orig is the source path of a rename or copy, and empty otherwise.
	Orig string
}

// Untracked reports whether git has never seen this path.
func (e statusEntry) Untracked() bool { return e.X == '?' && e.Y == '?' }

// parseStatus reads the NUL separated porcelain v1 format.
//
// The renames are the reason this is a function rather than a loop over strings.Split at each call
// site. With -z, a rename emits the new path and then the old path as two separate NUL terminated
// fields, so a naive split sees the old path as another entry and reads its first two characters as
// status codes. A rename of `old.go` counted as one staged and one unstaged change on top of the
// real one, which is exactly the kind of quiet wrongness that never gets noticed because the number
// is only ever slightly too big.
func parseStatus(out string) []statusEntry {
	fields := strings.Split(out, "\x00")

	var entries []statusEntry
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if len(field) < 4 {
			continue
		}

		entry := statusEntry{X: field[0], Y: field[1], Path: field[3:]}
		if entry.X == 'R' || entry.X == 'C' {
			if i+1 < len(fields) {
				entry.Orig = fields[i+1]
				i++
			}
		}
		entries = append(entries, entry)
	}
	return entries
}
