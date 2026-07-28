package session

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func TestOutcomeHistoryIsProjectScopedAndRefreshesRatherThanDuplicates(t *testing.T) {
	storage := testStorage(t)
	first := OutcomeSample{
		ProjectID: "project-a", SessionID: "s1", Revision: "revision:a", Agent: "one",
		Model: "cheap", CostUSD: .10, CostKnown: true, Passing: 1, Required: 2,
		ObservedAt: storedAt,
	}
	if err := storage.saveOutcome(first); err != nil {
		t.Fatal(err)
	}
	first.CostUSD = .15
	first.Passing = 2
	if err := storage.saveOutcome(first); err != nil {
		t.Fatal(err)
	}
	if err := storage.saveOutcome(OutcomeSample{
		ProjectID: "project-b", SessionID: "s2", Revision: "revision:b", Agent: "two",
		Model: "expensive", CostUSD: .50, CostKnown: true, Passing: 2, Required: 2,
		ObservedAt: storedAt,
	}); err != nil {
		t.Fatal(err)
	}

	history, err := storage.loadOutcomes("project-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 {
		t.Fatalf("project-a history = %+v", history)
	}
	if history[0].CostUSD != .15 || history[0].Passing != 2 {
		t.Errorf("the refreshed observation did not win: %+v", history[0])
	}
}

func TestEstimateUsesOnlySimilarTurnsFromThisProject(t *testing.T) {
	engine := New(nil)
	engine.SetProjectID("project-a")

	add := func(id, project, prompt string, cost float64) {
		engine.sessions[id] = &core.Session{ID: id, Turns: []core.Turn{{
			Request: core.Message{Role: core.RoleUser, Text: prompt},
			Usage:   core.Usage{CostUSD: cost, CostKnown: true},
		}}}
		engine.order = append(engine.order, id)
		engine.projects[id] = project
	}
	add("similar-1", "project-a", "refactor authentication middleware", .10)
	add("similar-2", "project-a", "test authentication middleware", .20)
	add("similar-3", "project-a", "fix authentication middleware", .30)
	add("unrelated", "project-a", "write database migration", 99)
	add("other-project", "project-b", "refactor authentication middleware", 88)

	estimate := engine.Estimate("improve authentication middleware", 2)
	if !estimate.Known() || estimate.Samples != 3 {
		t.Fatalf("estimate = %+v", estimate)
	}
	if estimate.Confidence != "low" {
		t.Errorf("confidence = %q", estimate.Confidence)
	}
	if estimate.Low != 1.6 || estimate.High != 10 {
		t.Errorf("range = %.2f..%.2f, want 1.60..10.00 from the .20 median", estimate.Low, estimate.High)
	}
	if !strings.Contains(estimate.Basis, "similar") || !strings.Contains(estimate.Basis, "this project") {
		t.Errorf("basis does not name its filters: %q", estimate.Basis)
	}
}

func TestEstimateRefusesWhenSimilarProjectHistoryIsTooSmall(t *testing.T) {
	engine := New(nil)
	engine.SetProjectID("project-a")
	engine.sessions["one"] = &core.Session{ID: "one", Turns: []core.Turn{{
		Request: core.Message{Role: core.RoleUser, Text: "fix authentication"},
		Usage:   core.Usage{CostUSD: .2, CostKnown: true},
	}}}
	engine.order = []string{"one"}
	engine.projects["one"] = "project-a"

	estimate := engine.Estimate("fix authentication", 2)
	if estimate.Known() || !strings.Contains(estimate.Basis, "1 priced turns matched") {
		t.Errorf("estimate = %+v", estimate)
	}
}

func TestSessionProjectAssociationSurvivesReopen(t *testing.T) {
	storage := testStorage(t)
	session := core.Session{ID: "s1", CreatedAt: storedAt, UpdatedAt: storedAt}
	if err := storage.SaveSessionForProject(session, "project-a"); err != nil {
		t.Fatal(err)
	}
	// An ordinary later save must not erase the project.
	if err := storage.SaveSession(session); err != nil {
		t.Fatal(err)
	}
	projects, err := storage.projectIDs()
	if err != nil {
		t.Fatal(err)
	}
	if projects["s1"] != "project-a" {
		t.Errorf("projects = %+v", projects)
	}
}
