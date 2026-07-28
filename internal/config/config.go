package config

// The per project file, and why it is strict.
//
// This file tells Canopy which commands are evidence about a project. Getting that wrong is not a
// cosmetic failure: a mistyped field name that is quietly ignored means the test somebody believed
// was gating their work never ran, and every agent goes green on the strength of a suite nobody
// executed. So an unknown field is an error, a malformed duration is an error, and a path that
// leaves the worktree is an error. The cost is a config file that occasionally refuses to load. The
// alternative is one that lies.
//
// JSON rather than a friendlier format. TOML and YAML both want a dependency, and YAML in
// particular wants a parser with a history of surprising people about what a bare `no` means. The
// real cost is that JSON has no comments, which is a genuine loss for a file humans edit, and it is
// written down in LIMITATIONS.md rather than pretended away.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileName is what Canopy looks for in the root of a repository.
const FileName = "canopy.json"

// Test is one configured command that produces evidence.
type Test struct {
	Name    string `json:"name"`
	Command string `json:"command"`

	// Required decides whether this test can block a green roll-up. It defaults to false, which is
	// the safe direction: a test somebody forgot to mark required shows its result and cannot
	// silently become the thing a green tick rests on.
	Required bool `json:"required"`

	// Timeout is a duration string such as "15m". Empty means the runner's default.
	Timeout string `json:"timeout"`
}

// Project is the whole file.
type Project struct {
	// Base is the branch an agent's work is measured against. Empty means the repository's default.
	Base string `json:"base"`

	// Setup brings a fresh worktree to a runnable state.
	Setup        string `json:"setup"`
	SetupTimeout string `json:"setup_timeout"`

	// Copy lists paths that cannot be rebuilt and have to be carried into a new worktree, in
	// practice a dotenv. Every entry is confirmed with the user before anything is copied; listing
	// it here only makes it offerable.
	Copy []string `json:"copy"`

	Tests []Test `json:"tests"`

	// Instructions are prepended to every agent's system prompt in this project.
	Instructions string `json:"instructions"`

	// Commands are reusable prompts invoked as /name, and Hooks run something when a state
	// transition actually happens. Both are declared before the work that fills them in, so that
	// two pairs building A8-04 and A8-05 at the same time never add adjacent lines to this struct.
	// The types and their checks live in commands.go and hooks.go respectively.
	Commands []Command `json:"commands"`
	Hooks    []Hook    `json:"hooks"`

	// MCP lists Model Context Protocol servers to connect to. Their types and checks are in mcp.go.
	MCP []MCPServer `json:"mcp"`

	// Trust is the default trust level for agents here: read-only, confined, standard or broad.
	// Empty means Canopy's own default rather than the most permissive one.
	Trust string `json:"trust"`
}

// Load reads the configuration from a repository root.
//
// A missing file is not an error. Most projects will not have one, and refusing to start in a
// directory that has never heard of Canopy would be absurd. The second return says whether a file
// was actually found, so a caller can tell "no configuration" from "configuration that happens to
// be empty", which matter differently: the first explains why nothing is configured and the second
// is somebody having deleted their tests.
func Load(dir string) (Project, bool, error) {
	path := filepath.Join(dir, FileName)

	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, fmt.Errorf("reading %s: %w", FileName, err)
	}

	project, err := Parse(content)
	if err != nil {
		// The filename goes in the message because the error surfaces at startup, where the reader
		// may not yet know Canopy reads a file at all.
		return Project{}, true, fmt.Errorf("%s: %w", FileName, err)
	}
	return project, true, nil
}

// Parse reads and validates the configuration.
func Parse(content []byte) (Project, error) {
	var project Project

	decoder := json.NewDecoder(strings.NewReader(string(content)))
	// The whole strictness argument in one call. A field named "test" where "tests" was meant would
	// otherwise load cleanly and run nothing.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&project); err != nil {
		return Project{}, fmt.Errorf("this file could not be read: %w", err)
	}

	if err := project.Validate(); err != nil {
		return Project{}, err
	}
	return project, nil
}

