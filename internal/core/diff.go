package core

import (
	"fmt"
	"strings"
)

// What an agent changed, expressed so that everything downstream can agree on it.
//
// These live here rather than in the git package for the same reason the roll-up does. The
// interface is allowed to depend on this package and on nothing else underneath it, which is what
// keeps it swappable between the real engine and the fake. A diff view that imported the git
// package directly would tie the screen to the one implementation that happens to shell out to git.

// DiffStat is the size of a change.
type DiffStat struct {
	FilesChanged int
	Insertions   int
	Deletions    int

	// Binary counts files whose change cannot be measured in lines. They are counted rather than
	// ignored, because a replaced 4 MB asset is a bigger change than a one line edit and reporting
	// it as zero would make the smallest looking diff the one nobody can review.
	Binary int
}

// Lines is the total churn, which is what ranking uses as a tiebreak.
func (s DiffStat) Lines() int { return s.Insertions + s.Deletions }

// Empty reports whether anything changed at all.
func (s DiffStat) Empty() bool { return s.FilesChanged == 0 && s.Binary == 0 }

// Summary is the one line form, in git's own idiom so it reads familiarly.
func (s DiffStat) Summary() string {
	if s.Empty() {
		return "no changes"
	}

	parts := []string{fmt.Sprintf("%d %s changed", s.FilesChanged, count(s.FilesChanged, "file", "files"))}
	if s.Insertions > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Insertions, count(s.Insertions, "insertion", "insertions")))
	}
	if s.Deletions > 0 {
		parts = append(parts, fmt.Sprintf("%d %s", s.Deletions, count(s.Deletions, "deletion", "deletions")))
	}
	if s.Binary > 0 {
		parts = append(parts, fmt.Sprintf("%d binary %s", s.Binary, count(s.Binary, "file", "files")))
	}
	return strings.Join(parts, ", ")
}

// FileChange is one file's worth of an agent's work.
type FileChange struct {
	Path string

	// Old is the previous path of a rename, and empty otherwise. Without it a review shows a delete
	// and an add and leaves the reader to notice they are the same file.
	Old string

	// Status is git's own letter: A added, M modified, D deleted, R renamed.
	Status byte

	Insertions int
	Deletions  int
	Binary     bool
}

func count(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// CommitDraft is a commit message with the subject left out.
//
// Split rather than handed over as one string, because the subject is the part a machine cannot
// write. A diff says which files changed and says nothing about why, so a generated subject like
// "update auth.go" is worse than none: plausible enough to commit by accident, and useless to
// whoever reads the history later. The type and the scope are readable from the files, so those are
// derived; the sentence is left for a person.
type CommitDraft struct {
	// Prefix is the conventional commit type and scope, such as "feat(chat)".
	Prefix string

	// Body lists what changed, one line per file.
	Body string
}

// Message assembles the final commit message. An empty subject gives an empty message, which the
// commit path refuses rather than silently committing a body with no summary.
func (d CommitDraft) Message(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return ""
	}
	if d.Prefix != "" {
		subject = d.Prefix + ": " + subject
	}
	if d.Body == "" {
		return subject + "\n"
	}
	return subject + "\n\n" + d.Body + "\n"
}

// Overlap is one file that more than one agent has changed.
type Overlap struct {
	Path string

	// Agents are the ones that touched it, in a stable order.
	Agents []string

	// Deleted names the agents that removed the file. A delete against an edit is the case most
	// likely to actually conflict, and the one a plain file list shows as an ordinary overlap.
	Deleted []string
}

// Contested reports whether at least one agent deleted the file and another did not.
func (o Overlap) Contested() bool {
	return len(o.Deleted) > 0 && len(o.Deleted) < len(o.Agents)
}
