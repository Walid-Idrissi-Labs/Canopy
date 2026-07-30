package copilot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// writeExecutable puts something on disk that FindCLI will accept.
func writeExecutable(path string) error {
	return os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755)
}

func canopyTools() []core.ToolDefinition {
	return []core.ToolDefinition{
		{Name: "read_file", Description: "read a file", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "run_command", Description: "run a command"},
	}
}

// The claim this whole route rests on, held against the values that actually ship.
//
// ModeEmpty is the SDK's promise that a session starts with no built-in tools, and the allowlist is
// Canopy's requirement that it stays that way. What could go wrong is not the mode, it is the list:
// the obvious empty list also excludes Canopy's own tools, and the obvious "allow everything" list
// hands the model a shell. This holds that the list names Canopy's tools and nothing else, by source
// rather than by name, so a vendor tool that arrives under a new name in a later release is still
// outside it.
func TestTheToolAllowlistNamesCanopysOwnToolsAndNoVendorSourceAtAll(t *testing.T) {
	allowed := AllowedTools(canopyTools())
	if len(allowed) == 0 {
		t.Fatal("a session with tools was given an empty allowlist, which is a session with none")
	}
	for _, entry := range allowed {
		if !strings.HasPrefix(entry, "custom:") {
			t.Errorf("the allowlist contains %q, and anything that is not custom: is somebody else's "+
				"tool reaching the model", entry)
		}
	}
	for _, banned := range []string{"builtin:", "mcp:"} {
		for _, entry := range allowed {
			if strings.HasPrefix(entry, banned) {
				t.Errorf("the allowlist admits %s tools through %q", banned, entry)
			}
		}
	}
}

// The other half of the same claim. A conversation with no tools is a legitimate thing to want, and
// the way to say it is an empty list rather than no list: ModeEmpty refuses to create a session at
// all when AvailableTools is nil, which is the SDK making the decision explicit and would otherwise
// surface as a session that would not start.
func TestAConversationWithNoToolsAsksForNoneRatherThanForWhateverIsGoing(t *testing.T) {
	allowed := AllowedTools(nil)
	if allowed == nil {
		t.Fatal("a nil allowlist is what ModeEmpty rejects, so the session would never be created")
	}
	if len(allowed) != 0 {
		t.Errorf("a conversation with no tools asked for %v", allowed)
	}
}

// Every switch that could let somebody else's agent touch this machine, held one at a time.
//
// ModeEmpty already defaults most of these, and they are set again on purpose: the defaults are the
// SDK's promise and these are Canopy's requirement, and writing them down separately is what makes a
// future release that relaxes one of them a failing test rather than a silent widening of what the
// vendor's agent may do in somebody's repository.
func TestASessionIsCreatedWithEveryFeatureThatTouchesTheMachineSwitchedOff(t *testing.T) {
	state := t.TempDir()
	config := sessionConfig(Conversation{
		Token: core.NewSecret("gho_TOKEN"),
		Model: "gpt-5.2-codex",
		Tools: canopyTools(),
	}, state)

	for _, what := range []struct {
		name string
		flag *bool
		want bool
	}{
		{"config discovery", config.EnableConfigDiscovery, false},
		{"file hooks", config.EnableFileHooks, false},
		{"host git operations", config.EnableHostGitOperations, false},
		{"the cross-session store", config.EnableSessionStore, false},
		{"skills", config.EnableSkills, false},
		{"on-demand instruction discovery", config.EnableOnDemandInstructionDiscovery, false},
		{"session telemetry", config.EnableSessionTelemetry, false},
		{"skipping embedding retrieval", config.SkipEmbeddingRetrieval, true},
		{"skipping custom instructions", config.SkipCustomInstructions, true},
	} {
		if what.flag == nil {
			t.Errorf("%s was left to the runtime's default rather than decided here", what.name)
			continue
		}
		if *what.flag != what.want {
			t.Errorf("%s is %v, want %v", what.name, *what.flag, what.want)
		}
	}

	if config.Memory == nil || config.Memory.Enabled {
		t.Error("memory is on, so the vendor keeps something about this user between conversations")
	}
	if config.MCPServers != nil {
		t.Error("MCP servers were configured, and none of them would be Canopy's")
	}
	if config.WorkingDirectory != state {
		t.Errorf("the session runs in %q rather than in Canopy's own directory, so the runtime's "+
			"unconditional loading of instruction files reads the user's project", config.WorkingDirectory)
	}
	if config.Streaming == nil || !*config.Streaming {
		t.Error("streaming is off, so the reply lands in one piece at the end and the screen looks dead")
	}
	if config.ClientName != userAgent {
		t.Errorf("the session identifies itself as %q, want %q", config.ClientName, userAgent)
	}
}

