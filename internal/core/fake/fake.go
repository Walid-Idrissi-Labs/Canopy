package fake

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/store"
)

// Store is an in-memory implementation of every core interface, backed by four scripted
// worktrees.
//
// It exists so the interface can be built and demonstrated before any real git or process code
// lands, and it doubles as the test double for the rest of the project. The four workspaces are
// the target demo: one passing for its current revision, one failing, one that goes stale the
// instant you touch it, and one with nothing configured.
//
// The event delivery here is a real implementation rather than a stub, because the coalescing
// rules are part of the contract the UI is written against. If the fake delivered every event in
// order with an unbounded buffer, the UI would be developed against behaviour the real store
// cannot provide, and the difference would only surface under load.
type Store struct {
	mu      sync.Mutex
	project core.ProjectSnapshot

	// events is the shared broker from internal/store, so this fake and the real stores deliver
	// events by exactly the same rules. A second implementation would drift, and the way it would
	// drift is a dropped final transition under load.
	events *store.Broker

	// runs holds every run ever started, keyed by run ID.
	runs    map[string]*core.TestRun
	nextRun int

	// outcomes scripts what a given test does when it is run, keyed by workspace ID and test
	// name. Tests that are not listed pass.
	outcomes map[string]core.TestState

	// touches counts edits per workspace so each one produces a genuinely different revision.
	touches map[string]int

	// now is the clock, injectable so tests are not timing dependent.
	now func() time.Time
}

var (
	_ core.WorkspaceSource = (*Store)(nil)
	_ core.RevisionTracker = (*Store)(nil)
	_ core.TestRunner      = (*Store)(nil)
	_ core.HealthChecker   = (*Store)(nil)
	_ core.SnapshotStore   = (*Store)(nil)
)

// New returns a store preloaded with the four scripted workspaces.
func New() *Store {
	start := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	s := &Store{
		events:   store.NewBroker(),
		runs:     map[string]*core.TestRun{},
		outcomes: map[string]core.TestState{},
		touches:  map[string]int{},
		now:      func() time.Time { return start },
	}

	revLogin := core.RevisionKey{HeadSHA: "a1b2c3d4e5f6", DirtyDigest: ""}
	revCache := core.RevisionKey{HeadSHA: "b2c3d4e5f6a1", DirtyDigest: ""}
	revAPI := core.RevisionKey{HeadSHA: "c3d4e5f6a1b2", DirtyDigest: ""}
	revSearch := core.RevisionKey{HeadSHA: "d4e5f6a1b2c3", DirtyDigest: ""}

	s.project = core.ProjectSnapshot{
		Sequence:    0,
		TakenAt:     start,
		RepoRoot:    "/repo",
		ConfigState: core.ConfigLoaded,
		TrustState:  core.TrustGranted,
		Workspaces: []core.WorkspaceSnapshot{
			{
				ID:           "ws-feat-login",
				Name:         "feat-login",
				Path:         "/repo/../wt/feat-login",
				Branch:       "feat/login",
				Ownership:    core.OwnershipExternalReadOnly,
				Revision:     revLogin,
				LastActivity: start.Add(-3 * time.Second),
				Tests: []core.TestSnapshot{
					{Name: "unit", Required: true, Latest: completedRun("run-seed-1", "ws-feat-login", "unit", revLogin, core.TestPassing, start)},
				},
				Services: []core.ServiceSnapshot{
					healthyService("ws-feat-login", "web", 4100, start),
				},
			},
			{
				ID:           "ws-fix-cache",
				Name:         "fix-cache",
				Path:         "/repo/../wt/fix-cache",
				Branch:       "fix/cache",
				Ownership:    core.OwnershipExternalReadOnly,
				Revision:     revCache,
				LastActivity: start.Add(-time.Minute),
				Tests: []core.TestSnapshot{
					{Name: "unit", Required: true, Latest: completedRun("run-seed-2", "ws-fix-cache", "unit", revCache, core.TestFailing, start)},
				},
				Services: []core.ServiceSnapshot{
					healthyService("ws-fix-cache", "web", 4101, start),
				},
			},
			{
				ID:           "ws-refactor-api",
				Name:         "refactor-api",
				Path:         "/repo/../wt/refactor-api",
				Branch:       "refactor/api",
				Ownership:    core.OwnershipExternalReadOnly,
				Revision:     revAPI,
				LastActivity: start.Add(-12 * time.Minute),
				Tests: []core.TestSnapshot{
					{Name: "unit", Required: true, Latest: completedRun("run-seed-3", "ws-refactor-api", "unit", revAPI, core.TestPassing, start)},
				},
			},
			{
				ID:           "ws-spike-search",
				Name:         "spike-search",
				Path:         "/repo/../wt/spike-search",
				Branch:       "spike/search",
				Ownership:    core.OwnershipPrimary,
				Revision:     revSearch,
				LastActivity: start.Add(-2 * time.Hour),
			},
		},
	}

	s.outcomes["ws-fix-cache|unit"] = core.TestFailing
	return s
}

