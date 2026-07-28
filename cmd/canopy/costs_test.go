package main

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func TestCostOutcomeAttributionRefusesMixedModelSessions(t *testing.T) {
	conversation := core.Session{Model: "fallback", Turns: []core.Turn{
		{State: core.TurnComplete, Model: "cheap",
			Usage: core.Usage{CostUSD: .10, CostKnown: true}},
		{State: core.TurnComplete, Model: "expensive",
			Usage: core.Usage{CostUSD: .50, CostKnown: true}},
	}}

	if _, _, state := sessionModelUsage(conversation); state != mixedModelUsage {
		t.Errorf("mixed session state = %v", state)
	}
}

func TestCostOutcomeAttributionUsesSettledTurnsOnly(t *testing.T) {
	conversation := core.Session{Model: "cheap", Turns: []core.Turn{
		{State: core.TurnComplete, Model: "cheap",
			Usage: core.Usage{CostUSD: .10, CostKnown: true}},
		{State: core.TurnStreaming, Model: "expensive"},
	}}

	model, usage, state := sessionModelUsage(conversation)
	if state != singleModelUsage || model != "cheap" {
		t.Fatalf("model=%q state=%v", model, state)
	}
	if !usage.CostKnown || usage.CostUSD != .10 {
		t.Errorf("usage = %+v", usage)
	}
}

func TestCostOutcomeAttributionRefusesNoSettledUsage(t *testing.T) {
	conversation := core.Session{Model: "cheap", Turns: []core.Turn{{
		State: core.TurnStreaming, Model: "cheap",
	}}}
	if _, _, state := sessionModelUsage(conversation); state != noModelUsage {
		t.Errorf("empty session state = %v", state)
	}
}
