package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The file tools, and the one idea that makes them safe to hand to a model.
//
// An edit is computed against content the model read at some earlier point. If the file has moved
// since, applying that edit is guesswork: the line the model meant to change may be somewhere else,
// or gone, or now belong to something entirely different. This is the freshness idea from the
// verification engine applied to a file rather than a test run, and it exists for the same reason.
// Applying an edit against content that has moved is how an agent silently destroys work, including
// another agent's.

// maxFileBytes is the largest file the tools will read or write in one call.
//
// Bounded because a model asked to read a directory will eventually be pointed at a database dump,
// and a hundred megabytes of it arriving as a tool result costs real money and destroys the context
// window in one call.
const maxFileBytes = 1 << 20 // 1 MiB

// readLedger remembers what each file looked like when it was last read.
//
// Per workspace rather than global, because two agents editing the same file in different worktrees
// have genuinely independent views of it, and sharing the ledger would have one agent's read
// satisfy the other's edit.
type readLedger struct {
	mu     sync.Mutex
	digest map[string]string
}

func newReadLedger() *readLedger { return &readLedger{digest: map[string]string{}} }

func (l *readLedger) record(path, digest string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.digest[path] = digest
}

func (l *readLedger) lastRead(path string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	digest, ok := l.digest[path]
	return digest, ok
}

func (l *readLedger) forget(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.digest, path)
}

// FileTools builds the read, write, edit, glob and grep tools for a workspace.
//
// Built together rather than registered independently because they share the read ledger, and a
// write tool that did not know what the read tool had seen could not enforce the freshness check at
// all.
func FileTools(w *Workspace) []core.Tool {
	ledger := newReadLedger()
	return []core.Tool{
		&readTool{w: w, ledger: ledger},
		&editTool{w: w, ledger: ledger},
		&writeTool{w: w, ledger: ledger},
		&globTool{w: w},
		&grepTool{w: w},
	}
}

func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:8])
}

// failure builds a result the model can act on.
//
// A tool result rather than a Go error, because a failure the model could recover from is
// information, not a fault. Returning an error would end the turn; returning this lets the model
// try a different path, which is usually what it should do.
func failure(format string, args ...any) core.ToolResult {
	return core.ToolResult{Content: fmt.Sprintf(format, args...), IsError: true}
}

// refusal is a tool result for an operation stopped by a safety boundary.
//
// This reaches the model as an error, but the audit trail records it as denied and not run. Keeping
// that distinction is what lets the refused-call view answer what an agent tried to do.
func refusal(format string, args ...any) core.ToolResult {
	return core.ToolResult{Content: fmt.Sprintf(format, args...), IsError: true, Refused: true}
}

func pathFailure(err error) core.ToolResult {
	if errors.Is(err, ErrOutsideWorkspace) {
		return refusal("%v", err)
	}
	return failure("%v", err)
}

// readTool reads a file.
type readTool struct {
	w      *Workspace
	ledger *readLedger
}

func (t *readTool) Name() string        { return "read_file" }
func (t *readTool) Kind() core.ToolKind { return core.ToolRead }

func (t *readTool) Description() string {
	return "Read a file from the workspace. Returns its contents with line numbers. " +
		"You must read a file before you can edit it."
}

