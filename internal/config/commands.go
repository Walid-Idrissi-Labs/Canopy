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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// GlobalCommandsFile is the user-level command file under Canopy's config directory.
	GlobalCommandsFile = "commands.json"

	// GlobalCommandsEnv overrides the global command file. It exists mainly so tests and portable
	// installations do not have to alter the user's real config directory.
	GlobalCommandsEnv = "CANOPY_COMMANDS_FILE"

	// ArgumentsPlaceholder is the only substitution slash commands perform.
	//
	// One literal placeholder rather than a template language is a safety property: arguments never
	// become syntax, are never evaluated, and are never recursively expanded.
	ArgumentsPlaceholder = "$ARGUMENTS"
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

// CommandScope says where a resolved command came from.
type CommandScope string

const (
	CommandGlobal  CommandScope = "global"
	CommandProject CommandScope = "project"
)

// ResolvedCommand is a command together with the scope whose definition won.
type ResolvedCommand struct {
	Command
	Scope CommandScope
}

// CommandSet is the command catalog for one running project.
//
// It is built per run rather than kept in a process-global registry. That is what makes a project
// command impossible to leak into another project opened by the same binary.
type CommandSet struct {
	commands map[string]ResolvedCommand
}

// ResolveCommands combines user-level and project-level commands.
//
// Project definitions intentionally shadow global definitions. A repository can therefore make
// `/test` mean its actual test workflow without forcing the user to rename a broadly useful global
// command, and the listing still says which definition is active.
func ResolveCommands(global, project []Command) CommandSet {
	resolved := make(map[string]ResolvedCommand, len(global)+len(project))
	for _, command := range global {
		resolved[command.Name] = ResolvedCommand{Command: command, Scope: CommandGlobal}
	}
	for _, command := range project {
		resolved[command.Name] = ResolvedCommand{Command: command, Scope: CommandProject}
	}
	return CommandSet{commands: resolved}
}

// All returns the active definitions in invocation order.
func (s CommandSet) All() []ResolvedCommand {
	out := make([]ResolvedCommand, 0, len(s.commands))
	for _, command := range s.commands {
		out = append(out, command)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Expand turns a slash invocation into the prompt sent to the model.
//
// The boolean is false for ordinary text. Arguments are copied literally in one pass: `$ARGUMENTS`
// inside an argument remains text, and shell metacharacters have no special meaning because this
// output enters the chat prompt path, not a shell.
func (s CommandSet) Expand(input string) (prompt string, invocation bool, err error) {
	if !strings.HasPrefix(input, "/") || strings.HasPrefix(input, "//") {
		return input, false, nil
	}

	nameAndArgs := strings.TrimSpace(strings.TrimPrefix(input, "/"))
	name, arguments, _ := strings.Cut(nameAndArgs, " ")
	command, ok := s.commands[name]
	if !ok {
		return "", true, fmt.Errorf("unknown command /%s; type /commands to list the commands available here", name)
	}
	arguments = strings.TrimSpace(arguments)

	if strings.Contains(command.Prompt, ArgumentsPlaceholder) {
		return strings.ReplaceAll(command.Prompt, ArgumentsPlaceholder, arguments), true, nil
	}
	if arguments == "" {
		return command.Prompt, true, nil
	}
	return command.Prompt + "\n\nArguments:\n" + arguments, true, nil
}

// GlobalCommandsPath returns the user-level command file location.
func GlobalCommandsPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv(GlobalCommandsEnv)); override != "" {
		return override, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the user config directory: %w", err)
	}
	return filepath.Join(base, "canopy", GlobalCommandsFile), nil
}

// LoadGlobalCommands reads the optional user-level command file.
func LoadGlobalCommands() ([]Command, bool, error) {
	path, err := GlobalCommandsPath()
	if err != nil {
		return nil, false, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("reading global commands from %s: %w", path, err)
	}

	var file struct {
		Commands []Command `json:"commands"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, true, fmt.Errorf("%s: this file could not be read: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return nil, true, fmt.Errorf("%s: trailing content: %w", path, err)
	}
	if err := validateCommandList(file.Commands); err != nil {
		return nil, true, fmt.Errorf("%s: %w", path, err)
	}
	return file.Commands, true, nil
}

// validateCommands checks what can be checked without running anything.
//
// Structural only, and deliberately so. How arguments are substituted, and whether a project command
// may shadow a global one, are decisions A8-04 makes, and they are made here rather than in Validate
// so that only one line of the shared function ever had to change.
func (p Project) validateCommands() error {
	return validateCommandList(p.Commands)
}

func validateCommandList(commands []Command) error {
	seen := make(map[string]bool, len(commands))
	for i, command := range commands {
		switch {
		case command.Name == "":
			return fmt.Errorf("the command at position %d has no name", i+1)
		case strings.HasPrefix(command.Name, "/"):
			// Written with the slash is the obvious mistake, and it would otherwise register as a
			// command called "/deploy" that nobody can reach by typing /deploy.
			return fmt.Errorf("the command %q carries its own slash, write it as %q",
				command.Name, strings.TrimPrefix(command.Name, "/"))
		case !validCommandName(command.Name):
			return fmt.Errorf("the command name %q must use lowercase letters, numbers, hyphens or underscores",
				command.Name)
		case command.Name == "commands":
			return errors.New(`the command name "commands" is reserved for listing available commands`)
		case strings.TrimSpace(command.Description) == "":
			return fmt.Errorf("the command %q has no description, so a listing cannot explain it", command.Name)
		case strings.ContainsAny(command.Description, "\r\n"):
			return fmt.Errorf("the description of command %q must fit on one line", command.Name)
		case strings.TrimSpace(command.Prompt) == "":
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

func validCommandName(name string) bool {
	if len(name) > 48 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		case (r == '-' || r == '_') && i > 0:
		default:
			return false
		}
	}
	return name != ""
}
