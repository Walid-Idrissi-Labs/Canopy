package chat

import (
	"strconv"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// Showing an edit as an edit, rather than as the news that one happened.
//
// An agent rewriting your files is the most consequential thing on this screen, and until this file
// existed the transcript said `edit  edit_file  internal/foo.go` and how long it took. That is the
// same sentence for a one character typo fix and for a rewrite that deleted a function, which means
// the person watching cannot do the one job watching is for. The diff is not decoration; it is the
// difference between supervising and being told afterwards.
//
// Computed here rather than asked of git, because the edit has not been committed and may never be:
// `edit_file` carries the old and the new text in the call itself, so the transcript already holds
// everything a diff needs and asking the filesystem would only answer a different question, namely
// what the file looks like now after every later edit.

// DiffOp is what happened to one line.
type DiffOp int

const (
	// DiffKeep is a line both sides have. Shown for context, never counted as a change.
	DiffKeep DiffOp = iota
	DiffAdd
	DiffRemove
)

// DiffLine is one line of a diff, with the side it came from.
type DiffLine struct {
	Op   DiffOp
	Text string
}

// diffLimit is the point past which lines are counted rather than aligned.
//
// The alignment below is quadratic in the number of lines, so a pathological pair of inputs, two
// thousand-line texts with nothing in common, is four million cells of work on a redraw that happens
// several times a second. Past this bound the diff degrades to "this many out, this many in", which
// is still true and still useful, rather than making the interface stutter to say it more precisely.
const diffLimit = 600

// Diff aligns two texts line by line.
//
// Longest common subsequence rather than anything cleverer. It is the algorithm whose output reads
// the way a person expects a diff to read, and at the sizes an edit_file call carries, a few dozen
// lines either side, the difference between this and Myers is invisible.
func Diff(before, after string) []DiffLine {
	old := splitLines(before)
	current := splitLines(after)

	// Common prefix and suffix are peeled off first. They are the bulk of a typical edit and the
	// part the alignment below is worst at paying for.
	var head, tail []DiffLine
	for len(old) > 0 && len(current) > 0 && old[0] == current[0] {
		head = append(head, DiffLine{Op: DiffKeep, Text: old[0]})
		old, current = old[1:], current[1:]
	}
	for len(old) > 0 && len(current) > 0 && old[len(old)-1] == current[len(current)-1] {
		tail = append([]DiffLine{{Op: DiffKeep, Text: old[len(old)-1]}}, tail...)
		old, current = old[:len(old)-1], current[:len(current)-1]
	}

	var middle []DiffLine
	if len(old)*len(current) > diffLimit*diffLimit {
		// Too big to align. Every remaining line on each side is reported as itself, which is what
		// a diff of two texts with nothing in common would have said anyway.
		for _, line := range old {
			middle = append(middle, DiffLine{Op: DiffRemove, Text: line})
		}
		for _, line := range current {
			middle = append(middle, DiffLine{Op: DiffAdd, Text: line})
		}
	} else {
		middle = align(old, current)
	}

	out := append(head, middle...)
	return append(out, tail...)
}

// align is the longest common subsequence table and the walk back through it.
func align(old, current []string) []DiffLine {
	n, m := len(old), len(current)
	if n == 0 && m == 0 {
		return nil
	}

	// table[i][j] is the length of the longest common subsequence of old[i:] and current[j:].
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if old[i] == current[j] {
				table[i][j] = table[i+1][j+1] + 1
				continue
			}
			table[i][j] = max(table[i+1][j], table[i][j+1])
		}
	}

	var out []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case old[i] == current[j]:
			out = append(out, DiffLine{Op: DiffKeep, Text: old[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			// A removal before an addition when the table is indifferent, so a replaced line reads
			// as the old one going out and the new one coming in, in that order, every time.
			out = append(out, DiffLine{Op: DiffRemove, Text: old[i]})
			i++
		default:
			out = append(out, DiffLine{Op: DiffAdd, Text: current[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{Op: DiffRemove, Text: old[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{Op: DiffAdd, Text: current[j]})
	}
	return out
}

// splitLines splits a text into lines without inventing a trailing empty one.
//
// A text ending in a newline and the same text without it are the same set of lines to a reader, and
// showing a phantom blank line as an addition is a change the agent did not make.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

// DiffCounts is how many lines came out and how many went in.
func DiffCounts(lines []DiffLine) (added, removed int) {
	for _, line := range lines {
		switch line.Op {
		case DiffAdd:
			added++
		case DiffRemove:
			removed++
		case DiffKeep:
		}
	}
	return added, removed
}

// contextLines is how much unchanged text to keep either side of a change.
//
// Three, the same as git's default, for the same reason: it is enough to recognise where in the file
// you are and little enough that the changed lines stay the thing your eye lands on.
const contextLines = 3

// trimToChanges drops the runs of unchanged lines that are too far from any change to help.
//
// An edit_file call carries whatever surrounding context the model needed to make its old text
// unique, which is frequently more than a reader needs. Without this, a one line change inside a
// thirty line block renders as thirty lines, twenty-nine of which are identical on both sides.
func trimToChanges(lines []DiffLine) []DiffLine {
	keep := make([]bool, len(lines))
	changed := false
	for i, line := range lines {
		if line.Op == DiffKeep {
			continue
		}
		changed = true
		for j := max(0, i-contextLines); j <= min(len(lines)-1, i+contextLines); j++ {
			keep[j] = true
		}
	}
	if !changed {
		return nil
	}

	var out []DiffLine
	gap := false
	for i, line := range lines {
		if keep[i] {
			if gap {
				// A marker rather than silence, so a reader can tell "nothing changed here" from
				// "nothing is here".
				out = append(out, DiffLine{Op: DiffKeep, Text: elisionMarker})
				gap = false
			}
			out = append(out, line)
			continue
		}
		gap = true
	}
	return out
}

// elisionMarker stands in for unchanged lines the diff left out. Recognised on the way back out in
// renderDiff, which draws it muted rather than as source.
const elisionMarker = "\x00elided"

// renderDiff draws an aligned diff at a width, capped unless the reader asked for all of it.
//
// The marker carries the meaning and the colour reinforces it, which is the same rule the review
// screen's diff follows and the reason this survives NO_COLOR: the plus and the minus are the first
// character of every line, not a shade of it.
func renderDiff(lines []DiffLine, lang string, width, limit int) []string {
	t := theme.Current()

	const indent = "      "
	body := width - len(indent) - 2
	if body < 8 {
		body = 8
	}

	var out []string
	shown := 0
	for _, line := range lines {
		if limit > 0 && shown >= limit {
			out = append(out, t.Muted.Render(indent+dim(len(lines)-shown)))
			break
		}
		shown++

		if line.Text == elisionMarker {
			out = append(out, t.Muted.Render(indent+" ⋮"))
			continue
		}
		textLine := terminalSafe(line.Text)

		marker, style := " ", t.Muted
		switch line.Op {
		case DiffAdd:
			marker, style = "+", t.Success
		case DiffRemove:
			marker, style = "-", t.Danger
		case DiffKeep:
		}

		// Wrapped, not truncated. A diff that silently drops the end of a long line is a diff that
		// can hide the change it exists to show.
		for j, fragment := range wrapLine(expandTabs(textLine), body) {
			prefix := style.Render(marker) + " "
			if j > 0 {
				prefix = "  "
			}
			// An unchanged line is context and is drawn as quietly as the rest of the call's
			// detail. A changed one is source, and gets the same lexer the review screen's diff
			// uses, so the same line reads the same in both places.
			text := Highlight(lang, fragment)
			if line.Op == DiffKeep {
				text = t.Muted.Render(fragment)
			}
			out = append(out, indent+prefix+text)
		}
	}
	return out
}

// dim is the sentence at the bottom of a capped diff.
func dim(remaining int) string {
	if remaining == 1 {
		return "1 more line, ctrl+o for all of it"
	}
	return plural(remaining, "more line", "more lines") + ", ctrl+o for all of it"
}

// languageFor reads a language from a path, which is what the highlighter wants.
//
// The extension without its dot, handed to the same normaliser the fence tags go through, so `.go`
// and a ```go fence reach the same lexer rather than two tables that drift.
func languageFor(path string) string {
	dot := strings.LastIndex(path, ".")
	if dot < 0 || dot == len(path)-1 {
		return ""
	}
	return path[dot+1:]
}

func plural(n int, one, many string) string {
	word := many
	if n == 1 {
		word = one
	}
	return strconv.Itoa(n) + " " + word
}
