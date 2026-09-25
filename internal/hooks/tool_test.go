package hooks

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
)

func TestAPreToolAnswerIsReadStrictly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answer  Answer
		refused bool
		reason  string
	}{
		{"silence", Answer{}, false, ""},
		{"allow", Answer{Stdout: `{"decision": "allow"}`}, false, ""},
		{"deny with a reason", Answer{Stdout: `{"decision": "deny", "reason": "not on main"}`}, true, "not on main"},
		{"deny without one", Answer{Stdout: `{"decision": "deny"}`}, true, "a pre-tool hook refused it"},
		{"exit 2", Answer{ExitCode: 2, Stderr: "secrets file\n"}, true, "secrets file"},
		{"another exit", Answer{ExitCode: 1}, true, "failed (it exited 1)"},
		{"no answer", Answer{Failed: "it timed out"}, true, "could not give an answer (it timed out)"},
		{"not JSON", Answer{Stdout: "ok!"}, true, "not JSON"},
		{"a word it does not know", Answer{Stdout: `{"decision": "maybe"}`}, true, "not allow or deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refusal, _ := preToolVerdict(tc.answer)
			if (refusal != "") != tc.refused || !strings.Contains(refusal, tc.reason) {
				t.Fatalf("refusal %q, want refused %v containing %q", refusal, tc.refused, tc.reason)
			}
		})
	}
}

// scriptedExec answers each hook command from a table and records what it was given.
type scriptedExec struct {
	mu      sync.Mutex
	answers map[string]Answer
	ran     []string
	inputs  []map[string]any
}

func (s *scriptedExec) run(_ context.Context, command, _ string, _ time.Duration, input []byte) Answer {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ran = append(s.ran, command)
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	s.inputs = append(s.inputs, in)
	return s.answers[command]
}

var shellCall = permission.Request{SessionID: "s1", AgentID: "a1", Tool: "run_command", CallID: "c1",
	Kind: core.ToolExecute, Command: "git push", Arguments: `{"command":"git push"}`}

// Hooks run in order, the first refusal stands and the rest are not asked; a hook for other tools
// is not run at all; each is told the call in full.
func TestPreToolHooksRunInOrderForTheirTools(t *testing.T) {
	exec := &scriptedExec{answers: map[string]Answer{
		"guard-reads": {Stdout: `{"decision": "deny"}`},
		"guard-push":  {Stdout: `{"decision": "deny", "reason": "no pushing"}`},
		"never":       {},
	}}
	var reports []Report
	r := NewToolRunner([]Hook{
		{On: PreTool, Run: "guard-reads", Tools: []string{"read_file"}},
		{On: PreTool, Run: "guard-push", Tools: []string{"run_command"}},
		{On: PreTool, Run: "never"},
	}, "/work", exec.run, func(rep Report) { reports = append(reports, rep) })

	if why := r.Before(context.Background(), shellCall); why != "no pushing" {
		t.Fatalf("refusal %q", why)
	}
	if strings.Join(exec.ran, ",") != "guard-push" {
		t.Fatalf("ran %v", exec.ran)
	}
	in := exec.inputs[0]
	if in["event"] != "pre-tool" || in["tool"] != "run_command" || in["command"] != "git push" ||
		in["call_id"] != "c1" || in["arguments"].(map[string]any)["command"] != "git push" {
		t.Fatalf("the hook was told %v", in)
	}
	if len(reports) != 0 {
		t.Fatalf("a refusal was reported as a failure: %v", reports)
	}
}

// Post-tool notes are gathered from every hook that has one; a hook that fails is reported and
// adds nothing; a long result is cut before it is handed over, and says so.
func TestPostToolHooksAddNotes(t *testing.T) {
	exec := &scriptedExec{answers: map[string]Answer{
		"lint":   {Stdout: `{"note": "2 lint warnings"}`},
		"broken": {ExitCode: 3},
		"quiet":  {},
		"prose":  {Stdout: "hello"},
	}}
	var reports []Report
	r := NewToolRunner([]Hook{
		{On: PostTool, Run: "lint"}, {On: PostTool, Run: "broken"}, {On: PostTool, Run: "quiet"},
		{On: PostTool, Run: "prose"},
	}, "/work", exec.run, func(rep Report) { reports = append(reports, rep) })
	big := strings.Repeat("x", maxResultForHook+10)
	note := r.After(context.Background(), shellCall, core.ToolResult{Content: big})
	if note != "2 lint warnings" {
		t.Fatalf("note %q", note)
	}
	if len(reports) != 2 {
		t.Fatalf("reports %v", reports)
	}
	result := exec.inputs[0]["result"].(map[string]any)
	if len(result["content"].(string)) != maxResultForHook || result["truncated"] != true {
		t.Fatal("a long result was handed over whole, or without saying it was cut")
	}
}

func TestTurnEndHooksRunInTheBackground(t *testing.T) {
	exec := &scriptedExec{answers: map[string]Answer{"notify": {}, "fails": {Failed: "it could not be run"}}}
	var mu sync.Mutex
	var reports []Report
	r := NewToolRunner([]Hook{{On: TurnEnd, Run: "notify"}, {On: TurnEnd, Run: "fails"}}, "/work", exec.run,
		func(rep Report) { mu.Lock(); reports = append(reports, rep); mu.Unlock() })
	r.TurnEnded("s1", core.Turn{ID: "t1", State: core.TurnComplete, Text: "done"})
	r.Wait()
	if len(exec.inputs) != 2 || exec.inputs[0]["state"] != "complete" || exec.inputs[0]["text"] != "done" {
		t.Fatalf("told %v", exec.inputs)
	}
	if len(reports) != 1 {
		t.Fatalf("reports %v", reports)
	}
}

func TestNoToolHooksIsNoRunner(t *testing.T) {
	if r := NewToolRunner([]Hook{{On: TestsPassed, Run: "x"}}, "/work", nil, nil); r != nil {
		t.Fatal("a runner was built with nothing to run")
	}
	var r *ToolRunner
	if r.Before(context.Background(), shellCall) != "" || r.After(context.Background(), shellCall, core.ToolResult{}) != "" {
		t.Fatal("a nil runner had an opinion")
	}
}

// Through a real shell: the call arrives on stdin, stdout is the answer and stderr is kept apart.
func TestAJSONHookReadsItsInputAndAnswers(t *testing.T) {
	exec := ConfinedJSON(func(string) (*sandbox.Policy, []string) { return nil, nil })
	dir := t.TempDir()
	answer := exec(context.Background(),
		`input=$(cat); echo "noise" >&2; case "$input" in *'git push'*) echo '{"decision": "deny", "reason": "saw it"}';; esac`,
		dir, 5*time.Second, []byte(`{"command": "git push"}`))
	if answer.Failed != "" || answer.ExitCode != 0 || strings.TrimSpace(answer.Stderr) != "noise" {
		t.Fatalf("answer %+v", answer)
	}
	if why, _ := preToolVerdict(answer); why != "saw it" {
		t.Fatalf("refusal %q from stdout %q", why, answer.Stdout)
	}
	answer = exec(context.Background(), "echo 'protected path' >&2; exit 2", dir, 5*time.Second, nil)
	if why, _ := preToolVerdict(answer); why != "protected path" {
		t.Fatalf("exit 2 gave %q", why)
	}
	answer = exec(context.Background(), "sleep 5", dir, 100*time.Millisecond, nil)
	if why, _ := preToolVerdict(answer); !strings.Contains(why, "could not give an answer") {
		t.Fatalf("a hook that timed out gave %q", why)
	}
}
