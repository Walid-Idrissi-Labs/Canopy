package catalog

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Every one of these is a way somebody actually types a model name, and refusing any of them would
// be refusing over a distinction nobody intends.
func TestSpellingIsForgivenBeforeAnythingIsRefused(t *testing.T) {
	models := For(core.ProviderAnthropic, "")

	for spoken, want := range map[string]string{
		"claude-sonnet-4-6": "claude-sonnet-4-6",
		"claude sonnet 4 6": "claude-sonnet-4-6",
		"Claude Sonnet 4.6": "claude-sonnet-4-6",
		"CLAUDE_SONNET_4_6": "claude-sonnet-4-6",
		"sonnet-4-6":        "claude-sonnet-4-6",
		"sonnet 4 6":        "claude-sonnet-4-6",
		"opus 4 7":          "claude-opus-4-7",
		"haiku":             "claude-haiku-4-5",
		"  Opus  ":          "claude-opus-5",
	} {
		hits := Match(models, spoken)
		if len(hits) != 1 {
			t.Errorf("%q matched %d models, want one", spoken, len(hits))
			continue
		}
		if hits[0].ID != want {
			t.Errorf("%q resolved to %q, want %q", spoken, hits[0].ID, want)
		}
	}
}

// A bare family word is the newest of that family, not a question. The order of the list is what
// answers it, which is why the order is a fact the catalog holds rather than a presentation detail.
func TestAFamilyWordMeansTheNewestOfThatFamily(t *testing.T) {
	models := For(core.ProviderAnthropic, "")

	for _, spoken := range []string{"sonnet", "claude sonnet", "Sonnet"} {
		hits := Match(models, spoken)
		if len(hits) != 1 || hits[0].ID != "claude-sonnet-5" {
			t.Errorf("%q resolved to %+v, want the newest sonnet", spoken, hits)
		}
	}
}

// A model somebody added by hand answers to its display name as well as its id, since the name is
// the half they chose and therefore the half they will say.
func TestADisplayNameResolvesTheSameAsItsID(t *testing.T) {
	models := []Model{{ID: "minimaxai/minimax-m2.7", Name: "MiniMax M2.7"}}

	for _, spoken := range []string{
		"minimaxai/minimax-m2.7",
		"MiniMax M2.7",
		"minimax m2 7",
	} {
		hits := Match(models, spoken)
		if len(hits) != 1 || hits[0].ID != "minimaxai/minimax-m2.7" {
			t.Errorf("%q resolved to %+v", spoken, hits)
		}
	}
}

// Nothing is guessed. A word that names no model comes back empty so the caller can say what does
// exist, which is the only refusal that leaves somebody better off than before they asked.
func TestAnUnknownWordMatchesNothingRatherThanTheNearestThing(t *testing.T) {
	models := For(core.ProviderAnthropic, "")

	for _, spoken := range []string{"gpt-5.2", "sonnnet", "", "   ", "claude"} {
		if hits := Match(models, spoken); len(hits) != 0 {
			t.Errorf("%q was matched to %+v", spoken, hits)
		}
	}
}

// Two entries that could both be meant come back as two, so the caller refuses rather than picking.
// The same id appearing twice is not that: it is one answer written down twice.
func TestAmbiguityComesBackAsAmbiguityAndDuplicatesDoNot(t *testing.T) {
	ambiguous := []Model{
		{ID: "vendor-a/fast", Name: "Fast"},
		{ID: "vendor-b/fast", Name: "Fast"},
	}
	if hits := Match(ambiguous, "Fast"); len(hits) != 2 {
		t.Errorf("two models called Fast matched %d entries, want both", len(hits))
	}

	duplicated := []Model{
		{ID: "claude-sonnet-5", Name: "Claude Sonnet 5"},
		{ID: "claude-sonnet-5"},
	}
	if hits := Match(duplicated, "claude-sonnet-5"); len(hits) != 1 {
		t.Errorf("one model listed twice matched %d entries, want one", len(hits))
	}
}
