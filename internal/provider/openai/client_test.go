package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

const canary = "sk-CANARY-MUST-NOT-APPEAR-IN-ANY-ERROR-9f8e"

// sseServer replays a scripted event stream, which is how the whole path gets tested without a
// network or a credential.
func sseServer(t *testing.T, lines ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, line := range lines {
			_, _ = fmt.Fprintf(w, "%s\n\n", line)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func collect(t *testing.T, s core.Stream) ([]core.StreamEvent, core.StreamEvent) {
	t.Helper()
	var events []core.StreamEvent
	var final core.StreamEvent
	for s.Next() {
		e := s.Event()
		events = append(events, e)
		if e.Kind == core.EventDone {
			final = e
		}
	}
	return events, final
}

func request() core.Request {
	return core.Request{
		Model:    "some-model",
		Messages: []core.Message{{Role: core.RoleUser, Text: "hi"}},
	}
}

func TestStreamsText(t *testing.T) {
	srv := sseServer(t,
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		`data: {"choices":[{"delta":{"content":", world"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	)

	client := New(srv.URL, core.NewSecret(canary))
	stream, err := client.Stream(context.Background(), request())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	events, final := collect(t, stream)

	var text strings.Builder
	for _, e := range events {
		if e.Kind == core.EventText {
			text.WriteString(e.Text)
		}
	}
	if text.String() != "Hello, world" {
		t.Errorf("text = %q", text.String())
	}
	if final.StopReason != core.StopEndTurn {
		t.Errorf("stop reason = %q, want end-turn", final.StopReason)
	}
}

// Tool arguments arrive a few characters at a time across many chunks. A half received argument
// string is not something a caller can act on, so calls are emitted whole at the end.
func TestToolCallsAreAccumulatedAndEmittedWhole(t *testing.T) {
	srv := sseServer(t,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_time"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"zo"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"ne\":\"UTC\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	client := New(srv.URL, core.NewSecret(canary))
	stream, err := client.Stream(context.Background(), request())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	events, final := collect(t, stream)

	var calls []core.ToolCall
	for _, e := range events {
		if e.Kind == core.EventToolCall {
			calls = append(calls, *e.ToolCall)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(calls))
	}
	if calls[0].Name != "get_time" || calls[0].ID != "call_1" {
		t.Errorf("call = %+v", calls[0])
	}

	// The whole point of accumulating: the arguments must be valid JSON by the time anyone sees them.
	var args map[string]string
	if err := json.Unmarshal(calls[0].Input, &args); err != nil {
		t.Fatalf("accumulated arguments are not valid JSON: %v (%q)", err, calls[0].Input)
	}
	if args["zone"] != "UTC" {
		t.Errorf("arguments = %v", args)
	}
	if final.StopReason != core.StopToolUse {
		t.Errorf("stop reason = %q, want tool-use", final.StopReason)
	}
}

// Map iteration is random, so without an explicit sort a caller executing calls would see them in
// a different order than the model asked for.
func TestToolCallsAreEmittedInIndexOrder(t *testing.T) {
	srv := sseServer(t,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":2,"id":"c","function":{"name":"third","arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"a","function":{"name":"first","arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"second","arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)

	client := New(srv.URL, core.NewSecret(canary))
	stream, _ := client.Stream(context.Background(), request())
	defer func() { _ = stream.Close() }()

	events, _ := collect(t, stream)
	var names []string
	for _, e := range events {
		if e.Kind == core.EventToolCall {
			names = append(names, e.ToolCall.Name)
		}
	}
	want := []string{"first", "second", "third"}
	for i := range want {
		if i >= len(names) || names[i] != want[i] {
			t.Fatalf("call order = %v, want %v", names, want)
		}
	}
}

// This family's refusal. It arrives on a successful response, so mapping it to a normal stop would
// present a declined request as an answered one.
func TestContentFilterIsARefusal(t *testing.T) {
	srv := sseServer(t,
		`data: {"choices":[{"delta":{},"finish_reason":"content_filter"}]}`,
		`data: [DONE]`,
	)

	client := New(srv.URL, core.NewSecret(canary))
	stream, _ := client.Stream(context.Background(), request())
	defer func() { _ = stream.Close() }()

	_, final := collect(t, stream)
	if final.StopReason != core.StopRefusal {
		t.Errorf("stop reason = %q, want refusal", final.StopReason)
	}
	if final.StopReason.Complete() {
		t.Error("a refused turn is not complete")
	}
}

func TestFinishReasonMapping(t *testing.T) {
	cases := map[string]core.StopReason{
		"stop":           core.StopEndTurn,
		"length":         core.StopMaxTokens,
		"tool_calls":     core.StopToolUse,
		"function_call":  core.StopToolUse,
		"content_filter": core.StopRefusal,
		"":               core.StopError,
	}
	for in, want := range cases {
		if got := mapFinishReason(in); got != want {
			t.Errorf("mapFinishReason(%q) = %q, want %q", in, got, want)
		}
	}
	if mapFinishReason("").Complete() {
		t.Error("a stream with no finish reason did not complete")
	}
}

// Usage only arrives if it is asked for. A turn with no usage cannot be costed or budgeted.
func TestUsageIsRequestedAndCaptured(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}],"+
				"\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":22,"+
				"\"prompt_tokens_details\":{\"cached_tokens\":5}}}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := New(srv.URL, core.NewSecret(canary))
	stream, _ := client.Stream(context.Background(), request())
	defer func() { _ = stream.Close() }()

	_, final := collect(t, stream)
	if final.Usage.InputTokens != 11 || final.Usage.OutputTokens != 22 {
		t.Errorf("usage = %+v", final.Usage)
	}
	if final.Usage.CacheReadTokens != 5 {
		t.Errorf("cache read tokens = %d, want 5", final.Usage.CacheReadTokens)
	}
	if final.Usage.CostKnown {
		t.Error("this layer does not price anything, so cost must not read as known")
	}
	if !strings.Contains(string(body), "include_usage") {
		t.Errorf("the request should ask for usage, otherwise most providers report none:\n%s", body)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		want   core.ProviderErrorKind
	}{
		{http.StatusUnauthorized, core.ErrAuthentication},
		{http.StatusForbidden, core.ErrAuthentication},
		{http.StatusNotFound, core.ErrInvalidRequest},
		{http.StatusTooManyRequests, core.ErrRateLimited},
		{http.StatusInternalServerError, core.ErrOverloaded},
		{http.StatusServiceUnavailable, core.ErrOverloaded},
	}

	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))

		client := New(srv.URL, core.NewSecret(canary))
		_, err := client.Stream(context.Background(), request())
		srv.Close()

		if err == nil {
			t.Errorf("status %d should be an error", tc.status)
			continue
		}
		var provErr *core.ProviderError
		if !errors.As(err, &provErr) {
			t.Errorf("status %d: want a ProviderError, got %T", tc.status, err)
			continue
		}
		if provErr.Kind != tc.want {
			t.Errorf("status %d classified as %q, want %q", tc.status, provErr.Kind, tc.want)
		}
	}
}

// A provider that quotes the rejected credential back would otherwise put it on screen and into any
// screenshot of it.
func TestErrorsNeverEchoTheCredential(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":{"message":"invalid api key: %s"}}`, canary)
	}))
	defer srv.Close()

	client := New(srv.URL, core.NewSecret(canary))
	_, err := client.Stream(context.Background(), request())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), canary) {
		t.Errorf("the credential leaked into a provider error:\n%s", err)
	}
	if !strings.Contains(err.Error(), core.Redacted) {
		t.Errorf("the credential should be replaced with a redaction marker:\n%s", err)
	}
}

