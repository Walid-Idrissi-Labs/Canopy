package exec

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func fixedRevision(sha string) func(context.Context) (core.RevisionKey, string) {
	return func(context.Context) (core.RevisionKey, string) {
		return core.RevisionKey{HeadSHA: sha}, ""
	}
}

func target(t *testing.T, sha string) Target {
	t.Helper()
	return Target{WorkspaceID: "ws-1", Dir: t.TempDir(), Revision: fixedRevision(sha)}
}

// The three outcomes, and the reason this task exists: the third one is real and almost everybody
// collapses it into one of the other two.
func TestTheThreeOutcomesStayThree(t *testing.T) {
	ctx := context.Background()

	t.Run("exit zero is passing", func(t *testing.T) {
		outcome := RunTest(ctx, Test{Name: "unit", Command: ShellLine("exit 0")}, target(t, "abc"), "run-1")
		if outcome.Run.State != core.TestPassing {
			t.Errorf("state is %q, want passing", outcome.Run.State)
		}
		if outcome.Run.ExitCode == nil || *outcome.Run.ExitCode != 0 {
			t.Errorf("exit code is %v, want 0", outcome.Run.ExitCode)
		}
		if outcome.Run.ErrorMessage != "" {
			t.Errorf("a passing run carries an error message: %q", outcome.Run.ErrorMessage)
		}
	})

	t.Run("exit non zero is failing", func(t *testing.T) {
		outcome := RunTest(ctx, Test{Name: "unit", Command: ShellLine("exit 3")}, target(t, "abc"), "run-2")
		if outcome.Run.State != core.TestFailing {
			t.Errorf("state is %q, want failing", outcome.Run.State)
		}
		if outcome.Run.ExitCode == nil || *outcome.Run.ExitCode != 3 {
			t.Errorf("exit code is %v, want 3", outcome.Run.ExitCode)
		}
	})

	// The third outcome, and the reason D-05 makes the argument form the default.
	//
	// A missing program says nothing about the code. Reporting it as a failing test tells somebody
	// their work is broken when what is broken is their configuration, and they will go looking in
	// the wrong place. This is the case that used to be logged rather than asserted.
	t.Run("a command that cannot start is an error, not a failure", func(t *testing.T) {
		outcome := RunTest(ctx,
			Test{Name: "unit", Command: Argv("canopy-definitely-not-a-real-binary")},
			target(t, "abc"), "run-3")

		if outcome.Run.State != core.TestError {
			t.Errorf("state is %q, want %q: a program that is not installed is a broken "+
				"configuration, and calling it a failing test sends somebody to read their own code",
				outcome.Run.State, core.TestError)
		}
		if outcome.Run.ExitCode != nil {
			t.Errorf("exit code is %v, want none: nothing ran, so there is no status to report",
				*outcome.Run.ExitCode)
		}
		if !strings.Contains(outcome.Run.ErrorMessage, "could not be started") {
			t.Errorf("the message does not say it never started: %q", outcome.Run.ErrorMessage)
		}
	})

	// The cost of opting into a shell, asserted rather than left implicit. A shell starts perfectly
	// well and exits 127, so the distinction above is genuinely unavailable here. Anyone reading this
	// should see that the choice in canopy.json is what decides it.
	t.Run("through a shell the same mistake is indistinguishable from a failure", func(t *testing.T) {
		outcome := RunTest(ctx,
			Test{Name: "unit", Command: ShellLine("canopy-definitely-not-a-real-binary")},
			target(t, "abc"), "run-3b")

		if outcome.Run.State != core.TestFailing {
			t.Errorf("state is %q, want %q: the shell ran and exited non zero, which is all the "+
				"runner can see", outcome.Run.State, core.TestFailing)
		}
		if !strings.Contains(outcome.Output, "not found") &&
			!strings.Contains(outcome.Output, "No such file") {
			t.Errorf("the output does not say the binary is missing: %q", outcome.Output)
		}
	})

	t.Run("an unconfigured command is an error", func(t *testing.T) {
		outcome := RunTest(ctx, Test{Name: "unit"}, target(t, "abc"), "run-4")
		if outcome.Run.State != core.TestError {
			t.Errorf("state is %q, want error: a test with no command has said nothing about the code",
				outcome.Run.State)
		}
		if outcome.Run.ExitCode != nil {
			t.Error("a run that never happened reported an exit code")
		}
		if !strings.Contains(outcome.Run.ErrorMessage, "no command") {
			t.Errorf("the reason is %q, which does not say what is wrong", outcome.Run.ErrorMessage)
		}
	})
}

