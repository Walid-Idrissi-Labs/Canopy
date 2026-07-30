package acp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func TestADelegatedTurnsReplyArrivesOverACP(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) {
		a.thought("the answer is a greeting")
		a.text("hello ")
		a.text("back")
		a.end(stopEndTurn, nil)
	}})

	if got.text != "hello back" {
		t.Errorf("the reply was %q, want %q", got.text, "hello back")
	}
	if got.thinking != "the answer is a greeting" {
		t.Errorf("the thinking was %q", got.thinking)
	}
	if got.stop != core.StopEndTurn {
		t.Errorf("the turn stopped with %q, want %q", got.stop, core.StopEndTurn)
	}
	if got.err != nil {
		t.Errorf("a turn that finished normally reported %v", got.err)
	}
}

func TestTheHandshakeNamesCanopyAndOffersNoFilesystemOrTerminal(t *testing.T) {
	t.Parallel()

	a := &agent{script: func(a *agent) { a.end(stopEndTurn, nil) }}
	ask(t, a)

	params := paramsOf[initializeParams](t, a.sent(), methodInitialize)
	if params.ProtocolVersion != 1 {
		t.Errorf("Canopy asked for protocol version %d, want 1", params.ProtocolVersion)
	}
	if params.ClientInfo.Name != "canopy" {
		t.Errorf("Canopy introduced itself as %q, and identifying as anything else is the one "+
			"behaviour D-51 says would make this route indefensible", params.ClientInfo.Name)
	}
	if params.ClientCapabilities.FS.ReadTextFile || params.ClientCapabilities.FS.WriteTextFile {
		t.Error("Canopy advertised a filesystem capability, which would put it in the path of tool " +
			"calls it has no gate for")
	}
	if params.ClientCapabilities.Terminal {
		t.Error("Canopy advertised a terminal capability, which would mean running the delegated " +
			"agent's commands on its behalf")
	}
}

// The Q-23 answer, in the only form a protocol can express it: there is no door.
func TestNoCanopyToolIsOfferedToADelegatedTurn(t *testing.T) {
	t.Parallel()

	a := &agent{script: func(a *agent) { a.end(stopEndTurn, nil) }}
	a.t = t
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	request := turn("edit the file")
	request.Tools = []core.ToolDefinition{
		{Name: "write_file", Description: "writes a file", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "run", Description: "runs a command", InputSchema: []byte(`{"type":"object"}`)},
	}

	stream, err := client.Stream(context.Background(), request)
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()
	drain(t, stream)

	session := paramsOf[newSessionParams](t, a.sent(), methodSessionNew)
	if len(session.MCPServers) != 0 {
		t.Errorf("the session was opened with %d MCP servers, and MCP is the only way ACP lets a "+
			"client hand its own tools to an agent", len(session.MCPServers))
	}

	for _, sent := range a.sent() {
		if strings.Contains(string(sent.Params), "write_file") {
			t.Fatalf("a Canopy tool definition reached the delegated agent in %s: %s",
				sent.Method, sent.Params)
		}
	}
}

func TestCanopyDeclinesToApproveTheDelegatedAgentsToolCalls(t *testing.T) {
	t.Parallel()

	var outcome permissionOutcome
	got := ask(t, &agent{script: func(a *agent) {
		outcome = a.ask("Write src/main.go", []permissionOption{
			{OptionID: "yes", Name: "Allow", Kind: "allow_once"},
			{OptionID: "yes-always", Name: "Always allow", Kind: "allow_always"},
			{OptionID: "no", Name: "Reject", Kind: "reject_once"},
		})
		a.text("understood")
		a.end(stopEndTurn, nil)
	}})

	if outcome.Outcome != "selected" || outcome.OptionID != "no" {
		t.Fatalf("Canopy answered a permission request with %+v, and anything other than the "+
			"reject_once option is Canopy approving a tool call it does not gate", outcome)
	}
	if !got.noticed("declined") {
		t.Errorf("a refused permission request was not reported to the reader: %q", got.notices)
	}
	if got.text != "understood" {
		t.Errorf("the turn did not continue after the refusal: %q", got.text)
	}
}

