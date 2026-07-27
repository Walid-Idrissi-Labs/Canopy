package config

// Something to run when a state actually changes.
//
// Declared ahead of A8-05 for the same reason as commands.go: the field exists from the start, so
// no merge ever has to reconcile two pairs adding one to the same struct.

import "fmt"

// Hook is a command bound to a transition.
type Hook struct {
	// On names the transition. The vocabulary is A8-05's to define, and checking a name against it
	// belongs there too: a hook naming an event Canopy has never heard of has to be an error,
	// because one that silently never fires cannot be told apart from one that fires and does
	// nothing, and the first is a typo while the second is a working configuration.
	On string `json:"on"`

	// Run is the command.
	Run string `json:"run"`

	// Timeout is a duration string such as "30s". Empty means the runner's default.
	Timeout string `json:"timeout"`
}

// validateHooks checks what can be checked before the event vocabulary exists.
func (p Project) validateHooks() error {
	for i, hook := range p.Hooks {
		switch {
		case hook.On == "":
			return fmt.Errorf("the hook at position %d does not say what it runs on", i+1)
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
