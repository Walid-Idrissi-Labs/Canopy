package session

// Isolating an agent, and the assumption this exists to avoid.
//
// **An agent is not a branch.** The re-plan treated every agent as isolated, which quietly made
// "run an agent" mean "make a worktree" and charged the common case for the rare one. Most work is
// one person, one agent, one checkout, and that should stay ordinary. Isolation is a mode an agent
// is put into when there is a reason for it: several agents editing the same files, or the fan out
// at A6-05, where ranking three attempts is meaningless if all three are writing to one tree.
//
// Isolation is three things, and the middle one is the one that matters:
//
//  1. A worktree and a branch of its own, so the files are separate.
//  2. A tool registry rooted at that worktree, so the confinement is *enforced* rather than
//     intended. The agent is not asked to stay inside its worktree; it is handed a set of tools
//     that cannot express anywhere else. `tools.Workspace` already refuses a path that resolves
//     outside its root, so building one registry per worktree is the whole mechanism.
//  3. A disposition when it ends, because whatever is in there is work.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/git"
)

// Isolation is what an engine needs before it can give an agent a worktree of its own.
type Isolation struct {
	// Repo is the repository worktrees are made in. Required.
	Repo *git.Repo

	// Tools builds the tool set for a directory. Required.
	//
	// A function rather than a registry, because the entire point is that each isolated agent gets
	// one rooted somewhere different. Supplied by the caller rather than built in here because the
	// engine has never known what tools exist and should not learn now: it is handed a registry and
	// runs whatever is in it.
	Tools func(dir string) (*core.ToolRegistry, error)

	// Environment and Confirm describe how a fresh worktree is brought to a runnable state. Used by
	// PrepareAgent rather than at creation, and optional: a repository needing no preparation is
	// the common case.
	Environment git.Environment
	Confirm     git.Confirm
}

