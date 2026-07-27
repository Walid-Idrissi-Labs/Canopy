package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// fakeDispatcher records what it was asked to do without creating anything.
type fakeDispatcher struct {
	profiles []Profile
	estimate Estimate
	running  int
	limit    int
	spawned  []Dispatch
	failure  error
}

func (f *fakeDispatcher) Profiles() []Profile { return f.profiles }

func (f *fakeDispatcher) Estimate(string, int) Estimate { return f.estimate }

func (f *fakeDispatcher) Concurrency() (int, int) { return f.running, f.limit }

func (f *fakeDispatcher) Spawn(_ context.Context, request Dispatch) ([]Agent, error) {
	if f.failure != nil {
		return nil, f.failure
	}
	f.spawned = append(f.spawned, request)

	agents := make([]Agent, 0, request.Count)
	for i := range request.Count {
		agents = append(agents, Agent{
			Name:   string(rune('a'+i)) + "-agent",
			Branch: "canopy/" + string(rune('a'+i)),
		})
	}
	return agents, nil
}

func dispatching(t *testing.T, answer bool) (*spawnTool, *fakeDispatcher, *[]Confirmation) {
	t.Helper()

	dispatcher := &fakeDispatcher{
		profiles: []Profile{
			{Name: "claude", Model: "claude-opus-5", Priced: true},
			{Name: "sonnet", Model: "claude-sonnet-5", Priced: true},
			{Name: "nim", Model: "minimaxai/minimax-m2.7"},
		},
		estimate: Estimate{Low: 0.40, High: 1.20, Samples: 18,
			Basis: "based on 18 similar turns in this project", Confidence: "high"},
		limit: 8,
	}

	var asked []Confirmation
	tool := &spawnTool{dispatcher: dispatcher, confirm: func(c Confirmation) bool {
		asked = append(asked, c)
		return answer
	}}
	return tool, dispatcher, &asked
}

