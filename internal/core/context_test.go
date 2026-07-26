package core

import (
	"strings"
	"testing"
)

func TestWindowForKnownAndUnknownModels(t *testing.T) {
	if got := WindowFor("claude-opus-5"); got != 1_000_000 {
		t.Errorf("window = %d", got)
	}
	// A build suffix and a gateway prefix are the same model with the same window, and a meter that
	// fell back to the conservative default on either would tell somebody they were nearly full
	// when they had used a tenth of the room.
	for _, name := range []string{
		"claude-opus-5-20260101",
		"anthropic/claude-opus-5",
		"CLAUDE-OPUS-5",
	} {
		if got := WindowFor(name); got != 1_000_000 {
			t.Errorf("WindowFor(%q) = %d, want the same window as the plain name", name, got)
		}
	}

	// Guessing high on an unknown model means the first sign of being wrong is a rejected request
	// the user already waited for.
	if got := WindowFor("somebody/new-model-v3"); got != DefaultContextWindow {
		t.Errorf("an unknown model got %d, want the conservative default", got)
	}
	if DefaultContextWindow > 200_000 {
		t.Error("the default should be conservative, since guessing high fails at the worst moment")
	}
}

// A reported figure is what was billed. An estimate can be out by a third, and a meter that
// presented a guess as a measurement would have people trusting a conversation with no room left.
func TestReportedUsageIsPreferredOverAnEstimate(t *testing.T) {
	answered := Session{Model: "claude-opus-5", Turns: []Turn{{
		ID: "t1", State: TurnComplete,
		Request: Message{Role: RoleUser, Text: "hello"},
		Text:    "hi",
		Usage:   Usage{InputTokens: 50_000, OutputTokens: 1_000},
	}},
	}
	use := answered.ContextUse()
	if use.Estimated {
		t.Error("a session with a reported figure should not be reporting an estimate")
	}
	if use.Tokens != 51_000 {
		t.Errorf("tokens = %d, want the reported input plus output", use.Tokens)
	}

	unanswered := Session{Model: "claude-opus-5", Turns: []Turn{{
		ID: "t1", State: TurnPending,
		Request: Message{Role: RoleUser, Text: strings.Repeat("x", 4000)},
	}}}
	use = unanswered.ContextUse()
	if !use.Estimated {
		t.Error("with nothing reported yet the figure is an estimate and has to say so")
	}
	if use.Tokens == 0 {
		t.Error("a long unanswered question is not free context")
	}
	if !strings.Contains(use.String(), "about") {
		t.Errorf("an estimate should be hedged on screen, got %q", use.String())
	}
}

// A long question typed after the last answer must not read as free until it has been answered,
// or the meter says there is room right up to the request that overflows.
func TestTextSinceTheLastAnswerCounts(t *testing.T) {
	session := Session{Model: "claude-opus-5", Turns: []Turn{
		{
			ID: "t1", State: TurnComplete,
			Request: Message{Role: RoleUser, Text: "short"},
			Usage:   Usage{InputTokens: 1000, OutputTokens: 100},
		},
		{
			ID: "t2", State: TurnPending,
			Request: Message{Role: RoleUser, Text: strings.Repeat("x", 40_000)},
		},
	}}

	use := session.ContextUse()
	if use.Tokens <= 1100 {
		t.Errorf("tokens = %d, want the pasted text since the last answer counted too", use.Tokens)
	}
}

// Compacting at the point of overflow leaves the compaction itself nowhere to run, since
// summarising a conversation is a model call needing room for both the conversation and the summary.
func TestCompactionTriggersWithRoomLeftToDoIt(t *testing.T) {
	if CompactionThreshold >= 1 {
		t.Fatal("compacting at the limit leaves no room to perform the compaction")
	}
	if CompactionThreshold < 0.5 {
		t.Error("compacting this early would throw away context nobody needed to lose")
	}

	full := ContextUse{Tokens: 900_000, Window: 1_000_000}
	if !full.NeedsCompaction() {
		t.Error("a conversation at ninety percent needs compacting")
	}
	half := ContextUse{Tokens: 500_000, Window: 1_000_000}
	if half.NeedsCompaction() {
		t.Error("a conversation at half needs nothing")
	}
}

func TestFractionIsBoundedAndRemainingNeverGoesNegative(t *testing.T) {
	over := ContextUse{Tokens: 2_000_000, Window: 1_000_000}
	if over.Fraction() != 1 {
		t.Errorf("fraction = %v, want it capped at 1 so a meter cannot draw past its own end",
			over.Fraction())
	}
	if over.Remaining() != 0 {
		t.Errorf("remaining = %d, want 0 rather than a negative token count", over.Remaining())
	}

	// A session with no model and no turns must not divide by zero on the very first render.
	empty := Session{}.ContextUse()
	if empty.Fraction() < 0 || empty.Fraction() > 1 {
		t.Errorf("fraction = %v", empty.Fraction())
	}
}

func TestContextUseReadsAsASentence(t *testing.T) {
	use := ContextUse{Tokens: 250_000, Window: 1_000_000}
	got := use.String()
	if !strings.Contains(got, "25%") || !strings.Contains(got, "1M") {
		t.Errorf("String() = %q, want a percentage and a window somebody can read", got)
	}
}
