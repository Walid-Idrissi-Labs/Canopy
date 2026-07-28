package chat

// Selecting conversation text with the mouse.
//
// Mouse reporting is on so the wheel arrives as a wheel, and the price of that is the terminal's
// own drag-to-select no longer reaches the terminal. This is the feature that pays that price
// back: drag over the conversation, and what was dragged over is on the clipboard by the time the
// button comes up, with a small confirmation that fades on its own.
//
// Only the conversation. The selection begins on a transcript row or not at all, so the chrome —
// the box, the panels, the header and footer the frame draws around this screen — can never be
// selected. Selecting an interface is how screenshots of interfaces end up pasted into bug
// reports as text that looks like output; the conversation is the only text here that is content
// rather than furniture.
//
// Columns are counted in runes, which is one cell each for everything the transcript usually
// carries. A conversation full of double-width characters will select a column or two off; that
// is a real limit accepted knowingly, because cell-accurate width mapping costs a dependency on
// every render and the failure is an offset selection rather than a wrong feature.

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// point is a place in the transcript: a line index into the full transcript, and a column.
type point struct {
	line int
	col  int
}

// after reports whether p is later in reading order than q.
func (p point) after(q point) bool {
	return p.line > q.line || (p.line == q.line && p.col > q.col)
}

// selection is a drag over the conversation, from where the button went down to where it is now.
//
// It survives the release — the highlight stays until the next click — because the moment after
// letting go is exactly when somebody looks at what they copied.
type selection struct {
	active   bool
	dragging bool
	anchor   point
	head     point
}

// bounds is the selection in reading order, whichever way it was dragged.
func (s selection) bounds() (from, to point) {
	if s.anchor.after(s.head) {
		return s.head, s.anchor
	}
	return s.anchor, s.head
}

// window is the slice of the transcript on screen: the full lines, and the half open range shown.
//
// One function rather than the same clamping arithmetic in Body and in the mouse handlers, because
// the two have to agree exactly: a selection mapped through arithmetic one line different from the
// renderer's would highlight the row above the one under the pointer.
func (m Model) window() (lines []string, start, end int) {
	lines = m.transcript()
	end = len(lines) - m.scroll
	if end > len(lines) {
		end = len(lines)
	}
	if end < 1 {
		end = 1
	}
	start = end - m.transcriptHeight()
	if start < 0 {
		start = 0
	}
	return lines, start, end
}

// beginSelection starts a drag at a body coordinate, or clears the selection when the press is not
// on the conversation.
func (m *Model) beginSelection(x, y int) {
	m.sel = selection{}
	if m.blank() {
		return
	}
	_, start, end := m.window()
	if y < 0 || y >= end-start {
		// The press landed on the chrome below the conversation, which is not selectable.
		return
	}
	at := point{line: start + y, col: maxOf(x, 0)}
	m.sel = selection{active: true, dragging: true, anchor: at, head: at}
}

// extendSelection moves the live end of the drag, clamped to the conversation's rows.
func (m *Model) extendSelection(x, y int) {
	if !m.sel.dragging {
		return
	}
	_, start, end := m.window()
	line := start + y
	if line < start {
		line = start
	}
	if line >= end {
		line = end - 1
	}
	m.sel.head = point{line: line, col: maxOf(x, 0)}
}

// finishSelection ends the drag, copies what it covered, and says so.
func (m Model) finishSelection() (Model, tea.Cmd) {
	m.sel.dragging = false

	if m.sel.anchor == m.sel.head {
		// A click, not a drag. The inclusive span below would call this one character, and a click
		// that silently replaced the clipboard with a letter is worse than one that does nothing.
		m.sel = selection{}
		return m, nil
	}

	text := m.selectedText()
	if strings.TrimSpace(text) == "" {
		// A click, or a drag over nothing but air. No copy and no message, because "copied" over
		// an empty clipboard would be the interface lying about the one thing it just did.
		m.sel = selection{}
		return m, nil
	}

	if err := m.clip(text); err != nil {
		m.err = "could not copy: " + err.Error()
		return m, nil
	}

	// The confirmation, and the timer that takes it away. The generation is what stops an old
	// timer clearing a newer copy's message: each copy moves the count on, and a tick that arrives
	// carrying an older one is a timer for a message that is no longer there.
	m.copiedGeneration++
	m.copied = true
	generation := m.copiedGeneration
	return m, tea.Tick(copiedFor, func(time.Time) tea.Msg {
		return copiedClearMsg{generation: generation}
	})
}

// selectedText is the plain text under the selection, styling stripped and lines joined.
func (m Model) selectedText() string {
	if !m.sel.active {
		return ""
	}
	lines, _, _ := m.window()
	from, to := m.sel.bounds()

	var out []string
	for i := from.line; i <= to.line && i < len(lines); i++ {
		if i < 0 {
			continue
		}
		runes := []rune(ansi.Strip(lines[i]))
		lo, hi := 0, len(runes)
		if i == from.line && from.col < hi {
			lo = from.col
		}
		if i == from.line && from.col >= hi {
			lo = hi
		}
		if i == to.line && to.col+1 < hi {
			hi = to.col + 1
		}
		if lo > hi {
			lo = hi
		}
		out = append(out, strings.TrimRight(string(runes[lo:hi]), " "))
	}
	return strings.Join(out, "\n")
}

// highlighted draws one transcript row with the selected span reversed.
//
// The span loses its own colours while it is selected and takes the reversed text style instead,
// which is what every terminal's native selection does too: the wash is the signal, and it lifts
// the moment the selection is cleared because the underlying row was never changed.
func (m Model) highlighted(line string, absolute int) string {
	if !m.sel.active {
		return line
	}
	from, to := m.sel.bounds()
	if absolute < from.line || absolute > to.line {
		return line
	}

	t := theme.Current()
	runes := []rune(ansi.Strip(line))
	lo, hi := 0, len(runes)
	if absolute == from.line {
		lo = minOf(from.col, hi)
	}
	if absolute == to.line {
		hi = minOf(to.col+1, hi)
	}
	if lo >= hi {
		return line
	}
	return t.Body.Render(string(runes[:lo])) +
		t.Cursor.Render(string(runes[lo:hi])) +
		t.Body.Render(string(runes[hi:]))
}

// copiedFor is how long the confirmation stays up. Long enough to be seen, short enough that it is
// gone before anyone wonders how to dismiss it.
const copiedFor = 1500 * time.Millisecond

// copiedClearMsg takes the confirmation away.
type copiedClearMsg struct{ generation int }

func maxOf(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minOf(a, b int) int {
	if a < b {
		return a
	}
	return b
}