func (t *readTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path relative to the workspace root."}
		},
		"required": ["path"]
	}`)
}

func (t *readTool) Run(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return failure("could not read the arguments: %v", err), nil
	}

	path, err := t.w.Resolve(args.Path)
	if err != nil {
		return pathFailure(err), nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return failure("%s: %v", args.Path, err), nil
	}
	if info.IsDir() {
		return failure("%s is a directory. Use glob to list what is in it.", args.Path), nil
	}
	if info.Size() > maxFileBytes {
		return failure("%s is %d bytes, larger than the %d byte limit. Use grep to find what you "+
			"need in it.", args.Path, info.Size(), maxFileBytes), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return failure("%s: %v", args.Path, err), nil
	}

	// Recorded against the resolved path, so two names for one file cannot be used to get around
	// the freshness check.
	t.ledger.record(path, digestOf(content))

	return core.ToolResult{Content: numberLines(string(content))}, nil
}

// numberLines prefixes each line with its number.
//
// Because an edit refers to a place in the file, and a model that can see line numbers describes
// that place accurately far more often than one counting newlines in its head.
func numberLines(content string) string {
	if content == "" {
		return "(this file is empty)"
	}

	lines := strings.Split(content, "\n")
	// A trailing newline produces a final empty element that is not a line of the file.
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, line)
	}
	return b.String()
}

// editTool replaces an exact string in a file.
type editTool struct {
	w      *Workspace
	ledger *readLedger
}

func (t *editTool) Name() string        { return "edit_file" }
func (t *editTool) Kind() core.ToolKind { return core.ToolWrite }

func (t *editTool) Description() string {
	return "Replace an exact piece of text in a file. The old text must appear exactly once, " +
		"so include enough surrounding context to make it unique. Read the file first."
}

func (t *editTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path relative to the workspace root."},
			"old_text": {"type": "string", "description": "The exact text to replace, including indentation."},
			"new_text": {"type": "string", "description": "What to replace it with."}
		},
		"required": ["path", "old_text", "new_text"]
	}`)
}

func (t *editTool) Run(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return failure("could not read the arguments: %v", err), nil
	}
	if args.OldText == args.NewText {
		return failure("the old and new text are identical, so this edit would change nothing"), nil
	}

	path, err := t.w.Resolve(args.Path)
	if err != nil {
		return pathFailure(err), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return failure("%s: %v", args.Path, err), nil
	}

	// The freshness check. Both halves matter and they say different things.
	seen, everRead := t.ledger.lastRead(path)
	if !everRead {
		return failure("read %s before editing it, so the edit is computed against what is "+
			"actually there", args.Path), nil
	}
	if now := digestOf(content); now != seen {
		t.ledger.forget(path)
		return failure("%s has changed since you read it, so this edit was computed against "+
			"content that has moved. Read it again and redo the edit against what is there now.",
			args.Path), nil
	}

	text := string(content)
	switch count := strings.Count(text, args.OldText); count {
	case 0:
		return failure("that exact text is not in %s. Check the indentation and whitespace, or "+
			"read the file again.", args.Path), nil
	case 1:
		// The only case that is safe to apply.
	default:
		// Replacing the first is a guess about which one was meant, and replacing all is a
		// different edit from the one asked for. Neither is worth doing silently.
		return failure("that text appears %d times in %s, so it is ambiguous which one you mean. "+
			"Include more surrounding context to make it unique.", count, args.Path), nil
	}

	updated := strings.Replace(text, args.OldText, args.NewText, 1)
	if err := writeFilePreservingMode(path, []byte(updated)); err != nil {
		return failure("writing %s: %v", args.Path, err), nil
	}

	// The ledger is updated rather than cleared, so a model making several edits to one file does
	// not have to re read it between each of them. It has just been told exactly what changed.
	t.ledger.record(path, digestOf([]byte(updated)))

	return core.ToolResult{Content: fmt.Sprintf("Edited %s.", args.Path)}, nil
}

// writeTool creates or overwrites a file.
type writeTool struct {
	w      *Workspace
	ledger *readLedger
}

func (t *writeTool) Name() string        { return "write_file" }
func (t *writeTool) Kind() core.ToolKind { return core.ToolWrite }

func (t *writeTool) Description() string {
	return "Write a file, creating it or replacing it entirely. To change part of an existing " +
		"file use edit_file instead, which is safer because it checks the file has not moved."
}

