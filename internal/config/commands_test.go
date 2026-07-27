package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectCommandsShadowGlobalCommandsWithoutChangingTheGlobalSet(t *testing.T) {
	global := []Command{{
		Name: "review", Description: "global review", Prompt: "review everything",
	}}
	project := []Command{{
		Name: "review", Description: "project review", Prompt: "run this project's review",
	}}

	first := ResolveCommands(global, project)
	prompt, invocation, err := first.Expand("/review")
	if err != nil || !invocation {
		t.Fatalf("Expand: invocation=%v err=%v", invocation, err)
	}
	if prompt != "run this project's review" {
		t.Errorf("project command did not win: %q", prompt)
	}
	if got := first.All()[0].Scope; got != CommandProject {
		t.Errorf("active command is labelled %q, want project", got)
	}

	// A second project gets a new catalog. The first project's override must not have mutated the
	// user-level definition or escaped the project it came from.
	second := ResolveCommands(global, nil)
	prompt, _, err = second.Expand("/review")
	if err != nil {
		t.Fatalf("second project: %v", err)
	}
	if prompt != "review everything" || second.All()[0].Scope != CommandGlobal {
		t.Errorf("the project command leaked into another project: prompt=%q commands=%+v",
			prompt, second.All())
	}
}

func TestArgumentsAreSubstitutedLiterallyOnce(t *testing.T) {
	set := ResolveCommands(nil, []Command{{
		Name: "fix", Description: "fix one thing", Prompt: "Fix this:\n" + ArgumentsPlaceholder,
	}})

	arguments := `$(touch /tmp/not-run) and $ARGUMENTS and "quotes"`
	prompt, invocation, err := set.Expand("/fix " + arguments)
	if err != nil || !invocation {
		t.Fatalf("Expand: invocation=%v err=%v", invocation, err)
	}
	if prompt != "Fix this:\n"+arguments {
		t.Errorf("arguments were interpreted or recursively expanded:\n%q", prompt)
	}
}

func TestArgumentsAreAppendedWhenThePromptHasNoPlaceholder(t *testing.T) {
	set := ResolveCommands(nil, []Command{{
		Name: "explain", Description: "explain a topic", Prompt: "Explain this carefully.",
	}})

	prompt, _, err := set.Expand("/explain the retry loop")
	if err != nil {
		t.Fatal(err)
	}
	if prompt != "Explain this carefully.\n\nArguments:\nthe retry loop" {
		t.Errorf("expanded to %q", prompt)
	}
}

func TestUnknownCommandsAreNamed(t *testing.T) {
	_, invocation, err := ResolveCommands(nil, nil).Expand("/typo")
	if !invocation || err == nil {
		t.Fatalf("unknown command: invocation=%v err=%v", invocation, err)
	}
	if !strings.Contains(err.Error(), "/typo") || !strings.Contains(err.Error(), "/commands") {
		t.Errorf("error does not explain the typo or recovery: %q", err)
	}
}

func TestGlobalCommandsLoadStrictlyFromTheOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.json")
	t.Setenv(GlobalCommandsEnv, path)
	content := `{"commands":[{"name":"review","description":"review this","prompt":"Review $ARGUMENTS"}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	commands, found, err := LoadGlobalCommands()
	if err != nil || !found {
		t.Fatalf("LoadGlobalCommands: found=%v err=%v", found, err)
	}
	if len(commands) != 1 || commands[0].Name != "review" {
		t.Errorf("loaded %+v", commands)
	}

	if err := os.WriteFile(path, []byte(`{"command":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadGlobalCommands(); err == nil || !strings.Contains(err.Error(), "command") {
		t.Errorf("unknown field was accepted or hidden: %v", err)
	}
}

func TestCommandDefinitionsThatWouldBeUnreachableAreRefused(t *testing.T) {
	cases := []struct {
		name string
		cmd  Command
		says string
	}{
		{"uppercase", Command{Name: "Review", Description: "review", Prompt: "do it"}, "lowercase"},
		{"reserved", Command{Name: "commands", Description: "list", Prompt: "do it"}, "reserved"},
		{"no description", Command{Name: "review", Prompt: "do it"}, "no description"},
		{"multiline description", Command{Name: "review", Description: "one\ntwo", Prompt: "do it"}, "one line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCommandList([]Command{tc.cmd})
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("error = %v, want %q", err, tc.says)
			}
		})
	}
}
