package tui

import (
	"fmt"
	"sort"
)

// CostOutcome is one project-local observation with exact verification evidence.
type CostOutcome struct {
	Model     string
	CostUSD   float64
	CostKnown bool
	Passing   int
	Required  int
}

// CostOutcomeHistory is what the comparison screen reads.
type CostOutcomeHistory struct {
	Samples           []CostOutcome
	CurrentUnranked   int
	CurrentMixedModel int
	CurrentNoUsage    int
}

// CostOutcomeSource supplies project-scoped historical observations.
type CostOutcomeSource interface {
	CostOutcomes() (CostOutcomeHistory, error)
}

type modelOutcome struct {
	model    string
	costs    []float64
	passing  int
	required int
	green    int
}

func costOutcomeLines(history CostOutcomeHistory) []string {
	groups := make(map[string]*modelOutcome)
	unknownCost := 0
	for _, sample := range history.Samples {
		if !sample.CostKnown {
			unknownCost++
			continue
		}
		group := groups[sample.Model]
		if group == nil {
			group = &modelOutcome{model: sample.Model}
			groups[sample.Model] = group
		}
		group.costs = append(group.costs, sample.CostUSD)
		group.passing += sample.Passing
		group.required += sample.Required
		if sample.Passing == sample.Required {
			group.green++
		}
	}

	ordered := make([]*modelOutcome, 0, len(groups))
	for _, group := range groups {
		sort.Float64s(group.costs)
		ordered = append(ordered, group)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if median(ordered[i].costs) != median(ordered[j].costs) {
			return median(ordered[i].costs) < median(ordered[j].costs)
		}
		return ordered[i].model < ordered[j].model
	})

	lines := []string{styleHeader.Render("cost versus verified outcome — this project only")}
	exact := 0
	for _, group := range ordered {
		exact += len(group.costs)
		passRate := 100 * float64(group.passing) / float64(group.required)
		lines = append(lines, fmt.Sprintf(
			"  %s  %d samples  median $%.3f  %.0f%% required tests passing  %d/%d green",
			group.model, len(group.costs), median(group.costs), passRate, group.green, len(group.costs)))
	}

	if len(ordered) == 0 {
		lines = append(lines, styleMuted.Render("  no exact cost-and-verification samples yet"))
	}
	lines = append(lines, "", styleReason.Render(costConclusion(ordered, exact)))
	if unknownCost > 0 {
		lines = append(lines, styleCaveat.Render(fmt.Sprintf(
			"%d samples excluded because their provider cost is unknown", unknownCost)))
	}
	if history.CurrentUnranked > 0 {
		lines = append(lines, styleCaveat.Render(fmt.Sprintf(
			"%d current agents excluded because their verification is stale or unknown",
			history.CurrentUnranked)))
	}
	if history.CurrentMixedModel > 0 {
		lines = append(lines, styleCaveat.Render(fmt.Sprintf(
			"%d current agents excluded because their session used more than one model",
			history.CurrentMixedModel)))
	}
	if history.CurrentNoUsage > 0 {
		lines = append(lines, styleCaveat.Render(fmt.Sprintf(
			"%d current agents excluded because they have no settled single-model attribution",
			history.CurrentNoUsage)))
	}
	return lines
}

func costConclusion(groups []*modelOutcome, exact int) string {
	if len(groups) < 2 {
		return fmt.Sprintf(
			"no conclusion: %d exact samples across %d model; at least 2 models with 3 samples each are required",
			exact, len(groups))
	}
	for _, group := range groups {
		if len(group.costs) < 3 {
			return fmt.Sprintf(
				"no conclusion: %d exact samples; every compared model needs at least 3 and %s has %d",
				exact, group.model, len(group.costs))
		}
	}

	cheap, expensive := groups[0], groups[len(groups)-1]
	cheapRate := float64(cheap.passing) / float64(cheap.required)
	expensiveRate := float64(expensive.passing) / float64(expensive.required)
	switch {
	case median(cheap.costs) == median(expensive.costs):
		return fmt.Sprintf(
			"no conclusion: %d exact samples; the compared models have the same median cost", exact)
	case expensiveRate > cheapRate:
		return fmt.Sprintf(
			"%d exact samples: the costlier model %s passed a higher share of required tests (%.0f%% vs %.0f%%). This is an association in this project's history, not proof the model caused it.",
			exact, expensive.model, 100*expensiveRate, 100*cheapRate)
	case expensiveRate < cheapRate:
		return fmt.Sprintf(
			"%d exact samples: the costlier model %s passed a lower share of required tests (%.0f%% vs %.0f%%).",
			exact, expensive.model, 100*expensiveRate, 100*cheapRate)
	default:
		return fmt.Sprintf(
			"%d exact samples: the costlier model %s did not pass a higher share of required tests; both are at %.0f%%.",
			exact, expensive.model, 100*expensiveRate)
	}
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	middle := len(values) / 2
	if len(values)%2 == 0 {
		return (values[middle-1] + values[middle]) / 2
	}
	return values[middle]
}