func (t *writeTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "Path relative to the workspace root."},
			"content": {"type": "string", "description": "The complete contents of the file."}
		},
		"required": ["path", "content"]
	}`)
}

func (t *writeTool) Run(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return failure("could not read the arguments: %v", err), nil
	}
	if len(args.Content) > maxFileBytes {
		return failure("that content is %d bytes, larger than the %d byte limit",
			len(args.Content), maxFileBytes), nil
	}

	path, err := t.w.Resolve(args.Path)
	if err != nil {
		return pathFailure(err), nil
	}

	// Overwriting an existing file wholesale is the destructive case, so it gets the same freshness
	// rule as an edit. Creating a new one has nothing to be stale against.
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		seen, everRead := t.ledger.lastRead(path)
		if !everRead {
			return failure("%s already exists. Read it first if you mean to replace it, or use "+
				"edit_file to change part of it.", args.Path), nil
		}
		if digestOf(existing) != seen {
			t.ledger.forget(path)
			return failure("%s has changed since you read it. Read it again before replacing it.",
				args.Path), nil
		}
	case !os.IsNotExist(err):
		return failure("%s: %v", args.Path, err), nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return failure("creating the directory for %s: %v", args.Path, err), nil
	}
	if err := writeFilePreservingMode(path, []byte(args.Content)); err != nil {
		return failure("writing %s: %v", args.Path, err), nil
	}
	t.ledger.record(path, digestOf([]byte(args.Content)))

	return core.ToolResult{Content: fmt.Sprintf("Wrote %s, %d bytes.",
		args.Path, len(args.Content))}, nil
}

// writeFilePreservingMode writes a file without changing its permissions if it already exists.
//
// os.WriteFile only applies the mode when creating, so this is mostly about the create case, where
// 0644 is right for source and wrong for anything that needs to be executable. A script an agent
// writes and then cannot run is a confusing failure, so an existing file keeps whatever it had.
func writeFilePreservingMode(path string, content []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, content, mode)
}

// globTool lists files matching a pattern.
type globTool struct{ w *Workspace }

func (t *globTool) Name() string        { return "glob" }
func (t *globTool) Kind() core.ToolKind { return core.ToolRead }

func (t *globTool) Description() string {
	return "Find files by name pattern, for example \"**/*.go\" or \"internal/**/*_test.go\". " +
		"Returns paths relative to the workspace root."
}

func (t *globTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {"type": "string", "description": "A glob pattern. ** matches any number of directories."}
		},
		"required": ["pattern"]
	}`)
}

// maxMatches bounds a listing.
//
// A model pointed at node_modules would otherwise get sixty thousand paths, which is expensive and
// useless in equal measure.
const maxMatches = 300

func (t *globTool) Run(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return failure("could not read the arguments: %v", err), nil
	}
	if args.Pattern == "" {
		return failure("a pattern is required"), nil
	}
	if err := checkPattern(args.Pattern); err != nil {
		// Said explicitly rather than left to match nothing. A model told "nothing matches" for a
		// pattern with a typo in it concludes the files are not there and stops looking, which is
		// a much more expensive mistake than a syntax error.
		return failure("%q is not a valid pattern: %v", args.Pattern, err), nil
	}

	matches, truncated, err := t.walk(args.Pattern)
	if err != nil {
		return failure("%v", err), nil
	}
	if len(matches) == 0 {
		return core.ToolResult{Content: fmt.Sprintf("Nothing matches %q.", args.Pattern)}, nil
	}

	sort.Strings(matches)
	content := strings.Join(matches, "\n")
	if truncated {
		content += fmt.Sprintf("\n\n(stopped at %d matches, there are more)", maxMatches)
	}
	return core.ToolResult{Content: content}, nil
}

