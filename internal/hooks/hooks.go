package hooks

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Event is something that happened to a subject, in the vocabulary a hook can name.
//
// Deliberately small. Every entry here is a state the verification engine already establishes and
// already stands behind, because a trigger is only worth as much as the evidence under it. Adding
// one means being able to say what evidence makes it true and when it stops being true.
type Event string

const (
	// TestsPassed means every configured test for this subject passed against the current revision.
	// Against the current revision is the whole of it: a pass that has gone stale is not this.
	TestsPassed Event = "tests-passed"

	// TestsFailed means a test failed against the current revision.
	TestsFailed Event = "tests-failed"

	// Verified means the roll-up went green, which is stricter than the tests passing: every
	// required piece of evidence is present, current and good.
	Verified Event = "verified"

	// Idle means an agent finished its turn and is waiting for a person.
	Idle Event = "agent-idle"

	// Blocked means an agent asked to do something and is waiting on an answer, which is the state
	// worth a notification because nothing moves until somebody looks.
	Blocked Event = "agent-blocked"
)

// Events returns the whole vocabulary, for validation and for the error message that lists it.
func Events() []Event { return []Event{TestsPassed, TestsFailed, Verified, Idle, Blocked} }

// ValidEvent reports whether a name is one Canopy will act on.
//
// Exported because internal/config validates the file against it. An event Canopy has never heard
// of has to be an error at load time: a hook that silently never fires cannot be told apart from one
// that fires and does nothing, and the first is a typo while the second is a working configuration.
func ValidEvent(name string) bool {
	for _, event := range Events() {
		if Event(name) == event {
			return true
		}
	}
	return false
}

// EventNames lists the vocabulary in an error message.
func EventNames() string {
	names := make([]string, 0, len(Events()))
	for _, event := range Events() {
		names = append(names, string(event))
	}
	return strings.Join(names, ", ")
}

// Hook is one command bound to one event.
type Hook struct {
	On      Event
	Run     string
	Timeout time.Duration
}

// Observation is everything the runner needs to know about one subject right now.
//
// A snapshot rather than a delta, because the poller produces snapshots and because a runner that
// took deltas would have to be told about every intermediate state to stay correct. Working out
// what changed is this package's job, and doing it in one place is what makes the edge rule
// enforceable.
type Observation struct {
	// Subject is the agent this is about, and is what a hook's environment names.
	Subject string

	// Revision is the code the evidence below describes. An unknown revision means no evidence
	// about code can fire anything.
	Revision core.RevisionKey

	// Tests is the derived state, already accounting for staleness, not the recorded one.
	Tests core.TestState

	// Green is the roll-up verdict.
	Green bool

	// Agent is what the agent itself is doing, which is true regardless of the revision.
	Agent core.AgentState
}

// Report is what a hook did, whether or not it worked.
//
// Every run produces one. A hook that fails has to be visible: the whole point of automation here is
// that somebody stops watching, and a failure nobody is told about means they stopped watching
// something that stopped working.
type Report struct {
	Subject  string
	Event    Event
	Command  string
	Output   string
	Duration time.Duration

	// Err is set when the hook did not run, timed out, or exited non zero.
	Err error
}

// Failed reports whether this run needs somebody's attention.
func (r Report) Failed() bool { return r.Err != nil }

// Summary is one line for the transcript or the status area.
func (r Report) Summary() string {
	if r.Err == nil {
		return fmt.Sprintf("the %s hook ran for %s", r.Event, r.Subject)
	}
	return fmt.Sprintf("the %s hook for %s failed: %v", r.Event, r.Subject, r.Err)
}

// Executor runs a command. Injected so the tests do not need a shell.
type Executor func(ctx context.Context, command, dir string, env []string) (string, error)

// Runner decides what fires and runs it.
//
// The zero value is not usable. Use New.
type Runner struct {
	hooks map[Event][]Hook
	dir   string

	exec   Executor
	report func(Report)

	mu sync.Mutex
	// fired remembers what has already happened, keyed by subject, event and revision together.
	// The revision is what stops a hook that changes the worktree from re-triggering itself, and
	// the subject is what keeps two agents from sharing one memory.
	fired map[string]bool
	// state remembers where each subject was, so a hook fires on entering a state rather than on
	// being in one.
	state map[string]map[Event]bool

	running sync.WaitGroup
}

// New builds a runner for a project's hooks.
//
// Reports go to the callback, which is how a failure reaches a person. A nil callback is allowed and
// means the caller has decided not to surface them, which is a choice they have to make explicitly
// rather than the default.
func New(hooks []Hook, dir string, exec Executor, report func(Report)) *Runner {
	byEvent := make(map[Event][]Hook, len(hooks))
	for _, hook := range hooks {
		byEvent[hook.On] = append(byEvent[hook.On], hook)
	}
	return &Runner{
		hooks:  byEvent,
		dir:    dir,
		exec:   exec,
		report: report,
		fired:  map[string]bool{},
		state:  map[string]map[Event]bool{},
	}
}

