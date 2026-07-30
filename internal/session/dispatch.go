package session

// Dispatching agents from the conversation, and the reason it is a tool rather than a parser.
//
// "use 2 claude sonnet agents for the auth refactor" has to work, and so does "get a couple of
// sonnets on this", and "spin up three of the cheap one and have them each try the migration". A
// regex over the user's message handles the first and fails on the second, and it fails silently,
// which is worse: something spawns, it is not what was asked for, and nobody can see why.
//
// So the model does the reading, which is what it is good at, and the extraction arrives as
// arguments Canopy can check: a count, a profile name, a task. Every one of those is then verified
// against reality before anything is created. An unknown profile is refused with the list of real
// ones. A count outside the limit is refused with the limit. And nothing spawns at all until a
// person has seen the count, the profile, the task and the estimated cost, and said yes.
//
// The confirmation is not a formality. A misparsed 20 instead of 2 is twenty worktrees and twenty
// times the bill, and it is the single most expensive mistake this feature can make.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// MaxAgentsPerDispatch bounds one spawn request.
//
// Not a resource limit, a blast radius limit. Six agents on one task is already an unusual thing to
// want, and any number well past it is far more likely to be a misread than a plan.
const MaxAgentsPerDispatch = 6

// Dispatcher is what the spawn tool needs from the world.
//
// An interface rather than the engine itself, so the tools can be tested without a provider, a
// keychain or a repository, and so that the thing which actually creates agents stays visible in
// one place instead of being reachable from a tool package.
type Dispatcher interface {
	// Profiles returns the named credentials an agent can be created on, with their default models.
	Profiles() []Profile

	// Estimate returns what a task like this has cost before.
	Estimate(task string, count int) Estimate

	// Spawn creates the agents. Called only after confirmation.
	Spawn(ctx context.Context, request Dispatch) ([]Agent, error)

	// Concurrency is how many agents may run at once, and how many are already running.
	Concurrency() (running, limit int)
}

// Profile is a named credential an agent can run on.
type Profile struct {
	Name  string
	Model string

	// Models is everything this profile can be asked for, which is what makes "spawn two sonnet
	// agents" work with no key called sonnet. The catalog for the credential's provider and endpoint
	// plus whatever its owner added, assembled by whoever builds the profiles, because a credential
	// is something this package has never known about.
	//
	// Empty means nobody said, and the default above is then the only model this profile offers.
	Models []catalog.Model

	// Priced says whether Canopy knows what this profile costs. An OpenAI compatible gateway with no
	// rate set does not, and an estimate that quietly treated it as free would be a lie in the one
	// place a number is supposed to protect somebody.
	Priced bool
}

// ModelEstimator is a dispatcher that can price a run on a particular model.
//
// Separate from Dispatcher rather than a wider Estimate on it, because the model is a late addition
// and every caller and fake that already satisfies Dispatcher must keep satisfying it. A dispatcher
// that does not implement this is asked the older question and answers it just as well; it simply
// cannot tell two models apart in its own history.
type ModelEstimator interface {
	EstimateOn(task string, count int, model string) Estimate
}

// Dispatch is a request to create agents.
type Dispatch struct {
	Count   int
	Profile string
	Task    string

	// Model is the model these agents will run, resolved by the spawn tool: the profile's default
	// unless the request named one, in which case it is whatever those words turned out to mean.
	// It travels with the request because the engine has never known what a credential is, and the
	// alternative it used to have, copying the model from whichever agent happened to exist, could
	// pair one provider's key with another provider's model name and fail on the first request.
	Model string

	// ModelNamed says the model above was asked for rather than inherited from the profile.
	//
	// It decides exactly one thing: whether a fan out onto a profile that already has an agent takes
	// that agent's model. Inheriting is right when nobody said which model they wanted, and wrong
	// the moment somebody did, since "two sonnet agents" from a conversation already running opus
	// would otherwise quietly produce two more opus agents.
	ModelNamed bool

	// Isolated gives each agent its own worktree and branch. Default for a fan out, since ranking
	// three attempts is meaningless if all three are writing to one tree, and off for a single agent
	// unless it was asked for, because an agent is not a branch.
	Isolated bool
}

