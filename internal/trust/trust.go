// Package trust decides whether a repository's own configuration may make Canopy run anything.
//
// canopy.json can name a setup command, test commands, hooks and MCP servers, and can carry
// instructions that are sent to the model. All of those are code or text from whoever wrote the
// repository. Opening a freshly cloned repository used to start its MCP servers and fire its hooks
// before anybody had read them. Now nothing a repository defines runs, and nothing it says reaches
// the model, until the person using Canopy has seen exactly what it asks for and said yes; and any
// change to those parts asks again.
//
// Vendor project settings (.claude/settings.json and friends) are listed as well, because a
// delegated agent started in this directory reads them, but they are recorded separately: trusting
// canopy.json is not trusting them.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/gitsafe"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
)

// Request is everything a repository asks Canopy to run or to tell the model.
type Request struct {
	Dir          string
	Setup        string
	Tests        []string
	Hooks        []string
	MCP          []string
	Instructions bool
	// VendorFiles are vendor agent settings found in the repository, by relative path.
	VendorFiles []string
	// InstructionFiles are AGENTS.md and the like, which are sent to the model.
	InstructionFiles []string

	fingerprint string
}

// Empty reports whether the repository asks for nothing that needs trust.
func (r Request) Empty() bool {
	return r.Setup == "" && len(r.Tests) == 0 && len(r.Hooks) == 0 && len(r.MCP) == 0 &&
		!r.Instructions && len(r.VendorFiles) == 0
}

// Fingerprint identifies exactly what is being trusted.
func (r Request) Fingerprint() string { return r.fingerprint }

// vendorSettings are files another vendor's agent reads from the directory it is started in.
var vendorSettings = []string{
	".claude/settings.json", ".claude/settings.local.json", ".mcp.json",
	".codex/config.toml", ".codex/hooks.json", ".gemini/settings.json",
}

// Describe builds the request for a directory and its loaded configuration.
func Describe(dir string, project config.Project) Request {
	return DescribeWith(dir, project, nil)
}

// DescribeWith is Describe with the named instruction files read as the contents given rather than
// from disk: what the request would be after a change that has not been made yet. A file not on disk
// is not added.
func DescribeWith(dir string, project config.Project, contents map[string][]byte) Request {
	req := Request{Dir: dir, Setup: project.Setup,
		Instructions: strings.TrimSpace(project.Instructions) != "" || len(project.Commands) > 0}
	for _, t := range project.Tests {
		req.Tests = append(req.Tests, t.Name+": "+describeCommand(t.Command))
	}
	for _, h := range project.Hooks {
		on := h.On
		if len(h.Tools) > 0 {
			on += " (" + strings.Join(h.Tools, ", ") + ")"
		}
		req.Hooks = append(req.Hooks, "on "+on+": "+h.Run)
	}
	for _, m := range project.MCP {
		if m.URL != "" {
			line := m.Name + ": " + m.URL
			// What it sends as well as where: a header built from an environment variable carries that
			// variable's value to this url, and the person approving must see which.
			if vars := m.HeaderVariables(); len(vars) > 0 {
				line += ", sending $" + strings.Join(vars, ", $")
			}
			req.MCP = append(req.MCP, line)
			continue
		}
		line := m.Name + ": " + strings.Join(append([]string{m.Command}, m.Args...), " ")
		if m.Unconfined {
			line += " (outside the sandbox)"
		}
		req.MCP = append(req.MCP, line)
	}

	h := sha256.New()
	canonical, _ := json.Marshal(struct {
		Setup        string
		Tests        []config.Test
		Hooks        []config.Hook
		MCP          []config.MCPServer
		Instructions string
		Copy         []string
		Commands     []config.Command
	}{project.Setup, project.Tests, project.Hooks, project.MCP, project.Instructions, project.Copy,
		project.Commands})
	h.Write(canonical)
	for _, rel := range config.InstructionFiles(dir) {
		data, err := os.ReadFile(filepath.Join(dir, rel)) // regular files only, checked by InstructionFiles
		if given, ok := contents[rel]; ok {
			data, err = given, nil
		}
		if err != nil {
			continue
		}
		req.Instructions = true
		req.InstructionFiles = append(req.InstructionFiles, rel)
		_, _ = fmt.Fprintf(h, "\x00%s\x00", rel)
		h.Write(data)
	}
	// Project skills are instructions too, loaded into the model when a task matches them. Every
	// file in a skill's folder counts, not only SKILL.md: the skill tool serves the others and
	// SKILL.md tells the model to follow them.
	for _, pattern := range []string{".claude/skills/*/SKILL.md", ".agents/skills/*/SKILL.md", ".canopy/skills/*/SKILL.md"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		sort.Strings(matches)
		for _, path := range matches {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			rel, _ := filepath.Rel(dir, path)
			req.Instructions = true
			req.InstructionFiles = append(req.InstructionFiles, filepath.ToSlash(rel))
			var files []string
			_ = filepath.WalkDir(filepath.Dir(path), func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && len(files) < 200 {
					files = append(files, p)
				}
				return nil
			})
			sort.Strings(files)
			for _, f := range files {
				data, err := os.ReadFile(f)
				if err != nil {
					continue
				}
				fr, _ := filepath.Rel(dir, f)
				_, _ = fmt.Fprintf(h, "\x00%s\x00", fr)
				h.Write(data)
			}
		}
	}
	// Agent definitions are standing instructions for the agents started from them.
	for _, pattern := range []string{".claude/agents/*.md", ".canopy/agents/*.md"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		sort.Strings(matches)
		for _, path := range matches {
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			rel, _ := filepath.Rel(dir, path)
			req.Instructions = true
			req.InstructionFiles = append(req.InstructionFiles, filepath.ToSlash(rel))
			_, _ = fmt.Fprintf(h, "\x00%s\x00", rel)
			h.Write(data)
		}
	}
	for _, rel := range vendorSettings {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		req.VendorFiles = append(req.VendorFiles, rel)
		_, _ = fmt.Fprintf(h, "\x00%s\x00", rel)
		h.Write(data)
	}
	req.fingerprint = hex.EncodeToString(h.Sum(nil))
	return req
}

