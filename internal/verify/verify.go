package verify

// Verification per agent, which is the thing this project exists to do.
//
// Everything underneath it already exists. A6-01 says what revision a worktree is at, A6-03 runs the
// tests and records which revision each result belongs to, and the roll-up in core decides what a
// user is told. This package is the part that holds one of those per agent and refuses to answer
// questions the evidence does not support.
//
// The refusal is the feature. Ranking three agents is easy if you are willing to guess; the reason
// nobody does it is that the guess is usually wrong, because the winning branch has been edited
// since its tests ran. So an agent whose evidence is stale is not ranked low, it is not ranked at
// all, and it says why.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/git"
)

// Subject is one agent, as far as verification is concerned.
//
// Deliberately not the session Agent type. Verification needs a name, a worktree and a branch, and
// nothing about models, transcripts or trust levels. Keeping the dependency this thin is what lets
// the primary checkout be a subject too, which matters because the common case is one person with
// one checkout and no isolated agents at all.
type Subject struct {
	Agent       string
	WorkspaceID string
	Dir         string
	Branch      string
}

// Verifier holds what is known about every agent's evidence.
type Verifier struct {
	repo   *git.Repo
	runner *exec.Runner

	// base is the branch an agent's work is measured against, usually the default branch.
	base string

	// tests are the configured commands. Empty is a legitimate configuration, and it produces
	// "nothing is configured" rather than a green roll-up.
	tests []exec.Test

	publish func(core.Event)

	mu       sync.Mutex
	subjects map[string]Subject
	revision map[string]core.RevisionKey
	reason   map[string]string
	latest   map[string]map[string]core.TestRun
	diffs    map[string]git.Stat
	order    []string
}

// New returns a verifier. publish may be nil.
func New(repo *git.Repo, base string, tests []exec.Test, publish func(core.Event)) *Verifier {
	if base == "" {
		base = "main"
	}
	if publish == nil {
		publish = func(core.Event) {}
	}

	v := &Verifier{
		repo:     repo,
		base:     base,
		tests:    tests,
		publish:  publish,
		subjects: make(map[string]Subject),
		revision: make(map[string]core.RevisionKey),
		reason:   make(map[string]string),
		latest:   make(map[string]map[string]core.TestRun),
		diffs:    make(map[string]git.Stat),
	}
	v.runner = exec.NewRunner(v.record)
	return v
}

// Runner exposes the test runner, so a caller can cancel a run or read its output.
func (v *Verifier) Runner() *exec.Runner { return v.runner }

// Watch replaces the set of agents being verified.
//
// Wholesale, matching discovery and the poller. An agent that has ended stops being in the list, and
// its evidence goes with it: a result about a worktree that no longer exists is not evidence, it is
// a row nobody can act on.
func (v *Verifier) Watch(subjects []Subject) {
	v.mu.Lock()
	defer v.mu.Unlock()

	fresh := make(map[string]Subject, len(subjects))
	order := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		fresh[subject.Agent] = subject
		order = append(order, subject.Agent)
	}

	for name := range v.subjects {
		if _, still := fresh[name]; !still {
			delete(v.revision, name)
			delete(v.reason, name)
			delete(v.latest, name)
			delete(v.diffs, name)
		}
	}
	v.subjects, v.order = fresh, order
}

// Observe takes a revision change from the poller.
//
// Nothing recomputes here beyond the diff, because staleness is derived rather than stored: a
// result recorded against the old revision becomes stale the moment this lands, without anything
// having to go around marking it.
func (v *Verifier) Observe(ctx context.Context, change git.Change) {
	v.mu.Lock()
	name := ""
	for agent, subject := range v.subjects {
		if subject.WorkspaceID == change.WorkspaceID {
			name = agent
			break
		}
	}
	if name == "" {
		v.mu.Unlock()
		return
	}
	v.revision[name] = change.To
	v.reason[name] = change.Reason
	subject := v.subjects[name]
	v.mu.Unlock()

	if stat, err := v.repo.Diff(ctx, subject.Dir, v.base); err == nil {
		v.mu.Lock()
		v.diffs[name] = stat
		v.mu.Unlock()
	}
}