// Estimate is what a dispatch is expected to cost.
//
// The basis travels with the number, always. An estimate presented more confidently than the data
// supports is its own small lie, and the case where there is no history at all is the common one on
// the day somebody installs this.
type Estimate struct {
	// Low and High bound the expected cost across all the agents, in dollars.
	Low, High float64

	// Samples is how many past turns the range was computed from. Zero means there is no history and
	// the range is meaningless.
	Samples int

	// Basis says in words what the number came from, or why there is not one.
	Basis string

	// Confidence is deliberately coarse. A sample of a dozen local turns cannot justify decimals
	// pretending to be a statistical model.
	Confidence string
}

// Known reports whether there is enough history for the range to mean anything.
func (e Estimate) Known() bool { return e.Samples > 0 }

// Summary is the line shown on the confirmation.
func (e Estimate) Summary() string {
	if !e.Known() {
		return e.Basis
	}
	return fmt.Sprintf("about $%.2f to $%.2f, %s confidence, %s",
		e.Low, e.High, e.Confidence, e.Basis)
}

// Confirmation is everything a person needs to see before agents are created.
type Confirmation struct {
	Dispatch Dispatch
	Estimate Estimate

	// Warnings are things that are true and worth saying, and none of them block. An unpriced
	// profile and a fan out that will use most of the concurrency limit are both worth knowing and
	// neither is a reason to refuse.
	Warnings []string
}

// Question is the confirmation as one line, in the words the user would use.
func (c Confirmation) Question() string {
	agents := fmt.Sprintf("%d agents", c.Dispatch.Count)
	if c.Dispatch.Count == 1 {
		agents = "1 agent"
	}

	where := "in this checkout"
	if c.Dispatch.Isolated {
		where = "each with its own worktree and branch"
	}

	// The model as well as the credential, because a request can name one and land somewhere other
	// than the profile's default. This line is the last place that can be seen before the money is
	// spent, so the thing that was resolved from words has to be written out in full here.
	on := c.Dispatch.Profile
	if c.Dispatch.Model != "" {
		on += " running " + c.Dispatch.Model
	}
	return fmt.Sprintf("start %s on %s, %s, for: %s", agents, on, where, c.Dispatch.Task)
}

// ErrNeedsConfirmation is returned by the spawn tool when nobody has said yes yet.
//
// A sentinel rather than a bool, because the caller that has to notice this is the tool loop, and a
// bool on a result is a thing that gets ignored.
var ErrNeedsConfirmation = fmt.Errorf("this needs to be confirmed before anything is created")

// The tool names live in constants because the engine has to recognise them again in
// toolsForLocked, where spawned agents have them structurally removed. A string repeated in two
// files is a rename away from a fan out that can multiply.
const (
	spawnToolName    = "spawn_agents"
	profilesToolName = "list_profiles"
)

// DispatchTools returns the tools an orchestrating agent uses to create other agents.
//
// current names the profile the orchestrating conversation itself runs on, and may return "". It is
// what makes "use 3 agents for this" work without a profile being named: the deterministic default
// is the credential already in use, which is also the one the person watching would guess.
func DispatchTools(dispatcher Dispatcher, current func() string, confirm func(Confirmation) bool) []core.Tool {
	return []core.Tool{
		&profilesTool{dispatcher: dispatcher, current: current},
		&spawnTool{dispatcher: dispatcher, current: current, confirm: confirm},
	}
}

type profilesTool struct {
	dispatcher Dispatcher
	current    func() string
}

func (t *profilesTool) Name() string        { return profilesToolName }
func (t *profilesTool) Kind() core.ToolKind { return core.ToolRead }