// Text is the request as a person reads it before deciding.
func (r Request) Text() string {
	r = r.printable()
	var b strings.Builder
	fmt.Fprintf(&b, "This repository's configuration asks Canopy to run or send:\n")
	if r.Setup != "" {
		fmt.Fprintf(&b, "  setup command (runs in each new agent worktree): %s\n", r.Setup)
	}
	for _, t := range r.Tests {
		fmt.Fprintf(&b, "  test command: %s\n", t)
	}
	for _, h := range r.Hooks {
		fmt.Fprintf(&b, "  hook: %s\n", h)
	}
	for _, m := range r.MCP {
		fmt.Fprintf(&b, "  MCP server (started when Canopy opens): %s\n", m)
	}
	if r.Instructions {
		fmt.Fprintf(&b, "  instructions or prompt commands that are sent to the model\n")
	}
	for _, f := range r.InstructionFiles {
		fmt.Fprintf(&b, "  instruction file sent to the model with every request: %s\n", f)
	}
	if len(r.VendorFiles) > 0 {
		fmt.Fprintf(&b, "Vendor agent settings are also present and are read by a delegated agent started here:\n")
		for _, f := range r.VendorFiles {
			fmt.Fprintf(&b, "  %s\n", f)
		}
	}
	return b.String()
}

type record struct {
	Fingerprint string    `json:"fingerprint"`
	At          time.Time `json:"at"`
}

// Store is the set of repositories the user has trusted, kept in the user's config directory.
type Store struct{ path string }

// Open opens the default store.
func Open() (*Store, error) {
	if override := os.Getenv("CANOPY_TRUST_FILE"); override != "" {
		return &Store{path: override}, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("finding the config directory: %w", err)
	}
	return &Store{path: filepath.Join(base, "canopy", "trust.json")}, nil
}

// At opens a store at an explicit path.
func At(path string) *Store { return &Store{path: path} }

func (s *Store) read() (map[string]record, error) {
	records := map[string]record{}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path, err)
	}
	return records, nil
}

// Trusted reports whether exactly this request was trusted for its directory.
func (s *Store) Trusted(req Request) bool {
	records, err := s.read()
	if err != nil {
		return false
	}
	rec, ok := records[key(req.Dir)]
	return ok && rec.Fingerprint == req.fingerprint
}

// Changed reports whether the directory was trusted before for a different request.
func (s *Store) Changed(req Request) bool {
	records, err := s.read()
	if err != nil {
		return false
	}
	rec, ok := records[key(req.Dir)]
	return ok && rec.Fingerprint != req.fingerprint
}