func call(t *testing.T, tool core.Tool, args string) core.ToolResult {
	t.Helper()
	result, err := tool.Run(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

// The acceptance criterion that costs the most money if it is wrong. A misparsed 20 instead of 2 is
// twenty worktrees and twenty times the bill.
func TestNothingSpawnsWithoutConfirmation(t *testing.T) {
	tool, dispatcher, asked := dispatching(t, false)

	result := call(t, tool, `{"count": 2, "profile": "sonnet", "task": "refactor auth"}`)

	if len(dispatcher.spawned) != 0 {
		t.Fatalf("agents were created without confirmation: %+v", dispatcher.spawned)
	}
	if len(*asked) != 1 {
		t.Fatalf("%d confirmations asked for, want 1", len(*asked))
	}
	if !result.IsError {
		t.Error("the refusal was reported to the model as a success")
	}
	if !strings.Contains(result.Content, "did not confirm") {
		t.Errorf("the model is not told the user refused: %q", result.Content)
	}
	if strings.Contains(result.Content, "try again") {
		t.Errorf("the model is being invited to retry a refusal: %q", result.Content)
	}
}

// The confirmation has to carry the three things that could be wrong, and the money.
func TestTheConfirmationShowsCountProfileTaskAndCost(t *testing.T) {
	tool, _, asked := dispatching(t, true)

	call(t, tool, `{"count": 3, "profile": "sonnet", "task": "refactor the auth package"}`)

	if len(*asked) != 1 {
		t.Fatalf("%d confirmations", len(*asked))
	}
	confirmation := (*asked)[0]

	question := confirmation.Question()
	for _, want := range []string{"3 agents", "sonnet", "refactor the auth package", "worktree"} {
		if !strings.Contains(question, want) {
			t.Errorf("the question is missing %q: %q", want, question)
		}
	}
	summary := confirmation.Estimate.Summary()
	if !strings.Contains(summary, "$0.40") || !strings.Contains(summary, "$1.20") {
		t.Errorf("the estimate reads %q", summary)
	}
	if !strings.Contains(summary, "18 similar turns") {
		t.Errorf("the estimate does not say what it is based on: %q", summary)
	}
	if !strings.Contains(summary, "high confidence") {
		t.Errorf("the estimate does not name its confidence: %q", summary)
	}
}

// An estimate presented more confidently than the data supports is its own small lie, and no
// history at all is the common case on the day somebody installs this.
func TestWithNoHistoryTheEstimateSaysSoRatherThanGuessing(t *testing.T) {
	tool, dispatcher, asked := dispatching(t, true)
	dispatcher.estimate = Estimate{Basis: "no similar work has been costed in this project yet"}

	call(t, tool, `{"count": 2, "profile": "sonnet", "task": "refactor auth"}`)

	confirmation := (*asked)[0]
	if confirmation.Estimate.Known() {
		t.Error("an estimate with no samples reported itself as known")
	}
	summary := confirmation.Estimate.Summary()
	if strings.Contains(summary, "$") {
		t.Errorf("a number was shown with nothing behind it: %q", summary)
	}
	if !strings.Contains(summary, "no similar work") {
		t.Errorf("the estimate does not explain itself: %q", summary)
	}

	var warned bool
	for _, warning := range confirmation.Warnings {
		if strings.Contains(warning, "no cost history") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("nothing warns that the estimate is missing: %v", confirmation.Warnings)
	}
}

// A model told "unknown profile" guesses again. A model told which profiles exist picks one.
func TestAnUnknownProfileSaysWhichOnesExist(t *testing.T) {
	tool, dispatcher, asked := dispatching(t, true)

	result := call(t, tool, `{"count": 2, "profile": "gpt-5", "task": "refactor auth"}`)

	if !result.IsError {
		t.Fatal("an unknown profile was accepted")
	}
	for _, want := range []string{"claude", "sonnet", "nim"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the refusal does not list %q: %q", want, result.Content)
		}
	}
	if len(dispatcher.spawned) != 0 || len(*asked) != 0 {
		t.Error("an unknown profile got as far as the confirmation")
	}
}

func TestTheThingsThatAreRefusedBeforeAnybodyIsAsked(t *testing.T) {
	cases := []struct {
		name string
		args string
		says string
	}{
		{"no task", `{"count": 2, "profile": "sonnet", "task": "  "}`, "nothing for the agents to do"},
		{"no count", `{"count": 0, "profile": "sonnet", "task": "refactor"}`, "at least one"},
		{"a wild count", `{"count": 40, "profile": "sonnet", "task": "refactor"}`, "limit"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tool, dispatcher, asked := dispatching(t, true)

			result := call(t, tool, c.args)
			if !result.IsError {
				t.Fatalf("%s was accepted", c.name)
			}
			if !strings.Contains(result.Content, c.says) {
				t.Errorf("the refusal reads %q", result.Content)
			}
			if len(*asked) != 0 {
				t.Error("the user was asked to confirm something that could not be done anyway")
			}
			if len(dispatcher.spawned) != 0 {
				t.Error("something was created")
			}
		})
	}
}

// A count outside the limit says what the limit is, and suggests asking rather than truncating.
// Silently spawning six when twenty were asked for would be the worst of both.
func TestTooManyAgentsIsRefusedRatherThanTrimmed(t *testing.T) {
	tool, dispatcher, _ := dispatching(t, true)

	result := call(t, tool, `{"count": 20, "profile": "sonnet", "task": "refactor"}`)
	if !result.IsError {
		t.Fatal("twenty agents was accepted")
	}
	if !strings.Contains(result.Content, "ask them") {
		t.Errorf("the refusal does not tell the model to check: %q", result.Content)
	}
	if len(dispatcher.spawned) != 0 {
		t.Errorf("agents were created anyway: %+v", dispatcher.spawned)
	}
}

