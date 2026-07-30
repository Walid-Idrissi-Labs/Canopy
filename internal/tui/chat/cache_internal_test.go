package chat

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// A cached line carries the colours it was drawn with, so switching theme has to throw the cache
// away. Asserted on the cache itself rather than on two rendered strings, because lipgloss strips
// colour when there is no terminal attached and a test comparing two palettes' output would pass
// here whether or not the invalidation existed.
//
// The failure this guards is silent: half a conversation in the old palette and half in the new one,
// with no error and nothing to notice except that it looks wrong.
func TestChangingThemeEmptiesTheRenderCache(t *testing.T) {
	before := theme.Current().Palette
	defer theme.Set(before)

	session := core.Session{ID: "s1", Turns: []core.Turn{{
		ID: "turn-1", State: core.TurnComplete, Request: core.Message{Text: "ask"}, Text: "answer",
	}}}
	Transcript(session, 60, ".", nil)

	renderedTurns.Lock()
	filled := len(renderedTurns.lines)
	renderedTurns.Unlock()
	if filled == 0 {
		t.Fatalf("a finished turn was not cached, so this test proves nothing")
	}

	theme.Set(theme.Monochrome)

	renderedTurns.Lock()
	left := len(renderedTurns.lines)
	renderedTurns.Unlock()
	if left != 0 {
		t.Errorf("%d rendered turns survived a theme change, still carrying the old palette", left)
	}
}

// The cache is bounded, or a conversation left running for a week grows it without limit.
func TestTheRenderCacheIsBounded(t *testing.T) {
	forgetRenders()

	for i := range cacheLimit + 50 {
		session := core.Session{ID: "s1", Turns: []core.Turn{{
			ID:      "turn-" + itoa(i),
			State:   core.TurnComplete,
			Request: core.Message{Text: "ask"},
			Text:    "answer " + itoa(i),
		}}}
		Transcript(session, 60, ".", nil)
	}

	renderedTurns.Lock()
	held := len(renderedTurns.lines)
	order := len(renderedTurns.order)
	renderedTurns.Unlock()

	if held > cacheLimit {
		t.Errorf("the cache holds %d renders, past its limit of %d", held, cacheLimit)
	}
	if held != order {
		t.Errorf("the cache holds %d renders and %d keys, so eviction leaks one of them", held, order)
	}
}
