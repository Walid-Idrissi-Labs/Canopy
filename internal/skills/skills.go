// Package skills loads Agent Skills: folders holding a SKILL.md whose frontmatter names the skill
// and says when to use it, and whose body is the instructions.
//
// Skills are progressive disclosure. Only each skill's name and one-line description go into the
// system prompt, which is cached; the body is read by the model through the skill tool when a task
// calls for it. A hundred skills cost a hundred lines, not a hundred documents.
//
// The folder layout is the open Agent Skills format, so skills written for other tools work here:
// Canopy reads ~/.canopy-compatible locations and the .claude/skills and .agents/skills folders.
package skills

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is one loaded skill.
type Skill struct {
	Name        string
	Description string
	// Dir is the skill's folder; Path its SKILL.md.
	Dir  string
	Path string
	// real is Dir with every symlink resolved, fixed at load: a file read later must still be under
	// it, so swapping the whole folder for a link after loading leads nowhere.
	real string
	// Project reports whether the skill came from the repository rather than the user's own folders.
	Project bool
}

// Set is the skills available in one place, by name.
type Set struct {
	byName map[string]Skill
	order  []string
}

const (
	maxSkills      = 100
	maxDescription = 300
	maxBodyBytes   = 64 * 1024
)

// Load gathers skills from the user's folders and, when includeProject is set, from the project at
// dir. A project skill of the same name as a user skill wins inside that project.
func Load(dir string, includeProject bool) *Set {
	set := &Set{byName: map[string]Skill{}}
	var roots []struct {
		path    string
		project bool
	}
	add := func(path string, project bool) {
		roots = append(roots, struct {
			path    string
			project bool
		}{path, project})
	}
	if config, err := os.UserConfigDir(); err == nil {
		add(filepath.Join(config, "canopy", "skills"), false)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".claude", "skills"), false)
		add(filepath.Join(home, ".agents", "skills"), false)
	}
	if includeProject && dir != "" {
		add(filepath.Join(dir, ".claude", "skills"), true)
		add(filepath.Join(dir, ".agents", "skills"), true)
		add(filepath.Join(dir, ".canopy", "skills"), true)
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			skillDir := filepath.Join(root.path, e.Name())
			path := filepath.Join(skillDir, "SKILL.md")
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			name, desc, err := frontmatter(path)
			if err != nil || name == "" {
				continue
			}
			if _, seen := set.byName[name]; !seen {
				set.order = append(set.order, name)
			}
			real, err := filepath.EvalSymlinks(skillDir)
			if err != nil {
				continue
			}
			set.byName[name] = Skill{Name: name, Description: desc, Dir: skillDir, Path: path, Project: root.project, real: real}
		}
	}
	sort.Strings(set.order)
	if len(set.order) > maxSkills {
		for _, n := range set.order[maxSkills:] {
			delete(set.byName, n)
		}
		set.order = set.order[:maxSkills]
	}
	return set
}

// frontmatter reads the name and description from a SKILL.md's leading --- block.
func frontmatter(path string) (string, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", "", errors.New("no frontmatter")
	}
	var name, desc string
	folding := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		// A folded or literal block (description: > or |) continues on indented lines.
		if folding && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			desc = strings.TrimSpace(desc + " " + strings.TrimSpace(line))
			continue
		}
		folding = false
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			if strings.HasPrefix(value, ">") || strings.HasPrefix(value, "|") {
				folding, value = true, ""
			}
			desc = value
		}
	}
	if len(desc) > maxDescription {
		desc = desc[:maxDescription] + "..."
	}
	return name, desc, scanner.Err()
}

// Empty reports whether there are no skills.
func (s *Set) Empty() bool { return s == nil || len(s.order) == 0 }

// Get returns a skill by name.
func (s *Set) Get(name string) (Skill, bool) {
	if s == nil {
		return Skill{}, false
	}
	sk, ok := s.byName[name]
	return sk, ok
}

// Listing is the part of the system prompt that tells the model which skills exist.
func (s *Set) Listing() string {
	if s.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Skills are instructions for particular kinds of task. When a task matches one, load it " +
		"with the skill tool before starting and follow it. Available skills:\n")
	for _, name := range s.order {
		fmt.Fprintf(&b, "- %s: %s\n", name, s.byName[name].Description)
	}
	return strings.TrimRight(b.String(), "\n")
}

// Body returns a skill's instructions, or one of its other files by path relative to its folder.
func (s *Set) Body(name, file string) (string, error) {
	sk, ok := s.Get(name)
	if !ok {
		return "", fmt.Errorf("there is no skill called %q", name)
	}
	// Checked at the moment of reading, for SKILL.md as much as for any other file: something may
	// have replaced a file with a symlink since the skill was loaded, and this runs in Canopy's own
	// process, outside any sandbox.
	target := sk.Path
	if file != "" {
		clean := filepath.Clean(file)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return "", errors.New("a skill file is named relative to the skill's own folder")
		}
		target = filepath.Join(sk.Dir, clean)
	}
	if info, err := os.Lstat(target); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file in the skill's folder", filepath.Base(target))
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(resolved, sk.real+string(filepath.Separator)) {
		return "", errors.New("that file is outside the skill's folder")
	}
	target = resolved
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if info.Size() > maxBodyBytes {
		return "", fmt.Errorf("%s is larger than %d bytes", filepath.Base(target), maxBodyBytes)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	body := string(data)
	if file == "" {
		var others []string
		_ = filepath.WalkDir(sk.Dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && p != sk.Path && len(others) < 50 {
				rel, _ := filepath.Rel(sk.Dir, p)
				others = append(others, rel)
			}
			return nil
		})
		if len(others) > 0 {
			body += "\n\nOther files in this skill, readable with the skill tool's file argument: " +
				strings.Join(others, ", ")
		}
	}
	return body, nil
}
