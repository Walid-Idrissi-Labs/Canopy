package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Long command output is kept out of the conversation and handed to the model as a summary and a
// handle. A test run that prints two thousand lines costs its whole length in every later request
// of the turn and of every turn after it, while what the model needs is usually the failures and
// the last few lines. The full text is stored on disk, owner only, and read_output pages through it
// on request, so nothing is lost by not sending it.

// inlineOutputBytes is how much output goes into the conversation whole.
const inlineOutputBytes = 8 * 1024

// capturedOutputBytes is how much of a command's output is kept at all, head and tail.
const capturedOutputBytes = 2 * 1024 * 1024

// outputRetention is how long stored output is kept before it is pruned.
const outputRetention = 7 * 24 * time.Hour

// OutputStore keeps full command output for read_output.
type OutputStore struct{ dir string }

// NewOutputStore opens the store in the user's cache directory, pruning old entries.
func NewOutputStore() *OutputStore {
	base, err := os.UserCacheDir()
	if err != nil {
		return &OutputStore{}
	}
	s := &OutputStore{dir: filepath.Join(base, "canopy", "outputs")}
	s.prune()
	return s
}

// OutputStoreAt opens a store in dir, for tests.
func OutputStoreAt(dir string) *OutputStore { return &OutputStore{dir: dir} }

var handlePattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

func (s *OutputStore) save(text string) (string, error) {
	if s == nil || s.dir == "" {
		return "", errors.New("no output store")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw)
	return id, os.WriteFile(filepath.Join(s.dir, id+".txt"), []byte(text), 0o600)
}

func (s *OutputStore) load(id string) (string, error) {
	if s == nil || s.dir == "" || !handlePattern.MatchString(id) {
		return "", fmt.Errorf("%q is not an output handle", id)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, id+".txt"))
	if err != nil {
		return "", fmt.Errorf("output %s is no longer available", id)
	}
	return string(data), nil
}

func (s *OutputStore) prune() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > outputRetention {
			_ = os.Remove(filepath.Join(s.dir, e.Name()))
		}
	}
}

// failureLine picks out the lines of test and build output that name what went wrong: go test,
// jest and vitest, pytest, cargo, compilers and panics. The exit code stays the verdict; these are
// only what is worth reading first.
var failureLine = regexp.MustCompile(`(?i)(^--- FAIL|^FAIL\b|FAILED|^\s*(✕|×|✗)|^\s*● |panic:|^error(\[|:)|: error:|Error:|assert|expected .* (got|received|but))`)

// offload returns what the conversation should carry for a command's output: the output itself
// when it is short, otherwise its first and last lines, the lines that look like failures, and a
// handle to the rest.
func offload(store *OutputStore, output string) string {
	if len(output) <= inlineOutputBytes {
		return output
	}
	id, err := store.save(output)
	if err != nil {
		// Without somewhere to keep it, the whole output is the only honest answer.
		return output
	}
	lines := strings.Split(output, "\n")
	head, tail := firstBytes(lines, 2500), lastBytes(lines, 3500)

	var failures []string
	for i, line := range lines {
		if failureLine.MatchString(line) {
			failures = append(failures, fmt.Sprintf("%d: %s", i+1, strings.TrimSpace(line)))
			if len(failures) == 40 {
				failures = append(failures, "(more omitted)")
				break
			}
		}
	}

	var b strings.Builder
	b.WriteString(strings.Join(head, "\n"))
	fmt.Fprintf(&b, "\n\n[%d lines, %d bytes in all. The middle is omitted here; read_output with "+
		"handle %q pages through the whole output, or searches it with grep.]\n", len(lines), len(output), id)
	if len(failures) > 0 {
		b.WriteString("[Lines that look like failures:]\n")
		b.WriteString(strings.Join(failures, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("[Last lines:]\n")
	b.WriteString(strings.Join(tail, "\n"))
	return b.String()
}

func firstBytes(lines []string, budget int) []string {
	var out []string
	for _, l := range lines {
		if budget -= len(l) + 1; budget < 0 {
			break
		}
		out = append(out, l)
	}
	return out
}

func lastBytes(lines []string, budget int) []string {
	start := len(lines)
	for start > 0 {
		if budget -= len(lines[start-1]) + 1; budget < 0 {
			break
		}
		start--
	}
	return lines[start:]
}

// readOutputTool pages through stored output.
type readOutputTool struct{ store *OutputStore }

// ReadOutputTool reads command output that was too long to send whole.
func ReadOutputTool(store *OutputStore) core.Tool { return &readOutputTool{store: store} }

func (t *readOutputTool) Name() string        { return "read_output" }
func (t *readOutputTool) Kind() core.ToolKind { return core.ToolRead }

func (t *readOutputTool) Description() string {
	return "Read command output that was too long to show whole, by the handle given with it. " +
		"Pass offset and limit for a range of lines, or grep to find matching lines."
}

func (t *readOutputTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"handle": {"type": "string"},
			"offset": {"type": "integer", "description": "First line, from 1."},
			"limit": {"type": "integer", "description": "Number of lines, default 200."},
			"grep": {"type": "string", "description": "Return only lines containing this text."}
		},
		"required": ["handle"]
	}`)
}

func (t *readOutputTool) Run(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Handle string `json:"handle"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
		Grep   string `json:"grep"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return failure("could not read the arguments: %v", err), nil
	}
	text, err := t.store.load(args.Handle)
	if err != nil {
		return failure("%v", err), nil
	}
	if args.Grep != "" {
		var hits []string
		for i, line := range strings.Split(text, "\n") {
			if strings.Contains(line, args.Grep) {
				hits = append(hits, fmt.Sprintf("%d: %s", i+1, line))
				if len(hits) == 300 {
					hits = append(hits, "(stopped at 300 matches)")
					break
				}
			}
		}
		if len(hits) == 0 {
			return core.ToolResult{Content: fmt.Sprintf("No lines contain %q.", args.Grep)}, nil
		}
		return core.ToolResult{Content: strings.Join(hits, "\n")}, nil
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 200
	}
	body, first, last, total := numberRange(text, args.Offset, limit)
	if total > 0 && (first > 1 || last < total) {
		body += fmt.Sprintf("\n(lines %d-%d of %d)", first, last, total)
	}
	return core.ToolResult{Content: body}, nil
}