func (t *profilesTool) Description() string {
	return "List the profiles agents can be started on. A profile is a named credential with a " +
		"default model. Call this before spawn_agents whenever the user names a model or a " +
		"provider, so you use a profile that exists rather than one you assumed."
}

func (t *profilesTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *profilesTool) Run(context.Context, json.RawMessage) (core.ToolResult, error) {
	profiles := t.dispatcher.Profiles()
	if len(profiles) == 0 {
		return core.ToolResult{
			Content: "No profiles are configured, so no agent can be started. " +
				"The user needs to add a credential first with `canopy keys add`.",
			IsError: true,
		}, nil
	}

	running, limit := t.dispatcher.Concurrency()

	own := ""
	if t.current != nil {
		own = t.current()
	}

	var out strings.Builder
	fmt.Fprintf(&out, "%d profiles. %d of %d agent slots are in use.\n\n", len(profiles), running, limit)
	for _, profile := range profiles {
		fmt.Fprintf(&out, "- %s (%s)", profile.Name, profile.Model)
		if !profile.Priced {
			// Said here rather than only at spawn time, so a model choosing between profiles can
			// prefer one whose cost is knowable.
			out.WriteString(", cost unknown for this endpoint")
		}
		if profile.Name == own {
			// Marked so the model knows which profile "the same as this" means, which is the
			// default when the user did not name one.
			out.WriteString(", the profile this conversation runs on")
		}
		out.WriteString("\n")

		if len(profile.Models) > 0 {
			// Ids only, on a line of their own. The display names people use are forgiven at
			// resolution time, so printing both would double the length of this listing to restate
			// something the caller does not have to get right.
			ids := make([]string, 0, len(profile.Models))
			for _, model := range profile.Models {
				ids = append(ids, model.ID)
			}
			fmt.Fprintf(&out, "  also runs: %s\n", strings.Join(ids, ", "))
		}
	}
	return core.ToolResult{Content: out.String()}, nil
}

type spawnTool struct {
	dispatcher Dispatcher
	current    func() string
	confirm    func(Confirmation) bool
}

func (t *spawnTool) Name() string { return spawnToolName }

// Kind is execute, which is the broadest thing the permission model has.
//
// Creating an agent is not a file write and not a git operation; it is starting something that will
// go on to do both, on somebody's account, at their expense. There is no narrower kind that is
// honest about that.
func (t *spawnTool) Kind() core.ToolKind { return core.ToolExecute }

func (t *spawnTool) Description() string {
	return "Start one or more agents working on a task, each in its own worktree and branch if " +
		"asked for. Use this when the user asks for agents to be run on something, for example " +
		"\"use 2 sonnet agents for the auth refactor\". Extract the count, the profile and the " +
		"task from what they said, translating words to numbers: \"a couple\" is 2, \"a few\" is 3. " +
		"If the user named no profile or model, omit both and the profile this conversation " +
		"runs on is used. If they named a model rather than a profile, pass their words as the " +
		"model and leave the profile out: Canopy resolves the words and finds the credential. " +
		"If the count or the task is unclear, ask them rather than guessing: " +
		"spawning the wrong number costs real money and real time. Call " +
		"list_profiles first if you are not certain a named profile exists."
}