func completedRun(id, workspaceID, testName string, rev core.RevisionKey, state core.TestState, at time.Time) *core.TestRun {
	finished := at.Add(-5 * time.Second)
	run := &core.TestRun{
		ID:             id,
		WorkspaceID:    workspaceID,
		TestName:       testName,
		Revision:       rev,
		CommandDisplay: "go test ./...",
		StartedAt:      finished.Add(-20 * time.Second),
		FinishedAt:     &finished,
		State:          state,
		OutputBufferID: "buf-" + id,
	}
	code := 0
	if state == core.TestFailing {
		code = 1
	}
	run.ExitCode = &code
	return run
}

func healthyService(workspaceID, name string, port int, at time.Time) core.ServiceSnapshot {
	instanceID := fmt.Sprintf("%s-%s-1", workspaceID, name)
	return core.ServiceSnapshot{
		Name:     name,
		Required: true,
		Managed:  false,
		Instance: &core.ServiceInstance{
			WorkspaceID:  workspaceID,
			ServiceName:  name,
			InstanceID:   instanceID,
			PID:          40000 + port,
			ProcessGroup: 40000 + port,
			Port:         port,
			StartedAt:    at.Add(-10 * time.Minute),
		},
		Health: &core.ServiceHealth{
			WorkspaceID:  workspaceID,
			ServiceName:  name,
			State:        core.ServiceHealthy,
			ProcessAlive: core.ObservationTrue,
			Ready:        core.ObservationTrue,
			Probe:        core.ProbeHTTP,
			InstanceID:   instanceID,
			CheckedAt:    at.Add(-2 * time.Second),
			Latency:      3 * time.Millisecond,
		},
	}
}

// SetClock replaces the clock. Only useful in tests.
func (s *Store) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
	s.events.SetClock(now)
}

// Snapshot implements core.SnapshotStore.
func (s *Store) Snapshot() core.ProjectSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// snapshotLocked returns a deep enough copy that a caller holding the result cannot be affected
// by later mutations, and cannot affect the store by writing to what it was given.
//
// The slices are copied because a shared backing array is the classic way an "immutable snapshot"
// quietly stops being one. The pointers inside are not deep copied, and the store never mutates a
// TestRun or ServiceHealth in place, it replaces them.
func (s *Store) snapshotLocked() core.ProjectSnapshot {
	out := s.project
	out.Sequence = s.events.Sequence()
	out.TakenAt = s.now()
	out.Workspaces = make([]core.WorkspaceSnapshot, len(s.project.Workspaces))
	for i, w := range s.project.Workspaces {
		w.Tests = append([]core.TestSnapshot(nil), w.Tests...)
		w.Services = append([]core.ServiceSnapshot(nil), w.Services...)
		out.Workspaces[i] = w
	}
	return out
}

// Discover implements core.WorkspaceSource.
func (s *Store) Discover(_ context.Context) ([]core.WorkspaceSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked().Workspaces, nil
}

// Current implements core.RevisionTracker.
func (s *Store) Current(_ context.Context, workspaceID string) (core.RevisionKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.workspaceLocked(workspaceID)
	if !ok {
		return core.RevisionKey{}, fmt.Errorf("unknown workspace %q", workspaceID)
	}
	if !w.Revision.Known() {
		reason := w.RevisionError
		if reason == "" {
			reason = "revision could not be determined"
		}
		return core.RevisionKey{}, fmt.Errorf("%s", reason)
	}
	return w.Revision, nil
}

