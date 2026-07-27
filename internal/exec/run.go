// Package exec runs commands on behalf of an agent.
//
// Three things it has to get right, and each of them is a way a long running session degrades
// invisibly if it does not:
//
//   - **No orphans.** Test runners and dev servers spawn children. Killing only the process we
//     started leaves those children holding ports and file handles, and the next run fails with
//     "address already in use" for reasons nobody can see. So every command gets its own process
//     group and the whole group is killed together.
//   - **Bounded output.** A command that prints a megabyte would put a megabyte into the model's
//     context, which costs real money and destroys the conversation in one call.
//   - **A timeout that is not optional.** A command waiting on stdin waits forever, and an agent
//     blocked on it is an agent that looks like it is thinking.
package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DefaultTimeout applies when a caller does not choose one.
//
// Two minutes is long enough for a test suite and short enough that a command waiting on input is
// noticed in the same sitting. There is no "no timeout" option, deliberately: a hung command is a
// session that looks like it is thinking, and the failure mode of forgetting to set one should be
// a command that stops rather than one that never does.
const DefaultTimeout = 2 * time.Minute

// MaxOutputBytes is how much output is kept.
const MaxOutputBytes = 32 * 1024

// Result is what a command did.
type Result struct {
	// Output is stdout and stderr combined, in the order they were written.
	//
	// Combined rather than separated because that is the order a person sees in a terminal, and a
	// compiler error printed to stderr means very little without the line of stdout it followed.
	// The cost is not being able to tell which stream a line came from, which matters far less.
	Output string

	// ExitCode is the command's status. Meaningful only when Ran is true.
	ExitCode int

	// Ran distinguishes a command that failed from one that never started.
	//
	// The two need different words: a missing binary is a mistake in the command, and a non zero
	// exit is usually the answer to the question that was asked.
	Ran bool

	// TimedOut and Cancelled say why a command stopped early, which a non zero exit code alone
	// cannot express.
	TimedOut  bool
	Cancelled bool

	Duration time.Duration

	// Truncated is how many bytes of output were dropped, zero when none were.
	Truncated int
}

// Succeeded reports whether the command ran and exited zero.
func (r Result) Succeeded() bool { return r.Ran && r.ExitCode == 0 && !r.TimedOut && !r.Cancelled }

// Options configure a run.
type Options struct {
	// Dir is where the command runs. Required: a command with no working directory runs wherever
	// Canopy happens to have been started, which is somebody else's repository.
	Dir string

	// Timeout defaults to DefaultTimeout.
	Timeout time.Duration

	// Env replaces the environment entirely when non nil.
	Env []string

	// MaxOutput defaults to MaxOutputBytes.
	MaxOutput int
}

// Run executes a command and waits for it.
func Run(ctx context.Context, name string, args []string, opts Options) (Result, error) {
	if opts.Dir == "" {
		return Result{}, errors.New("a working directory is required")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	limit := opts.MaxOutput
	if limit <= 0 {
		limit = MaxOutputBytes
	}

	// The deadline is separate from the caller's context so the two causes stay distinguishable.
	// "The command took too long" and "you pressed escape" lead somewhere different.
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.Command(name, args...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env

	output := &boundedBuffer{limit: limit}
	cmd.Stdout = output
	cmd.Stderr = output

	// Its own process group, so children can be killed with it. Without this, killing the shell
	// leaves whatever it started running.
	setProcessGroup(cmd)

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{
			Output:   fmt.Sprintf("could not run %s: %v", name, err),
			Duration: time.Since(started),
		}, nil
	}

	// Waiting in a goroutine so the kill path is not blocked behind it. Wait does not return until
	// the output pipes close, which for a process that has left children behind does not happen
	// until those children exit too, which is the case being handled here.
	//
	// reaped is closed the moment Wait returns, and it is closed before the result is handed over so
	// that anything watching it sees the reap before the caller does. The kill path needs to know
	// this and cannot get it from done: a process id stops being safe to address as a process group
	// the moment its leader has been waited on, and killGroup is the only thing in a position to
	// care.
	done := make(chan error, 1)
	reaped := make(chan struct{})
	go func() {
		err := cmd.Wait()
		close(reaped)
		done <- err
	}()

	var timedOut, cancelled bool
	select {
	case err := <-done:
		return finish(output, cmd, err, started, timedOut, cancelled), nil

	case <-runCtx.Done():
		timedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
		cancelled = !timedOut

		killGroup(cmd, reaped)

		// Give the group a moment to die before giving up on it. The wait is bounded because a
		// process that ignores a kill is not going to start obeying, and blocking forever here
		// would mean cancelling a session hung the program.
		select {
		case err := <-done:
			return finish(output, cmd, err, started, timedOut, cancelled), nil
		case <-time.After(2 * time.Second):
			result := finish(output, cmd, nil, started, timedOut, cancelled)
			result.Ran = false
			result.Output += "\n(the command did not stop when asked and has been abandoned)"
			return result, nil
		}
	}
}

func finish(
	output *boundedBuffer, cmd *exec.Cmd, waitErr error,
	started time.Time, timedOut, cancelled bool,
) Result {
	result := Result{
		Output:    output.String(),
		Ran:       true,
		TimedOut:  timedOut,
		Cancelled: cancelled,
		Duration:  time.Since(started),
		Truncated: output.Dropped(),
	}

	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
		result.ExitCode = 0
	case errors.As(waitErr, &exitErr):
		result.ExitCode = exitErr.ExitCode()
	default:
		result.Ran = false
		result.Output += "\n" + waitErr.Error()
	}

	if cmd.ProcessState != nil && result.ExitCode == 0 && !cmd.ProcessState.Success() {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	return result
}

// boundedBuffer keeps the head and the tail of a stream and counts what it dropped.
//
// Head and tail rather than just the tail, because the two ends carry different information and
// both are usually needed: the first error a compiler printed is at the top, and the summary of how
// many there were is at the bottom. Keeping only one end loses whichever half somebody needed.
type boundedBuffer struct {
	mu      sync.Mutex
	limit   int
	head    bytes.Buffer
	tail    []byte
	dropped int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	total := len(p)
	half := b.limit / 2

	for len(p) > 0 {
		if b.head.Len() < half {
			take := half - b.head.Len()
			if take > len(p) {
				take = len(p)
			}
			b.head.Write(p[:take])
			p = p[take:]
			continue
		}

		// Everything after the head is a sliding window of the most recent bytes.
		//
		// Trimmed before it is appended as well as after. Appending first and trimming afterwards
		// would mean a command that prints eight megabytes in one write allocates eight megabytes
		// here before dropping most of them again, which is the failure this buffer exists to
		// prevent rather than a briefer version of it. Nothing in a single write can survive except
		// its last half-limit bytes, so the rest never needs to be copied at all.
		if extra := len(p) - half; extra > 0 {
			p = p[extra:]
			b.dropped += extra
		}
		b.tail = append(b.tail, p...)
		if extra := len(b.tail) - half; extra > 0 {
			b.tail = b.tail[extra:]
			b.dropped += extra
		}
		break
	}
	return total, nil
}

func (b *boundedBuffer) Dropped() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dropped
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.dropped == 0 {
		return b.head.String() + string(b.tail)
	}
	// Said in the middle, where it belongs, and said in bytes so somebody can tell whether what
	// they needed is likely to be in the part that survived.
	return fmt.Sprintf("%s\n\n... %d bytes of output dropped from the middle ...\n\n%s",
		strings.TrimRight(b.head.String(), "\n"), b.dropped, string(b.tail))
}