// Observe takes the current state of a subject and runs whatever that newly satisfies.
//
// Returns the events it fired, which is what the tests assert on and what a caller can log. Hooks
// themselves run in the background, because a hook is a command somebody wrote and the poller must
// not wait on it.
func (r *Runner) Observe(ctx context.Context, obs Observation) []Event {
	now := eventsIn(obs)

	r.mu.Lock()
	previous := r.state[obs.Subject]
	if previous == nil {
		previous = map[Event]bool{}
	}

	var firing []Event
	for _, event := range Events() {
		switch {
		case !now[event]:
			// Not true any more, or never was. Nothing to fire, and leaving the record to be
			// replaced below is what lets it fire again when it becomes true later.
		case previous[event]:
			// Already in this state. This is the rule that stops a poll every two seconds from
			// being a commit every two seconds.
		case len(r.hooks[event]) == 0:
			// Nothing is listening, so there is no reason to spend a revision key on it.
		default:
			key := obs.Subject + "\x00" + string(event) + "\x00" + obs.Revision.String()
			if r.fired[key] {
				// Already fired for this exact revision. A hook that commits moves HEAD, which
				// moves the revision, which makes the tests stale, which makes them run again and
				// pass again. Without this that is a loop, and it is the first configuration
				// anybody writes.
				continue
			}
			r.fired[key] = true
			firing = append(firing, event)
		}
	}
	r.state[obs.Subject] = now
	hooks := r.hooksFor(firing)
	r.mu.Unlock()

	for i, event := range firing {
		for _, hook := range hooks[i] {
			r.start(ctx, obs, event, hook)
		}
	}
	return firing
}

// hooksFor collects the hooks to run while the lock is held, so the slice cannot change underneath.
func (r *Runner) hooksFor(events []Event) [][]Hook {
	out := make([][]Hook, len(events))
	for i, event := range events {
		out[i] = append([]Hook(nil), r.hooks[event]...)
	}
	return out
}

func (r *Runner) start(ctx context.Context, obs Observation, event Event, hook Hook) {
	r.running.Add(1)
	go func() {
		defer r.running.Done()
		r.run(ctx, obs, event, hook)
	}()
}

func (r *Runner) run(ctx context.Context, obs Observation, event Event, hook Hook) {
	timeout := hook.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	output, err := r.exec(ctx, hook.Run, r.dir, environmentFor(obs, event))
	elapsed := time.Since(started)

	if err != nil && ctx.Err() != nil {
		// Named as a timeout rather than passed through as "signal: killed", which tells a reader
		// nothing about what to change.
		err = fmt.Errorf("it did not finish within %s", timeout)
	}

	if r.report != nil {
		r.report(Report{
			Subject:  obs.Subject,
			Event:    event,
			Command:  hook.Run,
			Output:   bound(output),
			Duration: elapsed,
			Err:      err,
		})
	}
}

// DefaultTimeout bounds a hook that does not set one.
//
// A hook is somebody's own command and it runs unattended, so the failure that matters is the one
// that never returns: an unbounded hook on a green build holds a goroutine and a process for as long
// as the program runs, and nothing on screen says why.
const DefaultTimeout = 2 * time.Minute

// maxOutputBytes bounds what a hook's output contributes to a report.
const maxOutputBytes = 16 * 1024

func bound(output string) string {
	if len(output) <= maxOutputBytes {
		return output
	}
	return output[:maxOutputBytes] + fmt.Sprintf("\n[truncated at %d bytes]", maxOutputBytes)
}

// Wait blocks until every hook in flight has finished.
//
// Shutting down without this leaves commands running after the program that started them has gone,
// which is the same property A9-01 asserts for test runs and the same argument: a process Canopy
// started is Canopy's to account for.
func (r *Runner) Wait() { r.running.Wait() }

// eventsIn works out which events an observation currently satisfies.
//
// The one place staleness is decided, so there is one place to look when asking whether a hook can
// fire on evidence that does not describe the code.
func eventsIn(obs Observation) map[Event]bool {
	now := map[Event]bool{}

	switch obs.Agent {
	case core.AgentIdle:
		now[Idle] = true
	case core.AgentAwaitingPermission:
		now[Blocked] = true
	}

	// Everything below is a claim about code, so it needs a revision to be a claim about. An unknown
	// revision means Canopy could not establish what is in the worktree, and a hook firing on
	// evidence that cannot be tied to code is the failure this package exists to avoid.
	if !obs.Revision.Known() {
		return now
	}

	// The derived state, which is already stale rather than passing when the evidence has been
	// overtaken. Comparing against TestPassing is therefore the whole of the staleness rule, and it
	// is worth saying so here because it looks like it is missing.
	switch obs.Tests {
	case core.TestPassing:
		now[TestsPassed] = true
	case core.TestFailing:
		now[TestsFailed] = true
	}

	if obs.Green {
		now[Verified] = true
	}
	return now
}

// environmentFor is what a hook is told about why it ran.
//
// In the environment rather than substituted into the command, so the command in the config file is
// the command that runs. A reviewer reading canopy.json should not have to simulate a substitution
// to know what will execute, and a subject name containing a quote should not be able to change the
// shape of a shell command.
func environmentFor(obs Observation, event Event) []string {
	env := []string{
		"CANOPY_EVENT=" + string(event),
		"CANOPY_AGENT=" + obs.Subject,
		"CANOPY_REVISION=" + obs.Revision.String(),
		"CANOPY_TESTS=" + string(obs.Tests),
		"CANOPY_VERIFIED=" + boolText(obs.Green),
	}
	sort.Strings(env)
	return env
}

func boolText(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