// Check implements core.HealthChecker.
func (s *Store) Check(_ context.Context, workspaceID, serviceName string) (core.ServiceHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.workspaceLocked(workspaceID)
	if !ok {
		return unknownHealth(workspaceID, serviceName, "unknown workspace"), fmt.Errorf("unknown workspace %q", workspaceID)
	}
	svc, ok := w.Service(serviceName)
	if !ok || svc.Health == nil {
		return unknownHealth(workspaceID, serviceName, "service is not configured"),
			fmt.Errorf("unknown service %q in workspace %q", serviceName, workspaceID)
	}
	return *svc.Health, nil
}

// unknownHealth builds the value the HealthChecker contract requires on failure: state unknown
// with a filled in reason, never a zero value that would claim nothing at all.
func unknownHealth(workspaceID, serviceName, reason string) core.ServiceHealth {
	return core.ServiceHealth{
		WorkspaceID:   workspaceID,
		ServiceName:   serviceName,
		State:         core.ServiceUnknown,
		FailureReason: reason,
	}
}

// Start implements core.TestRunner. The run completes immediately with the scripted outcome, so
// callers that only want a result do not have to drive the run manually.
func (s *Store) Start(ctx context.Context, workspaceID, testName string) (string, error) {
	runID, err := s.BeginRun(ctx, workspaceID, testName)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	outcome, ok := s.outcomes[workspaceID+"|"+testName]
	s.mu.Unlock()
	if !ok {
		outcome = core.TestPassing
	}

	if err := s.CompleteRun(runID, outcome); err != nil {
		return "", err
	}
	return runID, nil
}

// BeginRun records a run and leaves it running, so a caller can observe the in-flight state and
// finish it later with CompleteRun.
func (s *Store) BeginRun(_ context.Context, workspaceID, testName string) (string, error) {
	s.mu.Lock()

	if !s.project.CanExecute() {
		s.mu.Unlock()
		return "", fmt.Errorf("cannot run tests: config is %s and trust is %s",
			s.project.ConfigState, s.project.TrustState)
	}

	w, ok := s.workspaceLocked(workspaceID)
	if !ok {
		s.mu.Unlock()
		return "", fmt.Errorf("unknown workspace %q", workspaceID)
	}
	if _, ok := w.Test(testName); !ok {
		s.mu.Unlock()
		return "", fmt.Errorf("unknown test %q in workspace %q", testName, workspaceID)
	}

	s.nextRun++
	runID := fmt.Sprintf("run-%d", s.nextRun)
	run := &core.TestRun{
		ID:          runID,
		WorkspaceID: workspaceID,
		TestName:    testName,
		// Captured at the start, because the result describes the code that was there when the
		// command began reading it.
		Revision:       w.Revision,
		CommandDisplay: "go test ./...",
		StartedAt:      s.now(),
		State:          core.TestRunning,
		OutputBufferID: "buf-" + runID,
	}
	s.runs[runID] = run
	s.setLatestRunLocked(workspaceID, testName, run)

	ev := core.Event{
		Kind:        core.EventTestRunUpdated,
		WorkspaceID: workspaceID,
		TestName:    testName,
		RunID:       runID,
	}
	s.publishLocked(ev)
	s.mu.Unlock()

	return runID, nil
}

// CompleteRun finishes a run in the given terminal state.
func (s *Store) CompleteRun(runID string, state core.TestState) error {
	s.mu.Lock()

	run, ok := s.runs[runID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown run %q", runID)
	}
	if !state.IsTerminal() {
		s.mu.Unlock()
		return fmt.Errorf("%q is not a terminal state", state)
	}

	finished := s.now()
	updated := *run
	updated.State = state
	updated.FinishedAt = &finished
	switch state {
	case core.TestPassing:
		code := 0
		updated.ExitCode = &code
	case core.TestFailing:
		code := 1
		updated.ExitCode = &code
	case core.TestError:
		updated.ErrorMessage = "the command could not be run"
	}

	s.runs[runID] = &updated
	s.setLatestRunLocked(updated.WorkspaceID, updated.TestName, &updated)

	// Final, so this can never be coalesced away. It is the last thing anyone hears about the run.
	s.publishLocked(core.Event{
		Kind:        core.EventTestRunUpdated,
		WorkspaceID: updated.WorkspaceID,
		TestName:    updated.TestName,
		RunID:       runID,
		Final:       true,
	})
	s.mu.Unlock()

	return nil
}