// The point of the whole file: the result belongs to the code that was on disk when the command
// started reading it.
func TestTheRevisionIsCapturedBeforeTheCommandRuns(t *testing.T) {
	var mu sync.Mutex
	current := "before"
	dir := t.TempDir()

	outcome := RunTest(context.Background(),
		Test{Name: "unit", Command: ShellLine("exit 0")},
		Target{
			WorkspaceID: "ws-1",
			Dir:         dir,
			Revision: func(context.Context) (core.RevisionKey, string) {
				mu.Lock()
				defer mu.Unlock()
				sha := current
				// The next read would see a different revision. If the runner asked at the end it
				// would get this one and bind the pass to code it never saw.
				current = "after"
				return core.RevisionKey{HeadSHA: sha}, ""
			},
		}, "run-1")

	if outcome.Run.Revision.HeadSHA != "before" {
		t.Errorf("the run recorded revision %q, want the one that was current when it started",
			outcome.Run.Revision.HeadSHA)
	}
}

// A run with no revision is not a failing run and not a passing one. The display layer turns it
// into unknown, which is what stops evidence gathered during an outage from ever going green.
func TestARunWithNoRevisionCannotGoGreen(t *testing.T) {
	outcome := RunTest(context.Background(),
		Test{Name: "unit", Command: ShellLine("exit 0")},
		Target{WorkspaceID: "ws-1", Dir: t.TempDir()}, "run-1")

	if outcome.Run.State != core.TestPassing {
		t.Fatalf("the recorded state is %q, want passing: the command really did exit zero", outcome.Run.State)
	}
	visible := core.VisibleTestState(&outcome.Run, core.RevisionKey{HeadSHA: "abc"})
	if visible.IsGreen() {
		t.Error("a run that recorded no revision displayed as green")
	}
	if visible != core.TestUnknown {
		t.Errorf("it displayed as %q, want unknown", visible)
	}
}

// A run that never finishes is an error rather than a failure, because it produced no verdict.
func TestATestThatHangsIsAnErrorRatherThanAFailure(t *testing.T) {
	outcome := RunTest(context.Background(),
		Test{Name: "unit", Command: ShellLine("sleep 30"), Timeout: 100 * time.Millisecond},
		target(t, "abc"), "run-1")

	if outcome.Run.State != core.TestError {
		t.Errorf("state is %q, want error", outcome.Run.State)
	}
	if outcome.Run.ExitCode != nil {
		t.Error("a timed out run reported an exit code, which would read as a verdict about the code")
	}
	if !strings.Contains(outcome.Run.ErrorMessage, "without finishing") {
		t.Errorf("the reason is %q, which does not distinguish a hang from a failure", outcome.Run.ErrorMessage)
	}
}

func TestADurationIsRecorded(t *testing.T) {
	outcome := RunTest(context.Background(),
		Test{Name: "unit", Command: ShellLine("exit 0")}, target(t, "abc"), "run-1")

	duration, finished := outcome.Run.Duration()
	if !finished {
		t.Fatal("the run has no finish time")
	}
	if duration <= 0 {
		t.Errorf("the run took %s, which cannot be right", duration)
	}
}