// The token goes on the session as well as on the client, because that is the field that makes the
// turn bill this user's seat rather than whatever the machine happens to be signed in to.
func TestTheUsersOwnGrantIsWhatTheSessionAuthenticatesWith(t *testing.T) {
	config := sessionConfig(Conversation{Token: core.NewSecret("gho_THE-USERS-TOKEN")}, t.TempDir())
	if config.GitHubToken != "gho_THE-USERS-TOKEN" {
		t.Errorf("the session was created with %q, and without the user's own grant the turn bills "+
			"somebody else", config.GitHubToken)
	}
}

// Declared and not implemented, which is what keeps Canopy's permission gate in the path. A handler
// here would run Canopy's tools inside the vendor's loop with the gate bypassed, which is exactly
// the arrangement Q-23 warns about.
func TestCanopysToolsAreDeclaredToTheAgentWithNoWayForItToRunThem(t *testing.T) {
	config := sessionConfig(Conversation{Tools: canopyTools()}, t.TempDir())
	if len(config.Tools) != 2 {
		t.Fatalf("%d tools were declared, want both of Canopy's", len(config.Tools))
	}
	for _, tool := range config.Tools {
		if tool.Handler != nil {
			t.Errorf("tool %q was given a handler, so its calls run inside the vendor's loop and "+
				"never reach Canopy's trust level or its approver", tool.Name)
		}
	}
	if config.Tools[0].Parameters == nil {
		t.Error("the schema was dropped, so the model is asked to call a tool it cannot fill in")
	}
}

// A tool whose schema will not parse loses that tool rather than the whole conversation. The runtime
// rejects a session over one malformed declaration, and one missing tool beats no session.
func TestAToolWithAnUnreadableSchemaLosesItsSchemaRatherThanTheSession(t *testing.T) {
	config := sessionConfig(Conversation{Tools: []core.ToolDefinition{
		{Name: "broken", InputSchema: []byte("{not json")},
	}}, t.TempDir())
	if len(config.Tools) != 1 {
		t.Fatalf("%d tools survived", len(config.Tools))
	}
	if config.Tools[0].Parameters != nil {
		t.Error("an unparseable schema was passed on")
	}
}

// The third lock on the door ModeEmpty and the allowlist already close. A permission request Canopy
// does not recognise arriving at all means one of the first two has stopped working, and the safe
// answer to that is no.
func TestThePermissionHandlerApprovesCanopysToolsAndRefusesEverythingElse(t *testing.T) {
	handle := refuseAnythingNotCanopys(canopyTools())

	decision, err := handle(&rpc.PermissionRequestCustomTool{ToolName: "read_file"}, sdk.PermissionInvocation{})
	if err != nil {
		t.Fatalf("deciding on one of Canopy's own tools: %v", err)
	}
	if _, ok := decision.(*rpc.PermissionDecisionApproveOnce); !ok {
		t.Errorf("Canopy's own tool was answered with %T, so the turn cannot use its own tools", decision)
	}

	for _, request := range []sdk.PermissionRequest{
		&rpc.PermissionRequestShell{},
		&rpc.PermissionRequestWrite{},
		&rpc.PermissionRequestURL{},
		&rpc.PermissionRequestCustomTool{ToolName: "bash"},
	} {
		decision, err := handle(request, sdk.PermissionInvocation{})
		if err != nil {
			t.Fatalf("deciding on %T: %v", request, err)
		}
		reject, ok := decision.(*rpc.PermissionDecisionReject)
		if !ok {
			t.Errorf("%T was answered with %T, want a rejection", request, decision)
			continue
		}
		if reject.Feedback == nil || *reject.Feedback == "" {
			t.Errorf("%T was refused with no reason, so the model retries rather than adjusts", request)
		}
	}
}

