package hooks

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// recorder stands in for a shell and remembers what it was asked to run.
type recorder struct {
	mu      sync.Mutex
	ran     []string
	envs    [][]string
	fail    error
	block   chan struct{}
	output  string
	started chan struct{}
}

func (r *recorder) exec(ctx context.Context, command, _ string, env []string) (string, error) {
	r.mu.Lock()
	r.ran = append(r.ran, command)
	r.envs = append(r.envs, env)
	block, started := r.block, r.started
	fail, output := r.fail, r.output
	r.mu.Unlock()

	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return output, fail
}

func (r *recorder) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ran...)
}

func (r *recorder) environment(i int) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := map[string]string{}
	for _, entry := range r.envs[i] {
		name, value, _ := strings.Cut(entry, "=")
		out[name] = value
	}
	return out
}

func rev(s string) core.RevisionKey { return core.RevisionKey{HeadSHA: s} }

func runnerWith(t *testing.T, exec Executor, reports *[]Report, hooks ...Hook) *Runner {
	t.Helper()

	var mu sync.Mutex
	r := New(hooks, t.TempDir(), exec, func(report Report) {
		mu.Lock()
		defer mu.Unlock()
		*reports = append(*reports, report)
	})
	t.Cleanup(r.Wait)
	return r
}

// The poller reports where everything stands every couple of seconds. A hook that fired on the
// state rather than on entering it would be an auto-commit every two seconds for as long as the
// build stayed green.
func TestAHookFiresOnEnteringAStateAndNotOnStayingInIt(t *testing.T) {
	exec := &recorder{}
	var reports []Report
	r := runnerWith(t, exec.exec, &reports, Hook{On: TestsPassed, Run: "make deploy"})

	green := Observation{Subject: "a1", Revision: rev("abc"), Tests: core.TestPassing}
	for range 5 {
		r.Observe(context.Background(), green)
	}
	r.Wait()

	if got := exec.commands(); len(got) != 1 {
		t.Errorf("the hook ran %d times across five identical polls: %v", len(got), got)
	}
}

// The rule that stops this package from writing a false green into history. A stale result is a
// claim about code that has since changed, and committing on the strength of one is that failure
// made permanent.
func TestNothingFiresOnStaleOrUnknownEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		obs  Observation
	}{
		{"stale", Observation{Subject: "a1", Revision: rev("abc"), Tests: core.TestStale}},
		{"unknown state", Observation{Subject: "a1", Revision: rev("abc"), Tests: core.TestUnknown}},
		{"unknown revision", Observation{Subject: "a1", Tests: core.TestPassing, Green: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exec := &recorder{}
			var reports []Report
			r := runnerWith(t, exec.exec, &reports,
				Hook{On: TestsPassed, Run: "commit"},
				Hook{On: Verified, Run: "commit"})

			r.Observe(context.Background(), tc.obs)
			r.Wait()

			if got := exec.commands(); len(got) != 0 {
				t.Errorf("a hook ran on %s evidence: %v", tc.name, got)
			}
		})
	}
}

// The other half of the staleness rule, and the one it would be easy to get wrong by suppressing
// too much. Going stale and passing again is new evidence about different code, so it fires again.
func TestPassingAgainAfterGoingStaleFiresAgain(t *testing.T) {
	exec := &recorder{}
	var reports []Report
	r := runnerWith(t, exec.exec, &reports, Hook{On: TestsPassed, Run: "notify"})
	ctx := context.Background()

	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("abc"), Tests: core.TestPassing})
	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("def"), Tests: core.TestStale})
	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("def"), Tests: core.TestPassing})
	r.Wait()

	if got := exec.commands(); len(got) != 2 {
		t.Errorf("ran %d times, want 2: a pass on new code is new evidence: %v", len(got), got)
	}
}

// The first hook anybody writes is "tests green, commit", and a commit moves HEAD, which changes the
// revision, which makes the results stale, which makes them run again and pass again. Without the
// revision in the key that is a loop rather than a feature.
func TestAHookThatChangesTheWorktreeDoesNotRetriggerItself(t *testing.T) {
	exec := &recorder{}
	var reports []Report
	r := runnerWith(t, exec.exec, &reports, Hook{On: Verified, Run: "git commit -am wip"})
	ctx := context.Background()

	// Green, commit fires. The commit moves HEAD and the evidence goes stale, then it passes again
	// against the same content, which is the same revision as far as the worktree is concerned.
	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("abc"), Green: true})
	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("abc"), Tests: core.TestStale})
	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("abc"), Green: true})
	r.Wait()

	if got := exec.commands(); len(got) != 1 {
		t.Errorf("the commit hook ran %d times for one revision: %v", len(got), got)
	}
}

