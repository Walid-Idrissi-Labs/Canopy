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

// An attempt that makes the check pass by emptying the tests is scored as a failure, not a pass.
func TestEditingTheTestsIsNotAPass(t *testing.T) {
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	tasks, _ = Select(tasks, []string{"go-off-by-one"})
	cheat := func(_ context.Context, _ Task, dir string) (Usage, error) {
		return Usage{}, os.WriteFile(filepath.Join(dir, "sum_test.go"), []byte("package sum\n"), 0o644)
	}
	results := Run(context.Background(), tasks, cheat, nil)
	if results[0].Passed || !strings.Contains(results[0].Error, "sum_test.go") {
		t.Fatalf("an attempt that emptied the tests scored %+v", results[0])
	}
}

// A task whose check already passes is refused before the attempt, since it would score a win for
// doing nothing.
func TestATaskThatAlreadyPassesIsRefused(t *testing.T) {
	task := Task{Name: "go-off-by-one", Kind: "bug fix", Check: []string{"true"}}
	ran := false
	results := Run(context.Background(), []Task{task}, func(context.Context, Task, string) (Usage, error) {
		ran = true
		return Usage{}, nil
	}, nil)
	if ran || results[0].Passed || !strings.Contains(results[0].Error, "measures nothing") {
		t.Fatalf("an already-passing task was attempted or scored: ran=%v %+v", ran, results[0])
	}
}

// Adding a test file games the check as surely as editing one: a TestMain that exits at once makes
// go test pass without running anything.
func TestAddingATestFileIsNotAPass(t *testing.T) {
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	tasks, _ = Select(tasks, []string{"go-off-by-one"})
	cheat := func(_ context.Context, _ Task, dir string) (Usage, error) {
		return Usage{}, os.WriteFile(filepath.Join(dir, "zz_test.go"),
			[]byte("package sum\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(0) }\n"), 0o644)
	}
	results := Run(context.Background(), tasks, cheat, nil)
	if results[0].Passed || !strings.Contains(results[0].Error, "zz_test.go") {
		t.Fatalf("an attempt that added a test file scored %+v", results[0])
	}
}

// Code that ends the process before any test runs passes the check without a test file touched;
// a pass needs the laid-out tests to be seen passing.
func TestExitingBeforeTheTestsRunIsNotAPass(t *testing.T) {
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	tasks, _ = Select(tasks, []string{"go-off-by-one"})
	cheat := func(_ context.Context, _ Task, dir string) (Usage, error) {
		return Usage{}, os.WriteFile(filepath.Join(dir, "zz.go"), []byte("package sum\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+
			"func init() {\n\tif testing.Testing() {\n\t\tos.Exit(0)\n\t}\n}\n"), 0o644)
	}
	results := Run(context.Background(), tasks, cheat, nil)
	if results[0].Passed || !strings.Contains(results[0].Error, "TestTotal") {
		t.Fatalf("an attempt that exits before the tests scored %+v", results[0])
	}
}

// An honest fix passes every task: each can be solved, and nothing a check leaves behind, Python's
// bytecode for one, is mistaken for a test the attempt added.
func TestHonestFixesPassEveryTask(t *testing.T) {
	fixes := map[string]map[string]string{
		"go-off-by-one": {"sum.go": "package sum\n\nfunc Total(nums []int) int {\n\ttotal := 0\n\tfor _, n := range nums {\n\t\ttotal += n\n\t}\n\treturn total\n}\n"},
		"go-reverse":    {"text.go": "package text\n\nfunc Reverse(s string) string {\n\tr := []rune(s)\n\tfor i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {\n\t\tr[i], r[j] = r[j], r[i]\n\t}\n\treturn string(r)\n}\n"},
		"py-slugify":    {"slug.py": "import re\n\n\ndef slugify(text):\n    return \"-\".join(re.findall(r\"[a-z0-9]+\", text.lower()))\n"},
	}
	tasks, err := Tasks()
	if err != nil {
		t.Fatal(err)
	}
	var chosen []Task
	for _, task := range tasks {
		if fixes[task.Name] != nil {
			chosen = append(chosen, task)
		}
	}
	fix := func(_ context.Context, task Task, dir string) (Usage, error) {
		for name, content := range fixes[task.Name] {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				return Usage{}, err
			}
		}
		return Usage{}, nil
	}
	for _, res := range Run(context.Background(), chosen, fix, nil) {
		if res.Skipped != "" {
			t.Logf("%s skipped: %s", res.Task, res.Skipped)
			continue
		}
		if !res.Passed {
			t.Errorf("an honest fix of %s did not pass: %s", res.Task, res.Error)
		}
	}
}
