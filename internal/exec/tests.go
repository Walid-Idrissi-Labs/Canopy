package exec

// Running the configured tests, which is where evidence actually comes from.
//
// Three outcomes, and the third is the one that gets collapsed into the others by nearly every tool
// that does this. Exit zero is a pass. Exit non zero is a fail. A command that never started, or
// started and could not finish, is neither: it is an error, and it says nothing at all about the
// code. Reporting a missing binary as a failing test tells a user their code is broken when what is
// broken is the configuration, and reporting it as passing is worse.
//
// The revision is read before the command starts, never after. A suite that takes four minutes
// describes the code that was on disk when it began reading, and binding the result to the revision
// at the end would quietly credit a passing run to an edit made while it ran.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// DefaultTestTimeout is how long a test command is given before it is treated as unable to finish.
//
// Longer than DefaultTimeout because a real suite legitimately takes minutes, and short enough that
// a hung run is noticed in the same sitting rather than holding an agent's slot all afternoon.
const DefaultTestTimeout = 15 * time.Minute

// Test is a configured test command.
type Test struct {
	Name string

	// Command runs through a shell, matching the shell tool and the A5-04 setup command. Test
	// commands come out of a project's own notes and are full of pipes and environment prefixes, and
	// a runner that only accepted argv would have half of them fail for reasons the user cannot see.
	Command string

	// Required decides whether this test can block a green roll-up. Carried here so the caller that
	// builds a TestSnapshot does not need a second lookup.
	Required bool

	// Timeout defaults to DefaultTestTimeout.
	Timeout time.Duration
}

// Target is the worktree a test runs in.
type Target struct {
	WorkspaceID string
	Dir         string

	// Revision is asked at the moment the run starts, and its second return is the reason the
	// revision is unknown. A nil Revision means the caller cannot say what code this is, which
	// records an unknown revision rather than pretending to one.
	//
	// A function rather than a value because it must be read at start time. Passing a RevisionKey in
	// would let a caller compute it early, hand it around, and bind a result to code that had
	// already been edited by the time the command ran.
	Revision func(ctx context.Context) (core.RevisionKey, string)
}

// Outcome is a finished run and what the command printed.
//
// The two are separate because D-08 keeps logs out of state: a state transition may never be
// dropped, and a log line may, so they cannot share a lifetime. Handing them back together and
// letting the caller store them separately keeps that split without making the caller run the
// command twice to see both.
type Outcome struct {
	Run    core.TestRun
	Output string
}

// RunTest executes one test and records what happened.
//
// It returns a finished run in every case, including the ones where nothing ran. There is no error
// return, deliberately: every way this can go wrong is a state the user needs shown, and an error
// return invites a caller to log it and display nothing.
func RunTest(ctx context.Context, test Test, target Target, runID string) Outcome {
	run := core.TestRun{
		ID:             runID,
		WorkspaceID:    target.WorkspaceID,
		TestName:       test.Name,
		CommandDisplay: test.Command,
		StartedAt:      time.Now(),
		OutputBufferID: runID,
		State:          core.TestRunning,
	}

	// Read before the command starts, never after. A suite that takes four minutes describes the
	// code that was on disk when it began, and reading the revision at the end would credit the
	// result to an edit made while it ran.
	if target.Revision != nil {
		run.Revision, _ = target.Revision(ctx)
	}

	switch {
	case test.Command == "":
		return finishTest(run, "", core.TestError, nil,
			fmt.Sprintf("the test %q has no command configured", test.Name))
	case target.Dir == "":
		return finishTest(run, "", core.TestError, nil,
			fmt.Sprintf("the test %q has nowhere to run: no worktree was given", test.Name))
	}

	timeout := test.Timeout
	if timeout <= 0 {
		timeout = DefaultTestTimeout
	}

	result, err := Run(ctx, "/bin/sh", []string{"-c", test.Command}, Options{
		Dir:     target.Dir,
		Timeout: timeout,
	})

	switch {
	case err != nil:
		return finishTest(run, result.Output, core.TestError, nil,
			fmt.Sprintf("the test %q could not be started: %v", test.Name, err))

	case !result.Ran:
		// The command never got off the ground. Distinct from failing, and the distinction is the
		// whole acceptance criterion for this task.
		return finishTest(run, result.Output, core.TestError, nil,
			fmt.Sprintf("the test %q could not be started: %s", test.Name, oneLine(result.Output)))

	case result.Cancelled:
		return finishTest(run, result.Output, core.TestCancelled, nil, "")

	case result.TimedOut:
		// A timeout produced no verdict about the code. It ran and it did not finish, so there is
		// nothing to report as either passing or failing.
		return finishTest(run, result.Output, core.TestError, nil,
			fmt.Sprintf("the test %q ran for %s without finishing", test.Name, timeout))

	case result.ExitCode == 0:
		return finishTest(run, result.Output, core.TestPassing, &result.ExitCode, "")

	default:
		return finishTest(run, result.Output, core.TestFailing, &result.ExitCode, "")
	}
}

func finishTest(
	run core.TestRun, output string, state core.TestState, exit *int, message string,
) Outcome {
	finished := time.Now()
	run.FinishedAt = &finished
	run.State = state
	run.ExitCode = exit
	run.ErrorMessage = message

	return Outcome{Run: run, Output: output}
}

// oneLine reduces command output to something that fits in a status line.
func oneLine(out string) string {
	const limit = 200

	trimmed := ""
	for _, r := range out {
		if r == '\n' || r == '\r' {
			if trimmed != "" {
				break
			}
			continue
		}
		trimmed += string(r)
		if len(trimmed) >= limit {
			break
		}
	}
	if trimmed == "" {
		return "no output"
	}
	return trimmed
}