// Two agents are the point of this product, so one agent's green must not consume another's hook.
func TestEachAgentGetsItsOwnHooks(t *testing.T) {
	exec := &recorder{}
	var reports []Report
	r := runnerWith(t, exec.exec, &reports, Hook{On: TestsPassed, Run: "report"})
	ctx := context.Background()

	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("abc"), Tests: core.TestPassing})
	r.Observe(ctx, Observation{Subject: "a2", Revision: rev("abc"), Tests: core.TestPassing})
	r.Wait()

	if got := exec.commands(); len(got) != 2 {
		t.Errorf("ran %d times for two agents, want 2: %v", len(got), got)
	}
}

// The second acceptance clause. Automation means somebody stops watching, so a hook that fails
// without saying so means they stopped watching something that stopped working.
func TestAFailingHookIsReportedRatherThanSwallowed(t *testing.T) {
	exec := &recorder{fail: errors.New("exit status 1"), output: "make: *** no rule to make target"}
	var reports []Report
	r := runnerWith(t, exec.exec, &reports, Hook{On: TestsFailed, Run: "make notify"})

	r.Observe(context.Background(), Observation{
		Subject: "a1", Revision: rev("abc"), Tests: core.TestFailing,
	})
	r.Wait()

	if len(reports) != 1 {
		t.Fatalf("%d reports for one failing hook", len(reports))
	}
	report := reports[0]
	if !report.Failed() {
		t.Error("a hook that exited non zero was reported as fine")
	}
	// What it printed is usually the only explanation there is.
	if !strings.Contains(report.Output, "no rule to make target") {
		t.Errorf("the hook's own output was dropped: %q", report.Output)
	}
	if !strings.Contains(report.Summary(), "failed") {
		t.Errorf("the summary does not say it failed: %q", report.Summary())
	}
}

// A successful hook is reported too, or the only evidence that automation is working is its silence.
func TestASuccessfulHookIsAlsoReported(t *testing.T) {
	exec := &recorder{}
	var reports []Report
	r := runnerWith(t, exec.exec, &reports, Hook{On: Idle, Run: "say done"})

	r.Observe(context.Background(), Observation{Subject: "a1", Agent: core.AgentIdle})
	r.Wait()

	if len(reports) != 1 || reports[0].Failed() {
		t.Fatalf("reports = %+v, want one success", reports)
	}
}

// A hook that never returns would otherwise hold a process for as long as the program runs, with
// nothing on screen saying why.
func TestAHookThatHangsIsStoppedAndSaidSo(t *testing.T) {
	exec := &recorder{block: make(chan struct{}), started: make(chan struct{}, 1)}
	defer close(exec.block)

	var reports []Report
	r := runnerWith(t, exec.exec, &reports,
		Hook{On: Idle, Run: "sleep forever", Timeout: 50 * time.Millisecond})

	r.Observe(context.Background(), Observation{Subject: "a1", Agent: core.AgentIdle})
	r.Wait()

	if len(reports) != 1 {
		t.Fatalf("%d reports", len(reports))
	}
	if !reports[0].Failed() {
		t.Fatal("a hook that never returned was reported as successful")
	}
	// Named as a timeout, because "signal: killed" tells a reader nothing about what to change.
	if !strings.Contains(reports[0].Err.Error(), "did not finish") {
		t.Errorf("error = %v, want it to name the timeout", reports[0].Err)
	}
}

// A hook is told why it ran through the environment rather than through its command line, so the
// command in the config file is the command that runs.
func TestAHookIsToldWhyItRanWithoutRewritingItsCommand(t *testing.T) {
	exec := &recorder{}
	var reports []Report
	// A name with a quote in it, which is what would break a command built by substitution.
	r := runnerWith(t, exec.exec, &reports, Hook{On: TestsPassed, Run: "record"})

	r.Observe(context.Background(), Observation{
		Subject: `weird"name`, Revision: rev("abc123"), Tests: core.TestPassing, Green: true,
	})
	r.Wait()

	if got := exec.commands(); len(got) != 1 || got[0] != "record" {
		t.Fatalf("the command was rewritten: %v", got)
	}
	env := exec.environment(0)
	for name, want := range map[string]string{
		"CANOPY_EVENT":    string(TestsPassed),
		"CANOPY_AGENT":    `weird"name`,
		"CANOPY_TESTS":    string(core.TestPassing),
		"CANOPY_VERIFIED": "yes",
	} {
		if env[name] != want {
			t.Errorf("%s = %q, want %q", name, env[name], want)
		}
	}
	if !strings.Contains(env["CANOPY_REVISION"], "abc123") {
		t.Errorf("CANOPY_REVISION = %q", env["CANOPY_REVISION"])
	}
}

