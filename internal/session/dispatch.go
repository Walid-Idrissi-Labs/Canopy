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

	// Priced says whether Canopy knows what this profile costs. An OpenAI compatible gateway with no
	// rate set does not, and an estimate that quietly treated it as free would be a lie in the one
	// place a number is supposed to protect somebody.
	Priced bool
}

// Dispatch is a request to create agents.
type Dispatch struct {
	Count   int
	Profile string
	Task    string

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
}

// Known reports whether there is enough history for the range to mean anything.
func (e Estimate) Known() bool { return e.Samples > 0 }

// Summary is the line shown on the confirmation.
func (e Estimate) Summary() string {
	if !e.Known() {
		return e.Basis
	}
	return fmt.Sprintf("about $%.2f to $%.2f, %s", e.Low, e.High, e.Basis)
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
	return fmt.Sprintf("start %s on %s, %s, for: %s",
		agents, c.Dispatch.Profile, where, c.Dispatch.Task)
}

// ErrNeedsConfirmation is returned by the spawn tool when nobody has said yes yet.
//
// A sentinel rather than a bool, because the caller that has to notice this is the tool loop, and a
// bool on a result is a thing that gets ignored.
var ErrNeedsConfirmation = fmt.Errorf("this needs to be confirmed before anything is created")

// DispatchTools returns the tools an orchestrating agent uses to create other agents.
func DispatchTools(dispatcher Dispatcher, confirm func(Confirmation) bool) []core.Tool {
	return []core.Tool{
		&profilesTool{dispatcher: dispatcher},
		&spawnTool{dispatcher: dispatcher, confirm: confirm},
	}
}

type profilesTool struct{ dispatcher Dispatcher }

func (t *profilesTool) Name() string        { return "list_profiles" }
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

	var out strings.Builder
	fmt.Fprintf(&out, "%d profiles. %d of %d agent slots are in use.\n\n", len(profiles), running, limit)
	for _, profile := range profiles {
		fmt.Fprintf(&out, "- %s (%s)", profile.Name, profile.Model)
		if !profile.Priced {
			// Said here rather than only at spawn time, so a model choosing between profiles can
			// prefer one whose cost is knowable.
			out.WriteString(", cost unknown for this endpoint")
		}
		out.WriteString("\n")
	}
	return core.ToolResult{Content: out.String()}, nil
}

type spawnTool struct {
	dispatcher Dispatcher
	confirm    func(Confirmation) bool
}

func (t *spawnTool) Name() string { return "spawn_agents" }

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
		"task from what they said. If any of the three is unclear, ask them rather than guessing: " +
		"spawning the wrong number or the wrong profile costs real money and real time. Call " +
		"list_profiles first if you are not certain the profile exists."
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
				"description": "The profile name to run them on, from list_profiles."
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
		"required": ["count", "profile", "task"]
	}`)
}

func (t *spawnTool) Run(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Count    int     `json:"count"`
		Profile  string  `json:"profile"`
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

	if refusal := t.check(request); refusal != "" {
		return core.ToolResult{Content: refusal, IsError: true}, nil
	}

	estimate := t.dispatcher.Estimate(request.Task, request.Count)
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

// check refuses a request against reality, and says what reality is.
//
// Every refusal names the correct answer. A model told "unknown profile" guesses again; a model
// told which profiles exist picks one.
func (t *spawnTool) check(request Dispatch) string {
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
	for _, profile := range profiles {
		if profile.Name == request.Profile {
			return ""
		}
	}

	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	sort.Strings(names)
	return fmt.Sprintf("There is no profile called %q. The profiles that exist are: %s. "+
		"Use one of those, or ask the user which they meant.",
		request.Profile, strings.Join(names, ", "))
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