func (t *globTool) walk(pattern string) (matches []string, truncated bool, err error) {
	root := t.w.Root()

	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// A directory we cannot read is not a reason to abandon the whole search. Skipping it
			// finds everything else, which is more useful than finding nothing.
			return nil //nolint:nilerr // deliberate: keep walking past unreadable directories
		}
		if entry.IsDir() {
			if shouldSkipDir(entry.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= maxMatches {
			truncated = true
			return filepath.SkipAll
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if matchesGlob(pattern, rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	return matches, truncated, err
}

// shouldSkipDir names the directories nobody means to search.
//
// A fixed list rather than reading .gitignore, deliberately. Parsing gitignore properly is a real
// piece of work with its own precedence rules, and getting it subtly wrong means silently not
// finding files that are there, which is worse than searching a few directories nobody wanted.
// These are the ones that are always noise and are never what somebody meant.
var skippedDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"__pycache__":  true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".next":        true,
}

func shouldSkipDir(name string) bool { return skippedDirs[name] }

// checkPattern reports whether a glob is well formed.
//
// filepath.Match only reports a bad pattern when it reaches the broken part, so this tests it
// against a path that exercises the whole thing rather than against nothing.
func checkPattern(pattern string) error {
	for _, part := range strings.Split(pattern, "/**/") {
		for _, segment := range strings.Split(strings.TrimPrefix(part, "**/"), "/") {
			if _, err := filepath.Match(segment, "probe"); err != nil {
				return err
			}
		}
	}
	return nil
}

// matchesGlob supports ** in addition to what filepath.Match handles.
//
// filepath.Match's * does not cross directory separators and it has no ** at all, so the pattern
// every model reaches for first, `**/*.go`, matches nothing. A tool whose most obvious input
// silently returns nothing is one a model will conclude the codebase is empty from.
func matchesGlob(pattern, path string) bool {
	if ok, err := filepath.Match(pattern, path); err == nil && ok {
		return true
	}
	if !strings.Contains(pattern, "**") {
		return false
	}

	// `**/x` means "x at any depth", which includes depth zero: `**/*.go` has to match `main.go` in
	// the root, or the pattern is useless in exactly the case people use it for.
	if rest, found := strings.CutPrefix(pattern, "**/"); found {
		if ok, err := filepath.Match(rest, filepath.Base(path)); err == nil && ok {
			return true
		}
		if matchesGlob(rest, path) {
			return true
		}
	}

	if before, after, found := strings.Cut(pattern, "/**/"); found {
		if !strings.HasPrefix(path, before+"/") {
			return false
		}
		return matchesGlob("**/"+after, strings.TrimPrefix(path, before+"/"))
	}
	return false
}

// grepTool searches file contents.
type grepTool struct{ w *Workspace }

func (t *grepTool) Name() string        { return "grep" }
func (t *grepTool) Kind() core.ToolKind { return core.ToolRead }

func (t *grepTool) Description() string {
	return "Search the text of files in the workspace. Returns matching lines with their file " +
		"and line number."
}

func (t *grepTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {"type": "string", "description": "The text to search for."},
			"glob": {"type": "string", "description": "Optional file pattern to limit the search, for example \"**/*.go\"."}
		},
		"required": ["query"]
	}`)
}

// maxGrepMatches bounds a search for the same reason a listing is bounded.
const maxGrepMatches = 200

func (t *grepTool) Run(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Query string `json:"query"`
		Glob  string `json:"glob"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return failure("could not read the arguments: %v", err), nil
	}
	if args.Query == "" {
		return failure("a query is required"), nil
	}
	if args.Glob != "" {
		if err := checkPattern(args.Glob); err != nil {
			return failure("%q is not a valid pattern: %v", args.Glob, err), nil
		}
	}

	root := t.w.Root()
	var hits []string
	var truncated bool

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // deliberate: keep walking past unreadable directories
		}
		if entry.IsDir() {
			if shouldSkipDir(entry.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if len(hits) >= maxGrepMatches {
			truncated = true
			return filepath.SkipAll
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if args.Glob != "" && !matchesGlob(args.Glob, rel) {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil && info.Size() > maxFileBytes {
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil || looksBinary(content) {
			// Binary files are skipped rather than reported. A match inside a compiled object is
			// never what somebody was looking for, and printing the line around it fills the
			// context with bytes that are not text.
			return nil
		}

		for i, line := range strings.Split(string(content), "\n") {
			if !strings.Contains(line, args.Query) {
				continue
			}
			if len(hits) >= maxGrepMatches {
				truncated = true
				return filepath.SkipAll
			}
			hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
		}
		return nil
	})
	if err != nil {
		return failure("%v", err), nil
	}

	if len(hits) == 0 {
		return core.ToolResult{Content: fmt.Sprintf("No matches for %q.", args.Query)}, nil
	}
	content := strings.Join(hits, "\n")
	if truncated {
		content += fmt.Sprintf("\n\n(stopped at %d matches, there are more)", maxGrepMatches)
	}
	return core.ToolResult{Content: content}, nil
}

// looksBinary reports whether content is probably not text.
//
// A null byte in the first chunk is the same heuristic git uses. Cheap, and wrong only for files
// nobody greps.
func looksBinary(content []byte) bool {
	limit := len(content)
	if limit > 8000 {
		limit = 8000
	}
	for _, b := range content[:limit] {
		if b == 0 {
			return true
		}
	}
	return false
}