func (t *spawnTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"count": {
				"type": "integer",
				"description": "How many agents to start. Only what the user asked for."
			},
			"profile": {
				"type": "string",
				"description": "The profile name to run them on, from list_profiles. Omit it when the user named no profile or model, and the profile this conversation runs on is used."
			},
			"model": {
				"type": "string",
				"description": "The model the user asked for, in their own words, for example \"sonnet\" or \"claude sonnet 4.6\". Omit it unless they named one. Spelling, spacing and a missing claude- prefix are all forgiven, a family name on its own means the newest of that family, and the credential is chosen from whichever profile offers the model. Do not translate a model into a profile name yourself."
			},
			"task": {
				"type": "string",
				"description": "What the agents should do, written as an instruction to them."
			},
			"isolated": {
				"type": "boolean",
				"description": "Give each agent its own worktree and branch. Default true for more than one agent, since several agents editing one checkout overwrite each other."
			}
		},
		"required": ["count", "task"]
	}`)
}

func (t *spawnTool) Run(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Count    int     `json:"count"`
		Profile  string  `json:"profile"`
		Model    string  `json:"model"`
		Task     string  `json:"task"`
		Isolated *bool   `json:"isolated"`
		Budget   float64 `json:"-"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return core.ToolResult{Content: fmt.Sprintf("the arguments are not valid JSON: %v", err), IsError: true}, nil
	}

	request := Dispatch{
		Count:    args.Count,
		Profile:  strings.TrimSpace(args.Profile),
		Task:     strings.TrimSpace(args.Task),
		Isolated: args.Count > 1,
	}
	if args.Isolated != nil {
		request.Isolated = *args.Isolated
	}

	// Whether a profile was named is remembered before resolve fills the empty case in, because the
	// two mean different things once a model is also in play: a named profile is a decision already
	// made, and a defaulted one is only where to look first.
	named := request.Profile != ""

	if refusal := t.resolve(&request); refusal != "" {
		return core.ToolResult{Content: refusal, IsError: true}, nil
	}
	if spoken := strings.TrimSpace(args.Model); spoken != "" {
		if refusal := t.resolveModel(&request, spoken, named); refusal != "" {
			return core.ToolResult{Content: refusal, IsError: true}, nil
		}
	}

	estimate := t.estimate(request)
	confirmation := Confirmation{
		Dispatch: request,
		Estimate: estimate,
		Warnings: t.warnings(request, estimate),
	}

	if t.confirm == nil || !t.confirm(confirmation) {
		// Refused, and reported to the model as a refusal rather than as a failure. The difference
		// matters: a model told this failed retries, and a model told the user said no does not.
		return core.ToolResult{
			Content: "The user did not confirm this. Nothing was started. Ask them what to change " +
				"rather than trying again with the same request.",
			IsError: true,
		}, nil
	}

	agents, err := t.dispatcher.Spawn(ctx, request)
	if err != nil {
		return core.ToolResult{Content: fmt.Sprintf("the agents could not be started: %v", err), IsError: true}, nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Started %d agents on %s:\n", len(agents), request.Profile)
	for _, agent := range agents {
		fmt.Fprintf(&out, "- %s", agent.Name)
		if agent.Branch != "" {
			fmt.Fprintf(&out, " on branch %s", agent.Branch)
		}
		out.WriteString("\n")
	}
	out.WriteString("\nThey are working now. Do not do the task yourself as well.")
	return core.ToolResult{Content: out.String()}, nil
}

// resolve refuses a request against reality, and says what reality is.
//
// Every refusal names the correct answer. A model told "unknown profile" guesses again; a model
// told which profiles exist picks one. Resolution also canonicalises the request: an empty profile
// becomes the conversation's own, a profile named in the wrong case becomes the real name, and the
// profile's default model is attached, so everything downstream and everything on the confirmation
// is the name and model that will actually run.
func (t *spawnTool) resolve(request *Dispatch) string {
	switch {
	case request.Task == "":
		return "No task was given, so there is nothing for the agents to do. Ask the user what " +
			"they want done."
	case request.Count < 1:
		return "A count of at least one is required. If the user did not say how many, ask them."
	case request.Count > MaxAgentsPerDispatch:
		return fmt.Sprintf(
			"%d agents is more than the limit of %d for one request. If the user really meant that "+
				"many, they can start another batch after these. If this came from a number you were "+
				"not sure about, ask them.",
			request.Count, MaxAgentsPerDispatch)
	}

	profiles := t.dispatcher.Profiles()
	if len(profiles) == 0 {
		return "No profiles are configured, so no agent can be started. The user needs to add a " +
			"credential with `canopy keys add`."
	}

	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	sort.Strings(names)

	if request.Profile == "" {
		if t.current != nil {
			request.Profile = t.current()
		}
		if request.Profile == "" {
			return fmt.Sprintf("No profile was named and this conversation does not have one to "+
				"fall back on. The profiles that exist are: %s. Ask the user which they meant.",
				strings.Join(names, ", "))
		}
	}

	for _, profile := range profiles {
		// Exact first, so that two profiles differing only in case both stay reachable.
		if profile.Name == request.Profile {
			request.Model = profile.Model
			return ""
		}
	}
	matched := 0
	var found Profile
	for _, profile := range profiles {
		if strings.EqualFold(profile.Name, request.Profile) {
			matched++
			found = profile
		}
	}
	if matched == 1 {
		request.Profile = found.Name
		request.Model = found.Model
		return ""
	}

	return fmt.Sprintf("There is no profile called %q. The profiles that exist are: %s. "+
		"Use one of those, or ask the user which they meant.",
		request.Profile, strings.Join(names, ", "))
}