// Grant records trust in exactly this request.
func (s *Store) Grant(req Request) error {
	return s.write(req.Dir, &record{req.fingerprint, time.Now().UTC()})
}

// Revoke forgets a directory.
func (s *Store) Revoke(dir string) error { return s.write(dir, nil) }

// List returns trusted directories, sorted.
func (s *Store) List() ([]string, error) {
	records, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(records))
	for dir := range records {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) write(dir string, rec *record) error {
	records, err := s.read()
	if err != nil {
		return err
	}
	if rec == nil {
		delete(records, key(dir))
	} else {
		records[key(dir)] = *rec
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func key(dir string) string {
	// A repository and every worktree cut from it share one answer: an agent's worktree carries the
	// same committed configuration, and asking again for each would make delegation unusable there.
	//
	// The path inside the repository is kept, so two configurations in different subdirectories of
	// one repository are separate answers rather than overwriting each other.
	if root := repositoryRoot(dir); root != "" {
		if top := toplevel(dir); top != "" {
			if rel, err := filepath.Rel(canonical(top), canonical(dir)); err == nil {
				return canonical(root) + "#" + filepath.ToSlash(rel)
			}
		}
		return canonical(root)
	}
	return canonical(dir)
}

// canonical is an absolute, symlink-free spelling of a path, when one can be had.
func canonical(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// Withhold returns the project with everything that needs trust removed.
func Withhold(project config.Project) config.Project {
	project.Setup = ""
	project.Tests = nil
	project.Hooks = nil
	project.MCP = nil
	project.Instructions = ""
	project.Copy = nil
	project.Commands = nil
	return project
}

func describeCommand(c config.TestCommand) string {
	if c.Shell != "" {
		return "sh -c " + c.Shell
	}
	return strings.Join(c.Argv, " ")
}

// ErrVendorSettingsUntrusted refuses a delegated agent in a directory whose vendor settings nobody
// has agreed to.
var ErrVendorSettingsUntrusted = errors.New("this repository has vendor agent settings that have not been trusted")

// DelegationAllowed reports whether a vendor agent may be started in dir. A delegated agent applies
// its own project settings from the directory it starts in, and those can include hooks the
// repository committed, which run without passing through Canopy at all. So a directory with such
// settings must have been trusted first; a directory without them needs nothing.
func DelegationAllowed(dir string) error {
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	project, _, err := config.Load(dir)
	if err != nil {
		project = config.Project{}
	}
	req := Describe(dir, project)
	if len(req.VendorFiles) == 0 {
		return nil
	}
	store, err := Open()
	if err == nil && store.Trusted(req) {
		return nil
	}
	return fmt.Errorf("%w (%s); review it with `canopy trust` in %s",
		ErrVendorSettingsUntrusted, strings.Join(req.VendorFiles, ", "), dir)
}

// printable strips control characters from everything the prompt shows, so a hook written with a
// carriage return or an erase-line sequence cannot hide the real command behind a harmless one.
func (r Request) printable() Request {
	clean := func(s string) string {
		return strings.Map(func(c rune) rune {
			if c < 0x20 || c == 0x7f || (c >= 0x80 && c <= 0x9f) {
				return '?'
			}
			return c
		}, s)
	}
	out := r
	out.Setup = clean(r.Setup)
	out.Tests, out.Hooks, out.MCP, out.VendorFiles, out.InstructionFiles = nil, nil, nil, nil, nil
	for _, x := range r.InstructionFiles {
		out.InstructionFiles = append(out.InstructionFiles, clean(x))
	}
	for _, x := range r.Tests {
		out.Tests = append(out.Tests, clean(x))
	}
	for _, x := range r.Hooks {
		out.Hooks = append(out.Hooks, clean(x))
	}
	for _, x := range r.MCP {
		out.MCP = append(out.MCP, clean(x))
	}
	for _, x := range r.VendorFiles {
		out.VendorFiles = append(out.VendorFiles, clean(x))
	}
	return out
}

// repositoryRoot is the main working tree of the repository dir belongs to, or "" outside one.
func repositoryRoot(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = dir
	cmd.Env = gitsafe.Inherited()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(out))
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

// toplevel is the root of the working tree dir is in, or "" outside one.
func toplevel(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	cmd.Env = gitsafe.Inherited()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
