package main

import (
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The view types below exist because a raw ProjectSnapshot is not the whole story. The states a
// user cares about, whether a result is stale and whether a workspace is verified, are derived
// from the snapshot rather than stored in it. Printing only the stored fields would show a run
// recorded as passing and leave the reader to work out for themselves that it no longer applies,
// which is the exact confusion this product exists to remove.

type projectView struct {
	Sequence    uint64          `json:"sequence"`
	TakenAt     time.Time       `json:"takenAt"`
	RepoRoot    string          `json:"repoRoot"`
	ConfigState string          `json:"configState"`
	ConfigError string          `json:"configError,omitempty"`
	TrustState  string          `json:"trustState"`
	CanExecute  bool            `json:"canExecute"`
	Workspaces  []workspaceView `json:"workspaces"`
}

type workspaceView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Branch    string `json:"branch,omitempty"`
	Detached  bool   `json:"detached,omitempty"`
	Ownership string `json:"ownership"`

	Revision      string `json:"revision"`
	RevisionKnown bool   `json:"revisionKnown"`
	RevisionError string `json:"revisionError,omitempty"`

	Dirty        dirtyView `json:"dirty"`
	LastActivity time.Time `json:"lastActivity"`

	Green  bool   `json:"green"`
	Reason string `json:"reason"`
	Caveat string `json:"caveat,omitempty"`

	Tests         string `json:"tests"`
	Services      string `json:"services"`
	TestsPassing  int    `json:"testsPassing"`
	TestsTotal    int    `json:"testsTotal"`
	ServicesUp    int    `json:"servicesUp"`
	ServicesTotal int    `json:"servicesTotal"`

	TestDetail    []testView    `json:"testDetail,omitempty"`
	ServiceDetail []serviceView `json:"serviceDetail,omitempty"`
}

// dirtyView mirrors core.DirtyState with wire names. The core package deliberately carries no
// serialisation tags, so how state is presented stays a decision of whatever is doing the
// presenting rather than something baked into the contract.
type dirtyView struct {
	Staged    int  `json:"staged"`
	Unstaged  int  `json:"unstaged"`
	Untracked int  `json:"untracked"`
	IsDirty   bool `json:"isDirty"`
}

type testView struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	State    string `json:"state"`
	Reason   string `json:"reason"`

	// TestedRevision is what the last run actually covered, which is the field that explains a
	// stale result at a glance.
	TestedRevision string     `json:"testedRevision,omitempty"`
	ExitCode       *int       `json:"exitCode,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	Command        string     `json:"command,omitempty"`
}

type serviceView struct {
	Name         string `json:"name"`
	Required     bool   `json:"required"`
	Managed      bool   `json:"managed"`
	State        string `json:"state"`
	Reason       string `json:"reason"`
	ProcessAlive string `json:"processAlive"`
	Ready        string `json:"ready"`
	Probe        string `json:"probe,omitempty"`
	Port         int    `json:"port,omitempty"`
	PID          int    `json:"pid,omitempty"`
	Failures     int    `json:"consecutiveFailures,omitempty"`
}

func newProjectView(snap core.ProjectSnapshot) projectView {
	out := projectView{
		Sequence:    snap.Sequence,
		TakenAt:     snap.TakenAt,
		RepoRoot:    snap.RepoRoot,
		ConfigState: snap.ConfigState.String(),
		ConfigError: snap.ConfigError,
		TrustState:  snap.TrustState.String(),
		CanExecute:  snap.CanExecute(),
		Workspaces:  make([]workspaceView, 0, len(snap.Workspaces)),
	}
	for _, w := range snap.Workspaces {
		out.Workspaces = append(out.Workspaces, newWorkspaceView(w))
	}
	return out
}

func newWorkspaceView(w core.WorkspaceSnapshot) workspaceView {
	rollup := core.RollUp(w)

	out := workspaceView{
		ID:            w.ID,
		Name:          w.Name,
		Path:          w.Path,
		Branch:        w.Branch,
		Detached:      w.Detached,
		Ownership:     w.Ownership.String(),
		Revision:      w.Revision.Short(),
		RevisionKnown: w.Revision.Known(),
		RevisionError: w.RevisionError,
		Dirty: dirtyView{
			Staged:    w.Dirty.Staged,
			Unstaged:  w.Dirty.Unstaged,
			Untracked: w.Dirty.Untracked,
			IsDirty:   w.Dirty.IsDirty(),
		},
		LastActivity:  w.LastActivity,
		Green:         rollup.Green,
		Reason:        rollup.Reason,
		Caveat:        rollup.Caveat,
		Tests:         rollup.Tests.String(),
		Services:      rollup.Services.String(),
		TestsPassing:  rollup.TestsPassing,
		TestsTotal:    rollup.TestsTotal,
		ServicesUp:    rollup.ServicesUp,
		ServicesTotal: rollup.ServicesTotal,
	}

	for _, test := range w.Tests {
		verdict := test.Explain(w.Revision)
		view := testView{
			Name:     test.Name,
			Required: test.Required,
			State:    verdict.State.String(),
			Reason:   verdict.Reason,
		}
		if test.Latest != nil {
			view.TestedRevision = test.Latest.Revision.Short()
			view.ExitCode = test.Latest.ExitCode
			view.FinishedAt = test.Latest.FinishedAt
			view.Command = test.Latest.CommandDisplay
		}
		out.TestDetail = append(out.TestDetail, view)
	}

	for _, service := range w.Services {
		verdict := service.Explain()
		view := serviceView{
			Name:     service.Name,
			Required: service.Required,
			Managed:  service.Managed,
			State:    verdict.State.String(),
			Reason:   verdict.Reason,
			// Reported separately rather than folded into State, because a live process that is
			// not answering is a different problem from no process at all, and the fix differs.
			ProcessAlive: core.ObservationUnknown.String(),
			Ready:        core.ObservationUnknown.String(),
		}
		if service.Health != nil {
			view.ProcessAlive = service.Health.ProcessAlive.String()
			view.Ready = service.Health.Ready.String()
			view.Probe = service.Health.Probe.String()
			view.Failures = service.Health.ConsecutiveFailures
		}
		if service.Instance != nil {
			view.Port = service.Instance.Port
			view.PID = service.Instance.PID
		}
		out.ServiceDetail = append(out.ServiceDetail, view)
	}

	return out
}