// resolveModel turns the words somebody used for a model into one that will actually be asked for,
// and picks the credential to ask on when they did not name one.
//
// The rules, which are D-46's rule 3 in code:
//
//   - Spelling is forgiven. Case, spaces, underscores, dots and a missing claude- prefix are all
//     the same request typed differently, and a bare family word means the newest of that family.
//   - The credential is the profile argument's when there was one, the conversation's own when that
//     offers the model, and otherwise the only profile that does. Never a choice between two:
//     picking which key gets billed is not a decision to make on somebody's behalf.
//   - Anything unknown or ambiguous is refused with the real choices listed. A model told which
//     models exist picks one; a model told "no" guesses again, and a guess that spawns the wrong
//     model spends real money politely.
func (t *spawnTool) resolveModel(request *Dispatch, spoken string, named bool) string {
	profiles := t.dispatcher.Profiles()

	// Where the request already points gets first refusal. Somebody who says "two sonnet agents"
	// while talking on a key that offers sonnet means that key, and moving them onto another one
	// because it also has sonnet would move their bill without saying so.
	for _, profile := range profiles {
		if profile.Name != request.Profile {
			continue
		}
		hits := catalog.Match(offeredBy(profile), spoken)
		if len(hits) == 1 {
			request.Model, request.ModelNamed = hits[0].ID, true
			return ""
		}
		if len(hits) > 1 {
			return fmt.Sprintf(
				"%q could mean more than one model on %s: %s. Ask the user which they meant.",
				spoken, profile.Name, strings.Join(idsOf(hits), " or "))
		}
		break
	}

	if named {
		// A named profile is not a suggestion. Quietly running the agents on a different credential
		// because that one happens to have the model would be answering a question nobody asked.
		return fmt.Sprintf("%s cannot run %q. %s Use one of those, name a different profile, "+
			"or ask the user which they meant.",
			request.Profile, spoken, whatRuns(profiles))
	}

	var offering []Profile
	var hits []catalog.Model
	for _, profile := range profiles {
		if found := catalog.Match(offeredBy(profile), spoken); len(found) > 0 {
			offering = append(offering, profile)
			hits = found
		}
	}

	switch len(offering) {
	case 1:
		if len(hits) > 1 {
			return fmt.Sprintf(
				"%q could mean more than one model on %s: %s. Ask the user which they meant.",
				spoken, offering[0].Name, strings.Join(idsOf(hits), " or "))
		}
		request.Profile = offering[0].Name
		request.Model, request.ModelNamed = hits[0].ID, true
		return ""

	case 0:
		return fmt.Sprintf("No profile here can run %q. %s Ask the user which they meant, or "+
			"they can add the model with `canopy keys models add`.", spoken, whatRuns(profiles))

	default:
		names := make([]string, 0, len(offering))
		for _, profile := range offering {
			names = append(names, profile.Name)
		}
		sort.Strings(names)
		return fmt.Sprintf("%q is offered by more than one profile: %s. Which credential gets "+
			"billed is not something to decide for the user, so ask them which to use and pass it "+
			"as the profile.", spoken, strings.Join(names, ", "))
	}
}