// Runner starts test runs and keeps track of the ones in flight.
//
// The synchronous RunTest above is the whole measurement. This adds exactly two things: run
// identity, so a result can be found again, and cancellation, so a suite that is going nowhere can
// be stopped without killing Canopy.
type Runner struct {
	// onUpdate is called for every state change of every run, including the last one. Called from
	// the run's own goroutine, so it must be cheap and must not call back into the runner.
	onUpdate func(core.TestRun)

	ids atomic.Uint64

	// running counts the runs in flight, so shutting down can wait for their processes to have
	// actually gone rather than only for the request to have been made. See CancelAll.
	running sync.WaitGroup

	mu     sync.Mutex
	live   map[string]*liveRun
	done   map[string]core.TestRun
	output map[string]string
}

// shutdownGrace bounds how long CancelAll waits for the runs it stopped.
//
// Comfortably longer than the time a single run needs to die: Run gives a group a quarter of a
// second to honour SIGTERM and then two more to respond to SIGKILL before it gives up on it. The
// bound exists for the same reason that one does, which is that quitting must not be able to hang.
const shutdownGrace = 5 * time.Second

type liveRun struct {
	cancel context.CancelFunc
	run    core.TestRun
}

// NewRunner returns a runner. onUpdate may be nil.
func NewRunner(onUpdate func(core.TestRun)) *Runner {
	if onUpdate == nil {
		onUpdate = func(core.TestRun) {}
	}
	return &Runner{
		onUpdate: onUpdate,
		live:     make(map[string]*liveRun),
		done:     make(map[string]core.TestRun),
		output:   make(map[string]string),
	}
}

// ErrNoSuchRun is returned when a run ID is not one this runner handed out.
var ErrNoSuchRun = errors.New("no run with that identifier")

// Start begins a run and returns its identifier.
//
// The context governs the lifetime of the runner rather than of this call: Start returns as soon as
// the process is launched, and the run continues in the background until it finishes, is cancelled,
// or the context ends.
func (r *Runner) Start(ctx context.Context, test Test, target Target) (string, error) {
	if test.Name == "" {
		return "", errors.New("a test needs a name")
	}

	runID := fmt.Sprintf("run-%d", r.ids.Add(1))
	runCtx, cancel := context.WithCancel(ctx)

	queued := core.TestRun{
		ID:             runID,
		WorkspaceID:    target.WorkspaceID,
		TestName:       test.Name,
		CommandDisplay: test.Command,
		StartedAt:      time.Now(),
		OutputBufferID: runID,
		State:          core.TestQueued,
	}

	r.mu.Lock()
	r.live[runID] = &liveRun{cancel: cancel, run: queued}
	r.mu.Unlock()

	r.onUpdate(queued)

	r.running.Add(1)
	go func() {
		// Counted down last, after the context has been released, so that a shutdown waiting on this
		// waits for the whole run to have unwound rather than for its last observable step.
		defer r.running.Done()
		defer cancel()

		running := queued
		running.State = core.TestRunning
		r.mu.Lock()
		if entry, ok := r.live[runID]; ok {
			entry.run = running
		}
		r.mu.Unlock()
		r.onUpdate(running)

		outcome := RunTest(runCtx, test, target, runID)

		r.mu.Lock()
		delete(r.live, runID)
		r.done[runID] = outcome.Run
		r.output[runID] = outcome.Output
		r.mu.Unlock()

		r.onUpdate(outcome.Run)
	}()

	return runID, nil
}

// Output returns what a finished run printed.
func (r *Runner) Output(runID string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	out, ok := r.output[runID]
	return out, ok
}

// Cancel stops a run in flight.
//
// Cancelling something that already finished is not an error. The alternative is a race every
// caller has to handle: the run finished between the user pressing the key and the key arriving.
func (r *Runner) Cancel(runID string) error {
	r.mu.Lock()
	entry, live := r.live[runID]
	_, finished := r.done[runID]
	r.mu.Unlock()

	switch {
	case live:
		entry.cancel()
		return nil
	case finished:
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrNoSuchRun, runID)
	}
}

// Run returns what is known about a run, whether it is in flight or finished.
func (r *Runner) Run(runID string) (core.TestRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if entry, ok := r.live[runID]; ok {
		return entry.run, true
	}
	run, ok := r.done[runID]
	return run, ok
}

// Latest returns the most recent finished run of a named test in a workspace.
func (r *Runner) Latest(workspaceID, testName string) (core.TestRun, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var best core.TestRun
	var found bool
	for _, run := range r.done {
		if run.WorkspaceID != workspaceID || run.TestName != testName {
			continue
		}
		if !found || run.StartedAt.After(best.StartedAt) {
			best, found = run, true
		}
	}
	return best, found
}

// CancelAll stops every run in flight and waits for them to have stopped, which is what shutting
// down has to do.
//
// Without it, killing Canopy leaves a test suite and everything it spawned running, holding the
// ports and the file handles that the next run will need.
//
// The waiting half is not a refinement of the cancelling half, it is the half that does the work.
// Cancelling only asks: the signalling and the reaping happen on each run's own goroutine, inside
// Run, and a caller that returned as soon as the requests were delivered would let the program exit
// a moment before any of it happened. Every orphan this is supposed to prevent lives in that
// moment.
//
// Bounded, because a process that ignores a kill is not going to start obeying and hanging on quit
// would be a worse failure than an orphan. Somebody who cannot quit reaches for the thing that
// leaves the orphans behind anyway.
func (r *Runner) CancelAll() {
	r.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(r.live))
	for _, entry := range r.live {
		cancels = append(cancels, entry.cancel)
	}
	r.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	stopped := make(chan struct{})
	go func() {
		r.running.Wait()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(shutdownGrace):
	}
}