// The runner adds identity and cancellation to the measurement, and nothing else. Both are asserted
// here because a run nobody can find again and a suite nobody can stop are the two ways this layer
// becomes useless.
func TestARunCanBeFoundAgainAndStopped(t *testing.T) {
	updates := make(chan core.TestRun, 8)
	runner := NewRunner(func(run core.TestRun) { updates <- run })

	ctx := context.Background()
	runID, err := runner.Start(ctx, Test{Name: "slow", Command: ShellLine("sleep 30")}, target(t, "abc"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Queued and running both arrive before the result, so a user sees the suite start rather than
	// a row that sits still for four minutes and then changes.
	first := <-updates
	if first.State != core.TestQueued {
		t.Errorf("the first update is %q, want queued", first.State)
	}
	var running core.TestRun
	select {
	case running = <-updates:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never published its running state")
	}
	if running.State != core.TestRunning {
		t.Fatalf("the second update is %q, want running", running.State)
	}
	if !running.Revision.Known() {
		t.Error("the running update has no revision, so the truth layer displays UNKNOWN instead of RUN")
	}
	if visible := core.VisibleTestState(&running, running.Revision); visible != core.TestRunning {
		t.Errorf("the running update displays as %q, want running", visible)
	}
	if run, ok := runner.Run(runID); !ok || run.ID != runID {
		t.Errorf("the run could not be found again: %+v %v", run, ok)
	}

	if err := runner.Cancel(runID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case update := <-updates:
			if !update.State.IsTerminal() {
				continue
			}
			if update.State != core.TestCancelled {
				t.Errorf("a cancelled run finished as %q, want cancelled", update.State)
			}
			if update.State.IsGreen() {
				t.Error("a cancelled run went green")
			}
			return
		case <-deadline:
			t.Fatal("the cancelled run never reached a terminal state, so the process is still out there")
		}
	}
}

// Cancelling something that already finished is a race every caller would otherwise have to handle
// themselves, and the answer is the same either way: the run is not going to produce anything more.
func TestCancellingAFinishedRunIsNotAnError(t *testing.T) {
	done := make(chan core.TestRun, 8)
	runner := NewRunner(func(run core.TestRun) {
		if run.State.IsTerminal() {
			done <- run
		}
	})

	runID, err := runner.Start(context.Background(), Test{Name: "quick", Command: ShellLine("exit 0")}, target(t, "abc"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the run never finished")
	}

	if err := runner.Cancel(runID); err != nil {
		t.Errorf("cancelling a finished run: %v", err)
	}
	if err := runner.Cancel("run-does-not-exist"); err == nil {
		t.Error("cancelling a run that was never started was accepted, so a typo would look like it worked")
	}
}

func TestTheLatestRunOfATestIsTheOneReported(t *testing.T) {
	done := make(chan core.TestRun, 8)
	runner := NewRunner(func(run core.TestRun) {
		if run.State.IsTerminal() {
			done <- run
		}
	})
	where := target(t, "abc")
	ctx := context.Background()

	for _, command := range []string{"exit 1", "exit 0"} {
		if _, err := runner.Start(ctx, Test{Name: "unit", Command: ShellLine(command)}, where); err != nil {
			t.Fatalf("Start: %v", err)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a run never finished")
		}
		// Separated in time so "most recent" is decidable rather than a coin toss.
		time.Sleep(2 * time.Millisecond)
	}

	latest, ok := runner.Latest("ws-1", "unit")
	if !ok {
		t.Fatal("no run was found for a test that has run twice")
	}
	if latest.State != core.TestPassing {
		t.Errorf("the latest run is %q, want the passing one that ran second", latest.State)
	}
}

func TestShuttingDownStopsEveryRun(t *testing.T) {
	terminal := make(chan core.TestRun, 8)
	runner := NewRunner(func(run core.TestRun) {
		if run.State.IsTerminal() {
			terminal <- run
		}
	})

	ctx := context.Background()
	for range 3 {
		if _, err := runner.Start(ctx, Test{Name: "slow", Command: ShellLine("sleep 30")}, target(t, "abc")); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}

	runner.CancelAll()

	for i := range 3 {
		select {
		case <-terminal:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 3 runs stopped, so shutting down leaves test processes behind", i)
		}
	}
}