func TestARefusalIsChosenByItsKindRatherThanItsLabel(t *testing.T) {
	t.Parallel()

	// The labels are deliberately misleading and the kinds are correct, which is the case a
	// comparison against a display string gets wrong.
	var outcome permissionOutcome
	ask(t, &agent{script: func(a *agent) {
		outcome = a.ask("Delete everything", []permissionOption{
			{OptionID: "a", Name: "No, thank you", Kind: "allow_once"},
			{OptionID: "b", Name: "Go ahead", Kind: "reject_once"},
		})
		a.end(stopEndTurn, nil)
	}})

	if outcome.OptionID != "b" {
		t.Errorf("Canopy picked option %q by its label rather than by its kind", outcome.OptionID)
	}
}

func TestAPermissionRequestWithNoWayToDeclineStopsTheTurn(t *testing.T) {
	t.Parallel()

	var outcome permissionOutcome
	got := ask(t, &agent{script: func(a *agent) {
		outcome = a.ask("Push to main", []permissionOption{
			{OptionID: "yes", Name: "Allow", Kind: "allow_once"},
		})
		a.end(stopCancelled, nil)
	}})

	if outcome.Outcome != "cancelled" {
		t.Errorf("Canopy answered %+v, and selecting from a list whose every entry is a yes is "+
			"approving something on the user's behalf", outcome)
	}
	if !got.noticed("stopped the turn") {
		t.Errorf("stopping the turn was not explained to the reader: %q", got.notices)
	}
}

// A delegated tool call is a report, never a request. internal/agent/loop.go runs every
// core.EventToolCall it is handed, so emitting one here would run somebody else's tool call again.
func TestAToolTheDelegatedAgentRanIsReportedAndNeverHandedBackToBeRun(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) {
		a.say(map[string]any{
			"sessionUpdate": updateToolCall,
			"toolCallId":    "call-1",
			"title":         "Read internal/core/provider.go",
			"kind":          "read",
			"status":        "pending",
		})
		a.say(map[string]any{
			"sessionUpdate": updateToolCallUpdate,
			"toolCallId":    "call-1",
			"status":        "completed",
		})
		a.text("done")
		a.end(stopEndTurn, nil)
	}})

	if len(got.toolCalls) != 0 {
		t.Fatalf("a delegated turn produced %d tool calls for Canopy to run, which would run the "+
			"vendor's own call a second time", len(got.toolCalls))
	}
	if !got.noticed("Read internal/core/provider.go") {
		t.Errorf("the delegated agent ran a tool and the reader was never told: %q", got.notices)
	}
	if count := strings.Count(strings.Join(got.notices, "\n"), "call-1"); count != 0 {
		t.Errorf("the tool call id leaked into a notice %d times, which is a protocol detail", count)
	}
}

func TestATurnSaysWhoseSubscriptionItRunsOnBeforeItSaysAnythingElse(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) {
		a.text("hello")
		a.end(stopEndTurn, nil)
	}})

	if len(got.notices) == 0 {
		t.Fatal("a delegated turn said nothing about being delegated")
	}
	first := got.notices[0]
	for _, phrase := range []string{"your own Claude Code", "someone@example.com", "usage limits"} {
		if !strings.Contains(first, phrase) {
			t.Errorf("the opening notice does not mention %q:\n%s", phrase, first)
		}
	}
	if !strings.Contains(first, "not in the path") {
		t.Errorf("the opening notice does not say Canopy's permissions are not in force:\n%s", first)
	}
}

func TestATurnOnAnAPIAccountSaysItIsBilledRatherThanMetered(t *testing.T) {
	t.Parallel()

	install := installed()
	install.Account = Account{Email: "team@example.com", Method: "console"}

	a := &agent{t: t, script: func(a *agent) { a.end(stopEndTurn, nil) }}
	client := New(install, WithWorkspace(t.TempDir()))
	a.launch(client)

	stream, err := client.Stream(context.Background(), turn("hello"))
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()

	got := drain(t, stream)
	if !got.noticed("billed to that account per token") {
		t.Errorf("a delegated turn on an API account did not say it is billed: %q", got.notices)
	}
}