// Canopy's effort levels and the vendor's do not have the same number of rungs. Max maps to the
// highest that exists rather than failing the turn, because somebody who asked for the most thinking
// available and got the most thinking available has been served.
func TestEffortMapsOntoTheVendorsLevelsAndMaxTakesTheHighestThereIs(t *testing.T) {
	for effort, want := range map[core.Effort]string{
		core.EffortDefault: "",
		core.EffortLow:     "low",
		core.EffortMedium:  "medium",
		core.EffortHigh:    "high",
		core.EffortXHigh:   "xhigh",
		core.EffortMax:     "xhigh",
	} {
		if got := effortFor(effort); got != want {
			t.Errorf("effort %q became %q, want %q", effort, got, want)
		}
	}
}

// Thinking off is the reasoning summary suppressed, which is the only thing the vendor offers that
// means it.
func TestTurningThinkingOffSuppressesTheVendorsReasoningSummary(t *testing.T) {
	on := sessionConfig(Conversation{}, t.TempDir())
	if on.ReasoningSummary == sdk.ReasoningSummaryNone {
		t.Error("thinking was suppressed on a request that did not ask for that")
	}
	off := sessionConfig(Conversation{DisableThinking: true}, t.TempDir())
	if off.ReasoningSummary != sdk.ReasoningSummaryNone {
		t.Errorf("thinking off produced %q", off.ReasoningSummary)
	}
}

// Canopy's system prompt is a mode's rules, which is an addition to how the vendor's agent behaves
// rather than a replacement for it. Replacing the whole message would also throw away the tool
// instructions the agent needs to use Canopy's own tools properly.
func TestCanopysSystemPromptIsAddedToTheVendorsRatherThanReplacingIt(t *testing.T) {
	config := sessionConfig(Conversation{System: "you are planning and may not edit"}, t.TempDir())
	if config.SystemMessage == nil {
		t.Fatal("the mode's prompt never reached the session, so the level is enforced and never explained")
	}
	if config.SystemMessage.Mode != "append" {
		t.Errorf("the system message mode is %q, want append", config.SystemMessage.Mode)
	}
	if !strings.Contains(config.SystemMessage.Content, "may not edit") {
		t.Errorf("the prompt arrived as %q", config.SystemMessage.Content)
	}
}

// An absent binary is answerable with one sentence naming what to install. The SDK's own answer is
// Go's exec error wrapped in "failed to start CLI server", which names a thing the user has never
// heard of and does not say to install it.
func TestAnAbsentRuntimeSaysWhatToInstallRatherThanThatAnExecFailed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv(CLIPathEnvVar, "")

	_, err := FindCLI()
	if err == nil {
		t.Fatal("a machine with no copilot binary reported one")
	}
	for _, want := range []string{"@github/copilot", CLIPathEnvVar} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not mention %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "executable file not found") {
		t.Errorf("the failure is Go's exec error rather than an answer: %v", err)
	}
}

// The SDK's own override is honoured, so Canopy's discovery and the SDK's cannot find different
// binaries. A path that is set and wrong says so rather than silently falling through to whatever is
// on PATH, because a stale override is a thing somebody has to be told about.
func TestAnOverriddenRuntimePathIsUsedAndSaysSoWhenItIsWrong(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "copilot")
	if err := writeExecutable(binary); err != nil {
		t.Fatalf("preparing a fake binary: %v", err)
	}

	t.Setenv(CLIPathEnvVar, binary)
	got, err := FindCLI()
	if err != nil {
		t.Fatalf("FindCLI with %s set: %v", CLIPathEnvVar, err)
	}
	if got != binary {
		t.Errorf("FindCLI returned %q, want the override", got)
	}

	t.Setenv(CLIPathEnvVar, filepath.Join(dir, "not-there"))
	if _, err := FindCLI(); err == nil {
		t.Error("an override pointing at nothing fell through silently")
	}
}

// Usage that the vendor did not report is absent rather than zero, and core has a type for the
// difference. Nothing here sets CostKnown, and nothing should.
func TestUsageTheVendorDidNotReportIsAbsentRatherThanZero(t *testing.T) {
	input, output := int64(11), int64(3)
	usage := usageOf(&sdk.AssistantUsageData{InputTokens: &input, OutputTokens: &output})
	if usage.InputTokens != 11 || usage.OutputTokens != 3 {
		t.Errorf("usage read as %+v", usage)
	}
	if usage.CacheReadTokens != 0 || usage.CostKnown {
		t.Errorf("fields the vendor said nothing about were invented: %+v", usage)
	}
}

