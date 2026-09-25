package bench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every task is real work: its check fails as laid out. A task that passed already would score a
// win for an agent that did nothing.
func TestEveryTaskFailsBeforeTheWork(t *testing.T) {
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) < 6 {
		t.Fatalf("%d tasks", len(tasks))
	}
	for _, task := range tasks {
		dir := t.TempDir()
		if err := LayOut(context.Background(), task, dir); err != nil {
			t.Fatalf("%s: %v", task.Name, err)
		}
		passed, out, err := RunCheck(context.Background(), task, dir)
		if errors.Is(err, ErrToolMissing) {
			t.Logf("%s: skipped, %v", task.Name, err)
			continue
		}
		if err != nil || passed {
			t.Errorf("%s: passed=%v err=%v before any work:\n%s", task.Name, passed, err, out)
		}
	}
}

// The run records a pass only when the check passes after the attempt, with what it consumed.
func TestARunScoresTheCheckAfterTheAttempt(t *testing.T) {
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	tasks, err = Select(tasks, []string{"go-off-by-one", "go-reverse"})
	if err != nil {
		t.Fatal(err)
	}
	fix := func(_ context.Context, task Task, dir string) (Usage, error) {
		if task.Name != "go-off-by-one" {
			return Usage{InputTokens: 7}, nil
		}
		path := filepath.Join(dir, "sum.go")
		data, err := os.ReadFile(path)
		if err != nil {
			return Usage{}, err
		}
		fixed := strings.Replace(string(data), "len(nums)-1", "len(nums)", 1)
		return Usage{InputTokens: 100, CostUSD: 0.01}, os.WriteFile(path, []byte(fixed), 0o644)
	}
	results := Run(context.Background(), tasks, fix, nil)
	if len(results) != 2 || !results[0].Passed || results[1].Passed || results[0].InputTokens != 100 {
		t.Fatalf("results = %+v", results)
	}
	report := Report{Results: results}
	table := report.Markdown()
	for _, want := range []string{"| go-off-by-one | bug fix | yes | 100 |", "| **total** | | 1/2 | 107 |", "$0.0100"} {
		if !strings.Contains(table, want) {
			t.Errorf("the table lacks %q:\n%s", want, table)
		}
	}
	if cmp := Compare(report, Report{Results: results[:1]}); !strings.Contains(cmp, "1/2 -> 1/1") {
		t.Errorf("compare = %s", cmp)
	}
}