// Several agents in one checkout overwrite each other, and it shows up as one agent's changes
// vanishing rather than as an error, so it has to be said before it happens.
func TestAFanOutIsIsolatedByDefaultAndSaysSoWhenItIsNot(t *testing.T) {
	isolating, dispatcher, _ := dispatching(t, true)

	call(t, isolating, `{"count": 3, "profile": "sonnet", "task": "refactor"}`)
	if !dispatcher.spawned[0].Isolated {
		t.Error("a fan out defaulted to sharing one checkout")
	}

	tool, dispatcher, asked := dispatching(t, true)
	call(t, tool, `{"count": 3, "profile": "sonnet", "task": "refactor", "isolated": false}`)

	if dispatcher.spawned[0].Isolated {
		t.Error("an explicit isolated:false was overridden")
	}
	var warned bool
	for _, warning := range (*asked)[0].Warnings {
		if strings.Contains(warning, "overwrite") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("nothing warns that they will overwrite each other: %v", (*asked)[0].Warnings)
	}
}

// One agent is not a branch. Isolation is a mode, not the definition of an agent, so a single agent
// works where it is unless somebody asked otherwise.
func TestASingleAgentIsNotGivenAWorktreeUnlessAsked(t *testing.T) {
	tool, dispatcher, _ := dispatching(t, true)

	call(t, tool, `{"count": 1, "profile": "claude", "task": "fix the failing test"}`)
	if dispatcher.spawned[0].Isolated {
		t.Error("one agent was given a worktree of its own without being asked for")
	}
}

func TestAnUnpricedProfileIsFlaggedRatherThanCostedAtZero(t *testing.T) {
	tool, _, asked := dispatching(t, true)

	call(t, tool, `{"count": 2, "profile": "nim", "task": "refactor"}`)

	var warned bool
	for _, warning := range (*asked)[0].Warnings {
		if strings.Contains(warning, "does not know what this profile costs") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("an unpriced profile was not flagged: %v", (*asked)[0].Warnings)
	}
}

func TestSpawningRespectsTheConcurrencyLimit(t *testing.T) {
	tool, dispatcher, asked := dispatching(t, true)
	dispatcher.running, dispatcher.limit = 6, 8

	call(t, tool, `{"count": 4, "profile": "sonnet", "task": "refactor"}`)

	var warned bool
	for _, warning := range (*asked)[0].Warnings {
		if strings.Contains(warning, "limit of 8") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("going past the concurrency limit was not mentioned: %v", (*asked)[0].Warnings)
	}
}

func TestListingProfilesNamesTheOnesThatExist(t *testing.T) {
	_, dispatcher, _ := dispatching(t, true)
	tool := &profilesTool{dispatcher: dispatcher}

	result := call(t, tool, `{}`)
	for _, want := range []string{"claude", "sonnet", "nim", "claude-opus-5"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the listing is missing %q:\n%s", want, result.Content)
		}
	}
	if !strings.Contains(result.Content, "cost unknown") {
		t.Errorf("an unpriced profile is not marked:\n%s", result.Content)
	}
}

func TestWithNoProfilesTheModelIsToldWhatToDo(t *testing.T) {
	dispatcher := &fakeDispatcher{limit: 8}
	tool := &profilesTool{dispatcher: dispatcher}

	result := call(t, tool, `{}`)
	if !result.IsError {
		t.Error("no profiles at all was reported as a normal listing")
	}
	if !strings.Contains(result.Content, "canopy keys add") {
		t.Errorf("the model is not told how to fix it: %q", result.Content)
	}
}

func TestAConfirmedSpawnReportsWhatItStarted(t *testing.T) {
	tool, dispatcher, _ := dispatching(t, true)

	result := call(t, tool, `{"count": 2, "profile": "sonnet", "task": "refactor auth"}`)
	if result.IsError {
		t.Fatalf("a confirmed spawn failed: %q", result.Content)
	}
	if len(dispatcher.spawned) != 1 || dispatcher.spawned[0].Count != 2 {
		t.Fatalf("what was spawned: %+v", dispatcher.spawned)
	}
	if !strings.Contains(result.Content, "canopy/a") {
		t.Errorf("the model is not told which branches were made: %q", result.Content)
	}
	if !strings.Contains(result.Content, "Do not do the task yourself") {
		t.Errorf("nothing stops the orchestrator doing the work as well: %q", result.Content)
	}
}
