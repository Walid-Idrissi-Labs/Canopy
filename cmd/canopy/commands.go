package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tools"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// runChat opens Canopy on a conversation in the current directory.
//
// This is what `canopy` with no arguments does, and the change of home screen is the point: a
// conversation is the common activity, and opening on a monitor made Canopy look like something you
// watch rather than something you talk to.
//
// Nothing here knows the workspace store is fake. Every screen is handed an interface and that is
// all it ever sees, which is what lets the real engine drop in at A5 without the UI changing.
func runChat() error {
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

	keyStore, err := openKeyStore()
	if err != nil {
		return err
	}

	resolver := session.NewKeyResolver(keyStore)
	engine := session.New(resolver)
	defer engine.Close()

	// History is attached if it can be, and the program runs without it if it cannot. A disk
	// problem should cost you the ability to look back at old conversations, not the ability to
	// have a new one.
	if err := attachHistory(engine); err != nil {
		fmt.Fprintf(os.Stderr, "warning: history is not being saved: %v\n", err)
	}

	dir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("finding the working directory: %w", err)
	}

	// Tools are scoped to the directory Canopy was started in, which is what "run canopy in a
	// project" means. A workspace that could not be opened is a directory the agent cannot work in,
	// and a conversation with no tools is still a useful thing, so it is a warning rather than a
	// failure.
	if err := attachTools(engine, dir); err != nil {
		fmt.Fprintf(os.Stderr, "warning: tools are not available: %v\n", err)
	}

	// One session to start in. Several sessions and the agents view arrive at A5; the engine
	// already holds a list rather than a single conversation, so that is a screen rather than a
	// rewrite.
	keyName := resolver.DefaultKeyName()
	engine.Create(keyName, defaultModelFor(keyStore, keyName))

	return tui.RunApp(store, keyStore, engine, filepath.Base(dir), keyName)
}

// attachTools gives the agent something to do besides talk.
//
// The trust level is standard for now, which reads and writes inside the workspace without asking
// and shows every shell command before running it. Per profile levels are configured at A5, and
// until there is a way to choose one, the level that asks about the dangerous half is the only
// defensible default.
func attachTools(engine *session.Engine, dir string) error {
	workspace, err := tools.OpenWorkspace(dir)
	if err != nil {
		return err
	}

	registry := core.NewToolRegistry()
	for _, tool := range tools.FileTools(workspace) {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	for _, tool := range tools.GitTools(workspace) {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	// The shell goes last, deliberately. Models weight earlier tool definitions more heavily, and
	// the ones that can be governed per argument should be reached for before the one that cannot.
	if err := registry.Register(tools.ShellTool(workspace)); err != nil {
		return err
	}

	// The engine asks the person watching. It implements the approver itself, which is what lets a
	// blocking question from a background goroutine reach an event loop that must never block.
	engine.WithTools(registry, core.TrustStandard, engine)
	return nil
}

// attachHistory gives the engine somewhere to persist to.
func attachHistory(engine *session.Engine) error {
	path, err := session.DefaultPath()
	if err != nil {
		return err
	}
	storage, err := session.OpenStorage(path)
	if err != nil {
		return err
	}
	// A write that fails mid session is reported once and does not end anything. The answer on
	// screen is still the answer, and taking the conversation down because a disk was full would be
	// the tail wagging the dog.
	return engine.WithStorage(storage, func(err error) {
		fmt.Fprintf(os.Stderr, "warning: could not save history: %v\n", err)
	})
}

// runSearch finds a message across every stored conversation.
func runSearch(args []string, out io.Writer) error {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return errors.New("what are you looking for? For example `canopy search bcrypt`")
	}

	path, err := session.DefaultPath()
	if err != nil {
		return err
	}
	storage, err := session.OpenStorage(path)
	if err != nil {
		return err
	}
	defer func() { _ = storage.Close() }()

	hits, err := storage.Search(query, 30)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		_, err := fmt.Fprintf(out, "Nothing in your history matches %q.\n", query)
		return err
	}

	w := &errWriter{w: out}
	for _, hit := range hits {
		title := hit.SessionTitle
		if title == "" {
			title = hit.SessionID
		}
		w.printf("%s  %s\n", hit.At.Local().Format("2006-01-02 15:04"), title)
		// The excerpt keeps SQLite's markers around the matched words rather than being styled
		// here, since a command writing to a pipe should not be emitting colour.
		w.printf("  %s\n\n", strings.ReplaceAll(
			strings.ReplaceAll(hit.Excerpt, "<<", ""), ">>", ""))
	}
	return w.err
}

// defaultModelFor picks the model a new session starts on.
//
// Anthropic has a default worth using, so an empty string means "the client's own". An OpenAI
// compatible endpoint has none, and guessing a model name for somebody else's gateway produces a
// confusing 404 rather than a clear message, so it is left empty and the first turn says what is
// missing.
func defaultModelFor(store *keys.Store, name string) string {
	if name == "" {
		return ""
	}
	meta, err := store.Metadata(core.KeyRef{Name: name})
	if err != nil || meta.Ref.Provider != core.ProviderAnthropic {
		return ""
	}
	return anthropic.DefaultModel
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
