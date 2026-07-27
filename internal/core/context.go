package core

import "fmt"

// How much of a model's context a conversation is using, and what to do as it fills.
//
// The rule this exists to enforce: **compaction is never silent**. An agent that quietly forgets
// half of what it was told and carries on answering is the same class of problem as a test result
// that says passing about code it never ran. Both are confident, both are wrong, and in both cases
// the person relying on them has no way to tell. So the meter is always on screen, compaction
// announces itself in the transcript, and nothing is ever deleted, only left out of what gets sent.

// ContextWindow is how many tokens a model will accept.
type ContextWindow int

// DefaultContextWindow is assumed when a model is not recognised.
//
// Deliberately modest. Guessing high on an unknown model means the first sign of the guess being
// wrong is the provider rejecting a request the user has already waited for, whereas guessing low
// costs an unnecessary compaction and a line in the transcript saying so.
const DefaultContextWindow ContextWindow = 128_000

// CompactionThreshold is the fraction of the window at which compaction becomes worthwhile.
//
// Not at the limit. Compacting at the point of overflow means the compaction itself has nowhere to
// run, since summarising a conversation is a model call that needs room for the conversation plus
// the summary. Leaving a fifth of the window free is what makes the recovery possible.
const CompactionThreshold = 0.80

// contextWindows are the sizes we know. Anything absent falls back to DefaultContextWindow.
//
// Matched by prefix rather than exactly, because a model id carries a build suffix and a gateway
// prefixes it with a vendor, so `anthropic/claude-opus-5` and `claude-opus-5-20260101` are the same
// model with the same window.
var contextWindows = map[string]ContextWindow{
	"claude-fable-5":    1_000_000,
	"claude-opus-5":     1_000_000,
	"claude-opus-4-8":   1_000_000,
	"claude-opus-4-7":   1_000_000,
	"claude-opus-4-6":   1_000_000,
	"claude-sonnet-5":   1_000_000,
	"claude-sonnet-4-6": 1_000_000,
	"claude-haiku-4-5":  200_000,
}

// WindowFor returns the context window for a model id.
func WindowFor(model string) ContextWindow {
	for name, window := range contextWindows {
		if containsFold(model, name) {
			return window
		}
	}
	return DefaultContextWindow
}

// containsFold reports whether haystack contains needle, ignoring case.
//
// Hand written rather than lowercasing both, because this runs on every render of the context meter
// and allocating two strings a frame for a substring test is the kind of thing that shows up once
// several agents are streaming.
func containsFold(haystack, needle string) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if equalFold(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	for i := 0; i < len(a); i++ {
		if lower(a[i]) != lower(b[i]) {
			return false
		}
	}
	return true
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// ContextUse is how full a conversation's context is.
type ContextUse struct {
	// Tokens is what the last turn actually reported sending, when there is one.
	Tokens int
	Window ContextWindow

	// Estimated is true when Tokens was inferred from the text rather than reported by a provider.
	//
	// The distinction matters on screen. A reported figure is what was billed; an estimate can be
	// out by a third, and a meter that presented a guess as a measurement would have people
	// compacting a conversation that had plenty of room, or trusting one that did not.
	Estimated bool
}

// Fraction is how full the window is, from zero to one.
func (c ContextUse) Fraction() float64 {
	if c.Window <= 0 {
		return 0
	}
	f := float64(c.Tokens) / float64(c.Window)
	if f > 1 {
		return 1
	}
	return f
}

// Remaining is how many tokens are left.
func (c ContextUse) Remaining() int {
	if left := int(c.Window) - c.Tokens; left > 0 {
		return left
	}
	return 0
}

// NeedsCompaction reports whether the conversation has grown past the point where it should be
// shortened before the next turn.
func (c ContextUse) NeedsCompaction() bool { return c.Fraction() >= CompactionThreshold }

func (c ContextUse) String() string {
	prefix := ""
	if c.Estimated {
		prefix = "about "
	}
	return fmt.Sprintf("%s%d%% of %s", prefix, int(c.Fraction()*100), formatTokens(int(c.Window)))
}

func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// ContextUse measures how full this session's context is.
//
// Prefers what the provider reported over an estimate, because the reported figure is the one that
// was billed and the one the next request will be measured against. An estimate is only used before
// the first turn has come back, when there is nothing else to go on.
func (s Session) ContextUse() ContextUse {
	use := ContextUse{Window: WindowFor(s.Model)}

	// The last terminal turn's input count is the size of everything sent up to that point, which
	// is the conversation so far. Later turns only add to it.
	for i := len(s.Turns) - 1; i >= 0; i-- {
		turn := s.Turns[i]
		if turn.Usage.InputTokens > 0 {
			// Plus what has been said since, so a long question does not read as free until after
			// it has been answered.
			use.Tokens = turn.Usage.InputTokens + turn.Usage.OutputTokens +
				estimateTokens(s.textAfter(i))
			return use
		}
	}

	use.Tokens = estimateTokens(s.allText())
	use.Estimated = true
	return use
}

func (s Session) textAfter(index int) int {
	var n int
	for _, turn := range s.Turns[index+1:] {
		n += len(turn.Request.Text) + len(turn.Text) + len(turn.Thinking)
	}
	return n
}

func (s Session) allText() int {
	var n int
	for _, turn := range s.Turns {
		n += len(turn.Request.Text) + len(turn.Text) + len(turn.Thinking)
	}
	return n
}

// bytesPerToken is the rough conversion used when nothing better is available.
//
// Four is the usual figure for English prose and is optimistic for code, which tokenises worse. Any
// estimate here is wrong; the point is to be wrong by a knowable amount and to say on screen that
// it is an estimate, rather than to ship a tokeniser per vendor and still be wrong when they change
// one.
const bytesPerToken = 4

func estimateTokens(bytes int) int { return bytes / bytesPerToken }
