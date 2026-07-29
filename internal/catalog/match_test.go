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

	// Byte identical is the easy half. The half that mattered is two spellings of one id: matching
	// compares them normalised, so counting them raw made "opus 5" ambiguous between a model and
	// itself, and the request was refused with the same id listed twice as the choices.
	for _, duplicated := range [][]Model{
		{{ID: "claude-sonnet-5", Name: "Claude Sonnet 5"}, {ID: "claude-sonnet-5"}},
		{{ID: "claude-opus-5", Name: "Claude Opus 5"}, {ID: "CLAUDE-OPUS-5"}},
		{{ID: "claude-opus-5"}, {ID: "claude_opus_5"}},
		{{ID: "gpt-5.2"}, {ID: "GPT 5.2"}},
	} {
		if hits := Match(duplicated, duplicated[0].ID); len(hits) != 1 {
			t.Errorf("%q and %q matched %d entries, want one model",
				duplicated[0].ID, duplicated[1].ID, len(hits))
		}
	}
}

// A number written up against the word before it is the same request as one written apart from it.
// Only in that direction: a digit followed by a letter is one word to the provider, and splitting
// "gpt-4o" would turn an id somebody typed correctly into one nothing answers to.
func TestANumberRunTogetherWithTheWordBeforeItStillResolves(t *testing.T) {
	anthropic := For(core.ProviderAnthropic, "")
	openAI := For(core.ProviderOpenAICompatible, "https://api.openai.com/v1")

	for _, probe := range []struct {
		models []Model
		spoken string
		want   string
	}{
		{anthropic, "sonnet5", "claude-sonnet-5"},
		{anthropic, "Sonnet5", "claude-sonnet-5"},
		{anthropic, "claude-sonnet5", "claude-sonnet-5"},
		{openAI, "gpt5.2", "gpt-5.2"},
		{openAI, "gpt5.1 codex max", "gpt-5.1-codex-max"},
	} {
		hits := Match(probe.models, probe.spoken)
		if len(hits) != 1 || hits[0].ID != probe.want {
			t.Errorf("%q resolved to %+v, want %q", probe.spoken, hits, probe.want)
		}
	}

	// Two numbers run together are not two numbers. "opus48" could be the eighth of four or the
	// forty eighth of nothing, and guessing between them is exactly what this refuses to do.
	if hits := Match(anthropic, "opus48"); len(hits) != 0 {
		t.Errorf("opus48 was guessed at %+v rather than refused", hits)
	}

	// And an id whose letters follow its digits survives the round trip, which is the whole reason
	// the split is one directional.
	fourOh := []Model{{ID: "gpt-4o"}, {ID: "gpt-4o-mini"}}
	for _, spoken := range []string{"gpt-4o", "GPT 4o", "gpt4o"} {
		hits := Match(fourOh, spoken)
		if len(hits) != 1 || hits[0].ID != "gpt-4o" {
			t.Errorf("%q resolved to %+v, want gpt-4o", spoken, hits)
		}
	}
}

// The store and the matcher have to agree about what counts as one model, or the store collects a
// row the matcher will never let anybody select.
func TestTwoSpellingsOfOneIDAreOneModel(t *testing.T) {
	for _, same := range [][2]string{
		{"claude-opus-5", "CLAUDE-OPUS-5"},
		{"claude-opus-5", "claude opus 5"},
		{"gpt-5.2", "gpt5.2"},
		{"minimaxai/minimax-m2.7", "MiniMaxAI/MiniMax-M2.7"},
	} {
		if !SameModel(same[0], same[1]) {
			t.Errorf("%q and %q read as different models", same[0], same[1])
		}
	}
	for _, different := range [][2]string{
		{"claude-opus-5", "claude-opus-4-8"},
		{"gpt-4o", "gpt-4"},
		{"vendor-a/fast", "vendor-b/fast"},
	} {
		if SameModel(different[0], different[1]) {
			t.Errorf("%q and %q read as the same model", different[0], different[1])
		}
	}
}