// WithIsolation lets agents in this engine opt into a worktree of their own.
//
// Optional, and its absence is not an error until somebody actually asks for an isolated agent.
// Canopy runs in directories that are not git repositories at all, and refusing to start there for
// want of an isolation mode nobody asked for would be the rare case taxing the common one again.
func (e *Engine) WithIsolation(isolation Isolation) error {
	switch {
	case isolation.Repo == nil:
		return errors.New("isolation needs a repository to make worktrees in")
	case isolation.Tools == nil:
		return errors.New("isolation needs a way to build tools for a worktree")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.isolation = &isolation
	return nil
}

// isolate gives an agent a worktree, a branch and a tool set confined to them.
//
// Ordered so a failure leaves nothing behind. If the tools cannot be built the worktree is removed
// again, because a worktree with no agent attached is a directory with a branch checked out that
// nobody will ever think to clean up.
func (e *Engine) isolate(ctx context.Context, agent *Agent) (*core.ToolRegistry, error) {
	e.mu.Lock()
	isolation := e.isolation
	e.mu.Unlock()

	if isolation == nil {
		return nil, fmt.Errorf(
			"%s asked for a worktree of its own, but this engine has no repository to make one in",
			agent.Name)
	}

	workspace, err := isolation.Repo.Create(ctx, agent.Name, agent.Branch)
	if err != nil {
		return nil, fmt.Errorf("making a worktree for %s: %w", agent.Name, err)
	}

	registry, err := isolation.Tools(workspace.Path)
	if err != nil {
		// Undone with the context detached, because the usual reason to be here is that the caller's
		// context was cancelled, and cleaning up is exactly the work that must still happen then.
		_ = isolation.Repo.Remove(context.WithoutCancel(ctx), workspace, true)
		return nil, fmt.Errorf("building tools for %s: %w", agent.Name, err)
	}

	agent.Dir = workspace.Path
	agent.WorkspaceID = workspace.ID
	agent.Branch = workspace.Branch
	return registry, nil
}

// toolsForLocked returns the tool set and trust level a session runs with.
//
// Called with the engine lock already held, since the turn takes it once for everything it needs.
//
// Two lookups rather than one because they answer to different things. The registry is what an
// isolated agent gets instead of the engine's, and it is what makes confinement structural. The
// trust level is per agent whether or not it is isolated: A4-04 stored one on every agent and
// nothing read it, so until now an agent configured as read only ran with whatever the engine was
// set to. Isolation is where that gap would have bitten hardest, since the reason to confine an
// agent to a worktree is usually to let it work more freely inside one.
func (e *Engine) toolsForLocked(sessionID string) (*core.ToolRegistry, core.TrustLevel) {
	tools, trust := e.tools, e.trust

	if registry, ok := e.agentTools[sessionID]; ok {
		tools = registry
	}
	for _, agent := range e.agents {
		if agent.SessionID == sessionID && agent.Trust != "" {
			trust = agent.Trust
			break
		}
	}
	return tools, trust
}

// PrepareAgent brings an isolated agent's worktree to a state where the project runs.
//
// Separate from AddAgent, deliberately. Making a worktree takes a moment; installing dependencies
// into one takes minutes, and an interface that blocked on the second while appearing to do the
// first would look frozen at exactly the moment somebody is watching. Splitting them also gives the
// result somewhere to be displayed, which is what stops a failed setup being invisible.
func (e *Engine) PrepareAgent(ctx context.Context, name string) (git.Prepared, error) {
	agent, ok := e.Agent(name)
	if !ok {
		return git.Prepared{}, fmt.Errorf("no agent called %q", name)
	}
	if !agent.Isolated {
		return git.Prepared{}, fmt.Errorf(
			"%s works in the repository itself rather than a worktree of its own, "+
				"so there is nothing to prepare", name)
	}

	e.mu.Lock()
	isolation := e.isolation
	e.mu.Unlock()

	if isolation == nil {
		return git.Prepared{}, errors.New("this engine has no repository to prepare a worktree in")
	}
	return isolation.Repo.Prepare(ctx, agent.workspace(), isolation.Environment, isolation.Confirm)
}

// workspace rebuilds the snapshot for an isolated agent's worktree.
//
// Ownership is asserted as managed rather than rediscovered, because the only way an agent has a
// WorkspaceID at all is that isolate created the worktree, and Create writes the marker that makes
// it Canopy's. Rediscovering would be a second source of truth for the same fact.
func (a Agent) workspace() core.WorkspaceSnapshot {
	return core.WorkspaceSnapshot{
		ID:        a.WorkspaceID,
		Name:      filepath.Base(a.Dir),
		Path:      a.Dir,
		Branch:    a.Branch,
		Ownership: core.OwnershipManaged,
	}
}

// Disposition is what happens to an isolated agent's worktree when the agent ends.
//
// Three rather than two, because "remove" and "throw away uncommitted work" are different answers
// to different questions and must not be reachable by the same keystroke. An agent's abandoned
// experiment is sometimes the only copy of an idea.
type Disposition string

const (
	// KeepWorktree leaves the worktree and its branch alone. The safe answer, and the default for
	// anything that is not an explicit choice.
	KeepWorktree Disposition = "keep"

	// RemoveWorktree deletes it, and refuses if there is uncommitted work in there.
	RemoveWorktree Disposition = "remove"

	// DiscardWorktree deletes it along with whatever was uncommitted. Only ever reachable from a
	// second, explicit answer given after the refusal above, never as a default and never as a
	// retry that happens automatically.
	DiscardWorktree Disposition = "discard"
)

// Valid reports whether a disposition is one of the three.
func (d Disposition) Valid() bool {
	switch d {
	case KeepWorktree, RemoveWorktree, DiscardWorktree:
		return true
	default:
		return false
	}
}

func (d Disposition) String() string { return string(d) }

// EndAgent dismisses an agent and does something deliberate with its worktree.
//
// The conversation is kept in every case, as RemoveAgent already promises: an agent is a worker and
// its transcript is a record of what was done, and dismissing the worker is not a reason to burn
// the record.
//
// A refused removal leaves the agent registered. That matters more than it looks: forgetting the
// agent and then failing to remove its worktree would leave a directory on disk with uncommitted
// work in it and nothing in Canopy that still refers to it, which is precisely how work gets lost
// quietly. The failure keeps the handle that leads back to it.
func (e *Engine) EndAgent(ctx context.Context, name string, disposition Disposition) error {
	if !disposition.Valid() {
		return fmt.Errorf("%q is not something to do with a worktree", disposition)
	}

	agent, ok := e.Agent(name)
	if !ok {
		return fmt.Errorf("no agent called %q", name)
	}

	if agent.Isolated && disposition != KeepWorktree {
		e.mu.Lock()
		isolation := e.isolation
		e.mu.Unlock()

		if isolation == nil {
			return errors.New("this engine has no repository, so it cannot remove a worktree")
		}
		// force only for discard, which is the answer somebody had to give a second time. Remove
		// refuses a dirty worktree on its own and reports what is in there, so this never has to
		// decide what counts as work worth keeping.
		err := isolation.Repo.Remove(ctx, agent.workspace(), disposition == DiscardWorktree)
		if err != nil {
			return fmt.Errorf("ending %s: %w", name, err)
		}
	}

	return e.RemoveAgent(name)
}
