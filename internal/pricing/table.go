// Package pricing turns token counts into money.
//
// The table is dated on purpose. Prices change, this is a snapshot rather than a feed, and a stale
// snapshot is a way to put a confident wrong number in front of somebody who is deciding whether to
// keep an agent running. So the date travels with the numbers, the interface says how old they are
// once they pass MaxAge, and anything not in the table reports as unpriced rather than as free.
//
// "Unpriced" and "free" are different claims and Canopy makes both, separately. A local model
// really is free and says so. An endpoint we have no rate for says we do not know, and why.
package pricing

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// AsOf is when these numbers were last checked against published pricing.
//
// Update this whenever you touch a rate. Leaving it while changing a number is worse than changing
// nothing, because it launders a guess as a verified figure.
var AsOf = time.Date(2026, time.June, 24, 0, 0, 0, 0, time.UTC)

// MaxAge is how long the table is trusted without comment.
//
// Not an expiry. An old table still gives a far better answer than no answer, so the numbers keep
// being used; the interface just stops presenting them as current.
const MaxAge = 90 * 24 * time.Hour

// Rates are dollars per million tokens.
type Rates struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
}

// Free is the rate for a model running on the user's own machine.
//
// Genuinely zero rather than unknown, which is the distinction CostKnown exists to make. Electricity
// is a real cost and this does not account for it; nothing is billed, which is what the figure on
// screen is about.
var Free = Rates{}

// Anthropic cache multipliers, applied to the input rate rather than written out per model.
//
// Derived rather than listed because they are properties of the API, not of any one model, and a
// hand copied cache column is somewhere for a typo to hide.
const (
	anthropicCacheReadMultiplier  = 0.1
	anthropicCacheWriteMultiplier = 1.25
)

// ModelID is everything needed to find a price.
//
// Host is part of the key because a model name alone does not determine a price for the
// OpenAI compatible family. The same weights reached through two gateways cost two different
// amounts, so pricing by name would be a guess dressed as a fact.
type ModelID struct {
	Provider core.Provider
	Host     string
	Model    string
}

// NewModelID builds an identifier from what a credential and a request already carry.
func NewModelID(provider core.Provider, baseURL, model string) ModelID {
	return ModelID{Provider: provider, Host: hostOf(baseURL), Model: model}
}

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

// localHosts are endpoints that mean "this machine", where nothing is billed.
var localHosts = map[string]bool{
	"localhost":            true,
	"127.0.0.1":            true,
	"::1":                  true,
	"0.0.0.0":              true,
	"host.docker.internal": true,
}

// anthropicRates are Anthropic first party rates, dollars per million tokens.
//
// First party only. The same models on Bedrock and Vertex are partner operated and priced
// separately, and Canopy has no way to reach them yet, so listing one price for both would be
// wrong for whichever one you were actually using.
//
// Where a published introductory rate is lower than the standard rate, the standard rate is used.
// Overstating cost is the safer error: it makes a session look more expensive than it was, which
// nobody acts on badly, whereas understating it hides spend that is really happening.
var anthropicRates = map[string]Rates{
	"claude-fable-5":    {Input: 10, Output: 50},
	"claude-opus-5":     {Input: 5, Output: 25},
	"claude-opus-4-8":   {Input: 5, Output: 25},
	"claude-opus-4-7":   {Input: 5, Output: 25},
	"claude-opus-4-6":   {Input: 5, Output: 25},
	"claude-sonnet-5":   {Input: 3, Output: 15},
	"claude-sonnet-4-6": {Input: 3, Output: 15},
	"claude-haiku-4-5":  {Input: 1, Output: 5},
}

// dateSuffix matches the pinned build suffix on a model id.
//
// Pinned and floating ids are the same model at the same price, so `claude-opus-4-6-20260101` has
// to find the `claude-opus-4-6` row rather than falling through to unpriced.
var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// Lookup finds the rate for a model.
//
// The second return distinguishes a rate we hold from one we do not. A caller that ignores it and
// reads the zero Rates as free would report every unknown model as costing nothing, which is the
// exact failure this signature exists to prevent.
func Lookup(id ModelID) (Rates, bool) {
	switch id.Provider {
	case core.ProviderAnthropic:
		return lookupAnthropic(id.Model)

	case core.ProviderOpenAICompatible:
		// A model on this machine is free whatever it is called. Nothing else in this family is
		// priced by name, because the gateway sets the price and there are many gateways.
		if localHosts[id.Host] {
			return Free, true
		}
		return Rates{}, false

	default:
		return Rates{}, false
	}
}

func lookupAnthropic(model string) (Rates, bool) {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		// An empty model means the client will use its own default, so price that.
		name = "claude-opus-5"
	}
	name = strings.TrimSuffix(name, "-latest")
	name = dateSuffix.ReplaceAllString(name, "")

	rates, ok := anthropicRates[name]
	if !ok {
		return Rates{}, false
	}
	rates.CacheRead = rates.Input * anthropicCacheReadMultiplier
	rates.CacheWrite = rates.Input * anthropicCacheWriteMultiplier
	return rates, true
}

// Cost prices a turn.
func (r Rates) Cost(usage core.Usage) float64 {
	const perMillion = 1_000_000.0
	return (float64(usage.InputTokens)*r.Input +
		float64(usage.OutputTokens)*r.Output +
		float64(usage.CacheReadTokens)*r.CacheRead +
		float64(usage.CacheWriteTokens)*r.CacheWrite) / perMillion
}

// Apply fills in the cost fields on a usage record.
//
// The second return is why the cost is unknown, empty when it is known. Saying "cost unknown" with
// no reason invites the reading that Canopy is broken, when usually the answer is that nobody has
// told it what this endpoint charges.
func Apply(id ModelID, usage core.Usage) (core.Usage, string) {
	rates, ok := Lookup(id)
	if !ok {
		return usage, unpricedReason(id)
	}
	usage.CostUSD = rates.Cost(usage)
	usage.CostKnown = true
	return usage, ""
}

func unpricedReason(id ModelID) string {
	switch id.Provider {
	case core.ProviderOpenAICompatible:
		where := id.Host
		if where == "" {
			where = "this endpoint"
		}
		return fmt.Sprintf(
			"no rate recorded for %s. The same model costs different amounts through different "+
				"gateways, so Canopy does not guess", where)
	case core.ProviderAnthropic:
		return fmt.Sprintf("no rate recorded for model %q", id.Model)
	default:
		return fmt.Sprintf("no rates recorded for provider %q", id.Provider)
	}
}

// Age is how long ago the table was checked.
func Age(now time.Time) time.Duration { return now.Sub(AsOf) }

// Stale reports whether the numbers are old enough to need saying so.
func Stale(now time.Time) bool { return Age(now) > MaxAge }

// StalenessNote is the line the interface shows beside an old price, empty when the table is fresh.
func StalenessNote(now time.Time) string {
	if !Stale(now) {
		return ""
	}
	days := int(Age(now).Hours() / 24)
	return fmt.Sprintf("prices last checked %s, %d days ago, so treat them as approximate",
		AsOf.Format("2006-01-02"), days)
}
