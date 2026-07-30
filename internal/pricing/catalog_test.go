package pricing

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The two lists of Anthropic models have to name the same models.
//
// There are two of them because they answer different questions: this package says what a model
// costs and the catalog says what a key can be pointed at, and neither is the other's business. What
// they must not do is disagree, since a model in one and not the other is either a model somebody
// can pick and cannot be told the price of, or a price for something nothing offers.
//
// The test lives here rather than in the catalog because the rates map is unexported, and exporting
// it so a test elsewhere could read it would be adding production API for a test's convenience.
func TestTheCatalogAndThePriceTableNameTheSameAnthropicModels(t *testing.T) {
	offered := make(map[string]bool)
	for _, model := range catalog.For(core.ProviderAnthropic, "") {
		offered[model.ID] = true
	}

	for id := range anthropicRates {
		if !offered[id] {
			t.Errorf("%s has a price here and is not in internal/catalog, so nothing offers it", id)
		}
	}
	for id := range offered {
		if _, priced := anthropicRates[id]; !priced {
			t.Errorf("internal/catalog offers %s and this table has no price for it, "+
				"so picking it would report every turn as unpriced", id)
		}
	}
}

// And every offered model has to be findable by the lookup itself, not only present in the map. A
// pinned or oddly spelled id in the catalog would pass the check above and still price as unknown.
func TestEveryOfferedAnthropicModelIsPriced(t *testing.T) {
	for _, model := range catalog.For(core.ProviderAnthropic, "") {
		if _, ok := Lookup(ModelID{Provider: core.ProviderAnthropic, Model: model.ID}); !ok {
			t.Errorf("%s is offered and prices as unknown", model.ID)
		}
	}
}
