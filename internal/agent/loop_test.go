package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// scriptedClient replies with a queue of scripted turns, so a multi step loop can be driven without
// a provider.
type scriptedClient struct {
	mu    sync.Mutex
	turns [][]core.StreamEvent
	seen  []core.Request
}

func (c *scriptedClient) Name() string { return "scripted" }

func (c *scriptedClient) Stream(_ context.Context, req core.Request) (core.Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.seen = append(c.seen, req)
	if len(c.turns) == 0 {
		return &scriptedStream{events: []core.StreamEvent{
			{Kind: core.EventDone, StopReason: core.StopEndTurn},
		}}, nil
	}
	events := c.turns[0]
	c.turns = c.turns[1:]
	return &scriptedStream{events: events}, nil
}

type scriptedStream struct {
	events  []core.StreamEvent
	current core.StreamEvent
}

func (s *scriptedStream) Next() bool {
	if len(s.events) == 0 {
		return false
	}
	s.current, s.events = s.events[0], s.events[1:]
	return true
}

func (s *scriptedStream) Event() core.StreamEvent { return s.current }
func (s *scriptedStream) Err() error              { return nil }
func (s *scriptedStream) Close() error            { return nil }

func says(text string) []core.StreamEvent {
	return []core.StreamEvent{
		{Kind: core.EventText, Text: text},
		{Kind: core.EventDone, StopReason: core.StopEndTurn,
			Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}
}

func asksFor(name, input string) []core.StreamEvent {
	return []core.StreamEvent{
		{Kind: core.EventToolCall, ToolCall: &core.ToolCall{
			ID: "call-" + name, Name: name, Input: json.RawMessage(input),
		}},
		{Kind: core.EventDone, StopReason: core.StopToolUse,
			Usage: core.Usage{InputTokens: 10, OutputTokens: 5}},
	}
}

// countingTool records what it was asked and answers with whatever it was told to.
type countingTool struct {
	name   string
	kind   core.ToolKind
	schema string
	answer string
	fail   bool
	runErr error

	mu    sync.Mutex
	calls []string
}

func (t *countingTool) Name() string        { return t.name }
func (t *countingTool) Description() string { return "does " + t.name }
func (t *countingTool) Kind() core.ToolKind { return t.kind }

func (t *countingTool) Schema() json.RawMessage {
	if t.schema == "" {
		return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
	}
	return json.RawMessage(t.schema)
}

func (t *countingTool) Run(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
	t.mu.Lock()
	t.calls = append(t.calls, string(input))
	t.mu.Unlock()

	if t.runErr != nil {
		return core.ToolResult{}, t.runErr
	}
	return core.ToolResult{Content: t.answer, IsError: t.fail}, nil
}

func (t *countingTool) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.calls)
}

func registryWith(tools ...core.Tool) *core.ToolRegistry {
	r := core.NewToolRegistry()
	for _, tool := range tools {
		r.MustRegister(tool)
	}
	return r
}

func loop(client *scriptedClient, tools *core.ToolRegistry, level core.TrustLevel) *Loop {
	return &Loop{
		Client:    client,
		Tools:     tools,
		Trust:     level,
		Grants:    permission.NewGrants(),
		Trail:     permission.NewTrail(),
		Approver:  ApproverFunc(func(context.Context, permission.Request, permission.Decision) bool { return true }),
		AgentID:   "a1",
		SessionID: "s1",
	}
}

func ask(text string) core.Request {
	return core.Request{
		Model:    "m",
		Messages: []core.Message{{Role: core.RoleUser, Text: text}},
	}
}

// The whole point: model asks for a tool, the tool runs, the result goes back, the model answers.
func TestAMultiStepTaskCompletes(t *testing.T) {
	tool := &countingTool{name: "read_file", kind: core.ToolRead, answer: "package main"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("read_file", `{"path":"main.go"}`),
		says("It is a main package."),
	}}

	outcome, err := loop(client, registryWith(tool), core.TrustStandard).
		Run(context.Background(), ask("what is in main.go"), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if outcome.Stop != core.StopEndTurn {
		t.Errorf("stop = %s, want end-turn", outcome.Stop)
	}
	if outcome.Steps != 2 {
		t.Errorf("steps = %d, want 2", outcome.Steps)
	}
	if tool.count() != 1 {
		t.Errorf("the tool ran %d times, want 1", tool.count())
	}

	// The result has to have reached the second request, or the model answered without it.
	if len(client.seen) != 2 {
		t.Fatalf("%d requests", len(client.seen))
	}
	var sawResult bool
	for _, msg := range client.seen[1].Messages {
		for _, result := range msg.ToolResults {
			if strings.Contains(result.Content, "package main") {
				sawResult = true
			}
		}
	}
	if !sawResult {
		t.Error("the tool result never reached the model")
	}

	// And usage accumulates across every step, or a turn with ten tool calls reports the cost of one.
	if outcome.Usage.InputTokens != 20 {
		t.Errorf("usage = %+v, want both steps counted", outcome.Usage)
	}
}