// offeredBy is everything a profile can be asked for by name.
//
// Its list plus its own default, which is not always in the list. A key pointed at a gateway nobody
// here ships a lineup for has an empty list and a default its owner typed, and without this that
// default was the one model the profile was certainly about to run and the one model it refused to
// be asked for. The refusal then contradicted itself out loud: "no profile here can run
// moonshot-v1-8k. nim runs moonshot-v1-8k."
//
// Matching and the refusals both read this, which is what stops them ever disagreeing again. The
// default is compared with spelling forgiven, so a list that already holds it in another
// capitalisation does not gain a second row for the same model.
func offeredBy(profile Profile) []catalog.Model {
	if profile.Model == "" {
		return profile.Models
	}
	for _, model := range profile.Models {
		if catalog.SameModel(model.ID, profile.Model) {
			return profile.Models
		}
	}
	// A fresh slice, since appending to the profile's own would write into whatever built it.
	offered := make([]catalog.Model, 0, len(profile.Models)+1)
	offered = append(offered, profile.Models...)
	return append(offered, catalog.Model{ID: profile.Model})
}

// whatRuns is every profile and what it can be asked for, which is what a refusal has to carry.
//
// A refusal that only says no sends a model round again with another guess. A refusal that names the
// real choices ends the exchange in one turn.
func whatRuns(profiles []Profile) string {
	lines := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		offered := offeredBy(profile)
		if len(offered) == 0 {
			lines = append(lines, fmt.Sprintf("%s runs %s", profile.Name, orDefault(profile.Model)))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s runs %s",
			profile.Name, strings.Join(idsOf(offered), ", ")))
	}
	sort.Strings(lines)
	return strings.Join(lines, "; ") + "."
}

func orDefault(model string) string {
	if model == "" {
		return "its provider's default"
	}
	return model
}

