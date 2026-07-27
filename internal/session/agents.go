package session

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// Several agents in one engine, and the two things that keeps separate.
//
// **A session per agent**, so one agent's conversation cannot reach another's. That is already true
// of the engine: sessions are keyed by ID and a turn only ever touches the one it belongs to.
//
// **A working directory per agent**, which defaults to the repository. An agent is not a worktree.
// Coupling the two would make "run an agent" mean "make a branch", which is not how anyone works
// most of the time and would make the common case pay for the rare one. Isolation is a mode an agent
// is put into, and A5-11 is where that lands.

// Agent is a named worker with its own conversation.
type Agent struct {
	// Name is what a person calls it: "refactor", "docs", "the one on the parser". Chosen rather
	// than generated, because a list of eight agents called agent-1 through agent-8 is a list
	// nobody can navigate.
	Name string

	// SessionID is the conversation this agent is having.
	SessionID string

	// KeyName is the credential it uses, and Model the model. Both per agent, which is where the
	// named key model from A1 pays off: one agent on Claude and one on a local model is a
	// configuration rather than a fork.
	KeyName string
	Model   string

	// Dir is where its tools operate. The repository unless it has been isolated.
	Dir string

	// Trust is how much it may do without asking.
	Trust core.TrustLevel

	// Isolated says it has a worktree of its own. Recorded here rather than inferred from Dir,
	// because a user could legitimately point an agent at a directory that happens to be a worktree
	// somebody else made, and that is not the same thing at all.
	Isolated bool
	// WorkspaceID is the worktree, for an isolated agent.
	WorkspaceID string
}

// AddAgent registers a named agent with a conversation of its own.
func (e *Engine) AddAgent(agent Agent) (Agent, error) {
	if err := validateAgentName(agent.Name); err != nil {
		return Agent{}, err
	}

	e.mu.Lock()
	if e.agents == nil {
		e.agents = map[string]*Agent{}
	}
	if _, taken := e.agents[agent.Name]; taken {
		e.mu.Unlock()
		// Refused rather than replaced. Two agents with one name means every later reference is
		// ambiguous, including the ones in the audit trail, which is where ambiguity costs most.
		return Agent{}, fmt.Errorf("there is already an agent called %q", agent.Name)
	}
	e.mu.Unlock()

	session := e.Create(agent.KeyName, agent.Model)
	agent.SessionID = session.ID

	if agent.Trust == "" {
		e.mu.Lock()
		agent.Trust = e.trust
		e.mu.Unlock()
	}

	e.mu.Lock()
	stored := agent
	e.agents[agent.Name] = &stored
	e.agentOrder = append(e.agentOrder, agent.Name)
	e.mu.Unlock()

	e.events.Publish(core.Event{Kind: core.EventSessionsChanged, SessionID: session.ID})
	return agent, nil
}

// Agent returns one agent by name.
func (e *Engine) Agent(name string) (Agent, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	agent, ok := e.agents[name]
	if !ok {
		return Agent{}, false
	}
	return *agent, true
}

// AgentFor returns the agent a session belongs to.
func (e *Engine) AgentFor(sessionID string) (Agent, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, agent := range e.agents {
		if agent.SessionID == sessionID {
			return *agent, true
		}
	}
	return Agent{}, false
}

// Agents returns every agent, in the order they were created.
//
// Creation order rather than alphabetical, because it is the order somebody built them in and
// therefore the order they already have in their head. Sorting would rearrange the list every time
// an agent was added, and a list that moves under you is one you have to re read.
func (e *Engine) Agents() []Agent {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]Agent, 0, len(e.agentOrder))
	for _, name := range e.agentOrder {
		if agent, ok := e.agents[name]; ok {
			out = append(out, *agent)
		}
	}
	return out
}

// RemoveAgent forgets an agent.
//
// The conversation is kept. An agent is a worker and its transcript is a record of what was done,
// and dismissing the worker is not a reason to burn the record. Removing the session as well would
// also make the audit trail refer to a session nobody can look at.
func (e *Engine) RemoveAgent(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.agents[name]; !ok {
		return fmt.Errorf("no agent called %q", name)
	}
	delete(e.agents, name)

	remaining := make([]string, 0, len(e.agentOrder))
	for _, existing := range e.agentOrder {
		if existing != name {
			remaining = append(remaining, existing)
		}
	}
	e.agentOrder = remaining
	return nil
}

// AgentStatus is one agent's line in a list.
type AgentStatus struct {
	Agent Agent
	State core.AgentState

	// Turns and Usage are what it has done and what that cost, attributed per agent, which is the
	// whole reason a key belongs to an agent rather than to the program.
	Turns int
	Usage core.Usage

	// Title is what its conversation is about, which is more use in a list than its name alone.
	Title string

	// Waiting is what it is blocked on, when it is blocked on a person.
	Waiting string
}

// AgentStatuses summarises every agent, the ones needing attention first.
//
// Sorted by need rather than by name, because with eight agents running the useful question is not
// "where is the one called docs" but "which of these has stopped and cannot start again without me".
// The name is how you find a specific one; this ordering is how you find the one that matters.
func (e *Engine) AgentStatuses() []AgentStatus {
	agents := e.Agents()

	out := make([]AgentStatus, 0, len(agents))
	for _, agent := range agents {
		status := AgentStatus{Agent: agent, State: core.AgentIdle}

		if session, ok := e.Session(agent.SessionID); ok {
			status.State = session.AgentState()
			status.Turns = len(session.Turns)
			status.Usage = session.Usage()
			status.Title = session.Title
		}
		if prompt, waiting := e.Pending(agent.SessionID); waiting {
			// Waiting on a person is its own state rather than a flavour of working, because an
			// agent parked here looks busy and is not, and several of them parked here is a queue
			// nobody can see unless it is named.
			status.State = core.AgentAwaitingPermission
			status.Waiting = prompt.Scope().String()
		}
		out = append(out, status)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return attentionRank(out[i].State) < attentionRank(out[j].State)
	})
	return out
}

// attentionRank orders agents by how much they need a person.
func attentionRank(state core.AgentState) int {
	switch state {
	case core.AgentAwaitingPermission:
		return 0
	case core.AgentFailed:
		return 1
	case core.AgentWorking:
		return 2
	case core.AgentStopped:
		return 3
	default:
		return 4
	}
}

// TrailFor returns what one agent actually did.
//
// Per agent because with eight running the interleaved trail is unreadable, and the question is
// almost always about one of them.
func (e *Engine) TrailFor(name string) []permission.Entry {
	agent, ok := e.Agent(name)
	if !ok {
		return nil
	}
	return e.Trail().ForAgent(agent.SessionID)
}

// validateAgentName constrains what an agent may be called.
//
// The same reasoning as credential names: an agent name is displayed, logged, put into events and
// written into the audit trail, so it has to be something that reads well in all of those and cannot
// be mistaken for anything else.
func validateAgentName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return fmt.Errorf("an agent needs a name. Something you would say out loud, like \"parser\"")
	case len(name) > 32:
		return fmt.Errorf("that agent name is too long, use 32 characters or fewer")
	case strings.ContainsAny(name, "\n\t\x00"):
		return fmt.Errorf("an agent name cannot contain control characters")
	case name != strings.TrimSpace(name):
		return fmt.Errorf("an agent name cannot start or end with a space")
	}
	return nil
}
