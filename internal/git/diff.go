package git

// What an agent actually changed.
//
// One question, asked three ways by three different features, so it is answered once here. Ranking
// needs a size (A6-05), review needs the patch (A7-01), and the conflict radar needs the file list
// (A7-03). All three mean the same thing by "an agent's changes": everything between the point its
// branch left the base and what is on disk right now, committed or not.
//
// Including uncommitted work is the part worth being deliberate about. An agent that has written a
// fix and not committed it has still done the work, and a review tool that showed nothing until it
// committed would be describing git's state rather than the agent's.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// Base returns the commit an agent's branch diverged from.
//
// The merge base rather than the base tip, so that work committed to the base after the agent
// started is not counted as something the agent did. Getting this wrong is how a branch that is two
// days behind main reports a two thousand line diff and loses every ranking on size.
func (r *Repo) Base(ctx context.Context, dir, base string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("a base to compare against is required")
	}
	worktree := &Repo{dir: dir}

	mergeBase, err := worktree.run(ctx, "merge-base", base, "HEAD")
	if err != nil {
		return "", fmt.Errorf("finding where this branch left %s: %w", base, err)
	}
	return mergeBase, nil
}

// Changes returns every file an agent has touched since it left the base, committed or not.
func (r *Repo) Changes(ctx context.Context, dir, base string) ([]core.FileChange, error) {
	from, err := r.Base(ctx, dir, base)
	if err != nil {
		return nil, err
	}
	worktree := &Repo{dir: dir}

	// numstat and name-status are two calls because one format does not carry both. They are joined
	// on path below rather than trusted to arrive in the same order, since a rename changes what
	// "the path" is in each.
	numstat, err := worktree.runRaw(ctx, "diff", "--numstat", "-z", from)
	if err != nil {
		return nil, fmt.Errorf("measuring the changes in %s: %w", dir, err)
	}
	nameStatus, err := worktree.runRaw(ctx, "diff", "--name-status", "-z", from)
	if err != nil {
		return nil, fmt.Errorf("listing the changes in %s: %w", dir, err)
	}

	changes := parseNumstat(numstat)
	applyNameStatus(changes, nameStatus)

	// Untracked files are not in any diff, and an agent that has written a new file and not added it
	// has still written it. Left out, a fresh implementation in a new file would rank as no work at
	// all.
	untracked, err := worktree.runRaw(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err == nil {
		for _, path := range strings.Split(untracked, "\x00") {
			if path == "" {
				continue
			}
			lines, binary := countLines(dir, path)
			changes = append(changes, core.FileChange{
				Path: path, Status: 'A', Insertions: lines, Binary: binary,
			})
		}
	}
	return changes, nil
}

// Diff returns the size of an agent's work.
func (r *Repo) Diff(ctx context.Context, dir, base string) (core.DiffStat, error) {
	changes, err := r.Changes(ctx, dir, base)
	if err != nil {
		return core.DiffStat{}, err
	}

	var stat core.DiffStat
	for _, change := range changes {
		stat.FilesChanged++
		if change.Binary {
			stat.Binary++
			continue
		}
		stat.Insertions += change.Insertions
		stat.Deletions += change.Deletions
	}
	return stat, nil
}

// Patch returns the diff for one file, in git's own format.
//
// Per file rather than whole, because a diff review that has to hold every hunk of a two thousand
// line change in memory to show the first one is the thing A7-01 exists to avoid.
func (r *Repo) Patch(ctx context.Context, dir, base, path string) (string, error) {
	from, err := r.Base(ctx, dir, base)
	if err != nil {
		return "", err
	}
	worktree := &Repo{dir: dir}

	// The -- separator matters: without it a path that happens to look like a revision is read as
	// one, and "main" as a filename is not a hypothetical in a repository full of branches.
	patch, err := worktree.run(ctx, "diff", from, "--", path)
	if err != nil {
		return "", fmt.Errorf("reading the changes to %s: %w", path, err)
	}
	if strings.TrimSpace(patch) == "" {
		// A file git has never seen has no diff against anything, so the ordinary form prints
		// nothing. --no-index compares it against an empty file and produces what an added file
		// would have looked like. It answers through its exit code the way check-ignore does, one
		// meaning differences exist, so its output cannot be read through run.
		result, runErr := exec.Run(ctx, "git",
			[]string{"diff", "--no-index", "--", os.DevNull, path},
			exec.Options{Dir: dir, Env: environ(), Timeout: 60 * time.Second})
		if runErr != nil || !result.Ran {
			return "", nil
		}
		return result.Output, nil
	}
	return patch, nil
}

// countLines measures an untracked file the way git would have, had it been added.
//
// The binary test is git's own: a NUL byte anywhere in the first block means the file is not text.
// Cheap, wrong occasionally, and wrong in the direction that reports a file as unreviewable rather
// than dumping a megabyte of bytes into a diff view.
func countLines(dir, path string) (int, bool) {
	const peek = 8000

	full := filepath.Join(dir, path)
	info, err := os.Lstat(full)
	if err != nil {
		return 0, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// Git records the link target as the content of a symlink. Following it here would let a
		// review-size measurement read an arbitrary file outside the worktree, disagreeing with the
		// revision key which deliberately hashes only this string.
		target, err := os.Readlink(full)
		if err != nil || target == "" {
			return 0, false
		}
		return 1 + strings.Count(strings.TrimSuffix(target, "\n"), "\n"), false
	}
	if !info.Mode().IsRegular() {
		return 0, true
	}

	file, err := os.Open(full)
	if err != nil {
		return 0, false
	}
	defer func() { _ = file.Close() }()

	buffer := make([]byte, 32*1024)
	lines, read, inspected := 0, 0, 0
	var last byte
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			if inspected < peek {
				end := n
				if end > peek-inspected {
					end = peek - inspected
				}
				if bytes.IndexByte(chunk[:end], 0) >= 0 {
					return 0, true
				}
				inspected += end
			}
			lines += bytes.Count(chunk, []byte{'\n'})
			read += n
			last = chunk[n-1]
		}
		if readErr != nil {
			if readErr != io.EOF {
				return 0, false
			}
			break
		}
	}

	if read > 0 && last != '\n' {
		lines++
	}
	return lines, false
}