func idsOf(models []catalog.Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// estimate prices the run, on the model it will actually happen on where the dispatcher can tell
// the difference. A dispatcher that cannot is asked the question it does understand.
//
// "The model it will actually happen on" is meant literally and covers the ordinary spawn as well as
// the one that named a model: by this point resolve has filled in the profile's own default, so a
// request nobody attached a model to is still priced against the model its agents will run rather
// than against the project's history at large. That is the better answer in both cases, and it is
// the same answer, which is why there is no branch here for one of them.
func (t *spawnTool) estimate(request Dispatch) Estimate {
	if on, ok := t.dispatcher.(ModelEstimator); ok && request.Model != "" {
		return on.EstimateOn(request.Task, request.Count, request.Model)
	}
	return t.dispatcher.Estimate(request.Task, request.Count)
}

// warnings are things worth saying that are not reasons to refuse.
func (t *spawnTool) warnings(request Dispatch, estimate Estimate) []string {
	var warnings []string

	if !estimate.Known() {
		warnings = append(warnings, "there is no cost history for this project yet, so the estimate "+
			"is a guess and is not shown")
	}
	for _, profile := range t.dispatcher.Profiles() {
		if profile.Name == request.Profile && !profile.Priced {
			warnings = append(warnings,
				"canopy does not know what this profile costs, so nothing here will be billed "+
					"against a budget")
		}
	}

	running, limit := t.dispatcher.Concurrency()
	if running+request.Count > limit {
		warnings = append(warnings, fmt.Sprintf(
			"this would put %d agents against a limit of %d, so some will wait", running+request.Count, limit))
	}
	if request.Count > 1 && !request.Isolated {
		// The one that actually loses work. Several agents editing one checkout overwrite each
		// other, and it shows up as one agent's changes vanishing rather than as an error.
		warnings = append(warnings, "these agents will share this checkout and can overwrite each "+
			"other's work")
	}
	return warnings
}

// MaxConcurrentAgents is how many agents may run at once.
//
// A number rather than no limit, because the failure mode without one is silent: eight agents each
// running a test suite on a laptop makes every one of them slower, and the person watching sees a
// tool that has become sluggish rather than a limit they chose to exceed.
const MaxConcurrentAgents = 8

// Estimate is what a fan out is expected to cost, from this project's own history.
//
// Deliberately crude, and the basis says so. Cost per turn is measured from turns that actually
// happened here, and the number of turns an agent takes is a range wide enough to be honest about
// not knowing. A narrow range computed from four samples would look like a measurement.
func (e *Engine) Estimate(task string, count int) Estimate {
	return e.EstimateOn(task, count, "")
}

// EstimateOn is the same question asked about a particular model.
//
// Worth asking separately because the models a key can run differ in price by a factor of ten, so
// an estimate built from opus turns is not an estimate of a haiku fan out. The model's own history
// is used when there is enough of it and the project's is used when there is not, and the basis says
// which, since a range from three turns of the right model is not obviously better than one from
// thirty of the wrong model and pretending otherwise would be the same overconfidence this whole
// function is written against.
func (e *Engine) EstimateOn(task string, count int, model string) Estimate {
	const (
		fewestTurns = 4
		mostTurns   = 25
		// Three is not a statistical threshold, it is the point below which showing a number would be
		// pretending. One expensive turn is not a rate.
		fewestSamples = 3
	)

	var costs, onModel []float64
	wanted := taskWords(task)
	e.mu.Lock()
	for _, id := range e.order {
		if e.projects[id] == "" || e.projects[id] != e.projectID {
			continue
		}
		for _, turn := range e.sessions[id].Turns {
			if turn.Usage.CostKnown && turn.Usage.CostUSD > 0 && similarTask(wanted, taskWords(turn.Request.Text)) {
				costs = append(costs, turn.Usage.CostUSD)
				if model != "" && turn.Model == model {
					onModel = append(onModel, turn.Usage.CostUSD)
				}
			}
		}
	}
	e.mu.Unlock()

	measured := "similar priced turns in this project"
	if len(onModel) >= fewestSamples {
		costs = onModel
		measured = "similar priced turns in this project on " + model
	}

	if len(costs) < fewestSamples {
		return Estimate{Basis: fmt.Sprintf(
			"there is not enough similar cost history in this project to estimate, %d priced turns matched",
			len(costs))}
	}

	sort.Float64s(costs)
	median := costs[len(costs)/2]

	confidence := "low"
	if len(costs) >= 15 {
		confidence = "high"
	} else if len(costs) >= 6 {
		confidence = "medium"
	}
	return Estimate{
		Low:        median * fewestTurns * float64(count),
		High:       median * mostTurns * float64(count),
		Samples:    len(costs),
		Confidence: confidence,
		Basis: fmt.Sprintf("from %d %s at a median of $%.3f, assuming %d to %d turns per agent",
			len(costs), measured, median, fewestTurns, mostTurns),
	}
}

func taskWords(text string) map[string]bool {
	stop := map[string]bool{
		"and": true, "for": true, "from": true, "into": true, "the": true, "this": true,
		"that": true, "with": true, "you": true, "your": true,
	}
	out := make(map[string]bool)
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r < 'a' || r > 'z'
	}) {
		if len(word) >= 3 && !stop[word] {
			out[word] = true
		}
	}
	return out
}

func similarTask(wanted, candidate map[string]bool) bool {
	if len(wanted) == 0 || len(candidate) == 0 {
		return false
	}
	common := 0
	for word := range wanted {
		if candidate[word] {
			common++
		}
	}
	if len(wanted) == 1 {
		return common == 1
	}
	return common >= 2 || float64(common)/float64(len(wanted)) >= 0.4
}

// Concurrency reports how many agents are running and how many may.
func (e *Engine) Concurrency() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	running := 0
	for _, agent := range e.agents {
		if session, ok := e.sessions[agent.SessionID]; ok {
			if _, active := session.Active(); active {
				running++
			}
		}
	}
	return running, MaxConcurrentAgents
}