// The cost clause. The tokens are real and are reported; the dollar figure is not Canopy's to state.
func TestADelegatedTurnReportsItsTokensAndNeverClaimsToKnowTheirCost(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) {
		a.text("pong")
		a.end(stopEndTurn, &promptUsage{
			InputTokens: 2, OutputTokens: 4, CachedReadTokens: 15273, CachedWriteTokens: 5723,
		})
	}})

	if got.usage.InputTokens != 2 || got.usage.OutputTokens != 4 {
		t.Errorf("the turn reported %+v, want the counts the agent gave", got.usage)
	}
	if got.usage.CacheReadTokens != 15273 || got.usage.CacheWriteTokens != 5723 {
		t.Errorf("the cache counts were dropped: %+v", got.usage)
	}
	if got.usage.CostKnown {
		t.Error("a delegated turn claimed to know what it cost. The tokens are metered against a " +
			"plan that is billed monthly, so any figure here is a number about somebody else's bill")
	}
	if got.usage.CostUSD != 0 {
		t.Errorf("a delegated turn reported a cost of %v", got.usage.CostUSD)
	}
}

func TestATurnWhoseAgentReportsNoTokensIsATurnWithNoTokenCount(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) {
		a.text("pong")
		a.end(stopEndTurn, nil)
	}})

	if !got.usage.IsZero() {
		t.Errorf("a bridge that reported no usage produced %+v rather than nothing", got.usage)
	}
}

func TestEveryWayATurnCanEndArrivesAsADoneEventCanopyUnderstands(t *testing.T) {
	t.Parallel()

	cases := []struct {
		acp  string
		want core.StopReason
	}{
		{stopEndTurn, core.StopEndTurn},
		{stopMaxTokens, core.StopMaxTokens},
		{stopMaxTurnRequests, core.StopMaxTokens},
		{stopRefusal, core.StopRefusal},
		{stopCancelled, core.StopCancelled},
	}

	for _, tc := range cases {
		t.Run(tc.acp, func(t *testing.T) {
			t.Parallel()
			got := ask(t, &agent{script: func(a *agent) { a.end(tc.acp, nil) }})
			if got.stop != tc.want {
				t.Errorf("%q became %q, want %q", tc.acp, got.stop, tc.want)
			}
			if got.err != nil {
				t.Errorf("%q was reported as a failure: %v", tc.acp, got.err)
			}
		})
	}
}

func TestALimitOnRequestsWithinOneTurnSaysWhichLimitItWas(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) { a.end(stopMaxTurnRequests, nil) }})
	if !got.noticed("limit on requests within one turn") {
		t.Errorf("the turn was cut off by a bound nobody was told about: %q", got.notices)
	}
	if got.stop.Complete() {
		t.Error("a turn cut off by a bound was reported as a whole answer")
	}
}

func TestAStopReasonThisBuildHasNeverSeenIsAFailureRatherThanAGuess(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) { a.end("compaction_required", nil) }})

	if got.stop != core.StopError {
		t.Errorf("an unknown stop reason became %q, want an error", got.stop)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "compaction_required") {
		t.Errorf("the unknown stop reason was not named: %v", got.err)
	}
}

func TestCancellingATurnAsksTheAgentToStopRatherThanKillingIt(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})

	a := &agent{t: t, script: func(a *agent) {
		a.text("thinking about it")
		close(started)
		// Nothing else. The agent answers only when the cancel notification arrives, which is what
		// the protocol asks of it.
	}}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	stream, err := client.Stream(ctx, turn("a long question"))
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()

	go func() {
		<-started
		cancel()
	}()

	got := drain(t, stream)
	if got.stop != core.StopCancelled {
		t.Errorf("a cancelled turn stopped with %q", got.stop)
	}
	if got.err != nil {
		t.Errorf("a cancelled turn was reported as a failure: %v", got.err)
	}
	if !strings.Contains(got.text, "thinking about it") {
		t.Errorf("the partial reply was thrown away: %q", got.text)
	}

	var cancelled bool
	for _, sent := range a.sent() {
		if sent.Method == methodSessionCancel {
			cancelled = true
		}
	}
	if !cancelled {
		t.Error("the turn was cancelled without telling the agent, so it would keep spending the " +
			"user's plan on an answer nobody is waiting for")
	}
}

