package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// fakeHooks records what it was asked and answers as it was told.
type fakeHooks struct {
	refuse, note string

	mu            sync.Mutex
	before, after []permission.Request
	results       []core.ToolResult
}

func (f *fakeHooks) Before(_ context.Context, req permission.Request) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.before = append(f.before, req)
	return f.refuse
}

func (f *fakeHooks) After(_ context.Context, req permission.Request, result core.ToolResult) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.after = append(f.after, req)
	f.results = append(f.results, result)
	return f.note
}

func toolResults(outcome Outcome) []core.ToolResult {
	var out []core.ToolResult
	for _, msg := range outcome.Messages {
		out = append(out, msg.ToolResults...)
	}
	return out
}

// A pre-tool hook's refusal stops the call before it runs, is what the model is told, and is
// audited as a denial.
func TestAPreToolHookRefusesACall(t *testing.T) {
	tool := &countingTool{name: "write_file", kind: core.ToolWrite, answer: "written"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("write_file", `{"path":"a.go","content":"x"}`),
		says("It was refused."),
	}}
	hooks := &fakeHooks{refuse: "no writes to generated files"}
	l := loop(client, registryWith(tool), core.TrustBroad)
	l.Hooks = hooks
	outcome, err := l.Run(context.Background(), ask("write it"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if tool.count() != 0 {
		t.Fatal("a call a pre-tool hook refused ran anyway")
	}
	results := toolResults(outcome)
	if len(results) != 1 || !results[0].IsError ||
		!strings.Contains(results[0].Content, "no writes to generated files") {
		t.Fatalf("the model was told %+v", results)
	}
	if len(hooks.before) != 1 || hooks.before[0].Tool != "write_file" || hooks.before[0].CallID != "call-write_file" ||
		len(hooks.before[0].Paths) != 1 {
		t.Fatalf("the hook was asked about %+v", hooks.before)
	}
	if len(hooks.after) != 0 {
		t.Fatal("a post-tool hook ran for a call that never did")
	}
	entries := l.Trail.Entries()
	if len(entries) != 1 || entries[0].Outcome != permission.Deny || entries[0].Ran ||
		!strings.Contains(entries[0].Reason, "pre-tool hook") {
		t.Fatalf("audited as %+v", entries)
	}
}

// A post-tool hook's note reaches the model with the result, marked as the hook's.
func TestAPostToolHookAddsANote(t *testing.T) {
	tool := &countingTool{name: "read_file", kind: core.ToolRead, answer: "package main"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("read_file", `{"path":"main.go"}`),
		says("Read."),
	}}
	hooks := &fakeHooks{note: "main.go is generated; edit gen.go instead"}
	l := loop(client, registryWith(tool), core.TrustStandard)
	l.Hooks = hooks
	outcome, err := l.Run(context.Background(), ask("read it"), nil)
	if err != nil {
		t.Fatal(err)
	}
	results := toolResults(outcome)
	if len(results) != 1 || !strings.HasPrefix(results[0].Content, "package main") ||
		!strings.Contains(results[0].Content, "[from the project's post-tool hook]\nmain.go is generated") {
		t.Fatalf("the model was told %+v", results)
	}
	if len(hooks.results) != 1 || hooks.results[0].Content != "package main" {
		t.Fatalf("the hook was given %+v", hooks.results)
	}
}

// A hook only ever sees calls the permission layer let through: one it refused, or one a person
// did not approve, never reaches a hook, so a hook cannot be where a refusal is turned round.
func TestHooksSeeOnlyCallsThatWouldRun(t *testing.T) {
	for name, setup := range map[string]func(*Loop){
		"refused by the level": func(l *Loop) { l.Trust = core.TrustReadOnly },
		"not approved": func(l *Loop) {
			l.Trust = core.TrustStandard
			l.Approver = ApproverFunc(func(context.Context, permission.Request, permission.Decision) bool { return false })
		},
	} {
		t.Run(name, func(t *testing.T) {
			tool := &countingTool{name: "run_command", kind: core.ToolExecute, answer: "ok",
				schema: `{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`}
			client := &scriptedClient{turns: [][]core.StreamEvent{
				asksFor("run_command", `{"command":"make"}`),
				says("Not run."),
			}}
			hooks := &fakeHooks{}
			l := loop(client, registryWith(tool), core.TrustStandard)
			setup(l)
			l.Hooks = hooks
			if _, err := l.Run(context.Background(), ask("run it"), nil); err != nil {
				t.Fatal(err)
			}
			if tool.count() != 0 || len(hooks.before) != 0 || len(hooks.after) != 0 {
				t.Fatalf("ran %d, hooks asked before %d after %d", tool.count(), len(hooks.before), len(hooks.after))
			}
		})
	}
}
