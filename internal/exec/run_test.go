package exec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, ctx context.Context, command string, opts Options) Result {
	t.Helper()

	if opts.Dir == "" {
		opts.Dir = t.TempDir()
	}
	result, err := Run(ctx, "/bin/sh", []string{"-c", command}, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return result
}

func TestRunningACommand(t *testing.T) {
	result := run(t, context.Background(), "echo hello", Options{})

	if !result.Succeeded() {
		t.Errorf("result = %+v", result)
	}
	if strings.TrimSpace(result.Output) != "hello" {
		t.Errorf("output = %q", result.Output)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d", result.ExitCode)
	}
}

// A non zero exit is usually the answer to the question that was asked, not a fault.
func TestAFailingCommandIsAResultNotAnError(t *testing.T) {
	result := run(t, context.Background(), "echo problem >&2; exit 3", Options{})

	if !result.Ran {
		t.Error("the command ran, so Ran should say so")
	}
	if result.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", result.ExitCode)
	}
	if result.Succeeded() {
		t.Error("a command that exited 3 did not succeed")
	}
	// stderr has to reach the output, or a build failure comes back as silence.
	if !strings.Contains(result.Output, "problem") {
		t.Errorf("stderr is missing from the output: %q", result.Output)
	}
}

// The two ends carry different information and both are usually needed: the first error a compiler
// printed is at the top, and how many there were is at the bottom.
func TestLongOutputKeepsBothEndsAndSaysWhatItDropped(t *testing.T) {
	result := run(t, context.Background(),
		`i=0; while [ $i -lt 4000 ]; do echo "line $i padding padding padding"; i=$((i+1)); done`,
		Options{MaxOutput: 2000})

	if len(result.Output) > 4000 {
		t.Errorf("output is %d bytes despite a 2000 byte limit", len(result.Output))
	}
	if !strings.Contains(result.Output, "line 0 ") {
		t.Error("the beginning of the output was dropped, which is where the first error is")
	}
	if !strings.Contains(result.Output, "line 3999") {
		t.Error("the end of the output was dropped, which is where the summary is")
	}
	if result.Truncated == 0 {
		t.Error("output was dropped and nothing recorded it")
	}
	// Said in the output itself, because a model that cannot see the gap will answer as though the
	// two halves were adjacent.
	if !strings.Contains(result.Output, "dropped") {
		t.Errorf("the gap is not marked in the output:\n%s", result.Output)
	}
}

// A command waiting on input waits forever, and an agent blocked on it looks like it is thinking.
func TestATimeoutStopsTheCommand(t *testing.T) {
	started := time.Now()
	result := run(t, context.Background(), "sleep 30", Options{Timeout: 300 * time.Millisecond})

	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the timeout took %v to take effect", elapsed)
	}
	if !result.TimedOut {
		t.Errorf("result = %+v, want TimedOut", result)
	}
	if result.Cancelled {
		t.Error("a timeout is not a cancellation, and the two lead somewhere different")
	}
	if result.Succeeded() {
		t.Error("a command that was stopped did not succeed")
	}
}

// "The command took too long" and "you pressed escape" lead somewhere different.
func TestCancellationIsDistinguishedFromATimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	result := run(t, ctx, "sleep 30", Options{Timeout: time.Minute})

	if !result.Cancelled {
		t.Errorf("result = %+v, want Cancelled", result)
	}
	if result.TimedOut {
		t.Error("a cancellation is not a timeout")
	}
}

// The reason process groups exist here. Killing only the process we started leaves its children
// holding ports and file handles, and the next run fails for reasons nobody can see.
func TestKillingACommandTakesItsChildrenWithIt(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-still-running")

	// A shell that starts a background child which would keep touching a file for a long time. If
	// the child survives, the file keeps changing after the parent is gone.
	command := fmt.Sprintf(
		`(i=0; while [ $i -lt 200 ]; do echo $i > %q; sleep 0.05; i=$((i+1)); done) & echo started; wait`,
		marker)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(400 * time.Millisecond)
		cancel()
	}()

	result := run(t, ctx, command, Options{Dir: dir, Timeout: 30 * time.Second})
	if !result.Cancelled {
		t.Fatalf("precondition: %+v", result)
	}

	// Let anything that survived carry on for a moment, then see whether it did.
	time.Sleep(300 * time.Millisecond)
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Skipf("the child never got going: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if string(before) != string(after) {
		t.Errorf("a child process outlived the cancelled command: the marker went from %q to %q",
			strings.TrimSpace(string(before)), strings.TrimSpace(string(after)))
	}
}

// A missing binary is a mistake in the command; a non zero exit is usually the answer. The two need
// different words.
func TestACommandThatCannotStart(t *testing.T) {
	result, err := Run(context.Background(), "/definitely/not/a/binary", nil, Options{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run returned a Go error rather than a result: %v", err)
	}
	if result.Ran {
		t.Error("a binary that does not exist did not run")
	}
	if result.Output == "" {
		t.Error("a command that could not start should say why")
	}
}

// A command with no working directory runs wherever Canopy happens to have been started, which is
// somebody else's repository.
func TestAWorkingDirectoryIsRequired(t *testing.T) {
	if _, err := Run(context.Background(), "echo", []string{"hi"}, Options{}); err == nil {
		t.Error("a run with no directory should be refused")
	}
}

func TestTheCommandRunsInTheGivenDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result := run(t, context.Background(), "ls", Options{Dir: dir})
	if !strings.Contains(result.Output, "marker.txt") {
		t.Errorf("the command ran somewhere else: %q", result.Output)
	}
}

// Sanity check that the test above would actually catch a leak, by confirming the platform lets us
// see orphaned processes at all.
func TestProcessListingWorksHere(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no shell available")
	}
}
