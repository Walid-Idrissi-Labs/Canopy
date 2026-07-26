package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// runDashboard starts the interactive dashboard.
//
// Nothing here knows the store is fake. The dashboard is handed a core.SnapshotStore and that is
// all it ever sees, which is what lets the real engine drop in later without the UI changing.
func runDashboard() error {
	store := fake.New()
	defer store.Close()

	store.SetClock(time.Now)

	// Without something changing, the dashboard would be a still image and would demonstrate
	// nothing. Editing a worktree on a timer is the behaviour worth watching anyway, since it is
	// what turns green results stale.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(6 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = store.Touch("ws-refactor-api")
			}
		}
	}()

	return tui.Run(store)
}

func runSnapshot(out io.Writer) error {
	store := fake.New()
	defer store.Close()

	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(newProjectView(store.Snapshot()))
}

func runWatch(out io.Writer) error {
	store := fake.New()
	defer store.Close()

	// The fake's clock is frozen so its tests stay deterministic. Watching a live stream is the
	// one place that is unhelpful, since every event would carry the same timestamp and there
	// would be no way to see how far apart they arrived.
	store.SetClock(time.Now)

	// Snapshot first, then subscribe from its sequence. In that order nothing can happen in the
	// gap between reading the state and starting to listen for changes to it.
	snap := store.Snapshot()
	events := store.Events(snap.Sequence)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "watching from sequence %d, interrupt to stop\n", snap.Sequence)

	// Nothing else drives the fake, so without this the command would sit silent forever and
	// prove nothing. Editing a worktree every couple of seconds is the behaviour worth watching
	// anyway: it is what turns green results stale.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = store.Touch("ws-refactor-api")
			}
		}
	}()

	encoder := json.NewEncoder(out)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if err := encoder.Encode(eventView(ev)); err != nil {
				return err
			}
		}
	}
}

type eventLine struct {
	Sequence    uint64    `json:"sequence"`
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	WorkspaceID string    `json:"workspaceId,omitempty"`
	TestName    string    `json:"testName,omitempty"`
	ServiceName string    `json:"serviceName,omitempty"`
	RunID       string    `json:"runId,omitempty"`
	Final       bool      `json:"final,omitempty"`
}

func eventView(ev core.Event) eventLine {
	return eventLine{
		Sequence:    ev.Sequence,
		At:          ev.At,
		Kind:        ev.Kind.String(),
		WorkspaceID: ev.WorkspaceID,
		TestName:    ev.TestName,
		ServiceName: ev.ServiceName,
		RunID:       ev.RunID,
		Final:       ev.Final,
	}
}

// errWriter defers error handling across a run of writes.
//
// Printing a table means a dozen calls in a row, and checking each one inline would bury the
// logic in error handling that says the same thing every time. This remembers the first failure,
// skips the rest, and hands it back once at the end. Ignoring write errors outright would be the
// easier option and the wrong one: out is caller supplied, so a closed pipe or a full disk is a
// real failure that should be reported rather than swallowed.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}

// runDemo shows the thing the whole project is built around: a green result becoming stale the
// moment the code underneath it changes, with nothing re-run and nothing restarted.
func runDemo(out io.Writer) error {
	store := fake.New()
	defer store.Close()

	const target = "ws-refactor-api"

	snap := store.Snapshot()
	events := store.Events(snap.Sequence)

	w := &errWriter{w: out}

	w.printf("before the edit\n")
	printTable(w, store.Snapshot())

	if err := store.Touch(target); err != nil {
		return fmt.Errorf("editing %s: %w", target, err)
	}

	// Wait for the notification rather than sleeping, so this demonstrates the event path rather
	// than merely outrunning it.
	deadline := time.After(5 * time.Second)
	for waiting := true; waiting; {
		select {
		case ev, ok := <-events:
			if !ok {
				return fmt.Errorf("the event stream closed before the change arrived")
			}
			if ev.Kind == core.EventRevisionChanged && ev.WorkspaceID == target {
				w.printf("\nedited %s, event #%d %s\n\n", target, ev.Sequence, ev.Kind)
				waiting = false
			}
		case <-deadline:
			return fmt.Errorf("timed out waiting for the revision change event")
		}
	}

	w.printf("after the edit\n")
	printTable(w, store.Snapshot())

	workspace, ok := store.Snapshot().Workspace(target)
	if !ok {
		return fmt.Errorf("workspace %s disappeared", target)
	}
	rollup := core.RollUp(workspace)
	if rollup.Tests != core.TestStale {
		return fmt.Errorf("expected %s to be stale, got %s", target, rollup.Tests)
	}
	w.printf("\n%s went from passing to stale without anything being re-run.\nreason: %s\n",
		target, rollup.Reason)

	return w.err
}

func printTable(out *errWriter, snap core.ProjectSnapshot) {
	tab := tabwriter.NewWriter(out.w, 0, 0, 2, ' ', 0)
	table := &errWriter{w: tab}

	table.printf("  WORKSPACE\tBRANCH\tREVISION\tTESTS\tSERVICES\tVERIFIED\n")

	for _, workspace := range snap.Workspaces {
		rollup := core.RollUp(workspace)

		verified := "no"
		if rollup.Green {
			verified = "yes"
		}

		services := rollup.Services.String()
		if rollup.ServicesTotal > 0 {
			services = fmt.Sprintf("%s %d/%d", rollup.Services, rollup.ServicesUp, rollup.ServicesTotal)
		}
		tests := rollup.Tests.String()
		if rollup.TestsTotal > 0 {
			tests = fmt.Sprintf("%s %d/%d", rollup.Tests, rollup.TestsPassing, rollup.TestsTotal)
		}

		table.printf("  %s\t%s\t%s\t%s\t%s\t%s\n",
			workspace.Name, workspace.Branch, workspace.Revision.Short(), tests, services, verified)
	}

	if err := tab.Flush(); err != nil && table.err == nil {
		table.err = err
	}
	if out.err == nil {
		out.err = table.err
	}
}
