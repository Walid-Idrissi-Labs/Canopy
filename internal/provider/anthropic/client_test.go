package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

const canary = "sk-ant-api03-CANARY-MUST-NOT-APPEAR-IN-ANY-ERROR"

func testClient() *Client { return New(core.NewSecret(canary)) }

func userRequest(text string) core.Request {
	return core.Request{
		Model:    "claude-opus-5",
		Messages: []core.Message{{Role: core.RoleUser, Text: text}},
	}
}

// Current models reject temperature, top_p and top_k with a 400. AgentProfile still carries a
// Temperature field from A1-01, so this layer has to drop it rather than trust every caller to
// remember. Asserting on the marshalled body rather than the struct, because that is what actually
// goes over the wire.
func TestSamplingParametersAreNeverSent(t *testing.T) {
	params, err := testClient().buildParams(userRequest("hello"))
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}

	body, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"temperature", "top_p", "top_k"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("request body contains %q, which current models reject with a 400:\n%s",
				forbidden, body)
		}
	}
}

func TestDefaultsAreApplied(t *testing.T) {
	params, err := testClient().buildParams(core.Request{
		Messages: []core.Message{{Role: core.RoleUser, Text: "hi"}},
		Model:    "claude-opus-5",
	})
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if params.MaxTokens != DefaultMaxTokens {
		t.Errorf("MaxTokens = %d, want the default %d", params.MaxTokens, DefaultMaxTokens)
	}

	// The cap covers thinking and answer together, and thinking is on by default, so a default
	// sized for an answer alone would truncate mid sentence.
	if DefaultMaxTokens < 16000 {
		t.Errorf("the default cap of %d leaves no room for thinking plus an answer", DefaultMaxTokens)
	}
}