// Cancel implements core.TestRunner.
func (s *Store) Cancel(_ context.Context, runID string) error {
	s.mu.Lock()
	run, ok := s.runs[runID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("unknown run %q", runID)
	}
	if run.State.IsTerminal() {
		s.mu.Unlock()
		return fmt.Errorf("run %q has already finished", runID)
	}
	s.mu.Unlock()

	return s.CompleteRun(runID, core.TestCancelled)
}

// Touch simulates an edit in a workspace. The revision changes, so any result recorded against
// the old one becomes stale.
//
// This is the fake's whole reason for existing: it is how the first demo is driven.
func (s *Store) Touch(workspaceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := s.indexLocked(workspaceID)
	if !ok {
		return fmt.Errorf("unknown workspace %q", workspaceID)
	}

	s.touches[workspaceID]++
	w := &s.project.Workspaces[idx]
	w.Revision = core.RevisionKey{
		HeadSHA:     w.Revision.HeadSHA,
		DirtyDigest: fmt.Sprintf("edit%d", s.touches[workspaceID]),
	}
	w.RevisionError = ""
	w.Dirty = core.DirtyState{Unstaged: s.touches[workspaceID]}
	w.LastActivity = s.now()

	s.publishLocked(core.Event{Kind: core.EventRevisionChanged, WorkspaceID: workspaceID})
	return nil
}

// SetRevisionUnknown makes a workspace's revision uncomputable, with a reason.
func (s *Store) SetRevisionUnknown(workspaceID, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := s.indexLocked(workspaceID)
	if !ok {
		return fmt.Errorf("unknown workspace %q", workspaceID)
	}
	w := &s.project.Workspaces[idx]
	w.Revision = core.RevisionKey{}
	w.RevisionError = reason

	s.publishLocked(core.Event{Kind: core.EventRevisionChanged, WorkspaceID: workspaceID})
	return nil
}

// SetServiceHealth replaces a service's health observation.
func (s *Store) SetServiceHealth(workspaceID, serviceName string, health core.ServiceHealth) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := s.indexLocked(workspaceID)
	if !ok {
		return fmt.Errorf("unknown workspace %q", workspaceID)
	}
	w := &s.project.Workspaces[idx]
	for i := range w.Services {
		if w.Services[i].Name != serviceName {
			continue
		}
		updated := health
		updated.WorkspaceID = workspaceID
		updated.ServiceName = serviceName
		updated.CheckedAt = s.now()
		w.Services[i].Health = &updated

		s.publishLocked(core.Event{
			Kind:        core.EventServiceHealthUpdated,
			WorkspaceID: workspaceID,
			ServiceName: serviceName,
		})
		return nil
	}
	return fmt.Errorf("unknown service %q in workspace %q", serviceName, workspaceID)
}

// SetOutcome scripts what a test does the next time it is run through Start.
func (s *Store) SetOutcome(workspaceID, testName string, state core.TestState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outcomes[workspaceID+"|"+testName] = state
}

// SetTrust changes the project trust state, so callers can exercise the refusal path.
func (s *Store) SetTrust(state core.TrustState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.project.TrustState = state
	s.publishLocked(core.Event{Kind: core.EventTrustChanged})
}

// RemoveWorkspace drops a workspace, simulating one deleted outside Canopy.
func (s *Store) RemoveWorkspace(workspaceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := s.indexLocked(workspaceID)
	if !ok {
		return fmt.Errorf("unknown workspace %q", workspaceID)
	}
	s.project.Workspaces = append(s.project.Workspaces[:idx], s.project.Workspaces[idx+1:]...)
	s.publishLocked(core.Event{Kind: core.EventWorkspacesChanged})
	return nil
}

func (s *Store) indexLocked(workspaceID string) (int, bool) {
	for i, w := range s.project.Workspaces {
		if w.ID == workspaceID {
			return i, true
		}
	}
	return 0, false
}

func (s *Store) workspaceLocked(workspaceID string) (core.WorkspaceSnapshot, bool) {
	idx, ok := s.indexLocked(workspaceID)
	if !ok {
		return core.WorkspaceSnapshot{}, false
	}
	return s.project.Workspaces[idx], true
}

func (s *Store) setLatestRunLocked(workspaceID, testName string, run *core.TestRun) {
	idx, ok := s.indexLocked(workspaceID)
	if !ok {
		return
	}
	w := &s.project.Workspaces[idx]
	for i := range w.Tests {
		if w.Tests[i].Name == testName {
			w.Tests[i].Latest = run
			return
		}
	}
}
