package chat

import (
	"strings"
	"sync"
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
	tail   string // the last bytes of that text, to notice text that was replaced rather than grown
	lines  []string
}

var streaming = struct {
	sync.Mutex
	byTurn map[string]*streamRender
}{byTurn: map[string]*streamRender{}}

// streamingMarkdown renders text the way RenderMarkdown does, reusing work from earlier calls for
// the same key while the text only grows.
func streamingMarkdown(key, text string, width int) []string {
	boundary := stableBoundary(text)

	streaming.Lock()
	state := streaming.byTurn[key]
	if state == nil || state.width != width || state.stable > boundary ||
		!strings.HasSuffix(text[:state.stable], state.tail) {
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
		state.tail = text[max(0, boundary-32):boundary]
	}
	head := state.lines
	streaming.Unlock()

	return append(append([]string(nil), head...), RenderMarkdown(text[boundary:], width)...)
}

// forgetStreaming drops the state for a turn once it has finished and is cached whole.
func forgetStreaming(key string) {
	streaming.Lock()
	delete(streaming.byTurn, key)
	streaming.Unlock()
}

// stableBoundary is the offset just past the last blank line that is not inside a code fence and is
// followed by more text: everything before it is complete blocks that later text cannot change.
func stableBoundary(text string) int {
	boundary := 0
	inFence := false
	offset := 0
	prevBlank := false
	for offset < len(text) {
		end := strings.IndexByte(text[offset:], '\n')
		if end < 0 {
			break
		}
		line := text[offset : offset+end]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
		blank := trimmed == ""
		next := offset + end + 1
		if blank && !inFence && !prevBlank && offset > 0 {
			boundary = next
		}
		prevBlank = blank
		offset = next
	}
	return boundary
}