func TestABridgeThatStopsMidTurnIsAFailureThatQuotesWhatItSaid(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, script: func(a *agent) {
		a.text("partial")
		// Closing the pipe is what a bridge that died looks like from here.
		if closer, ok := a.out.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	stream, err := client.Stream(context.Background(), turn("hello"))
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()

	got := drain(t, stream)
	if got.stop != core.StopError {
		t.Errorf("a bridge that died mid-turn stopped with %q", got.stop)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "stopped during the turn") {
		t.Errorf("the failure did not say the bridge stopped: %v", got.err)
	}
}

func TestABridgeSpeakingAnotherProtocolVersionIsToldWhichOneCanopySpeaks(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, protocolVersion: 3}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	_, err := client.Stream(context.Background(), turn("hello"))
	if err == nil {
		t.Fatal("a bridge speaking an unknown protocol version was accepted")
	}
	for _, phrase := range []string{"version 3", "version 1", "npm install"} {
		if !strings.Contains(err.Error(), phrase) {
			t.Errorf("the mismatch does not mention %q: %v", phrase, err)
		}
	}
}

func TestABridgeWithNobodySignedInSaysToSignInToClaudeCodeItself(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, authRequired: true}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	_, err := client.Stream(context.Background(), turn("hello"))
	if err == nil {
		t.Fatal("a logged-out bridge started a turn")
	}
	if !strings.Contains(err.Error(), "Run `claude` and sign in") {
		t.Errorf("the remedy is not the one that works: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "canopy keys signin") {
		t.Error("the error offers a Canopy sign-in, and Canopy has no Claude sign-in to offer")
	}
}

func TestClosingATurnStopsTheProcessBehindIt(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, script: func(a *agent) { a.end(stopEndTurn, nil) }}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	stream, err := client.Stream(context.Background(), turn("hello"))
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	drain(t, stream)

	if a.wasStopped() {
		t.Fatal("the bridge was stopped before Close was called")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("closing the stream: %v", err)
	}
	if !a.wasStopped() {
		t.Error("closing the stream left the bridge running, which is a Node process and a Claude " +
			"Agent SDK beneath it still holding the user's plan")
	}
	// Twice, because core.Stream says Close is safe more than once and a second process kill is not.
	if err := stream.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
}

func TestTheModelAndEffortTheRequestNamedAreAskedForWhenTheAgentOffersThem(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, configOptions: []configOption{
		{ID: "model", Name: "Model", Type: "select", CurrentValue: "opus", Options: []configOptionItem{
			{Value: "opus", Name: "Opus"}, {Value: "sonnet", Name: "Sonnet"},
		}},
		{ID: "effort", Name: "Effort", Type: "select", CurrentValue: "high", Options: []configOptionItem{
			{Value: "low", Name: "Low"}, {Value: "high", Name: "High"},
		}},
	}, script: func(a *agent) { a.end(stopEndTurn, nil) }}

	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	request := turn("hello")
	request.Model = "sonnet"
	request.Effort = core.EffortLow

	stream, err := client.Stream(context.Background(), request)
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()
	got := drain(t, stream)

	wanted := map[string]string{"model": "sonnet", "effort": "low"}
	for _, sent := range a.sent() {
		if sent.Method != methodSetConfigOption {
			continue
		}
		var params setConfigOptionParams
		if err := json.Unmarshal(sent.Params, &params); err != nil {
			t.Fatalf("decoding a config change: %v", err)
		}
		if want, ok := wanted[params.ConfigID]; ok && want == params.Value {
			delete(wanted, params.ConfigID)
		}
	}
	if len(wanted) != 0 {
		t.Errorf("the delegated session was never asked for %v", wanted)
	}
	for _, notice := range got.notices {
		if strings.Contains(notice, "does not offer") {
			t.Errorf("a setting that was applied was also reported as unavailable: %s", notice)
		}
	}
}

