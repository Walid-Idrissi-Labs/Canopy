package config

// Something to run when a state actually changes.
//
// Declared ahead of A8-05 for the same reason as commands.go: the field exists from the start, so
// no merge ever has to reconcile two pairs adding one to the same struct.

import (
	"fmt"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/hooks"
)

// Hook is a command bound to a transition.
type Hook struct {
	// On names the transition, from the vocabulary in internal/hooks. Checked here rather than at
	// startup, because a hook naming an event Canopy has never heard of has to be an error: one
	// that silently never fires cannot be told apart from one that fires and does nothing, and the
	// first is a typo while the second is a working configuration.
	On string `json:"on"`

	// Run is the command.
	Run string `json:"run"`

	// Timeout is a duration string such as "30s". Empty means the runner's default.
	Timeout string `json:"timeout"`
}

// HookTimeout is how long this hook may run, or zero for the runner's default.
//
// The same shape as a test's, so the two configuration fields behave the same way. Parse errors are
// caught by validation at load, so a bad value never reaches here; if one somehow did, zero is the
// safe answer because it means the default rather than no bound at all.
func (h Hook) HookTimeout() time.Duration {
	parsed, err := parseDuration(h.Timeout)
	if err != nil {
		return 0
	}
	return parsed
}

// Runnable converts the configured hooks into what the runner takes.
//
// Here rather than in the caller so the two vocabularies meet in one place: this package already
// depends on internal/hooks to validate the event names, and having the command that runs them do
// the translation would put a second copy of the mapping somewhere it could drift.
func (p Project) Runnable() []hooks.Hook {
	out := make([]hooks.Hook, 0, len(p.Hooks))
	for _, hook := range p.Hooks {
		out = append(out, hooks.Hook{
			On:      hooks.Event(hook.On),
			Run:     hook.Run,
			Timeout: hook.HookTimeout(),
		})
	}
	return out
}

// validateHooks checks what can be checked before the event vocabulary exists.
func (p Project) validateHooks() error {
	for i, hook := range p.Hooks {
		switch {
		case hook.On == "":
			return fmt.Errorf("the hook at position %d does not say what it runs on", i+1)
		case !hooks.ValidEvent(hook.On):
			// Listed rather than merely rejected. Somebody who wrote "tests-pass" needs to be told
			// what the word is, not that the word they used is wrong.
			return fmt.Errorf("%q is not something Canopy can run a hook on: use one of %s",
				hook.On, hooks.EventNames())
		case hook.Run == "":
			return fmt.Errorf("the hook on %q has no command, so there is nothing for it to run",
				hook.On)
		}

		if _, err := parseDuration(hook.Timeout); err != nil {
			return fmt.Errorf("the timeout on the %q hook: %w", hook.On, err)
		}
	}
	return nil
}
