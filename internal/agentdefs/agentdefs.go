// Package agentdefs loads agent definitions: markdown files whose frontmatter names an agent, says
// what it is for and optionally which model it runs on, and whose body is its standing instructions.
// A definition is dispatched by name, "use the reviewer agent on this", and the compatible layout
// means definitions written for Claude Code's .claude/agents work here too.
package agentdefs

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Definition is one agent definition.
type Definition struct {
	Name        string
	Description string
	Model       string
	Body        string
	Project     bool
	// Ceiling is the most an agent started from this definition may do, from a Claude Code tools
	// list: a reviewer given only Read and Grep does not run with an editor and a shell. Empty when
	// the definition names no tools.
	Ceiling core.TrustLevel
}

// Set is the definitions available, by name.
type Set struct {
	byName map[string]Definition
	order  []string
}

const maxBody = 32 * 1024

// Load gathers definitions from the user's folders and, when includeProject is set, the project's.
func Load(dir string, includeProject bool) *Set {
	set := &Set{byName: map[string]Definition{}}
	type root struct {
		path    string
		project bool
	}
	var roots []root
	if config, err := os.UserConfigDir(); err == nil {
		roots = append(roots, root{filepath.Join(config, "canopy", "agents"), false})
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, root{filepath.Join(home, ".claude", "agents"), false})
	}
	if includeProject && dir != "" {
		roots = append(roots, root{filepath.Join(dir, ".claude", "agents"), true},
			root{filepath.Join(dir, ".canopy", "agents"), true})
	}
	for _, r := range roots {
		matches, _ := filepath.Glob(filepath.Join(r.path, "*.md"))
		sort.Strings(matches)
		for _, path := range matches {
			def, err := parse(path)
			if err != nil || def.Name == "" {
				continue
			}
			def.Project = r.project
			if _, seen := set.byName[def.Name]; !seen {
				set.order = append(set.order, def.Name)
			}
			set.byName[def.Name] = def
		}
	}
	sort.Strings(set.order)
	return set
}

func parse(path string) (Definition, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBody {
		return Definition{}, fmt.Errorf("%s is not a readable definition", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return Definition{}, err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	var def Definition
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Definition{}, fmt.Errorf("%s has no frontmatter", path)
	}
	var lines []string
	for scanner.Scan() && strings.TrimSpace(scanner.Text()) != "---" {
		lines = append(lines, scanner.Text())
	}
	for i := 0; i < len(lines); i++ {
		key, value, ok := strings.Cut(lines[i], ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			def.Name = value
		case "description":
			def.Description = value
		case "model":
			// Claude Code's family names, and inherit, mean the model already in use here: the
			// family alone is ambiguous between generations and would fail the dispatch.
			switch strings.ToLower(value) {
			case "", "inherit", "sonnet", "opus", "haiku", "fable":
			default:
				def.Model = value
			}
		case "tools":
			tools := strings.Split(strings.Trim(value, "[]"), ",")
			// Or a block list on the lines that follow.
			for value == "" && i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "- ") {
				i++
				tools = append(tools, strings.TrimSpace(lines[i])[2:])
			}
			def.Ceiling = ceiling(tools)
		}
	}
	var body strings.Builder
	for scanner.Scan() {
		body.WriteString(scanner.Text())
		body.WriteString("\n")
	}
	def.Body = strings.TrimSpace(body.String())
	return def, scanner.Err()
}

// ceiling maps a Claude Code tools list to the trust that covers it and no more: no shell and no
// editing tool is read-only, editing without a shell is confined, and a shell is standard.
func ceiling(tools []string) core.TrustLevel {
	var named, writes, shell bool
	for _, tool := range tools {
		switch name := strings.TrimSpace(strings.Trim(strings.TrimSpace(tool), `"'`)); name {
		case "":
			continue
		case "Write", "Edit", "MultiEdit", "NotebookEdit":
			named, writes = true, true
		case "Bash":
			named, shell = true, true
		default:
			named = true
		}
	}
	switch {
	case !named:
		return ""
	case shell:
		return core.TrustStandard
	case writes:
		return core.TrustConfined
	}
	return core.TrustReadOnly
}

// Get returns a definition by name.
func (s *Set) Get(name string) (Definition, bool) {
	if s == nil {
		return Definition{}, false
	}
	d, ok := s.byName[strings.TrimSpace(name)]
	return d, ok
}

// Listing tells the orchestrating model which definitions it can dispatch by name.
func (s *Set) Listing() string {
	if s == nil || len(s.order) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Agent definitions you can start by name with spawn_agents' agent argument:\n")
	for _, name := range s.order {
		fmt.Fprintf(&b, "- %s: %s\n", name, s.byName[name].Description)
	}
	return strings.TrimRight(b.String(), "\n")
}
