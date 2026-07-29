package chat_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

func oneTurn(sessionID, turnID, text string, state core.TurnState) core.Session {
	return core.Session{ID: sessionID, Turns: []core.Turn{{
		ID: turnID, State: state, Request: core.Message{Text: "ask"}, Text: text,
	}}}
}

// A turn ID is unique within its session and nowhere else, which core says in as many words. Two
// conversations that both number their turns from one must not be shown each other's answers.
func TestTwoSessionsWithTheSameTurnIdsDoNotShareRenders(t *testing.T) {
	first := strings.Join(chat.Transcript(oneTurn("s1", "turn-1", "alpha", core.TurnComplete), 60, ".", nil), "\n")
	second := strings.Join(chat.Transcript(oneTurn("s2", "turn-1", "bravo", core.TurnComplete), 60, ".", nil), "\n")

	if !strings.Contains(plain(first), "alpha") {
		t.Errorf("the first session lost its own reply:\n%s", plain(first))
	}
	if !strings.Contains(plain(second), "bravo") || strings.Contains(plain(second), "alpha") {
		t.Errorf("the second session was shown the first one's reply:\n%s", plain(second))
	}
}

// A turn still arriving changes on every frame by definition, so it can never be served from a
// cache. This is the failure that would matter most: an answer that stops updating while it streams.
func TestAStreamingTurnIsNeverCached(t *testing.T) {
	session := oneTurn("s1", "turn-1", "partial", core.TurnStreaming)
	before := plain(strings.Join(chat.Transcript(session, 60, ".", nil), "\n"))
	if !strings.Contains(before, "partial") {
		t.Fatalf("the partial reply is missing:\n%s", before)
	}

	session.Turns[0].Text = "partial and then some more"
	after := plain(strings.Join(chat.Transcript(session, 60, ".", nil), "\n"))
	if !strings.Contains(after, "and then some more") {
		t.Errorf("a streaming turn was served from the cache, so the reply froze:\n%s", after)
	}
}

// The same turn at two widths is two different sets of lines.
func TestTheSameTurnAtADifferentWidthIsRenderedAgain(t *testing.T) {
	session := oneTurn("s1", "turn-1", strings.Repeat("word ", 40), core.TurnComplete)

	wide := chat.Transcript(session, 100, ".", nil)
	narrow := chat.Transcript(session, 40, ".", nil)

	for _, line := range narrow {
		if len([]rune(plain(line))) > 40 {
			t.Errorf("a line rendered at width 100 was reused at width 40: %q", plain(line))
		}
	}
	if len(narrow) <= len(wide) {
		t.Errorf("the narrow render is %d lines and the wide one %d", len(narrow), len(wide))
	}
}

// ctrl+o draws the same turn a third way, so it cannot share a key with the folded view.
func TestExpandingRendersTheSameTurnAgain(t *testing.T) {
	session := core.Session{ID: "s1", Turns: []core.Turn{{
		ID: "turn-1", State: core.TurnComplete, Request: core.Message{Text: "ask"},
		ToolCalls: []core.ToolCall{{ID: "c1", Name: "read_file", Input: []byte(`{"path":"x.go"}`)}},
		ToolResults: []core.ToolResult{{
			CallID: "c1", Content: strings.Repeat("a line\n", 40), Duration: time.Millisecond,
		}},
	}}}

	folded := strings.Count(plain(strings.Join(
		chat.TranscriptWith(session, 80, ".", nil, chat.Detail{}), "\n")), "a line")
	opened := strings.Count(plain(strings.Join(
		chat.TranscriptWith(session, 80, ".", nil, chat.Detail{Expanded: true}), "\n")), "a line")

	if opened <= folded {
		t.Errorf("the expanded view was served the folded render: %d then %d", folded, opened)
	}
}