// Without a step limit a confused model calling the same tool forever is an infinite loop.
func TestALoopThatGoesInCirclesIsStopped(t *testing.T) {
	tool := &countingTool{name: "read_file", kind: core.ToolRead, answer: "the same thing again"}

	turns := make([][]core.StreamEvent, 100)
	for i := range turns {
		turns[i] = asksFor("read_file", `{"path":"main.go"}`)
	}
	client := &scriptedClient{turns: turns}

	l := loop(client, registryWith(tool), core.TrustStandard)
	l.MaxSteps = 5

	outcome, err := l.Run(context.Background(), ask("go"), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.Steps > 5 {
		t.Errorf("ran %d steps despite a limit of 5", outcome.Steps)
	}
	// Said plainly, because "the model finished" and "we stopped it because it was going in
	// circles" are different things to tell a user.
	if outcome.LimitHit == "" {
		t.Error("the turn was stopped by a limit and nothing says so")
	}
	if !strings.Contains(outcome.LimitHit, "circles") {
		t.Errorf("limit message = %q", outcome.LimitHit)
	}
}

// Without a budget, going in circles is an infinite loop that spends real money.
func TestATokenBudgetStopsTheTurn(t *testing.T) {
	tool := &countingTool{name: "read_file", kind: core.ToolRead, answer: "x"}
	turns := make([][]core.StreamEvent, 100)
	for i := range turns {
		turns[i] = asksFor("read_file", `{"path":"main.go"}`)
	}

	l := loop(&scriptedClient{turns: turns}, registryWith(tool), core.TrustStandard)
	l.MaxTokens = 40 // each step reports 15

	outcome, err := l.Run(context.Background(), ask("go"), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.Usage.TotalTokens() > 100 {
		t.Errorf("spent %d tokens against a budget of 40", outcome.Usage.TotalTokens())
	}
	if !strings.Contains(outcome.LimitHit, "budget") {
		t.Errorf("limit message = %q, want the budget named", outcome.LimitHit)
	}
}

// A refused call is information: the model can try something within its remit, which is usually
// what it should do. Ending the turn would throw away everything it had worked out.
func TestADeniedToolIsReportedToTheModelRatherThanEndingTheTurn(t *testing.T) {
	tool := &countingTool{name: "edit_file", kind: core.ToolWrite, answer: "edited"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("edit_file", `{"path":"main.go"}`),
		says("I cannot change files, so here is what I would do instead."),
	}}

	outcome, err := loop(client, registryWith(tool), core.TrustReadOnly).
		Run(context.Background(), ask("fix it"), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.Stop != core.StopEndTurn {
		t.Errorf("stop = %s, want the turn to have continued", outcome.Stop)
	}
	if tool.count() != 0 {
		t.Error("a denied tool ran anyway")
	}

	var told bool
	for _, msg := range client.seen[1].Messages {
		for _, result := range msg.ToolResults {
			if result.IsError && strings.Contains(result.Content, "refused") {
				told = true
			}
		}
	}
	if !told {
		t.Error("the model was not told why the call did not run, so it cannot adapt")
	}
}

// An unattended run that approved by default would be an unattended run with broad trust, whatever
// the profile said.
func TestNothingIsApprovedWhenNobodyIsThereToAsk(t *testing.T) {
	tool := &countingTool{name: "run_command", kind: core.ToolExecute, answer: "ran"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("run_command", `{"command":"rm -rf /"}`),
		says("done"),
	}}

	l := loop(client, registryWith(tool), core.TrustStandard)
	l.Approver = nil // nobody there

	if _, err := l.Run(context.Background(), ask("go"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if tool.count() != 0 {
		t.Error("a command that needed approval ran with nobody there to approve it")
	}
}

// A tool that failed has told the model something useful. Crashing the turn throws that away.
func TestAFailingToolIsReportedRatherThanCrashingTheTurn(t *testing.T) {
	failing := &countingTool{name: "run_command", kind: core.ToolExecute,
		answer: "exit 1: no such file", fail: true}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("run_command", `{"command":"cat missing"}`),
		says("That file does not exist, let me look for it."),
	}}

	outcome, err := loop(client, registryWith(failing), core.TrustBroad).
		Run(context.Background(), ask("go"), nil)
	if err != nil {
		t.Fatalf("a failing tool ended the turn with a Go error: %v", err)
	}
	if outcome.Stop != core.StopEndTurn {
		t.Errorf("stop = %s", outcome.Stop)
	}
}

// A tool that could not run at all is different from one that ran and failed, and both have to
// reach the model or it waits for an answer that is never coming.
func TestAToolThatCannotRunStillProducesAResult(t *testing.T) {
	broken := &countingTool{name: "run_command", kind: core.ToolExecute,
		runErr: errors.New("the binary is missing")}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("run_command", `{"command":"x"}`),
		says("understood"),
	}}

	l := loop(client, registryWith(broken), core.TrustBroad)
	if _, err := l.Run(context.Background(), ask("go"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var got bool
	for _, msg := range client.seen[1].Messages {
		for _, result := range msg.ToolResults {
			if strings.Contains(result.Content, "could not run") {
				got = true
			}
		}
	}
	if !got {
		t.Error("a tool that could not run produced no result, so the model is waiting")
	}
}

// "Unknown tool" without the name leaves the model guessing which of the three it just asked for
// was wrong.
func TestAnUnknownToolIsNamedInTheRefusal(t *testing.T) {
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("delete_everything", `{}`),
		says("understood"),
	}}

	l := loop(client, registryWith(&countingTool{name: "read_file", kind: core.ToolRead}),
		core.TrustBroad)
	if _, err := l.Run(context.Background(), ask("go"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var named bool
	for _, msg := range client.seen[1].Messages {
		for _, result := range msg.ToolResults {
			if strings.Contains(result.Content, "delete_everything") {
				named = true
			}
		}
	}
	if !named {
		t.Error("the refusal did not name the tool the model asked for")
	}
}

// A turn that was stopped should not run the three tools it had queued up.
func TestCancellationStopsBeforeTheNextTool(t *testing.T) {
	tool := &countingTool{name: "run_command", kind: core.ToolExecute, answer: "ran"}

	ctx, cancel := context.WithCancel(context.Background())
	client := &scriptedClient{turns: [][]core.StreamEvent{{
		{Kind: core.EventToolCall, ToolCall: &core.ToolCall{
			ID: "c1", Name: "run_command", Input: json.RawMessage(`{"command":"one"}`)}},
		{Kind: core.EventToolCall, ToolCall: &core.ToolCall{
			ID: "c2", Name: "run_command", Input: json.RawMessage(`{"command":"two"}`)}},
		{Kind: core.EventToolCall, ToolCall: &core.ToolCall{
			ID: "c3", Name: "run_command", Input: json.RawMessage(`{"command":"three"}`)}},
		{Kind: core.EventDone, StopReason: core.StopToolUse},
	}}}

	// Standard trust, deliberately: broad runs shell commands without asking, so the approver would
	// never be reached and the cancel would never fire.
	l := loop(client, registryWith(tool), core.TrustStandard)
	l.Approver = ApproverFunc(func(context.Context, permission.Request, permission.Decision) bool {
		cancel() // stopped part way through the batch
		return true
	})

	outcome, err := l.Run(ctx, ask("go"), nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if outcome.Stop != core.StopCancelled {
		t.Errorf("stop = %s, want cancelled", outcome.Stop)
	}
	if tool.count() > 1 {
		t.Errorf("%d tools ran after cancellation, want at most the one already in flight",
			tool.count())
	}
}

// A call that produced no audit entry is a call nobody can find afterwards.
func TestEveryCallProducesExactlyOneAuditEntry(t *testing.T) {
	allowed := &countingTool{name: "read_file", kind: core.ToolRead, answer: "content"}
	denied := &countingTool{name: "edit_file", kind: core.ToolWrite, answer: "edited"}

	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("read_file", `{"path":"a.go"}`),
		asksFor("edit_file", `{"path":"a.go"}`),
		asksFor("nonexistent", `{}`),
		says("done"),
	}}

	l := loop(client, registryWith(allowed, denied), core.TrustReadOnly)
	if _, err := l.Run(context.Background(), ask("go"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	entries := l.Trail.Entries()
	if len(entries) != 3 {
		t.Fatalf("%d audit entries, want one per call: %+v", len(entries), entries)
	}

	byTool := map[string]permission.Entry{}
	for _, entry := range entries {
		byTool[entry.Tool] = entry
	}
	if byTool["read_file"].Outcome != permission.Allow || !byTool["read_file"].Ran {
		t.Errorf("read = %+v", byTool["read_file"])
	}
	if byTool["edit_file"].Outcome != permission.Deny || byTool["edit_file"].Ran {
		t.Errorf("a denied write is recorded as %+v", byTool["edit_file"])
	}
	if byTool["nonexistent"].Outcome != permission.Deny {
		t.Errorf("an unknown tool is recorded as %+v", byTool["nonexistent"])
	}

	// The trail answers "what did this agent actually do", including what it tried and could not.
	if len(l.Trail.Refused()) != 2 {
		t.Errorf("%d refusals recorded, want 2", len(l.Trail.Refused()))
	}
}

// The gap between a tool being asked for and being approved is exactly when a user wants to see
// what is being proposed.
func TestTheObserverSeesEverythingAsItHappens(t *testing.T) {
	tool := &countingTool{name: "read_file", kind: core.ToolRead, answer: "content"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("read_file", `{"path":"a.go"}`),
		says("here it is"),
	}}

	obs := &recorder{}
	if _, err := loop(client, registryWith(tool), core.TrustStandard).
		Run(context.Background(), ask("go"), obs); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if obs.requested != 1 {
		t.Errorf("%d tools announced before running, want 1", obs.requested)
	}
	if obs.finished != 1 {
		t.Errorf("%d tools reported finished, want 1", obs.finished)
	}
	if obs.text != "here it is" {
		t.Errorf("text = %q", obs.text)
	}
	// A running total has to be available before the turn ends, or a long turn shows nothing.
	if obs.steps != 2 {
		t.Errorf("%d steps reported, want 2", obs.steps)
	}
}

type recorder struct {
	mu                         sync.Mutex
	text                       string
	requested, finished, steps int
}

func (r *recorder) Text(chunk string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.text += chunk
}

func (r *recorder) Thinking(string) {}

func (r *recorder) ToolRequested(core.ToolCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requested++
}

func (r *recorder) ToolFinished(core.ToolCall, core.ToolResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished++
}

func (r *recorder) StepFinished(core.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps++
}

// An approval granted during a turn should not be asked about again in the same turn.
func TestAnApprovalIsNotAskedForTwice(t *testing.T) {
	tool := &countingTool{name: "run_command", kind: core.ToolExecute, answer: "ran"}
	client := &scriptedClient{turns: [][]core.StreamEvent{
		asksFor("run_command", `{"command":"make test"}`),
		asksFor("run_command", `{"command":"make test"}`),
		says("done"),
	}}

	var asked int
	l := loop(client, registryWith(tool), core.TrustStandard)
	l.Approver = ApproverFunc(func(context.Context, permission.Request, permission.Decision) bool {
		asked++
		return true
	})

	if _, err := l.Run(context.Background(), ask("go"), nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if asked != 1 {
		t.Errorf("asked %d times for the same command, want 1", asked)
	}
	if tool.count() != 2 {
		t.Errorf("the tool ran %d times, want 2", tool.count())
	}
}

// The permission layer works out which paths a call touches by convention, so every tool that takes
// a path has to use a name the convention knows. One that did not would silently lose its path
// scoping and be approved too broadly.
func TestPathsAreFoundByTheConventionalArgumentNames(t *testing.T) {
	for _, input := range []string{
		`{"path":"src/main.go"}`,
		`{"file":"src/main.go"}`,
		`{"directory":"src"}`,
	} {
		if got := pathsIn(json.RawMessage(input)); len(got) == 0 {
			t.Errorf("no path found in %s, so the call would be approved without path scoping", input)
		}
	}
	if got := commandIn(json.RawMessage(`{"command":"make test"}`)); got != "make test" {
		t.Errorf("command = %q", got)
	}
}

// The done event is by contract the last thing a stream has to say. Reading past it means depending
// on the stream also reporting that it is finished, promptly, and one that simply stops producing
// events would hang the turn rather than ending it.
//
// Found by a session test that hung for ten minutes.
func TestAStreamThatStopsAfterDoneDoesNotHangTheTurn(t *testing.T) {
	blocked := make(chan struct{})
	defer close(blocked)

	client := &blockingAfterDone{blocked: blocked}
	l := &Loop{Client: client, Tools: core.NewToolRegistry(), Trust: core.TrustReadOnly}

	finished := make(chan Outcome, 1)
	go func() {
		outcome, err := l.Run(context.Background(), ask("go"), nil)
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		finished <- outcome
	}()

	select {
	case outcome := <-finished:
		if outcome.Stop != core.StopEndTurn {
			t.Errorf("stop = %s", outcome.Stop)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the turn did not end after the done event, so it was reading past it")
	}
}

// blockingAfterDone produces a done event and then never returns from Next again.
type blockingAfterDone struct{ blocked chan struct{} }

func (c *blockingAfterDone) Name() string { return "blocking" }

func (c *blockingAfterDone) Stream(context.Context, core.Request) (core.Stream, error) {
	return &blockedStream{blocked: c.blocked}, nil
}

type blockedStream struct {
	blocked chan struct{}
	sent    bool
	current core.StreamEvent
}

func (s *blockedStream) Next() bool {
	if !s.sent {
		s.sent = true
		s.current = core.StreamEvent{Kind: core.EventDone, StopReason: core.StopEndTurn}
		return true
	}
	<-s.blocked
	return false
}

func (s *blockedStream) Event() core.StreamEvent { return s.current }
func (s *blockedStream) Err() error              { return nil }
func (s *blockedStream) Close() error            { return nil }
