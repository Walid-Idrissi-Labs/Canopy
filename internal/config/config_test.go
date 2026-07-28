package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAWholeFileLoads(t *testing.T) {
	project, err := Parse([]byte(`{
	  "base": "main",
	  "setup": "go mod download",
	  "setup_timeout": "5m",
	  "copy": [".env", "config/local.json"],
	  "tests": [
	    {"name": "unit", "command": {"argv": ["go", "test", "./..."]}, "required": true, "timeout": "15m"},
	    {"name": "lint", "command": {"shell": "golangci-lint run ./... | tee lint.log", "allow_shell": true}}
	  ],
	  "instructions": "match the surrounding code",
	  "trust": "confined"
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(project.Tests) != 2 {
		t.Fatalf("%d tests, want 2", len(project.Tests))
	}
	if !project.Tests[0].Required {
		t.Error("the required flag did not survive")
	}
	if project.Tests[1].Required {
		t.Error("a test with no required flag came back required, which would let it block a green " +
			"nobody asked it to gate")
	}
	if project.Tests[0].TestTimeout() != 15*time.Minute {
		t.Errorf("the timeout parsed as %s", project.Tests[0].TestTimeout())
	}
	if project.SetupDuration() != 5*time.Minute {
		t.Errorf("the setup timeout parsed as %s", project.SetupDuration())
	}
}

// The acceptance criterion, and the reason this file is strict at all. A misspelled field that
// loads cleanly means the test somebody believed was gating their work never ran.
func TestAnUnknownFieldIsRefused(t *testing.T) {
	_, err := Parse([]byte(`{"test": [{"name": "unit", "command": {"argv": ["go", "test"]}}]}`))
	if err == nil {
		t.Fatal("a file with the field misspelled loaded without complaint")
	}
	if !strings.Contains(err.Error(), "test") {
		t.Errorf("the error is %q and does not name the field", err)
	}
}

func TestTheThingsThatMakeAConfigUseless(t *testing.T) {
	cases := []struct {
		name  string
		input string
		says  string
	}{
		{"a test with no command", `{"tests":[{"name":"unit"}]}`, "no command"},
		{"a test with no name", `{"tests":[{"command":{"argv":["go","test"]}}]}`, "no name"},
		{
			"two tests with one name",
			`{"tests":[{"name":"unit","command":{"argv":["a"]}},{"name":"unit","command":{"argv":["b"]}}]}`,
			"two tests",
		},
		{"a timeout that is not one", `{"tests":[{"name":"u","command":{"argv":["a"]},"timeout":"15 minutes"}]}`, "not a duration"},
		{"a trust level that does not exist", `{"trust":"total"}`, "not a trust level"},
		{"an absolute path in the copy list", `{"copy":["/etc/passwd"]}`, "absolute path"},
		{"a copy path that escapes", `{"copy":["../../.ssh/id_rsa"]}`, "outside the project"},
		{"the whole project as a copy path", `{"copy":["."]}`, "whole project"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse([]byte(c.input))
			if err == nil {
				t.Fatalf("%s was accepted", c.name)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("the error is %q, want something containing %q", err, c.says)
			}
		})
	}
}

// A project with no file is the common case and must not be an error, but "no file" and "empty
// file" have to stay distinguishable: one explains why nothing is configured, the other is
// somebody having deleted their tests.
func TestNoFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()

	project, found, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on a directory with no config: %v", err)
	}
	if found {
		t.Error("Load reported finding a file that is not there")
	}
	if len(project.Tests) != 0 {
		t.Error("a project with no config came back with tests")
	}

	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, found, err = Load(dir); err != nil || !found {
		t.Errorf("an empty config: found=%v err=%v", found, err)
	}
}

func TestABrokenFileNamesItself(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(`{"tests": [`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, found, err := Load(dir)
	if err == nil {
		t.Fatal("a truncated file loaded")
	}
	if !found {
		t.Error("a file that exists and is broken was reported as absent, which reads as there being " +
			"no configuration rather than a broken one")
	}
	if !strings.Contains(err.Error(), FileName) {
		t.Errorf("the error is %q and never says which file it is about", err)
	}
}

func TestTemplatesResolveBeforeAnythingRuns(t *testing.T) {
	command := Expand("cd {{worktree}} && git log {{branch}} --oneline", map[string]string{
		"worktree": "/tmp/canopy-one",
		"branch":   "canopy/one",
		"agent":    "one",
	})

	if strings.Contains(command, "{{") {
		t.Errorf("a placeholder survived expansion: %q", command)
	}
	if !strings.Contains(command, "/tmp/canopy-one") || !strings.Contains(command, "canopy/one") {
		t.Errorf("the command expanded to %q", command)
	}
}

func TestAnUnknownPlaceholderIsReportedRatherThanLeftInPlace(t *testing.T) {
	unknown := Placeholders("PORT={{port}} npm test -- {{worktree}}")
	if len(unknown) != 1 || unknown[0] != "port" {
		t.Errorf("the unknown placeholders are %v, want just port: a literal {{port}} reaching the "+
			"shell fails in a way that looks like the project being broken", unknown)
	}
	if got := Placeholders("go test ./..."); len(got) != 0 {
		t.Errorf("a command with no templates reported %v", got)
	}
}
