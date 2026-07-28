package exec

// The robustness sweep's share of this package: timeouts that take a whole process group with them,
// output that stays bounded whatever a command decides to print, and paths that have spaces in them
// because somebody's projects live in a folder called "Side Projects".
//
// These are properties rather than features, so they are worth a test each even where they already
// hold. A property nobody asserts is one that survives until the first refactor that has no reason
// to know about it.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// marker watches a file a background child keeps rewriting, which is how a process that outlived
// the command that started it becomes visible from a test.
func markerStopsChanging(t *testing.T, path string, settle, watch time.Duration) {
	t.Helper()

	time.Sleep(settle)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("the child never got going: %v", err)
	}

	time.Sleep(watch)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if string(before) != string(after) {
		t.Errorf("a child process outlived the command that started it: the marker went from "+
			"%q to %q", strings.TrimSpace(string(before)), strings.TrimSpace(string(after)))
	}
}

// spawnsAChild is a shell that starts a background worker and waits for it, which is the shape of
// every test runner and dev server this package exists to handle.
func spawnsAChild(marker string) string {
	return fmt.Sprintf(
		`(i=0; while [ $i -lt 400 ]; do echo $i > %q; sleep 0.02; i=$((i+1)); done) & echo started; wait`,
		marker)
}

// A timeout has to take the whole group, not just the shell at the top of it. Cancellation was
// already covered; a timeout arrives through a different branch of the same select and there is
// nothing but this test stopping the two from drifting apart.
func TestATimeoutTerminatesTheWholeProcessGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-still-running")

	result := run(t, context.Background(), spawnsAChild(marker),
		Options{Dir: dir, Timeout: 400 * time.Millisecond})

	if !result.TimedOut {
		t.Fatalf("precondition: %+v, want a timed out run", result)
	}
	if result.Cancelled {
		t.Error("a timeout is not a cancellation, and the two lead somewhere different")
	}

	markerStopsChanging(t, marker, 300*time.Millisecond, 500*time.Millisecond)
}

// A command that prints more than the limit must cost the limit, not what it printed. The existing
// test asks this of a 2000 byte limit; this one asks it of the default, against a command that
// produces eight megabytes as fast as the machine will let it.
func TestOutputStaysBoundedWhateverACommandPrints(t *testing.T) {
	started := time.Now()
	result := run(t, context.Background(),
		`dd if=/dev/zero bs=1048576 count=8 2>/dev/null | tr '\0' x`, Options{})

	if !result.Ran {
		t.Skipf("dd or tr is not available here: %s", result.Output)
	}

	// The bound plus room for the sentence that says what was dropped. Anything beyond that is the
	// buffer having grown to fit the writer, which is what an unbounded buffer looks like from here.
	if limit := MaxOutputBytes + 200; len(result.Output) > limit {
		t.Errorf("output is %d bytes against a %d byte limit", len(result.Output), MaxOutputBytes)
	}
	if result.Truncated < 8<<20-MaxOutputBytes {
		t.Errorf("Truncated = %d, which does not account for eight megabytes of output",
			result.Truncated)
	}
	// Not a benchmark. It is here because the way this goes wrong is that the buffer grows to hold
	// everything, and a run that has to allocate and copy megabytes takes visibly longer than one
	// that throws them away as they arrive.
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Errorf("eight megabytes of output took %v to get through", elapsed)
	}
}

// The bound has to apply to the write, not only to what is kept afterwards. A single enormous write
// is not hypothetical: it is what any writer with its own buffering does, and a limit enforced after
// the copy is not a limit on memory at all.
func TestASingleEnormousWriteIsNotCopiedInFullBeforeBeingDropped(t *testing.T) {
	b := &boundedBuffer{limit: 4096}
	huge := bytes.Repeat([]byte("x"), 8<<20)

	// What the buffer allocates rather than what it ends up holding, because those are different
	// questions and only the first one is about memory. A buffer that copies eight megabytes in and
	// then keeps four kilobytes of it has still had eight megabytes in it, and re-slicing what it
	// keeps hides that from every measurement except this one.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if _, err := b.Write(huge); err != nil {
		t.Fatalf("Write: %v", err)
	}
	runtime.ReadMemStats(&after)

	if grew := after.TotalAlloc - before.TotalAlloc; grew > 1<<20 {
		t.Errorf("writing %d bytes into a %d byte buffer allocated %d bytes: a bound that only "+
			"applies after the writer has been copied in is not a bound on memory",
			len(huge), b.limit, grew)
	}
	if got := len(b.String()); got > b.limit+200 {
		t.Errorf("String() returned %d bytes against a %d byte limit", got, b.limit)
	}
	if b.dropped == 0 {
		t.Error("eight megabytes went in, a few kilobytes came out, and nothing recorded a drop")
	}
}

// Somebody's projects live in "~/Side Projects", and a worktree Canopy makes for them inherits the
// space. Nothing here goes through a shell, so this holds by construction, which is exactly the kind
// of thing that stops holding the day somebody builds a command string instead.
func TestACommandRunsInADirectoryWhosePathContainsSpaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my side projects")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result := run(t, context.Background(), "ls", Options{Dir: dir})
	if !result.Succeeded() {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Output, "a file.txt") {
		t.Errorf("the command ran somewhere other than the directory with spaces in it: %q",
			result.Output)
	}
}

// Arguments are handed to the process rather than to a shell, so a path with a space in it is one
// argument. Asserted because the failure is quiet: a split path turns into two arguments and git
// reports something about a file that was never named.
func TestAnArgumentWithSpacesArrivesAsOneArgument(t *testing.T) {
	dir := t.TempDir()
	name := "one file with spaces.txt"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("content\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	result, err := Run(context.Background(), "cat", []string{name}, Options{Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Succeeded() {
		t.Fatalf("result = %+v", result)
	}
	if strings.TrimSpace(result.Output) != "content" {
		t.Errorf("output = %q, want the file's content", result.Output)
	}
}

// Quitting is not "ask every run to stop", it is "every run has stopped". The gap between the two is
// where a test suite and everything it spawned carries on holding the ports the next run needs.
//
// The command ignores SIGTERM on purpose, so it can only be stopped by the escalation that follows a
// quarter of a second later. That is what makes the difference observable: a CancelAll that returns
// before the escalation has happened returns while the process is demonstrably still writing.
func TestQuittingDoesNotReturnUntilEveryTestProcessHasGone(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "suite-still-running")

	runner := NewRunner(nil)
	command := fmt.Sprintf(
		`trap '' TERM; i=0; while [ $i -lt 500 ]; do echo $i > %q; sleep 0.02; i=$((i+1)); done`,
		marker)

	if _, err := runner.Start(context.Background(),
		Test{Name: "suite", Command: command}, Target{WorkspaceID: "ws-1", Dir: dir}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the suite to be genuinely running, or this would be asserting against a process that
	// had not started yet.
	waitForFile(t, marker, 5*time.Second)

	runner.CancelAll()

	// Read straight away, with nothing in between. This is the instant the program exits at, so
	// anything still moving here is a process that quitting left behind.
	markerStopsChanging(t, marker, 0, 400*time.Millisecond)
}

func waitForFile(t *testing.T, path string, within time.Duration) {
	t.Helper()

	deadline := time.Now().Add(within)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared, so the command never got going", filepath.Base(path))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