// Validate checks everything that can be checked without running anything.
func (p Project) Validate() error {
	seen := make(map[string]bool, len(p.Tests))
	for i, test := range p.Tests {
		switch {
		case test.Name == "":
			return fmt.Errorf("the test at position %d has no name", i+1)
		case test.Command == "":
			return fmt.Errorf("the test %q has no command, so there is nothing for it to run", test.Name)
		case seen[test.Name]:
			// Two tests with one name means the second silently replaces the first everywhere results
			// are keyed by name, which is how a required test disappears without anybody noticing.
			return fmt.Errorf("two tests are called %q", test.Name)
		}
		seen[test.Name] = true

		if _, err := parseDuration(test.Timeout); err != nil {
			return fmt.Errorf("the timeout on the test %q: %w", test.Name, err)
		}
	}

	if _, err := parseDuration(p.SetupTimeout); err != nil {
		return fmt.Errorf("the setup timeout: %w", err)
	}

	for _, path := range p.Copy {
		if err := insideWorktree(path); err != nil {
			return fmt.Errorf("the copy list: %w", err)
		}
	}

	switch p.Trust {
	case "", "read-only", "confined", "standard", "broad":
	default:
		return fmt.Errorf("%q is not a trust level: use read-only, confined, standard or broad", p.Trust)
	}

	if err := p.validateCommands(); err != nil {
		return err
	}
	if err := p.validateHooks(); err != nil {
		return err
	}
	return p.validateMCP()
}

// insideWorktree refuses a path that would reach outside the project.
//
// The same rule the tools enforce, applied here as well rather than only there. A config file is
// committed and shared, so a path that escapes the worktree is not a mistake one person makes on
// their own machine, it is one that arrives with a clone.
func insideWorktree(path string) error {
	switch {
	case path == "":
		return errors.New("an empty path")
	case filepath.IsAbs(path):
		return fmt.Errorf("%q is an absolute path, and only paths inside the project can be copied", path)
	}

	clean := filepath.Clean(path)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%q reaches outside the project", path)
	}
	if clean == "." {
		return errors.New(`"." is the whole project, which is not something to copy into a worktree`)
	}
	return nil
}

// parseDuration accepts an empty string as "use the default".
func parseDuration(text string) (time.Duration, error) {
	if text == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration, write it like 90s or 15m", text)
	}
	if d < 0 {
		return 0, fmt.Errorf("%q is negative", text)
	}
	return d, nil
}

// TestTimeout returns a test's timeout as a duration, having already been validated.
func (t Test) TestTimeout() time.Duration {
	d, _ := parseDuration(t.Timeout)
	return d
}

// SetupDuration returns the setup timeout as a duration, having already been validated.
func (p Project) SetupDuration() time.Duration {
	d, _ := parseDuration(p.SetupTimeout)
	return d
}

// Expand substitutes the template values a command is allowed to use.
//
// Resolved before anything runs, and only these three. A general template language here would be a
// way to construct a command at execution time out of values Canopy does not control, and the whole
// point of the file being committed is that a reviewer can read it and know what it will do.
func Expand(command string, values map[string]string) string {
	for _, name := range []string{"worktree", "branch", "agent"} {
		command = strings.ReplaceAll(command, "{{"+name+"}}", values[name])
	}
	return command
}

// Placeholders returns any template name in a command that Expand does not know.
//
// Unknown placeholders are reported rather than left in place, because a command containing a
// literal {{port}} fails in a way that looks like the project being broken instead of the config
// being wrong.
func Placeholders(command string) []string {
	var unknown []string
	known := map[string]bool{"worktree": true, "branch": true, "agent": true}

	rest := command
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			return unknown
		}
		rest = rest[open+2:]
		close := strings.Index(rest, "}}")
		if close < 0 {
			return unknown
		}
		name := strings.TrimSpace(rest[:close])
		if !known[name] {
			unknown = append(unknown, name)
		}
		rest = rest[close+2:]
	}
}
