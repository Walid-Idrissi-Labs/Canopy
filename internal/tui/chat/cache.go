package chat

import (
	"hash/fnv"
	"strconv"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A finished turn is rendered once.
//
// The transcript is rebuilt from the session on every frame, which is the right architecture and was
// costing more than it needed to. The model holds no copy of what it drew, so a coalesced or dropped
// event cannot lose a token: whatever is in the session is what appears. Keeping that property while
// paying for it once is what this file is for.
//
// The cost was real rather than theoretical. Measured on a session of two hundred turns of ordinary
// replies, markdown with a table and a code block in each, one full render took a hundred and five
// milliseconds. The screen ticks every hundred and twenty, and a scroll renders twice, so a long
// conversation spent most of a core redrawing itself and answered every keystroke a tenth of a
// second late. Both of those get worse as the conversation gets longer, which is to say they get
// worse the longer somebody has been using the product.
//
// Only terminal turns are cached. A turn still running changes on every frame by definition, its
// text is growing and its spinner is turning, and it is exactly one turn: the last one. So the
// cache covers everything above the fold and nothing that moves, which is the whole win with none of
// the risk of showing somebody a stale answer.

// renderedTurns is the cache. Package level rather than a field, because Transcript is a package
// function called from two screens, and a cache that lived on the chat model would leave the agents
// mosaic paying full price to draw the same conversations.
var renderedTurns = struct {
	sync.Mutex
	lines map[string][]string
	order []string

	// generation counts theme changes.
	//
	// Emptying the map is not enough on its own. A render is produced outside the lock, on purpose,
	// and a theme change can land in that window: the map is cleared and then the render that was
	// already in flight, carrying the old palette, is stored into the map that was just emptied.
	// The result is half a conversation in each palette, with no error and nothing to notice except
	// that it looks wrong, which is the failure this cache is supposed to make impossible.
	//
	// So a render records which generation it was made in and is only stored if that is still the
	// current one.
	generation uint64
}{lines: make(map[string][]string)}

// cacheLimit is how many rendered turns to keep.
//
// Generous, because a rendered turn is a slice of strings that already exists in the session it came
// from, and mean, because a conversation is not the only thing in memory. Two thousand covers any
// conversation somebody is scrolling through and bounds the growth of one they have left running for
// a week.
const cacheLimit = 2000

// cachedTurn renders a turn, reusing the last render when the turn cannot have changed.
func cachedTurn(sessionID string, turn core.Turn, width int, spinner string, kinds KindOf, detail Detail) []string {
	if !terminal(turn.State) || turn.ID == "" || sessionID == "" {
		return renderTurn(turn, width, spinner, kinds, detail)
	}

	// Resolve the callback once. Besides making its answers part of the key, this guarantees the
	// render uses the exact classifications that produced that key if a registry is being replaced
	// concurrently by the screen above us.
	kinds = freezeKinds(turn, kinds)
	key := turnKey(sessionID, turn, width, kinds, detail)

	renderedTurns.Lock()
	if lines, ok := renderedTurns.lines[key]; ok {
		renderedTurns.Unlock()
		return lines
	}
	drawnIn := renderedTurns.generation
	renderedTurns.Unlock()

	// Rendered outside the lock. It is the expensive part and it depends on nothing the lock
	// protects, so holding the mutex across it would serialise every screen drawing at once for no
	// reason.
	lines := renderTurn(turn, width, spinner, kinds, detail)

	renderedTurns.Lock()
	if _, ok := renderedTurns.lines[key]; !ok && renderedTurns.generation == drawnIn {
		renderedTurns.lines[key] = lines
		renderedTurns.order = append(renderedTurns.order, key)
		for len(renderedTurns.order) > cacheLimit {
			delete(renderedTurns.lines, renderedTurns.order[0])
			renderedTurns.order = renderedTurns.order[1:]
		}
	}
	renderedTurns.Unlock()

	return lines
}

// freezeKinds snapshots the classifications this turn will render.
func freezeKinds(turn core.Turn, kinds KindOf) KindOf {
	if kinds == nil {
		return nil
	}
	type resolved struct {
		kind  core.ToolKind
		known bool
	}
	byName := make(map[string]resolved, len(turn.ToolCalls))
	for _, call := range turn.ToolCalls {
		if _, seen := byName[call.Name]; seen {
			continue
		}
		kind, known := kinds(call.Name)
		byName[call.Name] = resolved{kind: kind, known: known}
	}
	return func(name string) (core.ToolKind, bool) {
		answer, ok := byName[name]
		if !ok {
			return "", false
		}
		return answer.kind, answer.known
	}
}

// terminal reports whether a turn has finished changing.
//
// Listed rather than inferred from "not running", so that a state added later is not silently
// treated as final and cached forever the first time it appears.
func terminal(state core.TurnState) bool {
	switch state {
	case core.TurnComplete, core.TurnFailed, core.TurnRefused, core.TurnInterrupted, core.TurnTruncated:
		return true
	default:
		return false
	}
}

// turnKey identifies a rendered turn, including everything the rendering depends on.
//
// The session comes first and is not optional. A turn ID is unique within its session and nowhere
// else, which core says in as many words, so a key built from the turn alone would let two
// conversations that both number their turns from one collide, and the second one would be shown the
// first one's answer.
//
// The turn's identity is not enough on its own either. The same turn drawn at two widths is two
// different sets of lines, ctrl+o draws it a third way, and a tool registry can classify the same
// call differently on two surfaces. The resolved kind is part of the visible label and colour, so
// it is content for this purpose even though it is supplied by a callback rather than stored on the
// turn.
//
// The content is hashed rather than measured. The first version of this fingerprinted a turn by the
// lengths of its parts, on the reasoning that a terminal turn is not supposed to change anyway and
// two turns of identical length and state would be a coincidence. It stopped being a coincidence
// within one run of this package's own tests, where a session called s1 holding a turn called turn-1
// is what nearly every test builds. Hashing costs a pass over bytes that are already in memory,
// which is nothing beside the markdown pass it saves, and it turns "usually right" into right.
func turnKey(sessionID string, turn core.Turn, width int, kinds KindOf, detail Detail) string {
	h := fnv.New64a()
	write := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}

	write(sessionID)
	write(turn.ID)
	write(string(turn.State))
	write(turn.Request.Text)
	write(turn.Text)
	write(turn.Thinking)
	write(turn.Error)
	for _, call := range turn.ToolCalls {
		write(call.ID)
		write(call.Name)
		_, _ = h.Write(call.Input)
		_, _ = h.Write([]byte{0})
		if kinds == nil {
			write("kind:unknown")
		} else if kind, known := kinds(call.Name); known {
			write("kind:" + string(kind))
		} else {
			write("kind:unknown")
		}
	}
	for _, result := range turn.ToolResults {
		write(result.CallID)
		write(result.Content)
		if result.IsError {
			write("e")
		}
		if result.Refused {
			write("r")
		}
		write(result.Duration.String())
	}

	key := strconv.FormatUint(h.Sum64(), 36) + "|" + strconv.Itoa(width)
	if detail.Expanded {
		key += "|x"
	}
	return key
}

// A cached line carries the colours that were current when it was drawn, so switching theme has to
// throw the cache away. Registered here rather than left to the caller, because the failure it
// prevents is silent: half a conversation in the old palette and half in the new one, with no error
// and nothing to notice except that it looks wrong.
func init() { theme.OnChange(forgetRenders) }

// forgetRenders empties the cache.
func forgetRenders() {
	renderedTurns.Lock()
	renderedTurns.lines = make(map[string][]string)
	renderedTurns.order = nil
	renderedTurns.generation++
	renderedTurns.Unlock()
}
