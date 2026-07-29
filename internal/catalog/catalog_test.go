package catalog

import (
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The setup an Anthropic key needs before it can offer a choice is none. That is the whole point of
// shipping a list rather than asking everybody to type ids.
func TestAnAnthropicKeyOffersTheListWithNoSetup(t *testing.T) {
	models := For(core.ProviderAnthropic, "")
	if len(models) == 0 {
		t.Fatal("an anthropic key was offered nothing")
	}

	seen := make(map[string]bool, len(models))
	for _, model := range models {
		if model.ID == "" {
			t.Errorf("an entry has no id: %+v", model)
		}
		if seen[model.ID] {
			t.Errorf("%s is listed twice", model.ID)
		}
		seen[model.ID] = true
	}
	if !seen["claude-sonnet-5"] || !seen["claude-opus-5"] {
		t.Errorf("the list is missing a model that is definitely there: %+v", models)
	}
}

// A family word means the newest member of that family, and the order in the table is how that is
// decided, so the order is worth a test of its own rather than being left to whoever edits next.
func TestTheNewestOfEachFamilyComesFirst(t *testing.T) {
	models := For(core.ProviderAnthropic, "")

	first := map[string]string{}
	for _, model := range models {
		family := readFamily(model.ID)
		if _, seen := first[family]; !seen {
			first[family] = model.ID
		}
	}

	for family, want := range map[string]string{
		"opus":   "claude-opus-5",
		"sonnet": "claude-sonnet-5",
		"haiku":  "claude-haiku-4-5",
		"fable":  "claude-fable-5",
	} {
		if first[family] != want {
			t.Errorf("the first %s is %q, want %q", family, first[family], want)
		}
	}
}

// readFamily is the test's own reading of a model id, deliberately not shared with the code under
// test: a helper both sides used could agree with itself while being wrong about the order.
func readFamily(id string) string {
	rest := id
	if len(id) > len("claude-") && id[:len("claude-")] == "claude-" {
		rest = id[len("claude-"):]
	}
	for i, r := range rest {
		if r == '-' {
			return rest[:i]
		}
	}
	return rest
}

// The OpenAI compatible family is whatever endpoint a key was pointed at, so a lineup is offered for
// exactly the one host it is true of.
func TestOnlyOpenAIsOwnHostIsGivenTheOpenAIList(t *testing.T) {
	if models := For(core.ProviderOpenAICompatible, "https://api.openai.com/v1"); len(models) == 0 {
		t.Error("openai's own endpoint was offered nothing")
	}
	// Case and port must not change the answer, since a base URL is typed by hand.
	if models := For(core.ProviderOpenAICompatible, "https://API.OpenAI.com/v1"); len(models) == 0 {
		t.Error("the host comparison is case sensitive")
	}
}

// An unrecognised gateway gets nothing, which is what lets the screen say so out loud rather than
// offering GPT ids to an endpoint that has never heard of them.
func TestAnUnrecognisedEndpointIsOfferedNothing(t *testing.T) {
	for _, baseURL := range []string{
		"https://api.moonshot.cn/v1",
		"http://localhost:11434/v1",
		"",
	} {
		if models := For(core.ProviderOpenAICompatible, baseURL); len(models) != 0 {
			t.Errorf("%q was offered %d models, want none", baseURL, len(models))
		}
	}
}

// The table is a copy on the way out, so a caller that sorts what it was given does not reorder the
// list every other caller reads. Order is meaning here, which is what makes this worth holding.
func TestTheListCannotBeEditedThroughWhatItHandsBack(t *testing.T) {
	first := For(core.ProviderAnthropic, "")
	first[0] = Model{ID: "scribbled-over"}

	if again := For(core.ProviderAnthropic, ""); again[0].ID == "scribbled-over" {
		t.Error("a caller edited the table through the slice it was handed")
	}
}

// Knowledge carries its date, and a date nobody can read is not carried at all.
func TestTheListSaysWhenItWasTrue(t *testing.T) {
	if AsOf.IsZero() {
		t.Fatal("the catalog has no as-of date")
	}
	if Stale(AsOf.Add(MaxAge - time.Hour)) {
		t.Error("the list called itself stale while it was still inside its own max age")
	}
	if !Stale(AsOf.Add(MaxAge + time.Hour)) {
		t.Error("a list past its max age still presented itself as current")
	}
}

func TestANameIsShownOnlyWhenItSaysSomethingTheIDDoesNot(t *testing.T) {
	named := Model{ID: "minimaxai/minimax-m2.7", Name: "MiniMax M2.7"}
	if !named.Named() || named.Label() != "MiniMax M2.7" {
		t.Errorf("a named entry reads as %q", named.Label())
	}

	bare := Model{ID: "some-model"}
	if bare.Named() || bare.Label() != "some-model" {
		t.Errorf("an unnamed entry reads as %q", bare.Label())
	}

	same := Model{ID: "some-model", Name: "some-model"}
	if same.Named() {
		t.Error("a name identical to the id would be printed twice")
	}
}

// A date on screen is not the same as the screen saying the date is old, since reading one means
// knowing today's and doing the subtraction, which nobody does.
func TestAStaleListSaysSoInWords(t *testing.T) {
	if note := StalenessNote(AsOf.Add(MaxAge - time.Hour)); note != "" {
		t.Errorf("a list inside its own max age announced itself as stale: %q", note)
	}

	note := StalenessNote(AsOf.Add(MaxAge + 30*24*time.Hour))
	if note == "" {
		t.Fatal("a list well past its max age said nothing")
	}
	if !strings.Contains(note, AsOf.Format("2006-01-02")) {
		t.Errorf("the note does not say when the list was true: %q", note)
	}
	if !strings.Contains(note, "days ago") {
		t.Errorf("the note does not say how long ago that was: %q", note)
	}
}
