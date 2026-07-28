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
	tool := &spawnTool{
		dispatcher: dispatcher,
		current:    func() string { return "claude" },
		confirm: func(c Confirmation) bool {
			asked = append(asked, c)
			return answer
		},
	}
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

// "use 2 Sonnet agents" and a profile stored as "sonnet" are the same request. Refusing over case
// would send the model on a listing round trip for a distinction no user intends.
func TestAProfileNamedInAnotherCaseResolvesToTheRealOne(t *testing.T) {
	tool, dispatcher, asked := dispatching(t, true)

	result := call(t, tool, `{"count": 2, "profile": "Sonnet", "task": "refactor auth"}`)
	if result.IsError {
		t.Fatalf("a case difference was refused: %q", result.Content)
	}
	if dispatcher.spawned[0].Profile != "sonnet" {
		t.Errorf("the dispatch carries %q, want the real name %q", dispatcher.spawned[0].Profile, "sonnet")
	}
	if question := (*asked)[0].Question(); !strings.Contains(question, "sonnet") {
		t.Errorf("the confirmation does not show the resolved name: %q", question)
	}
}

// Two profiles differing only in case are both real, so a name that matches both is a genuine
// ambiguity rather than a typo to paper over.
func TestANameMatchingTwoProfilesByCaseIsRefused(t *testing.T) {
	tool, dispatcher, asked := dispatching(t, true)
	dispatcher.profiles = []Profile{
		{Name: "nim", Model: "a"},
		{Name: "NIM", Model: "b"},
	}

	result := call(t, tool, `{"count": 1, "profile": "Nim", "task": "refactor"}`)
	if !result.IsError {
		t.Fatal("an ambiguous name was accepted")
	}
	if len(dispatcher.spawned) != 0 || len(*asked) != 0 {
		t.Error("an ambiguous name got as far as the confirmation")
	}
	if !strings.Contains(result.Content, "nim") || !strings.Contains(result.Content, "NIM") {
		t.Errorf("the refusal does not list both candidates: %q", result.Content)
	}
}

// "use 3 agents for this" names no profile, and the deterministic default is the one this
// conversation already runs on, which is also the one the person watching would guess.
func TestNoProfileFallsBackToTheConversationsOwn(t *testing.T) {
	tool, dispatcher, asked := dispatching(t, true)

	result := call(t, tool, `{"count": 3, "task": "refactor auth"}`)
	if result.IsError {
		t.Fatalf("an unnamed profile was refused instead of defaulted: %q", result.Content)
	}
	if dispatcher.spawned[0].Profile != "claude" {
		t.Errorf("the dispatch ran on %q, want the conversation's own profile", dispatcher.spawned[0].Profile)
	}
	if question := (*asked)[0].Question(); !strings.Contains(question, "claude") {
		t.Errorf("the confirmation does not show which profile was defaulted to: %q", question)
	}
}

func TestNoProfileAndNoFallbackSaysWhichOnesExist(t *testing.T) {
	tool, dispatcher, asked := dispatching(t, true)
	tool.current = nil

	result := call(t, tool, `{"count": 2, "task": "refactor auth"}`)
	if !result.IsError {
		t.Fatal("a request with no profile and nothing to default to was accepted")
	}
	for _, want := range []string{"claude", "sonnet", "nim"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the refusal does not list %q: %q", want, result.Content)
		}
	}
	if len(dispatcher.spawned) != 0 || len(*asked) != 0 {
		t.Error("something happened before the profile question was settled")
	}
}

// The engine has never known what a credential is, so the profile's default model has to travel
// with the request. Without it, a fresh profile's agents start on whatever model another profile
// happened to be running, which is a key from one provider paired with a model name from another.
func TestTheProfilesDefaultModelTravelsWithTheDispatch(t *testing.T) {
	tool, dispatcher, _ := dispatching(t, true)

	call(t, tool, `{"count": 2, "profile": "nim", "task": "refactor"}`)
	if got := dispatcher.spawned[0].Model; got != "minimaxai/minimax-m2.7" {
		t.Errorf("the dispatch carries model %q, want the profile's own default", got)
	}
}

