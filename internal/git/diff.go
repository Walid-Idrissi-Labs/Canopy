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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// Stat is the size of a change.
type Stat struct {
	FilesChanged int
	Insertions   int
	Deletions    int

	// Binary counts files whose change cannot be measured in lines. They are counted rather than
	// ignored, because a replaced 4 MB asset is a bigger change than a one line edit and reporting
	// it as zero would make the smallest looking diff the one nobody can review.
	Binary int
}

// Lines is the total churn, which is what ranking uses as a tiebreak.
func (s Stat) Lines() int { return s.Insertions + s.Deletions }

// Empty reports whether anything changed at all.
func (s Stat) Empty() bool { return s.FilesChanged == 0 && s.Binary == 0 }

// Summary is the one line form, in git's own idiom so it reads familiarly.
func (s Stat) Summary() string {
	if s.Empty() {
		return "no changes"
	}
	parts := []string{fmt.Sprintf("%d %s changed", s.FilesChanged, plural(s.FilesChanged, "file", "files"))}
	if s.Insertions > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Insertions, plural(s.Insertions, "insertion", "insertions")))
	}
	if s.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Deletions, plural(s.Deletions, "deletion", "deletions")))
	}
	if s.Binary > 0 {
		parts = append(parts, fmt.Sprintf("%d binary %s", s.Binary, plural(s.Binary, "file", "files")))
	}
	return strings.Join(parts, ", ")
}

// FileChange is one file's worth of an agent's work.
type FileChange struct {
	Path string

	// Old is the previous path of a rename, and empty otherwise.
	Old string

	// Status is git's own letter: A added, M modified, D deleted, R renamed.
	Status byte

	Insertions int
	Deletions  int
	Binary     bool
}

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
func (r *Repo) Changes(ctx context.Context, dir, base string) ([]FileChange, error) {
	from, err := r.Base(ctx, dir, base)
	if err != nil {
		return nil, err
	}
	worktree := &Repo{dir: dir}

	// numstat and name-status are two calls because one format does not carry both. They are joined
	// on path below rather than trusted to arrive in the same order, since a rename changes what
	// "the path" is in each.
	numstat, err := worktree.run(ctx, "diff", "--numstat", "-z", from)
	if err != nil {
		return nil, fmt.Errorf("measuring the changes in %s: %w", dir, err)
	}
	nameStatus, err := worktree.run(ctx, "diff", "--name-status", "-z", from)
	if err != nil {
		return nil, fmt.Errorf("listing the changes in %s: %w", dir, err)
	}

	changes := parseNumstat(numstat)
	applyNameStatus(changes, nameStatus)

	// Untracked files are not in any diff, and an agent that has written a new file and not added it
	// has still written it. Left out, a fresh implementation in a new file would rank as no work at
	// all.
	untracked, err := worktree.run(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err == nil {
		for _, path := range strings.Split(untracked, "\x00") {
			if path == "" {
				continue
			}
			lines, binary := countLines(dir, path)
			changes = append(changes, FileChange{
				Path: path, Status: 'A', Insertions: lines, Binary: binary,
			})
		}
	}
	return changes, nil
}

// Diff returns the size of an agent's work.
func (r *Repo) Diff(ctx context.Context, dir, base string) (Stat, error) {
	changes, err := r.Changes(ctx, dir, base)
	if err != nil {
		return Stat{}, err
	}

	var stat Stat
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

	content, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil {
		return 0, false
	}

	head := content
	if len(head) > peek {
		head = head[:peek]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return 0, true
	}

	lines := bytes.Count(content, []byte{'\n'})
	if len(content) > 0 && !bytes.HasSuffix(content, []byte{'\n'}) {
		lines++
	}
	return lines, false
}

// parseNumstat reads the NUL separated numstat format into changes keyed by path.
func parseNumstat(out string) []FileChange {
	fields := strings.Split(out, "\x00")

	var changes []FileChange
	for i := 0; i < len(fields); i++ {
		line := fields[i]
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}

		change := FileChange{Path: parts[2], Status: 'M'}
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
func applyNameStatus(changes []FileChange, out string) {
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
