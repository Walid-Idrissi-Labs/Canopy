package pricing

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

func TestCostIsPerMillionTokens(t *testing.T) {
	rates := Rates{Input: 5, Output: 25}
	got := rates.Cost(core.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	if !closeTo(got, 30) {
		t.Errorf("cost = %v, want 30", got)
	}

	// The realistic case: a few thousand tokens should come out in cents, not dollars.
	got = rates.Cost(core.Usage{InputTokens: 2000, OutputTokens: 500})
	if !closeTo(got, 0.0225) {
		t.Errorf("cost = %v, want 0.0225", got)
	}
}

// A cache read count that is priced at the full input rate would show caching saving nothing, which
// is the opposite of what it is for.
func TestCachedTokensArePricedDifferently(t *testing.T) {
	rates, ok := Lookup(ModelID{Provider: core.ProviderAnthropic, Model: "claude-opus-5"})
	if !ok {
		t.Fatal("the default model should be priced")
	}
	if rates.CacheRead >= rates.Input {
		t.Errorf("a cache read costs %v against %v input, so caching would save nothing",
			rates.CacheRead, rates.Input)
	}
	if rates.CacheWrite <= rates.Input {
		t.Errorf("a cache write costs %v against %v input, so writing a cache would be free",
			rates.CacheWrite, rates.Input)
	}
}

// Pinned and floating ids are the same model at the same price.
func TestPinnedModelIDsFindTheirRate(t *testing.T) {
	floating, ok := Lookup(ModelID{Provider: core.ProviderAnthropic, Model: "claude-opus-4-6"})
	if !ok {
		t.Fatal("expected a rate")
	}
	for _, name := range []string{"claude-opus-4-6-20260101", "claude-opus-4-6-latest", "CLAUDE-OPUS-4-6"} {
		pinned, ok := Lookup(ModelID{Provider: core.ProviderAnthropic, Model: name})
		if !ok {
			t.Errorf("%q was not priced, though it is the same model", name)
			continue
		}
		if pinned != floating {
			t.Errorf("%q priced at %+v, want %+v", name, pinned, floating)
		}
	}
}

// An empty model means the client falls back to its own default, so that is what gets billed.
func TestEmptyModelPricesTheDefault(t *testing.T) {
	empty, ok := Lookup(ModelID{Provider: core.ProviderAnthropic})
	if !ok {
		t.Fatal("an unset model should price the client default rather than read as unknown")
	}
	named, _ := Lookup(ModelID{Provider: core.ProviderAnthropic, Model: "claude-opus-5"})
	if empty != named {
		t.Errorf("empty model priced at %+v, want the default at %+v", empty, named)
	}
}

// The distinction the whole package exists for. Free and unpriced are different claims.
func TestUnknownModelIsUnpricedNotFree(t *testing.T) {
	usage := core.Usage{InputTokens: 1000, OutputTokens: 1000}

	priced, reason := Apply(ModelID{Provider: core.ProviderAnthropic, Model: "claude-from-2029"}, usage)
	if priced.CostKnown {
		t.Error("a model with no rate must not report a known cost")
	}
	if priced.CostUSD != 0 {
		t.Errorf("an unpriced turn should carry no figure, got %v", priced.CostUSD)
	}
	if reason == "" {
		t.Error("an unknown cost needs a reason, otherwise it reads as a broken tool")
	}

	free, reason := Apply(ModelID{
		Provider: core.ProviderOpenAICompatible,
		Host:     "localhost",
		Model:    "qwen3:30b",
	}, usage)
	if !free.CostKnown {
		t.Error("a model on this machine is genuinely free, which is a known cost")
	}
	if free.CostUSD != 0 {
		t.Errorf("a local model billed %v", free.CostUSD)
	}
	if reason != "" {
		t.Errorf("a known cost needs no explanation, got %q", reason)
	}
}

func TestLocalHostsAreRecognisedFromABaseURL(t *testing.T) {
	local := []string{
		"http://localhost:11434/v1",
		"http://127.0.0.1:1234/v1",
		"http://host.docker.internal:11434/v1",
	}
	for _, baseURL := range local {
		id := NewModelID(core.ProviderOpenAICompatible, baseURL, "any")
		if _, ok := Lookup(id); !ok {
			t.Errorf("%s was not recognised as this machine, so a local model would look unpriced", baseURL)
		}
	}

	remote := NewModelID(core.ProviderOpenAICompatible, "https://integrate.api.nvidia.com/v1", "any")
	if _, ok := Lookup(remote); ok {
		t.Error("a hosted endpoint must not be priced as free")
	}
}

// Pricing a gateway by model name would be a guess presented as a fact, since the gateway sets the
// price and there are many gateways.
func TestHostedCompatibleEndpointsAreNotGuessedAt(t *testing.T) {
	id := NewModelID(core.ProviderOpenAICompatible,
		"https://openrouter.ai/api/v1", "anthropic/claude-opus-5")
	_, reason := Apply(id, core.Usage{InputTokens: 100})
	if reason == "" {
		t.Fatal("a hosted gateway should report why it is unpriced, not price by model name")
	}
	if !strings.Contains(reason, "openrouter.ai") {
		t.Errorf("the reason should name the endpoint, got %q", reason)
	}
}

func TestStalenessIsAnnounced(t *testing.T) {
	fresh := AsOf.Add(24 * time.Hour)
	if Stale(fresh) {
		t.Error("a day old table is not stale")
	}
	if note := StalenessNote(fresh); note != "" {
		t.Errorf("a fresh table needs no note, got %q", note)
	}

	old := AsOf.Add(MaxAge + 24*time.Hour)
	if !Stale(old) {
		t.Error("a table past MaxAge is stale")
	}
	note := StalenessNote(old)
	if note == "" {
		t.Fatal("a stale table must say so, otherwise old numbers read as current")
	}
	if !strings.Contains(note, AsOf.Format("2006-01-02")) {
		t.Errorf("the note should carry the date the numbers were checked, got %q", note)
	}
}

// The table is only worth having if it is right, and the cheapest way to be wrong is a transposed
// column.
func TestEveryRateIsCoherent(t *testing.T) {
	for model := range anthropicRates {
		rates, ok := lookupAnthropic(model)
		if !ok {
			t.Fatalf("%s is in the table but does not look up", model)
		}
		if rates.Input <= 0 || rates.Output <= 0 {
			t.Errorf("%s has a zero rate (%+v), which would read as free", model, rates)
		}
		if rates.Output <= rates.Input {
			t.Errorf("%s costs %v out against %v in, which is the wrong way round for every "+
				"model in this family", model, rates.Output, rates.Input)
		}
	}
}

// Caching is only worth having if you can see it working, and the honest version of "see it
// working" includes the turn where it costs money.
func TestCacheSavingIsNetAndCanBeNegative(t *testing.T) {
	id := ModelID{Provider: core.ProviderAnthropic, Model: "claude-opus-5"}

	// The first turn writes the cache and pays the write premium for nothing yet.
	first, ok := Saving(id, core.Usage{InputTokens: 100, CacheWriteTokens: 10_000})
	if !ok {
		t.Fatal("a turn that touched the cache on a priced model has a knowable saving")
	}
	if first >= 0 {
		t.Errorf("filling a cache costs more than plain input, so the first turn is a loss, got %v", first)
	}

	// Every turn after reads it back at a fraction of the input rate.
	later, ok := Saving(id, core.Usage{InputTokens: 100, CacheReadTokens: 10_000})
	if !ok {
		t.Fatal("expected a knowable saving")
	}
	if later <= 0 {
		t.Errorf("reading a cache is cheaper than resending the tokens, got %v", later)
	}
	// Which is why caching is worth doing at all: a single read more than repays the premium paid
	// to write the same tokens, so a session pays for the cache on its second turn.
	if later <= -first {
		t.Errorf("a read saved %v against a write premium of %v, so caching would never pay back",
			later, -first)
	}
}

func TestSavingIsSilentWhenItCannotBeKnown(t *testing.T) {
	// Nothing cached, nothing to say.
	if _, ok := Saving(ModelID{Provider: core.ProviderAnthropic, Model: "claude-opus-5"},
		core.Usage{InputTokens: 100}); ok {
		t.Error("a turn that cached nothing has no saving to report")
	}

	// No rate, so no counterfactual to compare against.
	unpriced := NewModelID(core.ProviderOpenAICompatible, "https://openrouter.ai/api/v1", "x")
	if _, ok := Saving(unpriced, core.Usage{CacheReadTokens: 10_000}); ok {
		t.Error("without a rate there is nothing to compare the saving against")
	}

	// Free is free either way, so a saving figure would be noise.
	local := NewModelID(core.ProviderOpenAICompatible, "http://localhost:11434/v1", "x")
	if _, ok := Saving(local, core.Usage{CacheReadTokens: 10_000}); ok {
		t.Error("a saving on something that was already free is not worth printing")
	}
}

// Canopy will never hold rates for every gateway in this family and should not pretend to. The
// person who signed up for one knows what they pay, so this turns "we cannot price this" into
// "tell us once".
func TestAUserRateProducesAFigureAndIsLabelledAsTheirs(t *testing.T) {
	id := NewModelID(core.ProviderOpenAICompatible,
		"https://integrate.api.nvidia.com/v1", "minimaxai/minimax-m2.7").
		WithUserRate(core.KeyRate{InputPerMTok: 0.3, OutputPerMTok: 1.2})

	usage, note := Apply(id, core.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	if !usage.CostKnown {
		t.Fatal("a rate the user supplied should produce a figure")
	}
	if !closeTo(usage.CostUSD, 1.5) {
		t.Errorf("cost = %v, want 1.5", usage.CostUSD)
	}
	// The point of the dated table is that Canopy is honest about where a number came from, and
	// quietly absorbing somebody's own figure into it would throw that away.
	if !strings.Contains(note, "your own") {
		t.Errorf("note = %q, want the figure attributed to the user", note)
	}
}

// They are the one being billed, so theirs is the answer to the question actually being asked,
// which is "what will this cost me" rather than "what is the list price".
func TestAUserRateWinsOverTheTable(t *testing.T) {
	id := ModelID{Provider: core.ProviderAnthropic, Model: "claude-opus-5"}

	published, _ := Apply(id, core.Usage{InputTokens: 1_000_000})
	theirs, note := Apply(id.WithUserRate(core.KeyRate{InputPerMTok: 1, OutputPerMTok: 2}),
		core.Usage{InputTokens: 1_000_000})

	if !closeTo(theirs.CostUSD, 1) {
		t.Errorf("cost = %v, want the user's own rate of 1", theirs.CostUSD)
	}
	if closeTo(theirs.CostUSD, published.CostUSD) {
		t.Error("the table won over the user's own rate")
	}
	if !strings.Contains(note, "your own") {
		t.Errorf("note = %q, want the figure attributed to the user", note)
	}
}

// Most gateways in this family either do not cache or do not say what they charge for it, so
// assuming a discount nobody promised would understate the bill.
func TestAnUnstatedCacheRateIsAssumedToBeFullPrice(t *testing.T) {
	rates, ok := Lookup(ModelID{Provider: core.ProviderOpenAICompatible, Host: "example.com"}.
		WithUserRate(core.KeyRate{InputPerMTok: 2, OutputPerMTok: 8}))
	if !ok {
		t.Fatal("expected a rate")
	}
	if rates.CacheRead != rates.Input {
		t.Errorf("cached tokens priced at %v against %v input, which claims a discount nobody gave",
			rates.CacheRead, rates.Input)
	}
}

// A rate of zero is a claim, not an absence, and the two need different words on screen.
func TestAZeroRateIsRefused(t *testing.T) {
	if err := (core.KeyRate{}).Validate(); err == nil {
		t.Error("a rate of zero would report every turn as free")
	}
	if err := (core.KeyRate{InputPerMTok: -1, OutputPerMTok: 2}).Validate(); err == nil {
		t.Error("a negative rate should be refused")
	}
	if err := (core.KeyRate{InputPerMTok: 0, OutputPerMTok: 2}).Validate(); err != nil {
		t.Errorf("an output only rate is legitimate, some gateways bill that way: %v", err)
	}
}