func TestListingProfilesMarksTheConversationsOwn(t *testing.T) {
	_, dispatcher, _ := dispatching(t, true)
	tool := &profilesTool{dispatcher: dispatcher, current: func() string { return "claude" }}

	result := call(t, tool, `{}`)
	marked := false
	for _, line := range strings.Split(result.Content, "\n") {
		if strings.Contains(line, "claude (") && strings.Contains(line, "this conversation") {
			marked = true
		}
		if strings.Contains(line, "sonnet") && strings.Contains(line, "this conversation") {
			t.Errorf("a profile that is not the conversation's is marked as it: %q", line)
		}
	}
	if !marked {
		t.Errorf("the conversation's own profile is not marked:\n%s", result.Content)
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

func dispatchEngine(t *testing.T) *Engine {
	t.Helper()
	e := New(fixedResolver{
		client: &scriptedClient{name: "claude", events: reply("done")},
		id:     anthropicID(),
	})
	t.Cleanup(e.Close)

	_, err := e.AddAgent(context.Background(), Agent{
		Name: "main", KeyName: "claude", Model: "claude-opus-5",
		Dir: t.TempDir(), Trust: core.TrustStandard,
	})
	if err != nil {
		t.Fatalf("AddAgent: %v", err)
	}
	return e
}

// "use 2 nemotron agents" with no agent on that profile yet has to produce agents that run the
// nemotron profile's own model. Copying the model from whichever agent already existed pairs one
// provider's key with another provider's model name, and every spawned agent fails on its first
// request.
func TestASpawnOnAFreshProfileRunsTheProfilesOwnModel(t *testing.T) {
	e := dispatchEngine(t)

	created, err := e.Spawn(context.Background(), Dispatch{
		Count: 2, Profile: "nemotron", Task: "try the migration",
		Model: "nvidia/nemotron-ultra",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	for _, agent := range created {
		if agent.KeyName != "nemotron" {
			t.Errorf("%s runs on %q, want the requested profile", agent.Name, agent.KeyName)
		}
		if agent.Model != "nvidia/nemotron-ultra" {
			t.Errorf("%s runs %q, want the profile's own model", agent.Name, agent.Model)
		}
	}
}

// A profile somebody already runs an agent on keeps that agent's model, because it is a choice the
// person made and a fan out should inherit it rather than resetting it.
func TestASpawnOnAKnownProfileInheritsTheExistingAgentsModel(t *testing.T) {
	e := dispatchEngine(t)

	created, err := e.Spawn(context.Background(), Dispatch{
		Count: 1, Profile: "claude", Task: "fix the failing test",
		Model: "some-other-default",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if created[0].Model != "claude-opus-5" {
		t.Errorf("the spawn runs %q, want the model the existing agent was already on", created[0].Model)
	}
}

// An OpenAI compatible profile with no default model cannot run a fresh agent, and the refusal has
// to name the command that fixes it rather than failing later on the first request.
func TestASpawnOnAProfileWithNoDefaultModelSaysHowToSetOne(t *testing.T) {
	e := dispatchEngine(t)

	_, err := e.Spawn(context.Background(), Dispatch{
		Count: 1, Profile: "nemotron", Task: "try the migration",
	})
	if err == nil {
		t.Fatal("an agent was created with no model to run")
	}
	if !strings.Contains(err.Error(), "canopy keys model nemotron") {
		t.Errorf("the refusal does not say how to fix it: %v", err)
	}
}

// A spawned agent must not see the dispatch tools, or one confirmation can multiply into an
// unbounded fan out. The orchestrating conversation keeps them; the spawned one loses them, even
// though the two share the engine's registry when the spawn is not isolated.
func TestADispatchedAgentDoesNotGetTheDispatchTools(t *testing.T) {
	e := dispatchEngine(t)

	registry := core.NewToolRegistry()
	for _, tool := range DispatchTools(&fakeDispatcher{limit: 8}, nil, nil) {
		registry.MustRegister(tool)
	}
	e.WithTools(registry, core.TrustStandard, nil)

	created, err := e.Spawn(context.Background(), Dispatch{
		Count: 1, Profile: "nemotron", Task: "try the migration",
		Model: "nvidia/nemotron-ultra",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	main, _ := e.Agent("main")

	names := func(sessionID string) map[string]bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		tools, _ := e.toolsForLocked(sessionID)
		out := map[string]bool{}
		for _, tool := range tools.Tools() {
			out[tool.Name()] = true
		}
		return out
	}

	if got := names(main.SessionID); !got[spawnToolName] || !got[profilesToolName] {
		t.Errorf("the orchestrating conversation lost its dispatch tools: %v", got)
	}
	if got := names(created[0].SessionID); got[spawnToolName] || got[profilesToolName] {
		t.Errorf("a spawned agent can spawn agents of its own: %v", got)
	}
}