// An event with nothing listening must not consume the one chance that revision had to fire, or a
// hook added to a running project would never fire for work already in flight.
func TestAnEventWithNoHookCostsNothing(t *testing.T) {
	exec := &recorder{}
	var reports []Report
	r := runnerWith(t, exec.exec, &reports, Hook{On: TestsFailed, Run: "notify"})

	fired := r.Observe(context.Background(), Observation{
		Subject: "a1", Revision: rev("abc"), Tests: core.TestPassing, Green: true,
	})
	r.Wait()

	if len(fired) != 0 {
		t.Errorf("fired %v with nothing listening for any of it", fired)
	}
	if got := exec.commands(); len(got) != 0 {
		t.Errorf("something ran: %v", got)
	}
}

// The vocabulary is shared with internal/config, which validates the file against it. A name that
// parses here and not there, or the other way round, is a hook that either cannot be configured or
// is configured and never fires.
func TestTheVocabularyIsClosed(t *testing.T) {
	for _, event := range Events() {
		if !ValidEvent(string(event)) {
			t.Errorf("%q is in the vocabulary and does not validate", event)
		}
	}
	for _, name := range []string{"", "tests-pass", "TESTS-PASSED", "verified ", "anything"} {
		if ValidEvent(name) {
			t.Errorf("%q validated and is not an event", name)
		}
	}
	if !strings.Contains(EventNames(), string(TestsPassed)) {
		t.Errorf("the error message does not list the vocabulary: %q", EventNames())
	}
}

// The executor is the one piece that cannot be proven with a fake, so it gets a real shell.
func TestTheShellExecutorRunsACommandAndReportsItsExit(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	out, err := Shell(ctx, "echo hello from $CANOPY_EVENT", dir, []string{"CANOPY_EVENT=verified"})
	if err != nil {
		t.Fatalf("Shell: %v", err)
	}
	if !strings.Contains(out, "hello from verified") {
		t.Errorf("output = %q, want the environment to have reached the command", out)
	}

	// A non zero exit has to come back as an error, or "tests red, notify" fails silently.
	if _, err := Shell(ctx, "exit 3", dir, nil); err == nil {
		t.Error("a command that exited 3 was reported as successful")
	} else if !strings.Contains(err.Error(), "3") {
		t.Errorf("error = %v, want it to name the exit code", err)
	}
}

// The case my own mutation testing found, and the reason "fire on entering the state" is the wrong
// rule for a claim about code.
//
// An agent works, its tests pass, it works again, and its tests pass again. If the poller never
// happens to catch an intermediate state, an edge rule keyed on the event alone sees green followed
// by green and suppresses the second one. That silently skips the auto-commit for the second piece
// of work, which is the failure nobody would notice until they went looking for a commit that was
// never made.
func TestANewRevisionPassingFiresAgainEvenWithNoStateSeenInBetween(t *testing.T) {
	exec := &recorder{}
	var reports []Report
	r := runnerWith(t, exec.exec, &reports, Hook{On: Verified, Run: "git commit -am wip"})
	ctx := context.Background()

	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("abc"), Green: true})
	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("def"), Green: true})
	r.Wait()

	if got := exec.commands(); len(got) != 2 {
		t.Errorf("ran %d times for two revisions that each went green, want 2: %v", len(got), got)
	}
}

// The other side of that distinction. An agent event is about the agent, so a revision changing
// because somebody edited a file must not make an idle agent idle a second time.
func TestAnAgentEventDoesNotRefireBecauseTheCodeChanged(t *testing.T) {
	exec := &recorder{}
	var reports []Report
	r := runnerWith(t, exec.exec, &reports, Hook{On: Idle, Run: "notify"})
	ctx := context.Background()

	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("abc"), Agent: core.AgentIdle})
	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("def"), Agent: core.AgentIdle})
	r.Observe(ctx, Observation{Subject: "a1", Revision: rev("ghi"), Agent: core.AgentIdle})
	r.Wait()

	if got := exec.commands(); len(got) != 1 {
		t.Errorf("the idle hook ran %d times while the agent sat still: %v", len(got), got)
	}
}