// Spawn creates the agents a confirmed dispatch asked for and hands each of them the task.
//
// Named from the task rather than numbered, so six rows in the agents view are distinguishable at a
// glance. The suffix is what keeps them unique when the same task is fanned out.
func (e *Engine) Spawn(ctx context.Context, request Dispatch) ([]Agent, error) {
	running, limit := e.Concurrency()
	if running+request.Count > limit {
		return nil, fmt.Errorf("%d agents are already running and the limit is %d", running, limit)
	}

	template, err := e.dispatchTemplate(request)
	if err != nil {
		return nil, err
	}

	created := make([]Agent, 0, request.Count)
	for i := range request.Count {
		agent := template
		agent.Name = dispatchName(request.Task, i, request.Count)
		agent.Isolated = request.Isolated
		agent.Dispatched = true

		started, err := e.AddAgent(ctx, agent)
		if err != nil {
			// Whatever was created stays created rather than being unwound. Half a fan out is
			// something a person can look at and act on; silently removing agents that had already
			// started work would destroy it to tidy up.
			return created, fmt.Errorf("after creating %d of %d agents: %w", len(created), request.Count, err)
		}
		created = append(created, started)

		if _, err := e.Send(started.SessionID, request.Task); err != nil {
			return created, fmt.Errorf("%s was created but could not be given the task: %w", started.Name, err)
		}
	}
	return created, nil
}

// dispatchTemplate is the credential, model and trust a spawned agent starts from.
func (e *Engine) dispatchTemplate(request Dispatch) (Agent, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// The model and trust come from an existing agent on the same profile where there is one, so a
	// fan out inherits the posture somebody already chose rather than resetting to a default they
	// would have to notice and change six times. Walked in creation order rather than over the map,
	// so two agents on one profile with different settings always yield the same template.
	for _, name := range e.agentOrder {
		if agent := e.agents[name]; agent.KeyName == request.Profile {
			model := agent.Model
			if request.ModelNamed {
				// Unless the request said which model. Somebody asking for sonnet agents from a
				// conversation already running opus means sonnet, and inheriting here would answer
				// them with two more of what they were trying to move away from.
				model = request.Model
			}
			return Agent{KeyName: request.Profile, Model: model, Trust: agent.Trust, Dir: agent.Dir}, nil
		}
	}
	if len(e.agentOrder) == 0 {
		return Agent{}, fmt.Errorf("there is no agent to copy a working directory from yet")
	}

	// Nobody has run this profile here before, so the model is the profile's own default rather
	// than an existing agent's. A model copied across profiles can pair one provider's key with
	// another provider's model name, and every spawned agent then fails on its first request.
	if request.Model == "" {
		return Agent{}, fmt.Errorf(
			"profile %q has no default model, so a new agent on it would not know what to run; "+
				"set one with `canopy keys model %s <model>`", request.Profile, request.Profile)
	}
	first := e.agents[e.agentOrder[0]]
	return Agent{KeyName: request.Profile, Model: request.Model, Trust: first.Trust, Dir: first.Dir}, nil
}

// dispatchName turns a task into something readable in a list of six.
func dispatchName(task string, index, total int) string {
	words := strings.Fields(strings.ToLower(task))

	var parts []string
	for _, word := range words {
		trimmed := strings.Trim(word, ".,:;!?\"'()[]")
		// Short words carry no meaning in a two word label and "the-auth" reads worse than "auth".
		if len(trimmed) < 4 || len(parts) == 2 {
			continue
		}
		clean := make([]rune, 0, len(trimmed))
		for _, r := range trimmed {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				clean = append(clean, r)
			}
		}
		if len(clean) >= 4 {
			parts = append(parts, string(clean))
		}
	}

	name := strings.Join(parts, "-")
	if name == "" {
		name = "agent"
	}
	if total > 1 {
		name = fmt.Sprintf("%s-%d", name, index+1)
	}
	return name
}
