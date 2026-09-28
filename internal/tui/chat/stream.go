package chat

import (
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// The turn being streamed is the one part of the transcript that changes on every event, so it
// cannot be cached whole the way a finished turn is. Rendering it from scratch on every event made a
// long reply cost the square of its length: 47 milliseconds a frame at 50 KB, a minute of CPU for
// the whole reply. Markdown blocks separated by a blank line outside a code fence render
// independently, so everything before the last such boundary is rendered once, appended to as the
// boundary moves, and only the open block at the end is rendered again.

type streamRender struct {
	width  int
	stable int    // bytes of text covered by lines
	prefix string // that text itself, to notice text that was replaced rather than grown
	lines  []string
}

var streaming = struct {
	sync.Mutex
	byTurn map[string]*streamRender
}{byTurn: map[string]*streamRender{}}

// streamingMarkdown renders text the way RenderMarkdown does, reusing work from earlier calls for
// the same key while the text only grows.
func streamingMarkdown(key, text string, width int) []string {
	streaming.Lock()
	state := streaming.byTurn[key]
	if state != nil && (state.width != width || state.stable > len(text) || text[:state.stable] != state.prefix) {
		state = nil
	}
	from := 0
	if state != nil {
		from = state.stable
	}
	boundary := from + stableBoundary(text[from:])

	if state == nil || state.stable > boundary {
		state = &streamRender{width: width}
		if len(streaming.byTurn) > 64 {
			streaming.byTurn = map[string]*streamRender{}
		}
		streaming.byTurn[key] = state
	}
	if boundary > state.stable {
		// Up to the blank line's own newline, not past it: a chunk that ended in a newline would
		// split into one more empty line than the whole text does at that point.
		state.lines = append(state.lines, RenderMarkdown(text[state.stable:boundary-1], width)...)
		state.stable = boundary
		state.prefix = text[:boundary]
	}
	head := state.lines
	streaming.Unlock()

	return append(append([]string(nil), head...), RenderMarkdown(text[boundary:], width)...)
}

// A streaming render keeps the lines it has drawn, in the colours of the moment, so a theme or
// background change drops them as it drops finished turns.
func init() { theme.OnChange(forgetAllStreaming) }

func forgetAllStreaming() {
	streaming.Lock()
	streaming.byTurn = map[string]*streamRender{}
	streaming.Unlock()
}

// forgetStreaming drops the state for a turn once it has finished and is cached whole.
func forgetStreaming(key string) {
	streaming.Lock()
	delete(streaming.byTurn, key)
	streaming.Unlock()
}

// stableBoundary is the offset just past the last blank line at which the renderer is between
// blocks and later text cannot reach back: everything before it renders the same whatever follows.
//
// It walks the text with the renderer's own block rules, so a fence is a fence exactly when
// RenderMarkdown thinks so. A blank line counts only when the line after it starts at the margin,
// because an indented line would continue a list item across the blank.
func stableBoundary(text string) int {
	lines := strings.Split(text, "\n")
	// The last element is the unfinished line (or "" after a final newline); blocks are only
	// judged on complete lines.
	complete := lines[:len(lines)-1]
	offsets := make([]int, len(lines))
	for i := 1; i < len(lines); i++ {
		offsets[i] = offsets[i-1] + len(lines[i-1]) + 1
	}

	boundary := 0
	i := 0
	for i < len(complete) {
		line := complete[i]
		var consumed int
		switch {
		case isFence(line):
			_, _, consumed = extractFence(complete[i:])
			if i+consumed >= len(complete) && !closedFence(complete[i:]) {
				// An open fence runs to the end of the text so far; nothing after it is stable.
				return boundary
			}
		case strings.TrimSpace(line) == "":
			if i > 0 && i+1 < len(complete) && startsAtMargin(complete[i+1]) {
				boundary = offsets[i+1]
			}
			consumed = 1
		case isTableStart(complete[i:]):
			_, consumed = collectTable(complete[i:])
		case isRule(line), headingLevel(line) > 0:
			consumed = 1
		case isQuoteLine(line):
			_, consumed = collectWhile(complete[i:], isQuoteLine)
		case listMarker(line) != nil:
			_, consumed = collectListItem(complete[i:])
		default:
			_, consumed = collectWhile(complete[i:], isParagraphLine)
		}
		if consumed < 1 {
			consumed = 1
		}
		i += consumed
	}
	return boundary
}

// closedFence reports whether the fence opened at lines[0] is closed within lines.
func closedFence(lines []string) bool {
	marker := fenceMarker(lines[0])
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == marker {
			return true
		}
	}
	return false
}

func startsAtMargin(line string) bool {
	return line != "" && line[0] != ' ' && line[0] != '\t'
}