func TestAModelTheDelegatedAgentDoesNotOfferIsSaidRatherThanSubstituted(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, configOptions: []configOption{
		{ID: "model", Name: "Model", Type: "select", CurrentValue: "opus", Options: []configOptionItem{
			{Value: "opus", Name: "Opus"},
		}},
	}, script: func(a *agent) { a.end(stopEndTurn, nil) }}

	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	request := turn("hello")
	request.Model = "gpt-5"

	stream, err := client.Stream(context.Background(), request)
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()
	got := drain(t, stream)

	if !got.noticed(`does not offer "gpt-5"`) {
		t.Errorf("a model that could not be applied was applied silently: %q", got.notices)
	}
	if !got.noticed(`ran on "opus" instead`) {
		t.Errorf("the notice does not say which model actually answered: %q", got.notices)
	}
}

func TestAnAgentThatOffersNoChoiceOfModelIsNotToldToChangeOne(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, script: func(a *agent) { a.end(stopEndTurn, nil) }}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	stream, err := client.Stream(context.Background(), turn("hello"))
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()
	got := drain(t, stream)

	for _, sent := range a.sent() {
		if sent.Method == methodSetConfigOption {
			t.Fatalf("a setting was changed on an agent that offered none: %s", sent.Params)
		}
	}
	if !got.noticed("did not offer a choice of model") {
		t.Errorf("the reader was not told the model was Claude Code's own: %q", got.notices)
	}
}

func TestTheWholeConversationReachesTheDelegatedAgentWithItsVoicesLabelled(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, script: func(a *agent) { a.end(stopEndTurn, nil) }}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	request := core.Request{
		Model:  "sonnet",
		System: "You are careful.",
		Messages: []core.Message{
			{Role: core.RoleUser, Text: "what is in this repo"},
			{Role: core.RoleAssistant, Text: "a Go program"},
			{Role: core.RoleUser, Text: "and what does it do"},
		},
	}
	stream, err := client.Stream(context.Background(), request)
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()
	drain(t, stream)

	params := paramsOf[promptParams](t, a.sent(), methodSessionPrompt)
	if len(params.Prompt) != 3 {
		t.Fatalf("the prompt had %d blocks, want the system prompt, the transcript and the "+
			"current message", len(params.Prompt))
	}
	if params.Prompt[0].Text != "You are careful." {
		t.Errorf("the system prompt was not the first block: %q", params.Prompt[0].Text)
	}
	for _, phrase := range []string{"User: what is in this repo", "Assistant: a Go program"} {
		if !strings.Contains(params.Prompt[1].Text, phrase) {
			t.Errorf("the transcript is missing %q:\n%s", phrase, params.Prompt[1].Text)
		}
	}
	if params.Prompt[2].Text != "and what does it do" {
		t.Errorf("the current message was %q", params.Prompt[2].Text)
	}
	if strings.Contains(params.Prompt[1].Text, "and what does it do") {
		t.Error("the current message was also put in the transcript, so the agent sees it twice")
	}
}

func TestASingleMessageGoesAcrossOnItsOwnWithNoTranscriptAroundIt(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, script: func(a *agent) { a.end(stopEndTurn, nil) }}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	stream, err := client.Stream(context.Background(), turn("hello"))
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()
	drain(t, stream)

	params := paramsOf[promptParams](t, a.sent(), methodSessionPrompt)
	if len(params.Prompt) != 1 || params.Prompt[0].Text != "hello" {
		t.Errorf("a one message turn was sent as %+v", params.Prompt)
	}
}

