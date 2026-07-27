package main

// The worktree monitor's data source.
//
// This exists because the shipped program was showing a fake one. `runChat` handed the dashboard
// `fake.New()`, which seeds four invented worktrees called feat-login, fix-cache, refactor-api and
// spike-search, rooted at a path of "/repo", and edited one of them on a six second timer to make
// the screen look alive. It was written when there was no real engine to read from and it was the
// right call then. It stopped being the right call the moment there was one, and nothing on screen
// said any of it was invented.
//
// For a program whose entire argument is that it will not show you a state the evidence does not
// support, shipping a screen of fabricated evidence is the worst available bug. This replaces it
// with what the verifier actually knows.

import (
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/store"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/verify"
)

// worktrees is a core.SnapshotStore over the verification engine.
//
// It owns no state of its own beyond the list of agents to ask about. Everything it reports comes
// from the verifier at the moment it is asked, which is what keeps the dashboard and the review
// screen from being able to disagree: there is one source and two readers.
type worktrees struct {
	mu       sync.Mutex
	root     string
	verifier *verify.Verifier
	watched  []watchedAgent

	events *store.Broker
	now    func() time.Time
}

// watchedAgent is one agent and the workspace it works in.
type watchedAgent struct {
	name string
	path string
}

// newWorktrees builds the store.
//
// A nil verifier is a legitimate state, not a failure: Canopy runs in directories that are not
// repositories, and there the honest answer is that there is nothing to watch. The dashboard says
// so rather than being hidden, in the same way the review screen does.
func newWorktrees(root string, verifier *verify.Verifier) *worktrees {
	return &worktrees{
		root:     root,
		verifier: verifier,
		events:   store.NewBroker(),
		now:      time.Now,
	}
}

// follow points the store at whatever agents currently exist.
func (w *worktrees) follow(agents []watchedAgent) {
	w.mu.Lock()
	w.watched = append([]watchedAgent(nil), agents...)
	w.mu.Unlock()
}

// changed announces that a workspace moved.
//
// Named for what it is rather than made an Observe method, because this store does not observe
// anything: the verifier does, and this is the poller telling the screen that asking again is now
// worth doing.
func (w *worktrees) changed(workspaceID string) {
	w.events.Publish(core.Event{Kind: core.EventRevisionChanged, WorkspaceID: workspaceID})
}

// Snapshot implements core.SnapshotStore.
func (w *worktrees) Snapshot() core.ProjectSnapshot {
	w.mu.Lock()
	root, verifier := w.root, w.verifier
	watched := append([]watchedAgent(nil), w.watched...)
	w.mu.Unlock()

	out := core.ProjectSnapshot{
		Sequence: w.events.Sequence(),
		TakenAt:  w.now(),
		RepoRoot: root,
	}
	if verifier == nil {
		return out
	}

	for _, agent := range watched {
		snapshot, ok := verifier.Snapshot(agent.name)
		if !ok {
			// Being watched and having nothing known about it yet is the state on the first tick,
			// and it is reported rather than skipped. A worktree that vanishes from the list for a
			// second because nobody has looked at it yet reads as the program losing track of it.
			snapshot = core.WorkspaceSnapshot{Name: agent.name, Path: agent.path}
		}
		if snapshot.Name == "" {
			snapshot.Name = agent.name
		}
		if snapshot.Path == "" {
			snapshot.Path = agent.path
		}
		out.Workspaces = append(out.Workspaces, snapshot)
	}
	return out
}

// Events implements core.SnapshotStore.
func (w *worktrees) Events(afterSequence uint64) <-chan core.Event {
	return w.events.Subscribe(afterSequence)
}

// Close releases the subscribers.
func (w *worktrees) Close() { w.events.Close() }
