package session

import (
	"context"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// oneAnthropicKey is the setup the whole feature exists for: a single credential, called whatever
// its owner called it, running one model and able to run seven others.
func oneAnthropicKey(t *testing.T, answer bool) (*spawnTool, *fakeDispatcher, *[]Confirmation) {
	t.Helper()

	dispatcher := &fakeDispatcher{
		profiles: []Profile{{
			Name:   "claude",
			Model:  "claude-opus-5",
			Models: catalog.For(core.ProviderAnthropic, ""),
			Priced: true,
		}},
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

// The acceptance criterion in one test: no key is called sonnet, and "spawn two sonnet agents" runs
// two sonnet agents anyway, on the newest sonnet, with the confirmation naming it before anything
// is created.
func TestSonnetAgentsSpawnWithNoKeyCalledSonnet(t *testing.T) {
	tool, dispatcher, asked := oneAnthropicKey(t, true)

	result := call(t, tool, `{"count": 2, "model": "sonnet", "task": "refactor the auth package"}`)
	if result.IsError {
		t.Fatalf("the request was refused: %s", result.Content)
	}

	if len(dispatcher.spawned) != 1 {
		t.Fatalf("what was spawned: %+v", dispatcher.spawned)
	}
	spawned := dispatcher.spawned[0]
	if spawned.Model != "claude-sonnet-5" {
		t.Errorf("the agents run %q, want the newest sonnet the catalog knows", spawned.Model)
	}
	if spawned.Profile != "claude" {
		t.Errorf("the agents run on profile %q, want the only credential there is", spawned.Profile)
	}
	if !spawned.ModelNamed {
		t.Error("a model asked for by name is not marked as asked for, so a fan out would inherit")
	}

	// Named before anything runs, on the line that is the last thing between the words and the bill.
	if len(*asked) != 1 {
		t.Fatalf("%d confirmations", len(*asked))
	}
	if question := (*asked)[0].Question(); !strings.Contains(question, "claude-sonnet-5") {
		t.Errorf("the confirmation does not name the model that will run: %q", question)
	}
}

// Spelling is forgiven before anything is refused, so the words somebody actually types reach the
// model they meant. The catalog holds the matching; this is that matching reaching the dispatch.
func TestTheWordsForAModelAreForgivenOnTheWayToADispatch(t *testing.T) {
	for spoken, want := range map[string]string{
		"claude sonnet 4 6": "claude-sonnet-4-6",
		"Claude Sonnet 4.6": "claude-sonnet-4-6",
		"opus 4 7":          "claude-opus-4-7",
		"haiku":             "claude-haiku-4-5",
	} {
		tool, dispatcher, _ := oneAnthropicKey(t, true)

		result := call(t, tool, `{"count": 1, "model": "`+spoken+`", "task": "fix the failing test"}`)
		if result.IsError {
			t.Errorf("%q was refused: %s", spoken, result.Content)
			continue
		}
		if got := dispatcher.spawned[0].Model; got != want {
			t.Errorf("%q spawned on %q, want %q", spoken, got, want)
		}
	}
}

// A model somebody added by hand answers to the name they gave it as well as to its id, since the
// name is the half they chose and therefore the half they will say out loud.
func TestADisplayNameResolvesTheSameAsItsIDWhenSpawning(t *testing.T) {
	dispatcher := &fakeDispatcher{
		profiles: []Profile{{
			Name:   "nim",
			Model:  "moonshot-v1-8k",
			Models: []catalog.Model{{ID: "minimaxai/minimax-m2.7", Name: "MiniMax M2.7"}},
		}},
		limit: 8,
	}
	tool := &spawnTool{
		dispatcher: dispatcher,
		current:    func() string { return "nim" },
		confirm:    func(Confirmation) bool { return true },
	}

	result := call(t, tool, `{"count": 1, "model": "MiniMax M2.7", "task": "try the migration"}`)
	if result.IsError {
		t.Fatalf("a display name was refused: %s", result.Content)
	}
	if got := dispatcher.spawned[0].Model; got != "minimaxai/minimax-m2.7" {
		t.Errorf("the dispatch carries %q, want the id rather than the label", got)
	}
}

// Nothing is guessed, and the refusal is worth as much as the answer: a model told what does exist
// picks one, where a model told only "no" tries again with another guess.
func TestAModelNobodyOffersIsRefusedWithWhatDoesExist(t *testing.T) {
	tool, dispatcher, _ := oneAnthropicKey(t, true)

	result := call(t, tool, `{"count": 2, "model": "gpt-5.2", "task": "refactor auth"}`)
	if !result.IsError {
		t.Fatal("a model no credential can run was accepted")
	}
	if len(dispatcher.spawned) != 0 {
		t.Fatal("agents were created for a model nothing can run")
	}
	for _, want := range []string{"claude", "claude-sonnet-5", "gpt-5.2"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, result.Content)
		}
	}
}

// The credential is the conversation's own when it offers the model, because moving somebody onto
// another key because it also has the model would move their bill without saying so.
func TestTheCurrentKeyKeepsAModelItOffers(t *testing.T) {
	dispatcher := &fakeDispatcher{
		profiles: []Profile{
			{Name: "work", Model: "claude-opus-5", Models: catalog.For(core.ProviderAnthropic, "")},
			{Name: "personal", Model: "claude-opus-5", Models: catalog.For(core.ProviderAnthropic, "")},
		},
		limit: 8,
	}
	tool := &spawnTool{
		dispatcher: dispatcher,
		current:    func() string { return "personal" },
		confirm:    func(Confirmation) bool { return true },
	}

	result := call(t, tool, `{"count": 1, "model": "sonnet", "task": "fix the test"}`)
	if result.IsError {
		t.Fatalf("refused: %s", result.Content)
	}
	if got := dispatcher.spawned[0].Profile; got != "personal" {
		t.Errorf("the spawn went to %q, want the conversation's own credential", got)
	}
}

// And when it does not offer the model, the answer is the only key that does. One, never a choice
// between two: which credential gets billed is not a decision to make on somebody's behalf.
func TestAModelOnlyOneKeyOffersFindsThatKeyAndTwoIsRefused(t *testing.T) {
	claude := Profile{Name: "claude", Model: "claude-opus-5", Models: catalog.For(core.ProviderAnthropic, "")}
	nim := Profile{
		Name:   "nim",
		Model:  "moonshot-v1-8k",
		Models: []catalog.Model{{ID: "minimaxai/minimax-m2.7", Name: "MiniMax M2.7"}},
	}

	dispatcher := &fakeDispatcher{profiles: []Profile{claude, nim}, limit: 8}
	tool := &spawnTool{
		dispatcher: dispatcher,
		current:    func() string { return "claude" },
		confirm:    func(Confirmation) bool { return true },
	}

	result := call(t, tool, `{"count": 1, "model": "minimax m2 7", "task": "try the migration"}`)
	if result.IsError {
		t.Fatalf("refused: %s", result.Content)
	}
	spawned := dispatcher.spawned[0]
	if spawned.Profile != "nim" || spawned.Model != "minimaxai/minimax-m2.7" {
		t.Errorf("the spawn landed on %s running %s, want the only key that offers it",
			spawned.Profile, spawned.Model)
	}

	// Two keys offering the same model is a question for the user, not a coin toss.
	second := &fakeDispatcher{profiles: []Profile{claude, {
		Name: "spare", Model: "claude-opus-5", Models: catalog.For(core.ProviderAnthropic, ""),
	}}, limit: 8}
	tool = &spawnTool{
		dispatcher: second,
		current:    func() string { return "" },
		confirm:    func(Confirmation) bool { return true },
	}
	result = call(t, tool, `{"count": 1, "profile": "claude", "model": "gpt-5.2", "task": "x"}`)
	if !result.IsError {
		t.Fatal("a named profile was silently swapped for another one that had the model")
	}
	if !strings.Contains(result.Content, "claude cannot run") {
		t.Errorf("the refusal does not say which profile could not do it:\n%s", result.Content)
	}
}

// A profile named in the request is a decision already made. Running the agents somewhere else
// because that credential happens to have the model would be answering a question nobody asked.
func TestANamedProfileIsNotSwappedForOneThatHasTheModel(t *testing.T) {
	dispatcher := &fakeDispatcher{
		profiles: []Profile{
			{Name: "cheap", Model: "claude-haiku-4-5", Models: []catalog.Model{{ID: "claude-haiku-4-5"}}},
			{Name: "full", Model: "claude-opus-5", Models: catalog.For(core.ProviderAnthropic, "")},
		},
		limit: 8,
	}
	tool := &spawnTool{
		dispatcher: dispatcher,
		current:    func() string { return "full" },
		confirm:    func(Confirmation) bool { return true },
	}

	result := call(t, tool, `{"count": 1, "profile": "cheap", "model": "opus", "task": "refactor"}`)
	if !result.IsError {
		t.Fatalf("the spawn was moved to %+v", dispatcher.spawned)
	}
	if !strings.Contains(result.Content, "claude-haiku-4-5") {
		t.Errorf("the refusal does not say what the named profile can run:\n%s", result.Content)
	}
}

// pricingDispatcher can tell two models apart in its own history, which is what the optional
// ModelEstimator interface is for. The plain fake deliberately does not implement it, so the
// fallback path stays exercised by every other test in this package.
type pricingDispatcher struct {
	*fakeDispatcher
	askedModel string
	onModel    Estimate
}

func (d *pricingDispatcher) EstimateOn(task string, count int, model string) Estimate {
	d.askedModel = model
	return d.onModel
}

// The estimate has to be for the model that will actually run. The models one key offers differ in
// price by a factor of ten, so an opus figure on a haiku fan out is a wrong number in the one place
// a number exists to protect somebody.
func TestTheEstimatePricesTheResolvedModelNotTheProfileDefault(t *testing.T) {
	_, plain, _ := oneAnthropicKey(t, true)
	plain.estimate = Estimate{Low: 9, High: 90, Samples: 20, Basis: "the profile default", Confidence: "high"}

	dispatcher := &pricingDispatcher{
		fakeDispatcher: plain,
		onModel: Estimate{Low: 0.10, High: 0.30, Samples: 12,
			Basis: "similar priced turns in this project on claude-haiku-4-5", Confidence: "medium"},
	}

	var asked []Confirmation
	tool := &spawnTool{
		dispatcher: dispatcher,
		current:    func() string { return "claude" },
		confirm: func(c Confirmation) bool {
			asked = append(asked, c)
			return true
		},
	}

	call(t, tool, `{"count": 1, "model": "haiku", "task": "fix the failing test"}`)

	if dispatcher.askedModel != "claude-haiku-4-5" {
		t.Errorf("the estimate was asked about %q, want the resolved model", dispatcher.askedModel)
	}
	if len(asked) != 1 {
		t.Fatalf("%d confirmations", len(asked))
	}
	if summary := asked[0].Estimate.Summary(); !strings.Contains(summary, "claude-haiku-4-5") {
		t.Errorf("the estimate on the confirmation is the profile default: %q", summary)
	}
}

// The listing has to name what each profile can run, or a model that wants to check before spawning
// has nowhere to look and goes back to guessing from credential names.
func TestListingProfilesNamesWhatEachOneCanRun(t *testing.T) {
	_, dispatcher, _ := oneAnthropicKey(t, true)
	tool := &profilesTool{dispatcher: dispatcher, current: func() string { return "claude" }}

	result := call(t, tool, `{}`)
	for _, want := range []string{"claude-sonnet-5", "claude-haiku-4-5", "also runs"} {
		if !strings.Contains(result.Content, want) {
			t.Errorf("the listing is missing %q:\n%s", want, result.Content)
		}
	}
	// And the conversation's own profile is still marked, on its own line rather than lost in the
	// list of models underneath it.
	for _, line := range strings.Split(result.Content, "\n") {
		if strings.Contains(line, "this conversation") && !strings.Contains(line, "claude (") {
			t.Errorf("the mark drifted onto the wrong line: %q", line)
		}
	}
}

// A fan out onto a profile somebody already runs inherits that agent's model, which is right until
// they say otherwise. "Two sonnet agents" from a conversation on opus means sonnet.
func TestAFanOutOntoANamedModelDoesNotInheritTheRunningAgentsModel(t *testing.T) {
	e := dispatchEngine(t)

	created, err := e.Spawn(context.Background(), Dispatch{
		Count: 1, Profile: "claude", Task: "fix the failing test",
		Model: "claude-sonnet-5", ModelNamed: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if created[0].Model != "claude-sonnet-5" {
		t.Errorf("the agent runs %q, want the model that was asked for", created[0].Model)
	}
}

// The engine's own half of pricing a resolved model: turns on that model are preferred over the
// project's turns at large, once there are enough of them to mean anything.
func TestTheEstimatePrefersHistoryFromTheModelItWasAskedAbout(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	e.mu.Lock()
	e.projectID = "project-1"
	for i, turn := range []core.Turn{
		{Model: "claude-opus-5", Usage: core.Usage{CostKnown: true, CostUSD: 1.00}},
		{Model: "claude-opus-5", Usage: core.Usage{CostKnown: true, CostUSD: 1.00}},
		{Model: "claude-opus-5", Usage: core.Usage{CostKnown: true, CostUSD: 1.00}},
		{Model: "claude-haiku-4-5", Usage: core.Usage{CostKnown: true, CostUSD: 0.01}},
		{Model: "claude-haiku-4-5", Usage: core.Usage{CostKnown: true, CostUSD: 0.01}},
		{Model: "claude-haiku-4-5", Usage: core.Usage{CostKnown: true, CostUSD: 0.01}},
	} {
		turn.Request = core.Message{Role: core.RoleUser, Text: "refactor the auth package"}
		id := "session-history"
		if e.sessions[id] == nil {
			e.sessions[id] = &core.Session{ID: id}
			e.order = append(e.order, id)
			e.projects[id] = "project-1"
		}
		turn.ID = string(rune('a' + i))
		e.sessions[id].Turns = append(e.sessions[id].Turns, turn)
	}
	e.mu.Unlock()

	whole := e.Estimate("refactor the auth package", 1)
	cheap := e.EstimateOn("refactor the auth package", 1, "claude-haiku-4-5")

	if !cheap.Known() || cheap.High >= whole.High {
		t.Errorf("the haiku estimate %+v is not cheaper than the project's %+v", cheap, whole)
	}
	if !strings.Contains(cheap.Basis, "claude-haiku-4-5") {
		t.Errorf("the basis does not say the figure is model specific: %q", cheap.Basis)
	}
	// And a model with no history of its own falls back to the project's rather than refusing to
	// answer, since a wide honest range beats no range at all.
	sparse := e.EstimateOn("refactor the auth package", 1, "claude-fable-5")
	if !sparse.Known() || strings.Contains(sparse.Basis, "claude-fable-5") {
		t.Errorf("a model with no history did not fall back: %+v", sparse)
	}
}

// A key on a gateway nobody here ships a lineup for has an empty list and a model its owner typed,
// and that model is the one thing the profile is certainly about to run. It was the one thing it
// refused to be asked for, and the refusal said so out loud: "no profile here can run
// moonshot-v1-8k. nim runs moonshot-v1-8k."
func TestAProfilesOwnDefaultCanBeAskedForByName(t *testing.T) {
	dispatcher := &fakeDispatcher{
		profiles: []Profile{{Name: "nim", Model: "moonshot-v1-8k"}},
		limit:    8,
	}
	tool := &spawnTool{
		dispatcher: dispatcher,
		current:    func() string { return "nim" },
		confirm:    func(Confirmation) bool { return true },
	}

	result := call(t, tool, `{"count": 2, "model": "moonshot-v1-8k", "task": "try the migration"}`)
	if result.IsError {
		t.Fatalf("a profile's own default was refused: %s", result.Content)
	}
	spawned := dispatcher.spawned[0]
	if spawned.Profile != "nim" || spawned.Model != "moonshot-v1-8k" {
		t.Errorf("the spawn landed on %s running %s", spawned.Profile, spawned.Model)
	}
	// Named rather than inherited, which is what stops a fan out onto an agent already running
	// something else from quietly using that instead.
	if !spawned.ModelNamed {
		t.Error("a model asked for by name was not marked as asked for")
	}

	// Two keys pointed at the same model is the case that separates "the current one keeps what it
	// offers" from "the only one that offers it": both offer it, and the conversation's own wins
	// rather than the request being refused as a choice between two credentials.
	shared := &fakeDispatcher{
		profiles: []Profile{
			{Name: "spare", Model: "moonshot-v1-8k"},
			{Name: "nim", Model: "moonshot-v1-8k"},
		},
		limit: 8,
	}
	sharedTool := &spawnTool{
		dispatcher: shared,
		current:    func() string { return "nim" },
		confirm:    func(Confirmation) bool { return true },
	}
	if result := call(t, sharedTool, `{"count": 1, "model": "moonshot-v1-8k", "task": "x"}`); result.IsError {
		t.Fatalf("two keys on one model refused the conversation's own: %s", result.Content)
	}
	if got := shared.spawned[0].Profile; got != "nim" {
		t.Errorf("the spawn went to %q, want the conversation's own credential", got)
	}

	// Spelling is forgiven on a typed default exactly as it is on a listed model.
	for _, spoken := range []string{"Moonshot V1 8k", "moonshot v1 8k"} {
		second := &fakeDispatcher{profiles: dispatcher.profiles, limit: 8}
		tool := &spawnTool{
			dispatcher: second,
			current:    func() string { return "nim" },
			confirm:    func(Confirmation) bool { return true },
		}
		if result := call(t, tool, `{"count": 1, "model": "`+spoken+`", "task": "x"}`); result.IsError {
			t.Errorf("%q was refused: %s", spoken, result.Content)
		}
	}
}

// And the refusal for a model that genuinely is not there stops naming it as available, which is the
// same defect from the other side: the listing and the matcher now read one function.
func TestARefusalDoesNotOfferWhatItJustRefused(t *testing.T) {
	dispatcher := &fakeDispatcher{
		profiles: []Profile{
			{Name: "nim", Model: "moonshot-v1-8k"},
			{Name: "claude", Model: "claude-opus-5", Models: catalog.For(core.ProviderAnthropic, "")},
		},
		limit: 8,
	}
	tool := &spawnTool{
		dispatcher: dispatcher,
		current:    func() string { return "nim" },
		confirm:    func(Confirmation) bool { return true },
	}

	result := call(t, tool, `{"count": 1, "model": "gpt-5.2", "task": "x"}`)
	if !result.IsError {
		t.Fatal("a model nothing runs was accepted")
	}
	// The listing names what does exist, including the typed default it would not have listed before.
	if !strings.Contains(result.Content, "moonshot-v1-8k") {
		t.Errorf("the refusal does not name the typed default that is available:\n%s", result.Content)
	}
	// And every id it offers is one that would actually resolve, which is the property that broke.
	for _, offered := range []string{"moonshot-v1-8k", "claude-sonnet-5"} {
		second := &fakeDispatcher{profiles: dispatcher.profiles, limit: 8}
		check := &spawnTool{
			dispatcher: second,
			current:    func() string { return "nim" },
			confirm:    func(Confirmation) bool { return true },
		}
		if again := call(t, check, `{"count": 1, "model": "`+offered+`", "task": "x"}`); again.IsError {
			t.Errorf("the refusal offered %q and asking for it was refused: %s", offered, again.Content)
		}
	}
}

// Every spawn is priced against the model its agents will run, not only the ones that named one.
//
// By the time the estimate is asked for, an unnamed model has been filled in with the profile's own
// default, so this path fires on the ordinary spawn too. That is deliberate and it is the better
// answer, but it was a behaviour change nothing could see: the plain fake has no EstimateOn, so
// every existing test went down the older path and could not have noticed either way.
func TestASpawnWithNoModelNamedIsStillPricedOnWhatItWillRun(t *testing.T) {
	plain := &fakeDispatcher{
		profiles: []Profile{{Name: "claude", Model: "claude-opus-5", Priced: true}},
		limit:    8,
	}
	dispatcher := &pricingDispatcher{
		fakeDispatcher: plain,
		onModel:        Estimate{Low: 1, High: 3, Samples: 9, Basis: "on the model", Confidence: "medium"},
	}
	tool := &spawnTool{
		dispatcher: dispatcher,
		current:    func() string { return "claude" },
		confirm:    func(Confirmation) bool { return true },
	}

	call(t, tool, `{"count": 2, "task": "refactor the auth package"}`)

	if dispatcher.askedModel != "claude-opus-5" {
		t.Errorf("the estimate was asked about %q, want the profile's default", dispatcher.askedModel)
	}
}
