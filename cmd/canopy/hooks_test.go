package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/hooks"
)

// The whole point of a hook is that somebody has stopped watching, so a failure nobody is told about
// means they stopped watching something that stopped working.
func TestAFailingHookIsKeptToBeReported(t *testing.T) {
	v := &verification{}

	v.recordHook(hooks.Report{
		Subject: "main", Event: hooks.TestsPassed, Command: "git commit -am wip",
		Err: errors.New("it exited 1"),
	})
	v.recordHook(hooks.Report{Subject: "main", Event: hooks.Verified, Command: "notify"})

	failures := v.HookFailures()
	if len(failures) != 1 {
		t.Fatalf("%d failures kept, want the one that failed: %v", len(failures), failures)
	}
	if !strings.Contains(failures[0], "exited 1") {
		t.Errorf("the failure does not say what went wrong: %q", failures[0])
	}
	// A hook that worked is what somebody configured it to do, and a list of those is a log nobody
	// reads.
	if strings.Contains(strings.Join(failures, " "), "notify") {
		t.Errorf("a hook that worked was reported as a failure: %v", failures)
	}
}

// Nothing configured must not produce a runner, or every poll would walk the agent list to ask a
// runner with no hooks in it what to fire.
func TestNoHooksConfiguredObservesNothing(t *testing.T) {
	v := &verification{}
	// Guarded rather than assumed: observeHooks is called on every tick and the ordinary project
	// declares no hooks at all.
	v.observeHooks(t.Context(), nil)

	if got := len(v.HookFailures()); got != 0 {
		t.Errorf("%d failures from a project with no hooks", got)
	}
}

// The two vocabularies meet in one place, so a hook's timeout means the same thing as a test's.
func TestConfiguredHooksConvertToRunnableOnes(t *testing.T) {
	project := config.Project{Hooks: []config.Hook{
		{On: string(hooks.TestsPassed), Run: "git commit -am wip", Timeout: "30s"},
		{On: string(hooks.Blocked), Run: "say blocked"},
	}}

	runnable := project.Runnable()
	if len(runnable) != 2 {
		t.Fatalf("%d runnable hooks, want 2", len(runnable))
	}
	if runnable[0].On != hooks.TestsPassed || runnable[0].Run != "git commit -am wip" {
		t.Errorf("the first hook did not survive conversion: %+v", runnable[0])
	}
	if runnable[0].Timeout.Seconds() != 30 {
		t.Errorf("timeout = %v, want 30s", runnable[0].Timeout)
	}
	// Empty means the runner's default, which is not the same as no bound at all.
	if runnable[1].Timeout != 0 {
		t.Errorf("an unset timeout became %v rather than the runner's default", runnable[1].Timeout)
	}
}