func TestToolTrafficFromAnEarlierCredentialIsRenderedRatherThanDropped(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, script: func(a *agent) { a.end(stopEndTurn, nil) }}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	request := core.Request{
		Model: "sonnet",
		Messages: []core.Message{
			{Role: core.RoleUser, Text: "read the file"},
			{Role: core.RoleAssistant, Text: "reading it", ToolCalls: []core.ToolCall{
				{ID: "1", Name: "read_file", Input: []byte(`{"path":"go.mod"}`)},
			}},
			{Role: core.RoleUser, ToolResults: []core.ToolResult{
				{CallID: "1", Content: "module example", Refused: true},
			}},
			{Role: core.RoleUser, Text: "so what is it"},
		},
	}
	stream, err := client.Stream(context.Background(), request)
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()
	drain(t, stream)

	transcript := paramsOf[promptParams](t, a.sent(), methodSessionPrompt).Prompt[0].Text
	for _, phrase := range []string{"read_file", "refused"} {
		if !strings.Contains(transcript, phrase) {
			t.Errorf("the transcript lost %q, so the next agent has to guess what happened:\n%s",
				phrase, transcript)
		}
	}
}

func TestARequestForACapabilityCanopyNeverOfferedIsRefusedRatherThanStubbed(t *testing.T) {
	t.Parallel()

	answered := make(chan *rpcError, 1)
	ask(t, &agent{script: func(a *agent) {
		id := int64(4242)
		a.write(message{JSONRPC: "2.0", ID: &id, Method: "fs/write_text_file",
			Params: encode(map[string]any{"sessionId": a.sessionID, "path": "/etc/passwd"})})

		for {
			line, err := a.in.ReadBytes('\n')
			if err != nil {
				answered <- nil
				return
			}
			var m message
			if err := json.Unmarshal(line, &m); err != nil {
				answered <- nil
				return
			}
			if m.ID != nil && *m.ID == id {
				answered <- m.Error
				a.end(stopEndTurn, nil)
				return
			}
		}
	}})

	got := <-answered
	if got == nil {
		t.Fatal("an unadvertised capability was answered with something other than a refusal")
	}
	if got.Code != methodNotFound {
		t.Errorf("the refusal used code %d, want %d", got.Code, methodNotFound)
	}
}

func TestTheNameOfThisRouteIsNotAnthropic(t *testing.T) {
	t.Parallel()

	// Usage is attributed by provider name. A delegated turn that called itself "anthropic" would be
	// indistinguishable from a metered one everywhere a provider is shown.
	if got := New(installed()).Name(); got != "claude-code" {
		t.Errorf("the provider name is %q", got)
	}
}

func TestATurnWithNoMessagesIsRefusedBeforeAnyProcessIsStarted(t *testing.T) {
	t.Parallel()

	started := false
	client := New(installed(), WithWorkspace(t.TempDir()))
	client.launch = func(ctx context.Context) (*process, error) {
		started = true
		return nil, nil
	}

	err := func() error { _, err := client.Stream(context.Background(), core.Request{Model: "sonnet"}); return err }()
	if err == nil {
		t.Fatal("an empty request was accepted")
	}
	var refused *core.ProviderError
	if !errors.As(err, &refused) || refused.Kind != core.ErrInvalidRequest {
		t.Errorf("a malformed request was reported as %v rather than as an invalid request", err)
	}
	if started {
		t.Error("a bridge was started for a request that could never be sent")
	}
}