// parseNumstat reads the NUL separated numstat format into changes keyed by path.
func parseNumstat(out string) []core.FileChange {
	fields := strings.Split(out, "\x00")

	var changes []core.FileChange
	for i := 0; i < len(fields); i++ {
		line := fields[i]
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}

		change := core.FileChange{Path: parts[2], Status: 'M'}
		if parts[0] == "-" && parts[1] == "-" {
			change.Binary = true
		} else {
			change.Insertions, _ = strconv.Atoi(parts[0])
			change.Deletions, _ = strconv.Atoi(parts[1])
		}

		// A rename in -z numstat leaves the path field empty and follows with the old and new paths
		// as their own fields. Same trap as the status parser, one format over.
		if change.Path == "" && i+2 < len(fields) {
			change.Old, change.Path = fields[i+1], fields[i+2]
			change.Status = 'R'
			i += 2
		}
		changes = append(changes, change)
	}
	return changes
}

// applyNameStatus fills in the status letters, which numstat does not carry.
func applyNameStatus(changes []core.FileChange, out string) {
	status := make(map[string]byte)

	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if field == "" {
			continue
		}
		letter := field[0]
		if (letter == 'R' || letter == 'C') && i+2 < len(fields) {
			status[fields[i+2]] = letter
			i += 2
			continue
		}
		if i+1 < len(fields) {
			status[fields[i+1]] = letter
			i++
		}
	}

	for i := range changes {
		if letter, ok := status[changes[i].Path]; ok {
			changes[i].Status = letter
		}
	}
}
