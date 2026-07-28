package config

// What a test actually runs, in the shape D-05 settled.
//
// The schema used to be a bare string that was handed to `/bin/sh -c`. That is the form D-05
// explicitly does not want as the default, and the reason is not tidiness. A shell always starts
// successfully. When the program named inside it does not exist, the shell exits 127 and Canopy sees
// a command that ran and failed, so a typo in the configuration is reported as failing tests. That is
// the false red counterpart of the false green this project exists to refuse, and there is no
// reliable way to recover the distinction afterwards: matching English on stderr is locale and shell
// dependent, and treating every 126 and 127 as an error misreads a suite that legitimately exits
// with one.
//
// An argument vector has no such ambiguity. The executable either exists or `Start` fails, and the
// two outcomes are different objects rather than the same integer.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// TestCommand is either an argument vector or a shell string that has been opted into.
type TestCommand struct {
	// Argv is the preferred form: the program and its arguments, with no shell in between.
	Argv []string `json:"argv,omitempty"`

	// Shell is a command line for `/bin/sh -c`, available for the cases that genuinely need one:
	// pipes, redirection, environment prefixes, shell builtins.
	Shell string `json:"shell,omitempty"`

	// AllowShell has to be set alongside Shell.
	//
	// A second field rather than inferring the opt-in from Shell being present, because the point is
	// that somebody decided. Writing a pipeline into a config file is easy to do without noticing
	// that it costs the ability to tell a missing program from a failing test, and a field that must
	// be typed out is where that gets noticed.
	AllowShell bool `json:"allow_shell,omitempty"`
}

// UnmarshalJSON accepts the object form and rejects the old bare string with an explanation.
//
// A bare string cannot decode into a struct at all, so without this the user gets Go's "cannot
// unmarshal string into Go value of type config.TestCommand", which says nothing about what to write
// instead. This is the one error in the file most people will hit exactly once, on upgrade.
func (c *TestCommand) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)

	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		return fmt.Errorf(
			"a test command is an object rather than a string. Write "+
				`"command": {"argv": %s} to run it directly, or `+
				`"command": {"shell": %q, "allow_shell": true} to keep the shell. `+
				"The argument form is preferred because a shell reports a missing program as a "+
				"failing test rather than as a broken configuration",
			argvSuggestion(text), text)
	}

	// An alias so the tag driven decoding still happens without recursing back into this method.
	type plain TestCommand
	var raw plain
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*c = TestCommand(raw)
	return nil
}

// argvSuggestion renders a plausible argv for the error message above.
//
// Splitting on whitespace is not a shell parser and is not trying to be. It produces the right answer
// for `go test ./...`, which is the overwhelming majority of what is in these files, and for anything
// with a pipe or a quote in it the user is going to want the shell form anyway.
func argvSuggestion(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return `["your", "command"]`
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return `["your", "command"]`
	}
	return string(encoded)
}

// Validate reports what is wrong with a command, if anything.
func (c TestCommand) Validate() error {
	hasArgv := len(c.Argv) > 0
	hasShell := strings.TrimSpace(c.Shell) != ""

	switch {
	case hasArgv && hasShell:
		// D-05 calls this a validation error rather than picking one, because either choice silently
		// ignores something the user wrote.
		return fmt.Errorf("it sets both argv and shell, so it is not clear which one should run")

	case !hasArgv && !hasShell:
		return fmt.Errorf(`it has no command: give it {"argv": ["go", "test", "./..."]}`)

	case hasShell && !c.AllowShell:
		return fmt.Errorf(
			"it uses a shell string, which has to be opted into with \"allow_shell\": true. " +
				"A shell reports a missing program as a failing test rather than as a broken " +
				"configuration, so the argument form is preferred where it will do")

	case hasArgv:
		for i, argument := range c.Argv {
			if strings.TrimSpace(argument) == "" && i == 0 {
				return fmt.Errorf("its first argument is empty, so there is no program to run")
			}
		}
	}
	return nil
}

// Display is the command as a person would read it, for the interface and the audit trail.
func (c TestCommand) Display() string {
	if len(c.Argv) > 0 {
		return strings.Join(c.Argv, " ")
	}
	return c.Shell
}