// An endpoint that returns an HTML error page would otherwise put a whole document into an error
// string that later gets rendered in a terminal.
func TestErrorBodyIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(strings.Repeat("x", 100_000)))
	}))
	defer srv.Close()

	client := New(srv.URL, core.NewSecret(canary))
	_, err := client.Stream(context.Background(), request())
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(err.Error()) > 8192 {
		t.Errorf("the error message is %d bytes, which is a document rather than a message", len(err.Error()))
	}
}

// This provider is defined by its endpoint, so there is no sensible default to fall back to.
func TestMissingBaseURLIsRejected(t *testing.T) {
	client := New("", core.NewSecret(canary))
	_, err := client.Stream(context.Background(), request())
	if err == nil {
		t.Fatal("a missing base URL should be rejected")
	}
	if !strings.Contains(err.Error(), "base URL") {
		t.Errorf("the error should name what is missing, got %q", err)
	}
}

func TestSamplingParametersAndEffortAreNeverSent(t *testing.T) {
	client := New("https://example.invalid", core.NewSecret(canary))
	req := request()
	req.Effort = core.EffortMax

	body, err := json.Marshal(client.buildRequest(req))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"temperature", "top_p", "top_k", "effort"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("request contains %q, which several providers in this family reject:\n%s",
				forbidden, body)
		}
	}
}

