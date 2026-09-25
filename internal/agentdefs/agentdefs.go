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
)

// Definition is one agent definition.
type Definition struct {
	Name        string
	Description string
	Model       string
	Body        string
	Project     bool
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
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
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
			if value != "inherit" {
				def.Model = value
			}
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
