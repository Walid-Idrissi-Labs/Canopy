// Package bench measures what a change to Canopy does to the cost of getting work done. Each task
// is a small project whose own check fails until the work is done; a run records whether the check
// passes afterwards and what that took in tokens, money and time, so two runs can be compared.
//
// Task files are stored with a .txt suffix, removed when a task is laid out, so the Go toolchain
// never builds or tests the fixtures as part of Canopy itself.
package bench

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed tasks
var embedded embed.FS

// Task is one piece of work with a check that decides whether it was done.
type Task struct {
	Name   string   `json:"-"`
	Prompt string   `json:"prompt"`
	Check  []string `json:"check"`
	Kind   string   `json:"kind"`
}

// Tasks returns the built-in tasks by name.
func Tasks() ([]Task, error) {
	entries, err := fs.ReadDir(embedded, "tasks")
	if err != nil {
		return nil, err
	}
	var tasks []Task
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(embedded, path.Join("tasks", e.Name(), "task.json"))
		if err != nil {
			return nil, fmt.Errorf("task %s: %w", e.Name(), err)
		}
		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			return nil, fmt.Errorf("task %s: %w", e.Name(), err)
		}
		task.Name = e.Name()
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Name < tasks[j].Name })
	return tasks, nil
}

// Select keeps the tasks named, or all of them when none are.
func Select(tasks []Task, names []string) ([]Task, error) {
	if len(names) == 0 {
		return tasks, nil
	}
	byName := map[string]Task{}
	for _, t := range tasks {
		byName[t.Name] = t
	}
	var out []Task
	for _, n := range names {
		t, ok := byName[strings.TrimSpace(n)]
		if !ok {
			return nil, fmt.Errorf("there is no bench task called %q", n)
		}
		out = append(out, t)
	}
	return out, nil
}

// LayOut writes a task's files into dir and commits them, so the agent starts from a clean
// repository the way it would in real work.
func LayOut(ctx context.Context, task Task, dir string) error {
	root := path.Join("tasks", task.Name)
	err := fs.WalkDir(embedded, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Base(p) == "task.json" {
			return err
		}
		data, err := fs.ReadFile(embedded, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".txt")))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=bench", "-c", "user.email=bench@localhost", "commit", "-q", "-m", "task"},
	} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", args[0], err, out)
		}
	}
	return nil
}

// ErrToolMissing means a task's check needs a program this machine does not have.
var ErrToolMissing = errors.New("the check's program is not installed")

// RunCheck runs a task's check in dir and reports whether it passed, with its output.
func RunCheck(ctx context.Context, task Task, dir string) (bool, string, error) {
	if len(task.Check) == 0 {
		return false, "", fmt.Errorf("task %s has no check", task.Name)
	}
	if _, err := exec.LookPath(task.Check[0]); err != nil {
		return false, "", fmt.Errorf("%w: %s", ErrToolMissing, task.Check[0])
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, task.Check[0], task.Check[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return false, string(out), err
	}
	return err == nil, string(out), nil
}

// Usage is what one attempt consumed, as the headless run reports it.
type Usage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	ToolCalls        int     `json:"tool_calls"`
}

// Result is one task's outcome.
type Result struct {
	Task    string  `json:"task"`
	Kind    string  `json:"kind"`
	Passed  bool    `json:"passed"`
	Skipped string  `json:"skipped,omitempty"`
	Error   string  `json:"error,omitempty"`
	Seconds float64 `json:"seconds"`
	Usage
}

// Report is a whole bench run.
type Report struct {
	At      time.Time `json:"at"`
	Key     string    `json:"key,omitempty"`
	Model   string    `json:"model,omitempty"`
	Effort  string    `json:"effort,omitempty"`
	Version string    `json:"version,omitempty"`
	Results []Result  `json:"results"`
}

// Totals sums the tasks that ran.
func (r Report) Totals() (passed, ran int, usage Usage, seconds float64) {
	for _, res := range r.Results {
		if res.Skipped != "" {
			continue
		}
		ran++
		if res.Passed {
			passed++
		}
		usage.InputTokens += res.InputTokens
		usage.OutputTokens += res.OutputTokens
		usage.CacheReadTokens += res.CacheReadTokens
		usage.CacheWriteTokens += res.CacheWriteTokens
		usage.CostUSD += res.CostUSD
		usage.ToolCalls += res.ToolCalls
		seconds += res.Seconds
	}
	return passed, ran, usage, seconds
}

