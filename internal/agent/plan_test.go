package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// A model that ignores the instruction and calls a tool anyway has to be stopped by the permission
// layer rather than by its own good behaviour.
func TestNoToolRunsWhilePlanning(t *testing.T) {
	tool := &countingTool{name: "edit_file", kind: core.ToolWrite, answer: "edited"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("edit_file", `{"path":"main.go"}`),
		says("here is the plan"),
	}}

	// Broad trust, deliberately. If planning relied on the agent's own level being low, an agent
	// configured broadly would execute during its own planning phase.
	l := loop(client, registryWith(tool), core.TrustBroad)

	_, err := l.Plan(context.Background(), ask("fix the bug"), nil)
	if err == nil && tool.count() != 0 {
		t.Error("a tool ran during planning")
	}
	if tool.count() != 0 {
		t.Errorf("the tool ran %d times while planning", tool.count())
	}
}

// Reusing the session's grants would mean "always allow edits" quietly turning plan mode into
// ordinary mode.
func TestAnEarlierApprovalDoesNotLeakIntoPlanning(t *testing.T) {
	tool := &countingTool{name: "run_command", kind: core.ToolExecute, answer: "ran"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("run_command", `{"command":"make test"}`),
		says("the plan"),
	}}

	l := loop(client, registryWith(tool), core.TrustBroad)
	// Everything of this kind already approved for the session.
	l.Grants.Grant(permission.KindScope(core.ToolExecute))

	if _, err := l.Plan(context.Background(), ask("go"), nil); err == nil && tool.count() != 0 {
		t.Error("an earlier approval let a tool run during planning")
	}
	if tool.count() != 0 {
		t.Errorf("the tool ran %d times while planning", tool.count())
	}
}

func TestPlanningReturnsWhatTheAgentSaid(t *testing.T) {
	client := &scriptedClient{turns: [][]core.StreamEvent{
		says("I will read main.go, change the loop, and run go test ./..."),
	}}
	l := loop(client, registryWith(&countingTool{name: "read_file", kind: core.ToolRead}),
		core.TrustStandard)

	plan, err := l.Plan(context.Background(), ask("fix it"), nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !strings.Contains(plan.Plan, "go test") {
		t.Errorf("plan = %q", plan.Plan)
	}
	// Producing a plan costs real tokens and they belong in the session total.
	if plan.Usage.TotalTokens() == 0 {
		t.Error("the plan's usage was not reported, so the session total would be short")
	}
}

// A plan written by something that does not know what it can do is a plan that proposes the
// impossible.
func TestThePlanningModelStillSeesTheTools(t *testing.T) {
	client := &scriptedClient{turns: [][]core.StreamEvent{says("a plan")}}
	tools := registryWith(
		&countingTool{name: "read_file", kind: core.ToolRead},
		&countingTool{name: "run_command", kind: core.ToolExecute},
	)

	l := loop(client, tools, core.TrustStandard)
	if _, err := l.Plan(context.Background(), ask("go"), nil); err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(client.seen) == 0 {
		t.Fatal("no request was made")
	}
	if len(client.seen[0].Tools) != 2 {
		t.Errorf("%d tools described while planning, want both of them",
			len(client.seen[0].Tools))
	}
}

func TestAnEmptyPlanIsRefused(t *testing.T) {
	client := &scriptedClient{turns: [][]core.StreamEvent{{
		{Kind: core.EventDone, StopReason: core.StopEndTurn},
	}}}
	l := loop(client, core.NewToolRegistry(), core.TrustStandard)

	if _, err := l.Plan(context.Background(), ask("go"), nil); err == nil {
		t.Error("a plan with nothing in it should be refused rather than approved")
	}
}

// A model that reads its own plan as its own words follows it. One handed the same text as an
// instruction from somebody else argues with it.
func TestExecutionPutsThePlanBackAsTheAgentsOwnWords(t *testing.T) {
	tool := &countingTool{name: "edit_file", kind: core.ToolWrite, answer: "edited"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("edit_file", `{"path":"main.go"}`),
		says("done"),
	}}

	l := loop(client, registryWith(tool), core.TrustStandard)
	if _, err := l.Execute(context.Background(), ask("fix it"), "I will change main.go", nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	messages := client.seen[0].Messages
	var planMessage core.Message
	for _, msg := range messages {
		if strings.Contains(msg.Text, "I will change main.go") {
			planMessage = msg
		}
	}
	if planMessage.Role != core.RoleAssistant {
		t.Errorf("the plan was sent as %q, want it as the agent's own words", planMessage.Role)
	}

	// And the approval is the user's reply, which is what authorises the work.
	last := messages[len(messages)-1]
	if last.Role != core.RoleUser || !strings.Contains(last.Text, "approved") {
		t.Errorf("last message = %+v, want the approval", last)
	}
	if tool.count() != 1 {
		t.Errorf("the tool ran %d times during execution, want 1", tool.count())
	}
}

// An approval covers the plan that was read. An agent that discovers halfway through that the real
// fix is somewhere else has been authorised for something nobody agreed to.
func TestExecutionTellsTheAgentToStopIfThePlanIsWrong(t *testing.T) {
	if !strings.Contains(ExecutePrompt, "stop and say so") {
		t.Error("the execution prompt does not tell the agent what to do when the plan will not work")
	}
	if !strings.Contains(ExecutePrompt, "not whatever") {
		t.Error("the execution prompt does not bound the approval to what was actually approved")
	}
	if !strings.Contains(PlanPrompt, "Do not call any tools") {
		t.Error("the planning prompt does not tell the agent to hold off")
	}
	// The plan is asked for in prose, because the reader is a person deciding whether to allow it.
	if strings.Contains(PlanPrompt, "JSON") || strings.Contains(PlanPrompt, "format") {
		t.Error("the planning prompt asks for a machine format, and the reader is a person")
	}
}