// A tool call has to be resolvable from a result that has travelled through a tool registry, a
// permission prompt and back, so the identifier Canopy carries is the one the vendor resolves on.
func TestAToolCallCarriesTheIdentifierThatResolvesItRatherThanTheOneThatNamesIt(t *testing.T) {
	agent := &sdkAgent{events: make(chan Event, 4)}
	agent.handle(sdk.SessionEvent{Data: &rpc.ExternalToolRequestedData{
		RequestID:  "request-abc",
		ToolCallID: "call-xyz",
		ToolName:   "read_file",
		Arguments:  map[string]any{"path": "go.mod"},
	}})

	event := <-agent.events
	if event.Kind != EventToolCall || event.Call == nil {
		t.Fatalf("the tool request arrived as %+v", event)
	}
	if event.Call.ID != "request-abc" {
		t.Errorf("the call carries %q, and HandlePendingToolCall resolves on the request id",
			event.Call.ID)
	}
	var arguments map[string]any
	if err := json.Unmarshal(event.Call.Input, &arguments); err != nil {
		t.Fatalf("the arguments did not survive as JSON: %v", err)
	}
	if arguments["path"] != "go.mod" {
		t.Errorf("the arguments arrived as %v", arguments)
	}
}

// A sub-agent's output is the vendor's agent talking to itself, and presenting it as the answer
// would attribute one thing to another. ModeEmpty leaves no way to spawn one, so this is the belt
// beside that brace.
func TestASubAgentsOutputIsNotPresentedAsTheAnswer(t *testing.T) {
	agent := &sdkAgent{events: make(chan Event, 4)}
	other := "sub-1"
	agent.handle(sdk.SessionEvent{
		AgentID: &other,
		Data:    &rpc.AssistantMessageDeltaData{DeltaContent: "I am a helper"},
	})
	select {
	case event := <-agent.events:
		t.Errorf("a sub-agent's words were forwarded as %+v", event)
	default:
	}
}

// The trap S-04 found on their route, checked on this one because the cost of getting it wrong is
// the same and worse. internal/agent/loop.go invokes every tool call event it is handed, so any
// vendor event that becomes core.EventToolCall for a tool the vendor already ran would have Canopy
// run it a second time.
//
// The only event here that becomes a tool call is the one that means "I am waiting for you to run
// this", which is the whole reason Canopy's tools are declared with no handler. The events that mean
// "I am running this" and "I have run this" produce nothing at all. In this session they cannot fire
// for anything but Canopy's own tools anyway, since ModeEmpty and the allowlist leave the vendor
// with none of its own, but the mapping is what makes that a property rather than a coincidence.
func TestNoEventThatMeansAToolAlreadyRanBecomesAToolCallCanopyWouldRunAgain(t *testing.T) {
	agent := &sdkAgent{events: make(chan Event, 8)}

	for _, data := range []sdk.SessionEventData{
		&rpc.ToolExecutionStartData{ToolCallID: "call-1", ToolName: "read_file"},
		&rpc.ToolExecutionProgressData{ToolCallID: "call-1", ProgressMessage: "reading"},
		&rpc.ToolExecutionCompleteData{ToolCallID: "call-1", Success: true},
		&rpc.ExternalToolCompletedData{RequestID: "request-1"},
		&rpc.PermissionRequestedData{RequestID: "perm-1"},
		&rpc.PermissionCompletedData{RequestID: "perm-1"},
	} {
		agent.handle(sdk.SessionEvent{Data: data})
		select {
		case event := <-agent.events:
			t.Errorf("%T produced %+v, and a tool call here would be run a second time by "+
				"internal/agent/loop.go", data, event)
		default:
		}
	}

	// The one that does, and must.
	agent.handle(sdk.SessionEvent{Data: &rpc.ExternalToolRequestedData{
		RequestID: "request-2", ToolName: "read_file",
	}})
	select {
	case event := <-agent.events:
		if event.Kind != EventToolCall {
			t.Errorf("a pending tool request produced %+v", event)
		}
	default:
		t.Error("a pending tool request produced nothing, so the turn waits forever")
	}
}