// There is no error flag on a tool message in this API, so a failure has to be stated in the
// content or the model reads it as a successful result.
func TestFailedToolResultsSaySo(t *testing.T) {
	client := New("https://example.invalid", core.NewSecret(canary))
	built := client.buildRequest(core.Request{
		Model: "m",
		Messages: []core.Message{
			{Role: core.RoleUser, Text: "go"},
			{Role: core.RoleUser, ToolResults: []core.ToolResult{
				{CallID: "c1", Content: "permission denied", IsError: true},
			}},
		},
	})

	var found bool
	for _, msg := range built.Messages {
		if msg.Role == "tool" {
			found = true
			if !strings.Contains(msg.Content, "error") {
				t.Errorf("a failed tool result must say so, got %q", msg.Content)
			}
		}
	}
	if !found {
		t.Error("the tool result did not become a tool message")
	}
}

func TestSystemPromptBecomesASystemMessage(t *testing.T) {
	client := New("https://example.invalid", core.NewSecret(canary))
	built := client.buildRequest(core.Request{
		Model:    "m",
		System:   "be helpful",
		Messages: []core.Message{{Role: core.RoleUser, Text: "hi"}},
	})
	if len(built.Messages) < 1 || built.Messages[0].Role != "system" {
		t.Fatalf("the system prompt should lead the messages, got %+v", built.Messages)
	}
}

func TestName(t *testing.T) {
	if got := New("u", core.Secret{}).Name(); got != "openai-compatible" {
		t.Errorf("default name = %q", got)
	}
	if got := New("u", core.Secret{}, WithName("kimi")).Name(); got != "kimi" {
		t.Errorf("named client = %q, want the name so usage attributes to it", got)
	}
}

// Cancelling unblocks the read with a transport error rather than at a context check, so a stream
// that inspected the error first would mark every turn the user stopped as a turn that broke.
//
// This was a real bug, found by a live test against a provider and not by any scripted one, because
// a script cannot reproduce the ordering: the read has to be genuinely blocked when the cancel
// arrives.
func TestCancellingMidStreamIsNotAFailure(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Held open with nothing more to say, which is the state a long reply is in when somebody
		// presses escape.
		<-release
	}))
	defer func() { close(release); srv.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	client := New(srv.URL, core.NewSecret(canary))
	stream, err := client.Stream(ctx, request())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	// Read the first chunk so the next read is genuinely blocked on the server.
	if !stream.Next() {
		t.Fatal("expected the first chunk")
	}
	cancel()

	var final core.StreamEvent
	for stream.Next() {
		if e := stream.Event(); e.Kind == core.EventDone {
			final = e
		}
	}

	if final.StopReason != core.StopCancelled {
		t.Errorf("stop reason = %q, want cancelled. A stopped turn and a failed one need different "+
			"words on screen and lead the reader somewhere different", final.StopReason)
	}
	if final.StopReason.Complete() {
		t.Error("a cancelled turn is not complete")
	}
}