// Adaptive thinking is stated for every model that takes it, because Opus 4.7 and 4.8 do not think
// unless asked and the others return nothing readable unless the display is set; models without it
// get no thinking field, since budget-style configuration is rejected by the ones that have it.
func TestThinkingIsAdaptiveWhereTheModelTakesIt(t *testing.T) {
	client := testClient()

	on, err := client.buildParams(userRequest("hi"))
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	body, _ := json.Marshal(on)
	for _, want := range []string{`"type":"adaptive"`, `"display":"summarized"`, `"prefix_mismatch_behavior":"drop_block"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("an Opus 5 request lacks %s:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "budget_tokens") {
		t.Errorf("budget_tokens is rejected by current models:\n%s", body)
	}

	old := userRequest("hi")
	old.Model = "claude-haiku-4-5"
	haiku, err := client.buildParams(old)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if body, _ := json.Marshal(haiku); strings.Contains(string(body), "thinking") {
		t.Errorf("a model without adaptive thinking was sent a thinking field:\n%s", body)
	}

	req := userRequest("hi")
	req.DisableThinking = true
	off, err := client.buildParams(req)
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	body, _ = json.Marshal(off)
	if !strings.Contains(string(body), "disabled") {
		t.Errorf("disabling thinking should be explicit in the request:\n%s", body)
	}
}

func TestEffortMapping(t *testing.T) {
	cases := map[core.Effort]sdk.OutputConfigEffort{
		core.EffortLow:     sdk.OutputConfigEffortLow,
		core.EffortMedium:  sdk.OutputConfigEffortMedium,
		core.EffortHigh:    sdk.OutputConfigEffortHigh,
		core.EffortXHigh:   sdk.OutputConfigEffortXhigh,
		core.EffortMax:     sdk.OutputConfigEffortMax,
		core.EffortDefault: "",
	}
	for in, want := range cases {
		if got := mapEffort(in); got != want {
			t.Errorf("mapEffort(%q) = %q, want %q", in, got, want)
		}
	}

	// The default must send nothing rather than picking a level on the caller's behalf.
	params, err := testClient().buildParams(userRequest("hi"))
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if body, _ := json.Marshal(params); strings.Contains(string(body), "effort") {
		t.Errorf("an unset effort should not appear in the request:\n%s", body)
	}
}

// A refusal arrives as a successful response with possibly empty content. Mapping it to end-turn
// would present a declined request as a completed one.
func TestStopReasonMapping(t *testing.T) {
	cases := map[sdk.StopReason]core.StopReason{
		sdk.StopReasonEndTurn:      core.StopEndTurn,
		sdk.StopReasonStopSequence: core.StopEndTurn,
		sdk.StopReasonToolUse:      core.StopToolUse,
		sdk.StopReasonMaxTokens:    core.StopMaxTokens,
		sdk.StopReasonRefusal:      core.StopRefusal,
		"":                         core.StopError,
	}
	for in, want := range cases {
		if got := mapStopReason(in); got != want {
			t.Errorf("mapStopReason(%q) = %q, want %q", in, got, want)
		}
	}

	if mapStopReason(sdk.StopReasonRefusal).Complete() {
		t.Error("a refused turn must never read as complete")
	}
	// A stream that ends without a stop reason did not finish. Calling it end-turn would present a
	// truncated answer as a whole one.
	if mapStopReason("").Complete() {
		t.Error("a stream with no stop reason did not complete")
	}
}

func TestErrorClassification(t *testing.T) {
	client := testClient()

	cases := []struct {
		status int
		want   core.ProviderErrorKind
	}{
		{http.StatusUnauthorized, core.ErrAuthentication},
		{http.StatusForbidden, core.ErrAuthentication},
		{http.StatusNotFound, core.ErrInvalidRequest},
		{http.StatusRequestEntityTooLarge, core.ErrContextLength},
		{http.StatusTooManyRequests, core.ErrRateLimited},
		{529, core.ErrOverloaded},
		{http.StatusInternalServerError, core.ErrOverloaded},
		{http.StatusBadRequest, core.ErrInvalidRequest},
		{http.StatusTeapot, core.ErrUnknown},
	}

	for _, tc := range cases {
		got := client.classify(&sdk.Error{StatusCode: tc.status})
		if got.Kind != tc.want {
			t.Errorf("status %d classified as %q, want %q", tc.status, got.Kind, tc.want)
		}
		if got.StatusCode != tc.status {
			t.Errorf("status %d not preserved on the error", tc.status)
		}
	}
}

// Each class needs a different response from the user, so each needs a different message. Getting
// this wrong sends someone hunting for a bad key when they were merely rate limited.
func TestErrorsSayWhatToDo(t *testing.T) {
	client := testClient()

	cases := map[int]string{
		http.StatusUnauthorized:          "credential",
		http.StatusNotFound:              "model",
		http.StatusRequestEntityTooLarge: "Compact",
		http.StatusTooManyRequests:       "rate limited",
		529:                              "overloaded",
	}
	for status, want := range cases {
		got := client.classify(&sdk.Error{StatusCode: status})
		if !strings.Contains(got.Message, want) {
			t.Errorf("status %d message %q should mention %q", status, got.Message, want)
		}
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	client := testClient()

	if got := client.classify(context.Canceled); got.Kind != core.ErrCancelled {
		t.Errorf("cancellation classified as %q", got.Kind)
	}
	if got := client.classify(context.DeadlineExceeded); got.Kind != core.ErrNetwork {
		t.Errorf("timeout classified as %q", got.Kind)
	}

	// A cancelled turn must not look retryable, or a loop will keep restarting work the user
	// deliberately stopped.
	if client.classify(context.Canceled).Retryable() {
		t.Error("a cancelled request is not retryable")
	}
}

// A bad key must never route to another credential: the user would be billed elsewhere, possibly
// answered by a weaker model, and never told the key was wrong.
func TestAuthenticationFailuresNeverFallBack(t *testing.T) {
	client := testClient()

	auth := client.classify(&sdk.Error{StatusCode: http.StatusUnauthorized})
	if auth.AllowsFallback() {
		t.Error("a rejected credential must be fixed, not routed around")
	}
	if auth.Retryable() {
		t.Error("retrying a rejected credential just fails again")
	}

	for _, status := range []int{http.StatusTooManyRequests, 529} {
		if !client.classify(&sdk.Error{StatusCode: status}).AllowsFallback() {
			t.Errorf("status %d is a load failure and should allow a fallback", status)
		}
	}
}

// The A1-04 finding, fixed at the layer that owns the credential.
//
// A provider replying "invalid x-api-key: sk-ant-..." would otherwise land on screen and in any
// screenshot of it. Scrubbing here is local and complete, whereas doing it at render time would
// mean loading every stored key so the renderer could search for it.
func TestProviderErrorsNeverEchoTheCredential(t *testing.T) {
	client := testClient()

	for _, raw := range []error{
		errors.New("authentication_error: invalid x-api-key: " + canary),
		errors.New("connection to https://api.anthropic.com failed with key=" + canary),
	} {
		got := client.classify(raw)
		if strings.Contains(got.Error(), canary) {
			t.Errorf("the credential leaked into a provider error:\n%s", got.Error())
		}
		if !strings.Contains(got.Error(), core.Redacted) {
			t.Errorf("the credential should be replaced with a redaction marker, got:\n%s", got.Error())
		}
	}
}

func TestScrubHandlesNoSecret(t *testing.T) {
	client := New(core.Secret{})
	text := "nothing to redact here"
	if got := client.scrub(text); got != text {
		t.Errorf("scrub with no secret changed the text: %q", got)
	}
}

func TestRequestValidationIsReportedAsInvalid(t *testing.T) {
	client := testClient()

	_, err := client.Stream(context.Background(), core.Request{})
	if err == nil {
		t.Fatal("an empty request should be rejected")
	}
	var provErr *core.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("want a ProviderError, got %T", err)
	}
	if provErr.Kind != core.ErrInvalidRequest {
		t.Errorf("kind = %q, want invalid-request", provErr.Kind)
	}
	if provErr.Retryable() {
		t.Error("a malformed request will not succeed on retry")
	}
}

func TestMessageBuilding(t *testing.T) {
	client := testClient()

	params, err := client.buildParams(core.Request{
		Model:  "claude-opus-5",
		System: "be helpful",
		Messages: []core.Message{
			{Role: core.RoleUser, Text: "run the tool"},
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
				{ID: "call-1", Name: "get_time", Input: []byte(`{"zone":"UTC"}`)},
			}},
			{Role: core.RoleUser, ToolResults: []core.ToolResult{
				{CallID: "call-1", Content: "12:00"},
			}},
		},
	})
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if len(params.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(params.Messages))
	}
	if len(params.System) != 1 || params.System[0].Text != "be helpful" {
		t.Error("the system prompt did not survive")
	}
}

// The system prompt is the largest stable prefix, so it is where the cache breakpoint belongs.
// Everything after it varies per turn.
func TestSystemPromptIsCached(t *testing.T) {
	params, err := testClient().buildParams(core.Request{
		Model:    "claude-opus-5",
		System:   "a long stable system prompt",
		Messages: []core.Message{{Role: core.RoleUser, Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	body, _ := json.Marshal(params)
	if !strings.Contains(string(body), "cache_control") {
		t.Errorf("the system prompt should carry a cache breakpoint:\n%s", body)
	}
}

func TestEmptyMessageIsRejected(t *testing.T) {
	_, err := testClient().buildParams(core.Request{
		Model:    "claude-opus-5",
		Messages: []core.Message{{Role: core.RoleUser}},
	})
	if err == nil {
		t.Error("a message with no content should be rejected here rather than by the API")
	}
}

func TestInvalidToolInputIsRejected(t *testing.T) {
	_, err := testClient().buildParams(core.Request{
		Model: "claude-opus-5",
		Messages: []core.Message{
			{Role: core.RoleUser, Text: "hi"},
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{
				{ID: "c1", Name: "broken", Input: []byte(`{not json`)},
			}},
		},
	})
	if err == nil {
		t.Error("a tool call with unparseable input should be rejected")
	}
}

func TestClientName(t *testing.T) {
	if got := testClient().Name(); got != "anthropic" {
		t.Errorf("Name() = %q", got)
	}
}

// The SDK's Error method dereferences the HTTP response it was built from. An error without one
// takes the process down, which turns a bad moment into a lost session and everything in it.
func TestMalformedSDKErrorDoesNotPanic(t *testing.T) {
	client := testClient()

	got := client.classify(&sdk.Error{StatusCode: http.StatusBadGateway})
	if got == nil {
		t.Fatal("expected a classified error")
	}
	if got.Message == "" {
		t.Error("a fallback message is better than an empty one")
	}
	if !strings.Contains(got.Message, "502") {
		t.Errorf("the fallback should at least name the status, got %q", got.Message)
	}
}

// hasCache reports whether a marshalled request carries a breakpoint in a named section.
func cacheBreakpoints(t *testing.T, params sdk.MessageNewParams) (tools, system, messages int) {
	t.Helper()

	for _, tool := range params.Tools {
		if tool.OfTool != nil && tool.OfTool.CacheControl.Type != "" {
			tools++
		}
	}
	for _, block := range params.System {
		if block.CacheControl.Type != "" {
			system++
		}
	}
	for _, msg := range params.Messages {
		for _, block := range msg.Content {
			if control := block.GetCacheControl(); control != nil && control.Type != "" {
				messages++
			}
		}
	}
	return tools, system, messages
}

// The system prompt's breakpoint caches the tools before it too, so the tools need none of their
// own; a request without a system prompt puts it on the last tool instead.
func TestToolsAndSystemAreCachedByOneBreakpoint(t *testing.T) {
	client := New(core.NewSecret(canary))
	tools := []core.ToolDefinition{
		{Name: "read", Description: "read a file", InputSchema: []byte(`{"type":"object"}`)},
		{Name: "write", Description: "write a file", InputSchema: []byte(`{"type":"object"}`)},
	}
	params, err := client.buildParams(core.Request{
		Model: "claude-opus-5", System: "you are a coding agent", Tools: tools,
		Messages: []core.Message{{Role: core.RoleUser, Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	toolMarks, system, _ := cacheBreakpoints(t, params)
	if system != 1 || toolMarks != 0 {
		t.Errorf("%d system and %d tool breakpoints, want 1 and 0", system, toolMarks)
	}

	bare, err := client.buildParams(core.Request{
		Model: "claude-opus-5", Tools: tools,
		Messages: []core.Message{{Role: core.RoleUser, Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if last := bare.Tools[len(bare.Tools)-1].OfTool; last == nil || last.CacheControl.Type == "" {
		t.Error("with no system prompt the last tool must carry the breakpoint")
	}
}

// The conversation is cached at its newest block through automatic placement. Caching is a prefix
// match, so what this request writes at its end is exactly what the next request, which repeats
// all of it and appends, reads back. A breakpoint one message back billed every new tool result
// twice: once as fresh input and once as a write on the following step.
func TestTheWholeConversationIsCachedAtItsNewestBlock(t *testing.T) {
	params, err := New(core.NewSecret(canary)).buildParams(core.Request{
		Model: "claude-opus-5",
		Messages: []core.Message{
			{Role: core.RoleUser, Text: "first"},
			{Role: core.RoleAssistant, Text: "answer"},
			{Role: core.RoleUser, Text: "newest"},
		},
	})
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}
	if params.CacheControl.Type == "" {
		t.Fatal("automatic caching is off, so the conversation so far is re-billed every step")
	}
	if _, _, messages := cacheBreakpoints(t, params); messages != 0 {
		t.Errorf("%d explicit message breakpoints alongside automatic placement; that spends a slot "+
			"on a position the next request cannot read", messages)
	}
}

// The API allows four, and going over is a 400 rather than a degraded response.
func TestBreakpointsStayWithinTheLimit(t *testing.T) {
	client := New(core.NewSecret(canary))
	params, err := client.buildParams(core.Request{
		Model:  "claude-opus-5",
		System: "system",
		Messages: []core.Message{
			{Role: core.RoleUser, Text: "a"},
			{Role: core.RoleAssistant, Text: "b"},
			{Role: core.RoleUser, Text: "c"},
		},
		Tools: []core.ToolDefinition{{Name: "t", InputSchema: []byte(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("buildParams: %v", err)
	}

	tools, system, messages := cacheBreakpoints(t, params)
	if total := tools + system + messages; total > 4 {
		t.Errorf("%d breakpoints, and the API rejects more than 4", total)
	}
}

// The advice says what to do and the provider's own sentence says what specifically happened.
// Both belong on the error: Anthropic's rate limit messages name which limit was hit and its
// billing messages name the account state, and advice that replaced them sent somebody to the
// console to rediscover a sentence this package was already holding.
func TestClassifiedErrorsKeepTheProviderOwnWords(t *testing.T) {
	client := testClient()

	var apiErr sdk.Error
	if err := apiErr.UnmarshalJSON([]byte(
		`{"type":"error","error":{"type":"rate_limit_error",` +
			`"message":"tokens per minute limit exceeded for this organization"}}`,
	)); err != nil {
		t.Fatalf("building the SDK error: %v", err)
	}
	apiErr.StatusCode = 429

	got := client.classify(&apiErr)
	for _, want := range []string{"rate limited", "tokens per minute limit exceeded"} {
		if !strings.Contains(got.Message, want) {
			t.Errorf("the message %q lost %q", got.Message, want)
		}
	}
}

// Web search is Anthropic's server tool, the filtering version on models that have it, and is only
// offered when asked for.
func TestWebSearchIsOfferedOnlyWhenAsked(t *testing.T) {
	req := userRequest("what changed in go 1.26")
	off, _ := testClient().buildParams(req)
	if body, _ := json.Marshal(off); strings.Contains(string(body), "web_search") {
		t.Fatalf("web search offered without being asked:\n%s", body)
	}
	req.WebSearch = true
	on, _ := testClient().buildParams(req)
	if body, _ := json.Marshal(on); !strings.Contains(string(body), `"web_search_20260209"`) {
		t.Fatalf("web search was not offered:\n%s", body)
	}
}

// A reply cut off while thinking has a thinking block with no signature. Sent back, it has the API
// refuse every later request in the conversation, so it is never kept; signed thinking and big
// numbers in a tool input come back exactly as received.
func TestAnUnsignedThinkingBlockIsNeverReplayed(t *testing.T) {
	var s stream
	if err := json.Unmarshal([]byte(`{"id":"m","type":"message","role":"assistant","model":"x",
		"content":[{"type":"thinking","thinking":"half a thou","signature":""}],
		"stop_reason":"max_tokens"}`), &s.message); err != nil {
		t.Fatal(err)
	}
	if n := s.nativeMessage(); n != nil {
		t.Fatalf("a reply holding only unsigned thinking was kept: %s", n.Data)
	}

	if err := json.Unmarshal([]byte(`{"id":"m","type":"message","role":"assistant","model":"x",
		"content":[{"type":"thinking","thinking":"done","signature":"sig"},
		{"type":"tool_use","id":"t","name":"f","input":{"n":12345678901234567890}}],
		"stop_reason":"tool_use"}`), &s.message); err != nil {
		t.Fatal(err)
	}
	n := s.nativeMessage()
	if n == nil || !strings.Contains(string(n.Data), `"signature":"sig"`) ||
		!strings.Contains(string(n.Data), "12345678901234567890") {
		t.Fatalf("a signed reply was not replayed exactly: %v", n)
	}
}

// A search is reported with its query as soon as its block ends, so a reply that fails afterwards
// still leaves the search it was billed for in the audit trail.
func TestASearchIsReportedBeforeTheReplyEnds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []string{
			`{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":1}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"go 1.26 release\"}"}}`,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
		} {
			var head struct{ Type string }
			_ = json.Unmarshal([]byte(e), &head)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", head.Type, e)
		}
	}))
	defer server.Close()
	client := New(core.NewSecret("k"), WithBaseURL(server.URL))
	stream, err := client.Stream(context.Background(), core.Request{Model: "claude-opus-5", WebSearch: true,
		Messages: []core.Message{{Role: core.RoleUser, Text: "look it up"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	var notices []string
	for stream.Next() {
		if e := stream.Event(); e.Kind == core.EventNotice {
			notices = append(notices, e.Text)
		}
	}
	if !strings.Contains(strings.Join(notices, "|"), WebSearchNotice+"go 1.26 release") {
		t.Fatalf("the search was not reported before the reply failed: %q (err %v)", notices, stream.Err())
	}
	if n := strings.Count(strings.Join(notices, "|"), WebSearchNotice); n != 1 {
		t.Fatalf("the search was reported %d times", n)
	}
}
