package tui

import (
	"strings"
	"testing"
)

type fakeCosts struct {
	history CostOutcomeHistory
	err     error
}

func (f fakeCosts) CostOutcomes() (CostOutcomeHistory, error) { return f.history, f.err }

func TestCostComparisonRefusesAnUndersizedSample(t *testing.T) {
	lines := costOutcomeLines(CostOutcomeHistory{Samples: []CostOutcome{
		{Model: "cheap", CostUSD: .01, CostKnown: true, Passing: 1, Required: 1},
		{Model: "cheap", CostUSD: .02, CostKnown: true, Passing: 1, Required: 1},
		{Model: "expensive", CostUSD: .10, CostKnown: true, Passing: 1, Required: 1},
	}})
	body := strings.Join(lines, "\n")
	if !strings.Contains(body, "3 exact samples") || !strings.Contains(body, "no conclusion") {
		t.Errorf("undersized history was not named and refused:\n%s", body)
	}
	if !strings.Contains(body, "at least 3") && !strings.Contains(body, "needs at least 3") {
		t.Errorf("the threshold is hidden:\n%s", body)
	}
}

func TestCostComparisonNamesTheSampleAndDoesNotClaimCausation(t *testing.T) {
	var samples []CostOutcome
	for range 3 {
		samples = append(samples,
			CostOutcome{Model: "cheap", CostUSD: .02, CostKnown: true, Passing: 1, Required: 2},
			CostOutcome{Model: "expensive", CostUSD: .20, CostKnown: true, Passing: 2, Required: 2},
		)
	}
	body := strings.Join(costOutcomeLines(CostOutcomeHistory{Samples: samples}), "\n")
	for _, want := range []string{"6 exact samples", "costlier model expensive", "association", "not proof"} {
		if !strings.Contains(body, want) {
			t.Errorf("comparison does not contain %q:\n%s", want, body)
		}
	}
}

func TestUnknownCostAndCurrentUnrankedEvidenceAreExcludedVisibly(t *testing.T) {
	body := strings.Join(costOutcomeLines(CostOutcomeHistory{
		Samples: []CostOutcome{
			{Model: "gateway", CostKnown: false, Passing: 1, Required: 1},
		},
		CurrentUnranked:   2,
		CurrentMixedModel: 1,
		CurrentNoUsage:    1,
	}), "\n")
	for _, want := range []string{"1 samples excluded", "cost is unknown", "2 current agents excluded",
		"stale or unknown", "more than one model", "no settled single-model attribution"} {
		if !strings.Contains(body, want) {
			t.Errorf("exclusion does not contain %q:\n%s", want, body)
		}
	}
}

func TestCostPaneIsPartOfTheReviewCycle(t *testing.T) {
	model, _ := loaded(t)
	model.SetCostOutcomes(fakeCosts{})
	model = press(model, "tab", "tab")

	if model.Pane() != "cost versus outcome" {
		t.Fatalf("two tabs landed on %q", model.Pane())
	}
	if !strings.Contains(model.Body(), "this project only") {
		t.Errorf("cost pane does not state its scope:\n%s", model.Body())
	}
}
