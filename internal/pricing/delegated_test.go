package pricing

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A turn on an agent somebody signed in to themselves has no price, and the two ways of getting one
// wrong are both wrong.
//
// Free would say the turn cost nothing, which is the claim a local model makes and this is not: a Max
// plan is paid for, monthly, and these tokens are metered against its limits. The list price would be
// a real number about an invoice nobody receives. So the answer is that there is no figure, with the
// reason attached, which is the third state Source exists to express.

func TestATurnOnSomebodyElsesAgentIsUnpricedRatherThanFree(t *testing.T) {
	t.Parallel()

	id := ModelID{Provider: core.ProviderAnthropic, Model: "claude-sonnet-5", Delegated: true}
	usage, reason := Apply(id, core.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})

	if usage.CostKnown {
		t.Errorf("a delegated turn was priced at $%v", usage.CostUSD)
	}
	if usage.CostUSD != 0 {
		t.Errorf("a delegated turn carries a cost of %v", usage.CostUSD)
	}
	if !strings.Contains(reason, "metered against that plan's limits") {
		t.Errorf("the missing figure was not explained: %q", reason)
	}
	if strings.Contains(reason, "canopy keys rate") {
		t.Error("the reason offers to fix this with a rate, and a per-token rate cannot describe " +
			"a plan billed monthly")
	}
}

// The one case where somebody's own figure does not win, which is worth its own test because the
// rule everywhere else in this package is that it does.
func TestARateSomebodySetOnADelegatedCredentialStillDoesNotProduceAFigure(t *testing.T) {
	t.Parallel()

	id := ModelID{
		Provider:  core.ProviderAnthropic,
		Model:     "claude-sonnet-5",
		Delegated: true,
		UserRate:  core.KeyRate{InputPerMTok: 3, OutputPerMTok: 15},
	}
	usage, _ := Apply(id, core.Usage{InputTokens: 1_000_000})

	if usage.CostKnown {
		t.Errorf("a rate on a delegated credential produced $%v for a turn nobody is billed "+
			"per token for", usage.CostUSD)
	}
}

func TestTheSameModelOnAPastedCredentialIsStillPriced(t *testing.T) {
	t.Parallel()

	id := ModelID{Provider: core.ProviderAnthropic, Model: "claude-sonnet-5"}
	usage, reason := Apply(id, core.Usage{OutputTokens: 1_000_000})

	if !usage.CostKnown {
		t.Fatalf("a pasted Anthropic credential stopped being priced: %q", reason)
	}
	if usage.CostUSD != 15 {
		t.Errorf("a million output tokens cost $%v", usage.CostUSD)
	}
}

func TestCacheSavingsAreNotReportedForATurnWithNoPrice(t *testing.T) {
	t.Parallel()

	id := ModelID{Provider: core.ProviderAnthropic, Model: "claude-sonnet-5", Delegated: true}
	if _, ok := Saving(id, core.Usage{CacheReadTokens: 1_000_000}); ok {
		t.Error("a delegated turn reported what caching saved, which is a figure in dollars and " +
			"therefore the same claim the cost line is not allowed to make")
	}
}