// The one rule this route does not enforce, and the reason it does not.
func TestATurnThatNamesNoModelIsTheOrdinaryCaseRatherThanAMalformedOne(t *testing.T) {
	t.Parallel()

	a := &agent{t: t, script: func(a *agent) {
		a.text("answered on whatever I am set to")
		a.end(stopEndTurn, nil)
	}}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	// No model, which is what a delegated credential's session carries: the vendor chooses, so
	// Canopy records none and must not invent one.
	stream, err := client.Stream(context.Background(), core.Request{
		Messages: []core.Message{{Role: core.RoleUser, Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("a turn naming no model was refused: %v", err)
	}
	defer func() { _ = stream.Close() }()

	got := drain(t, stream)
	if got.text != "answered on whatever I am set to" {
		t.Errorf("the reply was %q", got.text)
	}
	for _, sent := range a.sent() {
		if sent.Method == methodSetConfigOption {
			t.Errorf("a model was asked for when none was named: %s", sent.Params)
		}
	}
}

// A first message from the assistant is still refused, because that one is genuinely malformed.
func TestATurnThatDoesNotStartWithTheUserIsRefused(t *testing.T) {
	t.Parallel()

	client := New(installed(), WithWorkspace(t.TempDir()))
	_, err := client.Stream(context.Background(), core.Request{
		Messages: []core.Message{{Role: core.RoleAssistant, Text: "hello"}},
	})
	if err == nil {
		t.Fatal("a conversation starting with the assistant was accepted")
	}
}

// A guard on the shape rather than on behaviour, so that a later edit adding a background reader has
// to think about it rather than discover it.
func TestOneTurnUsesOneProcess(t *testing.T) {
	t.Parallel()

	launches := 0
	a := &agent{t: t, script: func(a *agent) { a.end(stopEndTurn, nil) }}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	inner := client.launch
	client.launch = func(ctx context.Context) (*process, error) {
		launches++
		return inner(ctx)
	}

	stream, err := client.Stream(context.Background(), turn("hello"))
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()
	drain(t, stream)

	if launches != 1 {
		t.Errorf("one turn started %d processes", launches)
	}
}

func TestAnUpdateThisBuildHasNeverHeardOfIsIgnoredRatherThanFatal(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) {
		a.say(map[string]any{"sessionUpdate": "quantum_entanglement_update", "spooky": true})
		a.say(map[string]any{"sessionUpdate": updateUsage, "used": 21002, "size": 1000000})
		a.say(map[string]any{
			"sessionUpdate": updateUserMessageChunk,
			"content":       map[string]any{"type": "text", "text": "hello"},
		})
		a.text("still here")
		a.end(stopEndTurn, nil)
	}})

	if got.text != "still here" {
		t.Errorf("an unknown update broke the turn: %q", got.text)
	}
	if strings.Contains(got.text, "hello") {
		t.Error("Canopy's own prompt was read back into the reply")
	}
	if got.stop != core.StopEndTurn {
		t.Errorf("the turn stopped with %q", got.stop)
	}
}

func TestAnImageInTheAgentsOwnOutputIsNotNarratedAsText(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) {
		a.say(map[string]any{
			"sessionUpdate": updateAgentMessageChunk,
			"content":       map[string]any{"type": "image", "data": "aGk=", "mimeType": "image/png"},
		})
		a.text("here it is")
		a.end(stopEndTurn, nil)
	}})

	if got.text != "here it is" {
		t.Errorf("an image block turned into text: %q", got.text)
	}
}

// The done event has to arrive even when the turn never really began, because the caller's whole
// contract is that it is the last thing it hears.
func TestADoneEventArrivesEvenWhenTheAgentAnswersWithAnError(t *testing.T) {
	t.Parallel()

	got := ask(t, &agent{script: func(a *agent) {
		a.fail(a.promptID, -32603, "the model provider returned 500")
	}})

	if got.stop != core.StopError {
		t.Errorf("a failed turn stopped with %q", got.stop)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "500") {
		t.Errorf("the failure did not carry what the agent said: %v", got.err)
	}
}

func TestCancellingBeforeAnythingArrivesStillEndsTheTurn(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	a := &agent{t: t, script: func(a *agent) {}}
	client := New(installed(), WithWorkspace(t.TempDir()))
	a.launch(client)

	stream, err := client.Stream(ctx, turn("hello"))
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()

	time.AfterFunc(10*time.Millisecond, cancel)

	got := drain(t, stream)
	if got.stop != core.StopCancelled {
		t.Errorf("a turn cancelled before it produced anything stopped with %q", got.stop)
	}
}
