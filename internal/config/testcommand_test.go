package config

// The shape of a test command, which D-05 settled and the schema did not implement.

import (
	"strings"
	"testing"
)

// The default form. Nothing extra to write, and no shell between Canopy and the program.
func TestAnArgumentVectorNeedsNoOptIn(t *testing.T) {
	project, err := Parse([]byte(`{"tests":[{"name":"unit","command":{"argv":["go","test","./..."]}}]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	got := project.Tests[0].Command
	if len(got.Argv) != 3 || got.Argv[0] != "go" || got.Argv[2] != "./..." {
		t.Errorf("argv = %q, want the three arguments as written", got.Argv)
	}
	if got.Display() != `["go","test","./..."]` {
		t.Errorf("Display = %q", got.Display())
	}
}

func TestAnUnknownFieldInsideACommandIsRefusedAsUnknown(t *testing.T) {
	_, err := Parse([]byte(
		`{"tests":[{"name":"unit","command":{"argcv":["go","test"]}}]}`))
	if err == nil {
		t.Fatal("a misspelled command field was accepted")
	}
	if !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "argcv") {
		t.Errorf("the nested typo was not identified as an unknown field: %v", err)
	}
}

func TestAllowShellWithoutAShellCommandIsRefused(t *testing.T) {
	_, err := Parse([]byte(
		`{"tests":[{"name":"unit","command":{"argv":["go","test"],"allow_shell":true}}]}`))
	if err == nil {
		t.Fatal("allow_shell was accepted without a shell command")
	}
	if !strings.Contains(err.Error(), "without a shell command") {
		t.Errorf("the error does not explain the stale opt-in: %v", err)
	}
}

func TestArgumentDisplayPreservesBoundaries(t *testing.T) {
	one := TestCommand{Argv: []string{"printf", "a b"}}.Display()
	two := TestCommand{Argv: []string{"printf", "a", "b"}}.Display()
	if one == two {
		t.Fatalf("different argv collapsed to the same display %q", one)
	}
	if one != `["printf","a b"]` {
		t.Errorf("display = %q, want exact JSON argv", one)
	}
}

// The old shape, which is the one error most people will meet exactly once. Go's own message for it
// says nothing about what to write instead, so the whole point of this case is the sentence.
func TestABareStringSaysWhatToWriteInstead(t *testing.T) {
	_, err := Parse([]byte(`{"tests":[{"name":"unit","command":"go test ./..."}]}`))
	if err == nil {
		t.Fatal("a bare string was accepted, which is a shell command nobody opted into")
	}

	for _, want := range []string{`"argv"`, `["go","test","./..."]`, "allow_shell"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not contain %s, so it does not show the way out: %v", want, err)
		}
	}
}

// A shell is available and has to be asked for, which is the other half of D-05. The opt-in is a
// separate field so that writing a pipeline is not by itself the decision.
func TestAShellCommandIsAcceptedOnlyWhenItIsOptedInto(t *testing.T) {
	line := `{"tests":[{"name":"unit","command":{"shell":"go test ./... | tee out.log"%s}}]}`

	if _, err := Parse([]byte(strings.Replace(line, "%s", "", 1))); err == nil {
		t.Error("a shell command without allow_shell was accepted")
	} else if !strings.Contains(err.Error(), "allow_shell") {
		t.Errorf("the error does not name the field that would allow it: %v", err)
	}

	project, err := Parse([]byte(strings.Replace(line, "%s", `,"allow_shell":true`, 1)))
	if err != nil {
		t.Fatalf("an explicit shell command was refused: %v", err)
	}
	if project.Tests[0].Command.Shell == "" {
		t.Error("the shell string did not survive parsing")
	}
}

// D-05 calls this a validation error rather than choosing one, because either choice silently
// ignores something somebody wrote and meant.
func TestSettingBothFormsIsRefused(t *testing.T) {
	_, err := Parse([]byte(
		`{"tests":[{"name":"unit","command":{"argv":["go","test"],"shell":"go test","allow_shell":true}}]}`))
	if err == nil {
		t.Fatal("a command with both forms was accepted, so one of them was being ignored")
	}
	if !strings.Contains(err.Error(), "both") {
		t.Errorf("the error does not say the two forms conflict: %v", err)
	}
}

// An empty command is not a test that passes trivially, it is a configuration nobody finished.
func TestACommandWithNothingInItIsRefused(t *testing.T) {
	for _, input := range []string{
		`{"tests":[{"name":"unit","command":{}}]}`,
		`{"tests":[{"name":"unit"}]}`,
		`{"tests":[{"name":"unit","command":{"argv":[]}}]}`,
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("%s was accepted", input)
		} else if !strings.Contains(err.Error(), "no command") {
			t.Errorf("for %s the error is %v", input, err)
		}
	}
}
