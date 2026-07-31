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

// Source says where a price came from.
//
// Three states, all distinguishable on screen, because Canopy is a tool for telling which of several
// things is actually true and a cost figure it cannot stand behind is exactly the kind of confident
// wrong answer the rest of the design avoids.
type Source string

const (
	// SourceNone means no price is known.
	SourceNone Source = ""
	// SourceTable means the price came from the dated table in this package.
	SourceTable Source = "table"
	// SourceUser means the user told us what this credential charges.
	SourceUser Source = "user"
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

	// UserRate is what the credential's owner said it charges, and it wins over the table.
	//
	// Wins rather than defers, because they are the one being billed. Where they have set a rate
	// and Canopy also holds one, theirs is the answer to the question actually being asked, which
	// is "what will this cost me" rather than "what is the list price".
	UserRate core.KeyRate

	// Delegated marks a turn that ran on somebody else's agent, on a subscription the user already
	// pays for, and it makes the turn unpriceable rather than free.
	//
	// The two are different claims and only one of them is true. The tokens on such a turn are real
	// and are counted, and the list price of those tokens is a genuine number, but it is a number
	// about an API bill that nobody is going to receive: a Max or Pro plan is charged monthly and
	// these tokens are metered against its limits. Printing the list price would be arithmetic
	// presented as somebody's spend, and printing zero would say the turn was free, which is the
	// claim Free exists to make and this is not. So there is no figure, and the reason is said.
	//
	// It beats UserRate as well as the table, which is the one case where somebody's own figure does
	// not win. A per-million-token rate cannot describe a plan billed monthly whoever supplied it,
	// and the reason below says so rather than quietly ignoring what they set.
	Delegated bool
}

// NewModelID builds an identifier from what a credential and a request already carry.
func NewModelID(provider core.Provider, baseURL, model string) ModelID {
	return ModelID{Provider: provider, Host: hostOf(baseURL), Model: model}
}

// WithUserRate attaches a rate the user supplied.
func (id ModelID) WithUserRate(rate core.KeyRate) ModelID {
	id.UserRate = rate
	return id
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
	rates, source := lookupWithSource(id)
	return rates, source != SourceNone
}

// lookupWithSource is Lookup, plus where the answer came from.
func lookupWithSource(id ModelID) (Rates, Source) {
	if id.Delegated {
		return Rates{}, SourceNone
	}
	if !id.UserRate.IsZero() {
		cacheRead := id.UserRate.CacheReadPerMTok
		if cacheRead == 0 {
			// Same as input, which is the honest default. Most gateways in this family either do
			// not cache or do not say what they charge for it, and assuming a discount nobody
			// promised would understate the bill.
			cacheRead = id.UserRate.InputPerMTok
		}
		return Rates{
			Input:      id.UserRate.InputPerMTok,
			Output:     id.UserRate.OutputPerMTok,
			CacheRead:  cacheRead,
			CacheWrite: id.UserRate.InputPerMTok,
		}, SourceUser
	}
	if rates, ok := lookupTable(id); ok {
		return rates, SourceTable
	}
	return Rates{}, SourceNone
}

func lookupTable(id ModelID) (Rates, bool) {
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

// CacheSaving is what caching changed about a turn's bill, positive when it saved money.
//
// Net, and it can be negative. A cache write costs more than plain input, so the turn that fills a
// cache genuinely pays a premium and this says so. The turn that reads it back pays a tenth. Netting
// them per turn rather than reporting only the reads is the honest version: it makes the first turn
// of a session look like the investment it is, instead of hiding the cost and showing only the
// return.
func (r Rates) CacheSaving(usage core.Usage) float64 {
	const perMillion = 1_000_000.0
	saved := float64(usage.CacheReadTokens) * (r.Input - r.CacheRead)
	premium := float64(usage.CacheWriteTokens) * (r.CacheWrite - r.Input)
	return (saved - premium) / perMillion
}

// Saving reports what caching did to a turn's bill, and whether that is knowable.
//
// Unknowable is the common case outside Anthropic: without a rate there is no counterfactual to
// compare against, so the answer is silence rather than zero.
func Saving(id ModelID, usage core.Usage) (float64, bool) {
	if usage.CacheReadTokens == 0 && usage.CacheWriteTokens == 0 {
		return 0, false
	}
	rates, ok := Lookup(id)
	if !ok || (rates == Free) {
		return 0, false
	}
	return rates.CacheSaving(usage), true
}

// Apply fills in the cost fields on a usage record.
//
// The second return is the note to show beside the figure: why there is none, or whose figure it is.
// Saying "cost unknown" with no reason invites the reading that Canopy is broken, when usually the
// answer is that nobody has told it what this endpoint charges. And a price the user supplied has
// to be labelled as theirs, or the dated table's whole purpose is lost.
func Apply(id ModelID, usage core.Usage) (core.Usage, string) {
	rates, source := lookupWithSource(id)
	if source == SourceNone {
		return usage, unpricedReason(id)
	}

	usage.CostUSD = rates.Cost(usage)
	usage.CostKnown = true

	if source == SourceUser {
		return usage, "priced at your own rate for this key"
	}
	return usage, ""
}

func unpricedReason(id ModelID) string {
	if id.Delegated {
		return "this turn ran on an agent you signed in to yourself, so its tokens are metered " +
			"against that plan's limits rather than billed per token. Canopy has no figure to show " +
			"and will not invent one"
	}
	switch id.Provider {
	case core.ProviderOpenAICompatible:
		where := id.Host
		if where == "" {
			where = "this endpoint"
		}
		return fmt.Sprintf(
			"no rate recorded for %s. The same model costs different amounts through different "+
				"gateways, so Canopy does not guess. Set yours with `canopy keys rate <name>`", where)
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
