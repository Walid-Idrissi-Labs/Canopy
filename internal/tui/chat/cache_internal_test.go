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

// A render is produced outside the lock, so a theme change can land between producing it and storing
// it. Without the generation check the render made in the old palette is stored into the map that
// the theme change had just emptied, and the conversation ends up half in each palette with nothing
// to notice except that it looks wrong.
func TestARenderFromTheOldPaletteIsNotStoredAfterAThemeChange(t *testing.T) {
	before := theme.Current().Palette
	defer theme.Set(before)

	forgetRenders()

	session := core.Session{ID: "s1", Turns: []core.Turn{{
		ID: "turn-1", State: core.TurnComplete, Request: core.Message{Text: "ask"}, Text: "answer",
	}}}

	// The generation this render belongs to, read the way cachedTurn reads it.
	renderedTurns.Lock()
	drawnIn := renderedTurns.generation
	renderedTurns.Unlock()

	// The theme changes while that render is notionally still in flight.
	theme.Set(theme.Monochrome)

	renderedTurns.Lock()
	moved := renderedTurns.generation != drawnIn
	renderedTurns.Unlock()
	if !moved {
		t.Fatal("a theme change did not move the generation, so nothing can detect a stale render")
	}

	// Anything rendered after the change belongs to the new generation and is cached normally.
	Transcript(session, 60, ".", nil)
	renderedTurns.Lock()
	held := len(renderedTurns.lines)
	renderedTurns.Unlock()
	if held == 0 {
		t.Error("a render made after the theme change was refused, so the cache never refills")
	}
}

// The first frame is drawn before the terminal says what its background is, on the dark
// assumption, so learning it is light has to throw away what was drawn: otherwise a picked up
// conversation stays in the dark palette on a light terminal. Setting the same background again is
// not a change and keeps the cache.
func TestLearningTheBackgroundEmptiesTheRenderCaches(t *testing.T) {
	defer theme.SetDark(true)
	theme.SetDark(true)
	session := core.Session{ID: "s1", Turns: []core.Turn{{
		ID: "turn-1", State: core.TurnComplete, Request: core.Message{Text: "ask"}, Text: "answer",
	}}}
	filled := func() int {
		Transcript(session, 60, ".", nil)
		renderedTurns.Lock()
		defer renderedTurns.Unlock()
		return len(renderedTurns.lines)
	}
	if filled() == 0 {
		t.Fatal("a finished turn was not cached, so this test proves nothing")
	}
	theme.SetDark(true)
	renderedTurns.Lock()
	kept := len(renderedTurns.lines)
	renderedTurns.Unlock()
	if kept == 0 {
		t.Fatal("the same background again emptied the cache")
	}
	theme.SetDark(false)
	renderedTurns.Lock()
	left := len(renderedTurns.lines)
	renderedTurns.Unlock()
	if left != 0 {
		t.Fatalf("%d renders in the dark palette survived learning the background is light", left)
	}

	// Highlighted code is cached apart, by its own key, which has to say which background it is for.
	code := []string{"func main() {}"}
	cachedChromaBlock("go", code, 40)
	theme.SetDark(true)
	chromaCache.Lock()
	before := len(chromaCache.entries)
	chromaCache.Unlock()
	cachedChromaBlock("go", code, 40)
	chromaCache.Lock()
	after := len(chromaCache.entries)
	chromaCache.Unlock()
	if after != before+1 {
		t.Fatal("a block highlighted for a light background was reused on a dark one")
	}
}

// A reply still streaming keeps the lines it has drawn so far; learning the background drops them
// too, or the rest of that reply would be drawn under a head in the other palette.
func TestLearningTheBackgroundEmptiesTheStreamingRenders(t *testing.T) {
	defer theme.SetDark(true)
	theme.SetDark(true)
	streamingMarkdown("s1|t1", "first paragraph\n\nsecond", 60)
	streaming.Lock()
	filled := len(streaming.byTurn)
	streaming.Unlock()
	if filled == 0 {
		t.Fatal("nothing was kept for the streaming reply, so this test proves nothing")
	}
	theme.SetDark(false)
	streaming.Lock()
	defer streaming.Unlock()
	if len(streaming.byTurn) != 0 {
		t.Fatal("lines drawn for a dark background survived learning it is light")
	}
}