// Markdown is the report as a table, one row per task and a total.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "| task | kind | passed | input | output | cache read | cache write | cost | tools | seconds |\n")
	fmt.Fprintf(&b, "|---|---|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, res := range r.Results {
		if res.Skipped != "" {
			fmt.Fprintf(&b, "| %s | %s | skipped: %s | | | | | | | |\n", res.Task, res.Kind, res.Skipped)
			continue
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %d | %d | $%.4f | %d | %.1f |\n", res.Task, res.Kind,
			yesNo(res.Passed), res.InputTokens, res.OutputTokens, res.CacheReadTokens, res.CacheWriteTokens,
			res.CostUSD, res.ToolCalls, res.Seconds)
	}
	passed, ran, u, seconds := r.Totals()
	fmt.Fprintf(&b, "| **total** | | %d/%d | %d | %d | %d | %d | $%.4f | %d | %.1f |\n", passed, ran,
		u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens, u.CostUSD, u.ToolCalls, seconds)
	if passed > 0 {
		fmt.Fprintf(&b, "\ncost per passing task: $%.4f\n", u.CostUSD/float64(passed))
	}
	return b.String()
}

// Compare says how a run differs from an earlier one, over the whole suite.
func Compare(before, after Report) string {
	bp, br, bu, bs := before.Totals()
	ap, ar, au, as := after.Totals()
	var b strings.Builder
	fmt.Fprintf(&b, "passed      %d/%d -> %d/%d\n", bp, br, ap, ar)
	fmt.Fprintf(&b, "cost        $%.4f -> $%.4f (%s)\n", bu.CostUSD, au.CostUSD, change(bu.CostUSD, au.CostUSD))
	fmt.Fprintf(&b, "input       %d -> %d (%s)\n", bu.InputTokens, au.InputTokens,
		change(float64(bu.InputTokens), float64(au.InputTokens)))
	fmt.Fprintf(&b, "output      %d -> %d (%s)\n", bu.OutputTokens, au.OutputTokens,
		change(float64(bu.OutputTokens), float64(au.OutputTokens)))
	fmt.Fprintf(&b, "cache read  %d -> %d (%s)\n", bu.CacheReadTokens, au.CacheReadTokens,
		change(float64(bu.CacheReadTokens), float64(au.CacheReadTokens)))
	fmt.Fprintf(&b, "seconds     %.1f -> %.1f (%s)\n", bs, as, change(bs, as))
	return b.String()
}

func change(before, after float64) string {
	if before == 0 {
		return "no baseline"
	}
	return fmt.Sprintf("%+.1f%%", (after-before)/before*100)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Attempt does the work on a laid-out task, in dir, and reports what it consumed.
type Attempt func(ctx context.Context, task Task, dir string) (Usage, error)

// Run lays out each task in a fresh directory, confirms its check fails before any work, makes the
// attempt and runs the check again. A task whose check program is missing is skipped, not failed.
func Run(ctx context.Context, tasks []Task, attempt Attempt, progress func(Result)) []Result {
	results := make([]Result, 0, len(tasks))
	for _, task := range tasks {
		res := runOne(ctx, task, attempt)
		if progress != nil {
			progress(res)
		}
		results = append(results, res)
		if ctx.Err() != nil {
			break
		}
	}
	return results
}

func runOne(ctx context.Context, task Task, attempt Attempt) Result {
	res := Result{Task: task.Name, Kind: task.Kind}
	dir, err := os.MkdirTemp("", "canopy-bench-"+task.Name+"-")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := LayOut(ctx, task, dir); err != nil {
		res.Error = err.Error()
		return res
	}
	// A check that already passes measures nothing, and would count as a win for doing nothing.
	passed, _, err := RunCheck(ctx, task, dir)
	switch {
	case errors.Is(err, ErrToolMissing):
		res.Skipped = err.Error()
		return res
	case err != nil:
		res.Error = err.Error()
		return res
	case passed:
		res.Error = "the check passes before any work, so the task measures nothing"
		return res
	}
	start := time.Now()
	usage, err := attempt(ctx, task, dir)
	res.Seconds = time.Since(start).Seconds()
	res.Usage = usage
	if err != nil {
		res.Error = err.Error()
	}
	// A check passed by rewriting it proves nothing: every test file must be as it was laid out.
	if changed := changedTests(task, dir); changed != "" {
		res.Error = "the attempt changed " + changed + ", so the check no longer measures the task"
		return res
	}
	res.Passed, _, err = RunCheck(ctx, task, dir)
	if err != nil && res.Error == "" {
		res.Error = err.Error()
	}
	return res
}

// changedTests names the first of a task's test files that is missing or differs from the one laid
// out, or returns "".
func changedTests(task Task, dir string) string {
	root := path.Join("tasks", task.Name)
	var changed string
	_ = fs.WalkDir(embedded, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || changed != "" {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, root+"/"), ".txt")
		if !isTest(rel) {
			return nil
		}
		want, _ := fs.ReadFile(embedded, p)
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(got, want) {
			changed = rel
		}
		return nil
	})
	return changed
}

// isTest reports whether a task file is one of its tests.
func isTest(rel string) bool {
	base := path.Base(rel)
	return strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "test_") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.")
}