// A provider that accepts a request and then goes silent is not a hypothetical: NVIDIA NIM does it
// under load. Without a stall timeout the turn waits on the HTTP client's own timeout, which is half
// an hour, and an agent hung for half an hour looks like an agent thinking.
//
// The end to end version of this test was written and then deleted: verifying a two minute timeout
// takes two minutes, and a suite nobody runs because it is slow catches nothing. What is tested here
// instead is the watchdog itself, which is where the logic lives, at a millisecond.

// The classification is what matters and it can be checked without waiting two minutes.
//
// Cancellation is observed through a channel rather than a bool set by the callback. The bool was
// both a data race and a flake: the guard marks itself fired and releases its lock before calling
// cancel, so a test that polls Fired and then reads the bool can look in the window between the two
// and see a watchdog that fired without cancelling. Waiting for the callback removes the window,
// because fired is already set by the time the callback runs.
func TestAStallIsNotACancellation(t *testing.T) {
	cancelled := make(chan struct{})
	guard := newStallGuard(func() { close(cancelled) }, time.Millisecond)

	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the watchdog never cancelled the request, so the read would still be blocked")
	}
	if !guard.Fired() {
		t.Error("the request was cancelled but the guard does not report firing, so a stall would " +
			"be reported as the user pressing escape")
	}

	// And once stopped it stays quiet, so a finished stream does not leave a timer running.
	guard.stop()
	fresh := newStallGuard(func() { t.Error("a stopped guard fired") }, time.Millisecond)
	fresh.stop()
	time.Sleep(20 * time.Millisecond)
}

// Every arriving line restarts the clock, or a slow but healthy stream would be killed part way
// through a long answer.
func TestActivityRestartsTheStallClock(t *testing.T) {
	guard := newStallGuard(func() { t.Error("the watchdog fired on a stream that was producing") },
		50*time.Millisecond)
	defer guard.stop()

	for i := 0; i < 10; i++ {
		time.Sleep(10 * time.Millisecond)
		guard.touch()
	}
	if guard.Fired() {
		t.Error("a stream producing something every 10ms was treated as stalled")
	}
}

// The advice on a classified error says what to do; the provider's own message says what actually
// happened. Both belong on the error, because on the third-party endpoints this client serves the
// body is often the only place the real story — which key, which model, what billing state — is
// told, and replacing it with advice sends somebody to a dashboard to rediscover it.
func TestClassifiedErrorsKeepTheProviderOwnWords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":{"message":"api key expired on 2026-07-01"}}`)
	}))
	defer srv.Close()

	client := New(srv.URL, core.NewSecret(canary))
	_, err := client.Stream(context.Background(), request())
	if err == nil {
		t.Fatal("a 401 did not fail")
	}
	for _, want := range []string{"canopy keys test", "api key expired on 2026-07-01"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q lost %q", err, want)
		}
	}
}

// A rate limit that names its retry window shows it, because "rate limited" alone leaves somebody
// guessing at a number the provider already sent.
func TestRetryAfterIsShownWhenTheProviderSaysIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "20")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := New(srv.URL, core.NewSecret(canary))
	_, err := client.Stream(context.Background(), request())
	if err == nil {
		t.Fatal("a 429 did not fail")
	}
	if !strings.Contains(err.Error(), "retry in 20s") {
		t.Errorf("the error %q does not name the retry window the provider sent", err)
	}
	var perr *core.ProviderError
	if !errors.As(err, &perr) || perr.RetryAfter != 20*time.Second {
		t.Errorf("RetryAfter = %v, want 20s", perr.RetryAfter)
	}
}