// record takes a test run update from the runner.
func (v *Verifier) record(run core.TestRun) {
	v.mu.Lock()
	name := ""
	for agent, subject := range v.subjects {
		if subject.WorkspaceID == run.WorkspaceID {
			name = agent
			break
		}
	}
	if name != "" {
		if v.latest[name] == nil {
			v.latest[name] = make(map[string]core.TestRun)
		}
		v.latest[name][run.TestName] = run
	}
	v.mu.Unlock()

	v.publish(core.Event{
		Kind:        core.EventTestRunUpdated,
		WorkspaceID: run.WorkspaceID,
		TestName:    run.TestName,
		RunID:       run.ID,
		Final:       run.State.IsTerminal(),
	})
}

// ErrUnknownAgent is returned for a name nobody is watching.
var ErrUnknownAgent = errors.New("no agent by that name is being verified")

// Verify starts every configured test for one agent.
func (v *Verifier) Verify(ctx context.Context, agent string) error {
	v.mu.Lock()
	subject, ok := v.subjects[agent]
	tests := v.tests
	v.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, agent)
	}
	if len(tests) == 0 {
		return errors.New("no test is configured, so there is nothing to run")
	}

	target := exec.Target{
		WorkspaceID: subject.WorkspaceID,
		Dir:         subject.Dir,
		Revision: func(ctx context.Context) (core.RevisionKey, string) {
			return v.repo.Revision(ctx, subject.Dir)
		},
	}
	for _, test := range tests {
		if _, err := v.runner.Start(ctx, test, target); err != nil {
			return fmt.Errorf("starting %s for %s: %w", test.Name, agent, err)
		}
	}
	return nil
}

// VerifyAll starts the configured tests for every agent being watched.
//
// The fan out at A6-05 is meaningless without it: ranking three attempts requires three sets of
// evidence, and asking a user to trigger each one by hand is asking them to forget one.
func (v *Verifier) VerifyAll(ctx context.Context) error {
	v.mu.Lock()
	names := append([]string(nil), v.order...)
	v.mu.Unlock()

	var failures []string
	for _, name := range names {
		if err := v.Verify(ctx, name); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// Snapshot assembles what the roll-up needs for one agent.
//
// Built here rather than stored, so there is no second copy of the truth to fall out of date. Every
// field comes from the poller, the runner or the configuration, and the derivation from those to a
// displayed state lives in core where it is already tested.
func (v *Verifier) Snapshot(agent string) (core.WorkspaceSnapshot, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.snapshotLocked(agent)
}

func (v *Verifier) snapshotLocked(agent string) (core.WorkspaceSnapshot, bool) {
	subject, ok := v.subjects[agent]
	if !ok {
		return core.WorkspaceSnapshot{}, false
	}

	snapshot := core.WorkspaceSnapshot{
		ID:            subject.WorkspaceID,
		Name:          subject.Agent,
		Path:          subject.Dir,
		Branch:        subject.Branch,
		Ownership:     core.OwnershipManaged,
		Revision:      v.revision[agent],
		RevisionError: v.reason[agent],
	}

	runs := v.latest[agent]
	for _, test := range v.tests {
		snap := core.TestSnapshot{Name: test.Name, Required: test.Required}
		if run, ran := runs[test.Name]; ran {
			// Copied into a local so the pointer does not alias the map's value across iterations,
			// which would leave every snapshot pointing at the last run in the loop.
			latest := run
			snap.Latest = &latest
		}
		snapshot.Tests = append(snapshot.Tests, snap)
	}
	return snapshot, true
}

// Rollup is the verification summary for one agent.
func (v *Verifier) Rollup(agent string) (core.Rollup, bool) {
	snapshot, ok := v.Snapshot(agent)
	if !ok {
		return core.Rollup{}, false
	}
	return core.RollUp(snapshot), true
}

// Diff returns the size of an agent's work as last measured.
func (v *Verifier) Diff(agent string) git.Stat {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.diffs[agent]
}
