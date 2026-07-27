package config

// Reusable prompts, invoked as /name.
//
// The type and the field on Project are declared ahead of A8-04 so the two pairs never both add a
// line to the same struct. That is not a hypothetical: the last merge stopped compiling because two
// changes landed on adjacent lines of one function and the resolution kept both bodies. A field that
// already exists cannot be added twice.
//
// Everything past the shape belongs to A8-04 and belongs in this file.

import (
	"fmt"
	"strings"
)

// Command is a reusable prompt the user invokes as /name.
type Command struct {
	// Name is what follows the slash, written without one.
	Name string `json:"name"`

	// Description is the single line shown when the commands are listed.
	Description string `json:"description"`

	// Prompt is what gets sent, with the invocation's arguments substituted in.
	Prompt string `json:"prompt"`
}

// validateCommands checks what can be checked without running anything.
//
// Structural only, and deliberately so. How arguments are substituted, and whether a project command
// may shadow a global one, are decisions A8-04 makes, and they are made here rather than in Validate
// so that only one line of the shared function ever had to change.
func (p Project) validateCommands() error {
	seen := make(map[string]bool, len(p.Commands))
	for i, command := range p.Commands {
		switch {
		case command.Name == "":
			return fmt.Errorf("the command at position %d has no name", i+1)
		case strings.HasPrefix(command.Name, "/"):
			// Written with the slash is the obvious mistake, and it would otherwise register as a
			// command called "/deploy" that nobody can reach by typing /deploy.
			return fmt.Errorf("the command %q carries its own slash, write it as %q",
				command.Name, strings.TrimPrefix(command.Name, "/"))
		case command.Prompt == "":
			return fmt.Errorf("the command %q has no prompt, so there is nothing for it to send",
				command.Name)
		case seen[command.Name]:
			// Same argument as two tests sharing a name. The second silently replaces the first
			// everywhere they are keyed by name, and nobody finds out which one they invoked.
			return fmt.Errorf("two commands are called %q", command.Name)
		}
		seen[command.Name] = true
	}
	return nil
}
