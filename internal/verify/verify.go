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
	"path/filepath"
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
	diffs    map[string]core.DiffStat
	diffErr  map[string]string
	shared   map[string]string
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
		diffs:    make(map[string]core.DiffStat),
		diffErr:  make(map[string]string),
		shared:   make(map[string]string),
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
	shared := sharedWorkspaces(subjects)

	for name := range v.subjects {
		if _, still := fresh[name]; !still {
			v.clearEvidenceLocked(name)
		}
	}
	for name, next := range fresh {
		previous, existed := v.subjects[name]
		if existed && (previous.WorkspaceID != next.WorkspaceID || previous.Dir != next.Dir) {
			// An agent name is a label. Evidence belongs to the workspace that produced it, and a
			// replacement workspace may legitimately have the same RevisionKey as the old one.
			// Keeping the old run in that case would make an agent green before it ran anything.
			v.clearEvidenceLocked(name)
		}
		if v.shared[name] != shared[name] {
			// Evidence gathered while a workspace was uniquely attributable cannot survive it
			// becoming shared, or vice versa.
			v.clearEvidenceLocked(name)
		}
	}
	v.subjects, v.order, v.shared = fresh, order, shared
}

// cleanPath is the one definition of what makes two workspace directories the same.
//
// One function rather than filepath.Clean at each site, so that every comparison in this file agrees
// and an empty path stays empty rather than becoming ".", which Clean does and which would make a
// subject with no directory match a change with no directory.
func cleanPath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Clean(dir)
}

func sharedWorkspaces(subjects []Subject) map[string]string {
	byID := make(map[string][]string)
	byPath := make(map[string][]string)
	for _, subject := range subjects {
		if subject.WorkspaceID != "" {
			byID[subject.WorkspaceID] = append(byID[subject.WorkspaceID], subject.Agent)
		}
		if path := cleanPath(subject.Dir); path != "" {
			byPath[path] = append(byPath[path], subject.Agent)
		}
	}

	shared := make(map[string]string)
	mark := func(names []string) {
		if len(names) >= 2 {
			reason := fmt.Sprintf("workspace is shared by %s; isolate the agents before attributing verification",
				strings.Join(names, ", "))
			for _, name := range names {
				shared[name] = reason
			}
		}
	}
	for _, names := range byID {
		mark(names)
	}
	for _, names := range byPath {
		mark(names)
	}
	return shared
}

func (v *Verifier) clearEvidenceLocked(name string) {
	delete(v.revision, name)
	delete(v.reason, name)
	delete(v.latest, name)
	delete(v.diffs, name)
	delete(v.diffErr, name)
}

// Observe takes a revision change from the poller.
//
// Nothing recomputes here beyond the diff, because staleness is derived rather than stored: a
// result recorded against the old revision becomes stale the moment this lands, without anything
// having to go around marking it.
func (v *Verifier) Observe(ctx context.Context, change git.Change) {
	v.mu.Lock()
	var names []string
	var subject Subject
	// Cleaned on both sides, because this compares two paths that reach here by different routes and
	// the failure is silent in the worst direction. A subject's directory is configured and a
	// change's path comes back through the poller's watch list, so one gaining a trailing separator
	// makes this match nothing: no revision is ever recorded, every agent stays unknown, and it
	// reads as a workspace where nothing has changed rather than as anything going wrong.
	// sharedWorkspaces already cleans before comparing, and two places deciding what makes two paths
	// equal by different rules is how the third one gets it wrong.
	wanted := cleanPath(change.Path)
	for agent, candidate := range v.subjects {
		if candidate.WorkspaceID == change.WorkspaceID &&
			(wanted == "" || cleanPath(candidate.Dir) == wanted) {
			names = append(names, agent)
			if len(names) == 1 {
				subject = candidate
			}
		}
	}
	if len(names) == 0 {
		v.mu.Unlock()
		return
	}
	for _, name := range names {
		v.revision[name] = change.To
		v.reason[name] = change.Reason
		delete(v.diffs, name)
	}
	if !change.To.Known() {
		// There can be no rank or review entry without a revision, so measuring the diff would do
		// work that cannot affect the answer. More importantly, the common reason for unknown is an
		// oversized untracked file; reading that entire file merely to count its lines can exhaust
		// memory while the truth layer is correctly refusing to use it.
		for _, name := range names {
			delete(v.diffErr, name)
		}
		v.mu.Unlock()
		return
	}
	for _, name := range names {
		v.diffErr[name] = "the diff is still being measured for this revision"
	}
	v.mu.Unlock()

	stat, err := v.repo.Diff(ctx, subject.Dir, v.base)
	v.mu.Lock()
	// Watch can replace an agent while Git is measuring the old workspace. Never attach that
	// result, successful or otherwise, to the replacement merely because it reused the name.
	for _, name := range names {
		if current, still := v.subjects[name]; still &&
			current.WorkspaceID == subject.WorkspaceID &&
			current.Dir == subject.Dir {
			if err == nil {
				v.diffs[name] = stat
				delete(v.diffErr, name)
			} else {
				delete(v.diffs, name)
				v.diffErr[name] = fmt.Sprintf("the diff could not be measured: %v", err)
			}
		}
	}
	v.mu.Unlock()
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
	if name != "" && v.shared[name] == "" {
		if v.latest[name] == nil {
			v.latest[name] = make(map[string]core.TestRun)
		}
		current, exists := v.latest[name][run.TestName]
		// Start publishes queued synchronously before its goroutine can publish running. That
		// queued update makes the new run authoritative immediately. Updates from the same run may
		// advance it; an older run finishing later may not replace it and flash a previous result
		// while the rerun is still in progress.
		if !exists ||
			current.ID == run.ID ||
			run.State == core.TestQueued ||
			run.StartedAt.After(current.StartedAt) {
			v.latest[name][run.TestName] = run
		}
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
	shared := v.shared[agent]
	tests := v.tests
	v.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAgent, agent)
	}
	if shared != "" {
		return errors.New(shared)
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
func (v *Verifier) Diff(agent string) core.DiffStat {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.diffs[agent]
}

// Changes lists the files an agent has touched.
//
// Read from git on each call rather than cached with the size. The stat is a number on a list and
// being a poll interval out of date costs nothing; a file list is what somebody is about to read,
// and showing them a file that is no longer changed is worse than the cost of one more git call.
func (v *Verifier) Changes(agent string) ([]core.FileChange, error) {
	v.mu.Lock()
	subject, ok := v.subjects[agent]
	v.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownAgent, agent)
	}
	return v.repo.Changes(context.Background(), subject.Dir, v.base)
}

// Patch returns the diff of one file in an agent's worktree.
func (v *Verifier) Patch(agent, path string) (string, error) {
	v.mu.Lock()
	subject, ok := v.subjects[agent]
	v.mu.Unlock()

	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownAgent, agent)
	}
	return v.repo.Patch(context.Background(), subject.Dir, v.base, path)
}
