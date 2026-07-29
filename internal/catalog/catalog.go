// Package catalog is the answer to "what can this key run".
//
// The knowledge was already in the building twice, as the pricing table and the context window
// table, and neither exported it, so the keys screen edited a model as free text and dispatch could
// only match credential names. This is the one place that says which models a provider is known to
// have, and it is spent by the keys screen, the CLI, the model picker and the spawn tool.
//
// Two rules, both from D-46, and the second is what makes the first safe to be wrong about:
//
//   - The list never gates. Every caller must still accept a model that is on no list, because the
//     day this file is out of date is the day it would block the one model somebody actually wants.
//   - The list carries its date, the way the pricing table does. A lineup that has gone stale says
//     so rather than presenting itself as current.
package catalog

import (
	"net/url"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// AsOf is when these lineups were last checked against what the providers publish.
//
// Update it whenever a model is added or removed below. Leaving it while changing the list is worse
// than changing nothing, because it launders a guess as a checked fact.
var AsOf = time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)

// MaxAge is how long a lineup is presented without comment.
//
// Longer than the pricing table's ninety days, because a model that shipped is still there a year
// later while its price may not be. Not an expiry either: a stale list still names models that work,
// so it keeps being offered and only stops calling itself current.
const MaxAge = 180 * 24 * time.Hour

// Stale reports whether the list is old enough that saying so is honest.
func Stale(now time.Time) bool { return now.Sub(AsOf) > MaxAge }

// Model is one thing a key can be pointed at.
//
// Two fields because a provider id and a readable name are not always the same string.
// "claude-sonnet-5" reads fine and "minimaxai/minimax-m2.7" does not, so a name can be attached to
// the ones that need one. The id is what goes on the wire either way.
type Model struct {
	ID   string
	Name string
}

// Label is what to show a person: the name where there is one, the id otherwise.
func (m Model) Label() string {
	if m.Name != "" {
		return m.Name
	}
	return m.ID
}

// Named reports whether this entry has a name worth showing beside its id.
func (m Model) Named() bool { return m.Name != "" && m.Name != m.ID }

// anthropicModels are the Anthropic models Canopy holds a price for.
//
// Newest first within each family, which is not cosmetic: a bare family word means the newest member
// of that family, and this order is how that is decided. Adding a model means putting it in front of
// its siblings, not at the end of the list.
//
// The ids are the same eight the pricing table knows, and a test in internal/pricing fails if the
// two ever disagree. Two hand kept lists of the same models is one list that goes stale silently.
var anthropicModels = []Model{
	{ID: "claude-fable-5", Name: "Claude Fable 5"},
	{ID: "claude-opus-5", Name: "Claude Opus 5"},
	{ID: "claude-opus-4-8", Name: "Claude Opus 4.8"},
	{ID: "claude-opus-4-7", Name: "Claude Opus 4.7"},
	{ID: "claude-opus-4-6", Name: "Claude Opus 4.6"},
	{ID: "claude-sonnet-5", Name: "Claude Sonnet 5"},
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
	{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5"},
}

// openAIModels are OpenAI's own lineup, and only OpenAI's.
//
// Short on purpose. This list is offered for exactly one host, because the OpenAI compatible family
// is whatever endpoint a key was pointed at, and offering GPT ids to somebody's local gateway would
// be inviting them to pick a model that endpoint has never heard of.
var openAIModels = []Model{
	{ID: "gpt-5.2", Name: "GPT-5.2"},
	{ID: "gpt-5.2-pro", Name: "GPT-5.2 Pro"},
	{ID: "gpt-5.1", Name: "GPT-5.1"},
	{ID: "gpt-5.1-codex-max", Name: "GPT-5.1 Codex Max"},
}

// openAIHost is the one OpenAI compatible endpoint whose lineup Canopy knows.
const openAIHost = "api.openai.com"

// For returns the models this provider is known to run at this endpoint.
//
// Nil is a real answer and the common one: an OpenAI compatible key pointed anywhere but OpenAI is
// pointed at a gateway whose lineup nobody here knows, and inventing one would be worse than saying
// nothing. What its owner adds by hand sits beside this, which is the keys store's half.
//
// The returned slice is a copy, so a caller that sorts or appends to it cannot edit the table
// underneath every other caller.
func For(provider core.Provider, baseURL string) []Model {
	switch provider {
	case core.ProviderAnthropic:
		return append([]Model(nil), anthropicModels...)

	case core.ProviderOpenAICompatible:
		if hostOf(baseURL) == openAIHost {
			return append([]Model(nil), openAIModels...)
		}
		return nil

	default:
		return nil
	}
}

// hostOf is the host part of a base URL.
//
// Its own copy rather than the pricing table's, because catalog must not import pricing: the test
// that keeps the two model lists in step lives in pricing and imports this, and a package cannot be
// on both ends of that.
func hostOf(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}
