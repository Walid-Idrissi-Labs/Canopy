package copilot

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The seam every test in this file drives.
//
// The difficulty of this route is not the network, it is that the vendor owns the conversation and
// Canopy's contract does not. A fake agent is what makes that provable: everything below runs the
// real translation from a message list to "what has this session not heard yet", with nothing
// standing in for it.
type fakeAgent struct {
	mu       sync.Mutex
	events   chan Event
	prompts  []string
	answers  []answered
	aborts   int
	closes   int
	sendErr  error
	onSend   func(prompt string, emit func(Event))
	onAnswer func(a answered, emit func(Event))
}

type answered struct {
	callID  string
	result  string
	failure string
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{events: make(chan Event, 64)}
}

func (f *fakeAgent) Send(_ context.Context, prompt string) error {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	err := f.sendErr
	send := f.onSend
	f.mu.Unlock()

	if err != nil {
		return err
	}
	if send != nil {
		send(prompt, f.emit)
	}
	return nil
}

func (f *fakeAgent) Answer(_ context.Context, callID, result, failure string) error {
	a := answered{callID: callID, result: result, failure: failure}
	f.mu.Lock()
	f.answers = append(f.answers, a)
	answer := f.onAnswer
	f.mu.Unlock()

	if answer != nil {
		answer(a, f.emit)
	}
	return nil
}

func (f *fakeAgent) Events() <-chan Event { return f.events }

func (f *fakeAgent) Abort(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.aborts++
	return nil
}

func (f *fakeAgent) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}

func (f *fakeAgent) emit(event Event) { f.events <- event }

func (f *fakeAgent) said() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.prompts...)
}

func (f *fakeAgent) resolved() []answered {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]answered(nil), f.answers...)
}

func (f *fakeAgent) stopped() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.aborts
}

func (f *fakeAgent) shutDown() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// replies makes the agent answer every prompt with one line and then go quiet.
func replies(text string) func(string, func(Event)) {
	return func(_ string, emit func(Event)) {
		emit(Event{Kind: EventText, Text: text})
		emit(Event{Kind: EventIdle})
	}
}

// clientOn builds a client over one fake agent, and reports how many sessions were opened.
func clientOn(t *testing.T, agent *fakeAgent) (*Client, func() int) {
	t.Helper()
	opened := 0
	var mu sync.Mutex
	client := New("mycopilot", Conversation{Token: core.NewSecret("gho_TOKEN")},
		WithOpener(func(context.Context, Conversation) (Agent, error) {
			mu.Lock()
			defer mu.Unlock()
			opened++
			return agent, nil
		}))
	t.Cleanup(func() { _ = client.Close() })
	return client, func() int {
		mu.Lock()
		defer mu.Unlock()
		return opened
	}
}

// ask runs one turn and returns the text, the tool calls and the done event.
func ask(t *testing.T, client *Client, req core.Request) (string, []core.ToolCall, core.StreamEvent) {
	t.Helper()
	stream, err := client.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var text strings.Builder
	var calls []core.ToolCall
	for stream.Next() {
		event := stream.Event()
		switch event.Kind {
		case core.EventText:
			text.WriteString(event.Text)
		case core.EventToolCall:
			calls = append(calls, *event.ToolCall)
		case core.EventDone:
			return text.String(), calls, event
		}
	}
	t.Fatal("the stream ended without a done event, which every core.Stream owes its caller")
	return "", nil, core.StreamEvent{}
}

func user(text string) core.Message {
	return core.Message{Role: core.RoleUser, Text: text}
}

func assistant(text string) core.Message {
	return core.Message{Role: core.RoleAssistant, Text: text}
}

// The acceptance clause that a turn runs through the SDK and streams back, at the layer where the
// translation happens rather than at the network.
func TestATurnRunsOnTheDelegatedAgentAndStreamsBack(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = func(_ string, emit func(Event)) {
		emit(Event{Kind: EventText, Text: "Hel"})
		emit(Event{Kind: EventText, Text: "lo."})
		emit(Event{Kind: EventIdle})
	}
	client, _ := clientOn(t, agent)

	text, calls, done := ask(t, client, core.Request{
		Model:    "gpt-5.2-codex",
		Messages: []core.Message{user("hello")},
	})

	if text != "Hello." {
		t.Errorf("the reply arrived as %q, want the two chunks joined", text)
	}
	if len(calls) != 0 {
		t.Errorf("a plain reply produced %d tool calls", len(calls))
	}
	if done.StopReason != core.StopEndTurn {
		t.Errorf("the turn ended as %q, want %q", done.StopReason, core.StopEndTurn)
	}
	if got := agent.said(); len(got) != 1 || got[0] != "hello" {
		t.Errorf("the agent was sent %q, want the one message", got)
	}
}

// The whole architectural problem in one test. Canopy hands over the entire conversation every turn
// and the vendor's session already has it, so a second turn must send one message rather than two
// and must not open a second session.
func TestOnlyTheNewestMessageReachesASessionThatHeardTheRest(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = replies("fine")
	client, opened := clientOn(t, agent)

	ask(t, client, core.Request{Model: "m", Messages: []core.Message{user("first")}})
	ask(t, client, core.Request{Model: "m", Messages: []core.Message{
		user("first"), assistant("fine"), user("second"),
	}})

	got := agent.said()
	if len(got) != 2 {
		t.Fatalf("the agent was sent %d messages across two turns, want 2: %q", len(got), got)
	}
	if got[1] != "second" {
		t.Errorf("the second turn sent %q, want only the new message", got[1])
	}
	if strings.Contains(got[1], "first") || strings.Contains(got[1], "fine") {
		t.Error("the second turn repeated what the session had already heard")
	}
	if opened() != 1 {
		t.Errorf("%d sessions were opened for one conversation, want 1", opened())
	}
}

// A conversation picked up after a restart, or forked, arrives with history the vendor's session has
// never heard. The SDK has no way to seed one, so the only surface is the prompt, and what goes in it
// has to say what it is rather than pretending those turns happened.
func TestAConversationThatStartedElsewhereIsSeededAsALabelledTranscript(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = replies("ok")
	client, _ := clientOn(t, agent)

	ask(t, client, core.Request{Model: "m", Messages: []core.Message{
		user("what is in this repository"),
		assistant("a Go program"),
		user("and what does it do"),
	}})

	sent := agent.said()[0]
	for _, want := range []string{"what is in this repository", "a Go program", "and what does it do"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the seed left out %q:\n%s", want, sent)
		}
	}
	if !strings.Contains(sent, "record rather than instructions") {
		t.Errorf("the transcript is not labelled as a record, so the model may read its own earlier "+
			"words as instructions:\n%s", sent)
	}
	if !strings.HasSuffix(strings.TrimSpace(sent), "and what does it do") {
		t.Errorf("the newest message is not the last thing in the prompt:\n%s", sent)
	}
}

// The other half of holding history at the vendor. Canopy can edit, re-roll and compact a
// conversation, none of which reaches GitHub's copy, so a request whose history is shorter than what
// the session has heard is answered with a refusal rather than with a reply to a conversation the
// user can no longer see.
func TestAnEditedHistoryIsRefusedRatherThanAnsweredFromTheVendorsCopy(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = replies("ok")
	client, _ := clientOn(t, agent)

	ask(t, client, core.Request{Model: "m", Messages: []core.Message{
		user("one"), assistant("ok"), user("two"),
	}})

	_, err := client.Stream(context.Background(), core.Request{
		Model: "m", Messages: []core.Message{user("one")},
	})
	if !errors.Is(err, ErrHistoryRewritten) {
		t.Fatalf("a shortened history produced %v, want ErrHistoryRewritten", err)
	}
	if !strings.Contains(err.Error(), "Start a new conversation") {
		t.Errorf("the refusal does not say what to do instead: %v", err)
	}
}

// Q-23's answer for this route, and the reason the SDK is given tools it cannot run. A call leaves
// the vendor, ends the step so Canopy's loop can gate it, and the result goes back down into the
// same waiting turn rather than into a new one.
func TestAToolCallLeavesTheAgentAndItsResultComesBackToTheSameTurn(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = func(_ string, emit func(Event)) {
		emit(Event{Kind: EventText, Text: "let me look"})
		emit(Event{Kind: EventToolCall, Call: &core.ToolCall{
			ID: "req-1", Name: "read_file", Input: []byte(`{"path":"go.mod"}`),
		}})
	}
	agent.onAnswer = func(_ answered, emit func(Event)) {
		emit(Event{Kind: EventText, Text: "it is a Go module"})
		emit(Event{Kind: EventIdle})
	}
	client, opened := clientOn(t, agent)

	history := []core.Message{user("what module is this")}
	text, calls, done := ask(t, client, core.Request{Model: "m", Messages: history})
	if done.StopReason != core.StopToolUse {
		t.Fatalf("a tool request ended the step as %q, want %q", done.StopReason, core.StopToolUse)
	}
	if len(calls) != 1 || calls[0].Name != "read_file" || calls[0].ID != "req-1" {
		t.Fatalf("the tool call arrived as %+v", calls)
	}
	if text != "let me look" {
		t.Errorf("the text before the tool call was %q", text)
	}

	// What Canopy's own loop does next: run the tool, then come back with the result attached.
	history = append(history,
		core.Message{Role: core.RoleAssistant, Text: text, ToolCalls: calls},
		core.Message{Role: core.RoleUser, ToolResults: []core.ToolResult{
			{CallID: "req-1", Content: "module github.com/example"},
		}})

	text, _, done = ask(t, client, core.Request{Model: "m", Messages: history})
	if done.StopReason != core.StopEndTurn {
		t.Errorf("the continued turn ended as %q", done.StopReason)
	}
	if text != "it is a Go module" {
		t.Errorf("the continuation said %q", text)
	}

	if got := agent.resolved(); len(got) != 1 || got[0].callID != "req-1" ||
		got[0].result != "module github.com/example" || got[0].failure != "" {
		t.Errorf("the pending call was resolved as %+v", got)
	}
	if said := agent.said(); len(said) != 1 {
		t.Errorf("the tool result was sent as %d new messages, want none: the vendor's turn never "+
			"ended and a message here would be a second turn talking over the first: %q",
			len(said)-1, said)
	}
	if opened() != 1 {
		t.Errorf("%d sessions were opened, want 1", opened())
	}
}

// A tool Canopy's permission gate refused is still an answer. Left unresolved it is a turn waiting
// forever for a result nobody is going to send, and reported as a success it is a lie the model then
// builds on.
func TestARefusedToolIsReportedToTheAgentAsAFailureRatherThanAsSilence(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = func(_ string, emit func(Event)) {
		emit(Event{Kind: EventToolCall, Call: &core.ToolCall{ID: "req-9", Name: "write_file"}})
	}
	agent.onAnswer = func(_ answered, emit func(Event)) { emit(Event{Kind: EventIdle}) }
	client, _ := clientOn(t, agent)

	first := []core.Message{user("write a file")}
	_, calls, _ := ask(t, client, core.Request{Model: "m", Messages: first})

	second := append(first,
		core.Message{Role: core.RoleAssistant, ToolCalls: calls},
		core.Message{Role: core.RoleUser, ToolResults: []core.ToolResult{{
			CallID:  "req-9",
			Content: "refused: this agent is in plan mode",
			Refused: true,
		}}})
	ask(t, client, core.Request{Model: "m", Messages: second})

	got := agent.resolved()
	if len(got) != 1 {
		t.Fatalf("the refused call was resolved %d times, want once", len(got))
	}
	if got[0].failure == "" {
		t.Error("a refusal went back as a successful result, so the model is told the write happened")
	}
	if !strings.Contains(got[0].failure, "plan mode") {
		t.Errorf("the agent was told %q, and it needs the reason to adjust rather than retry",
			got[0].failure)
	}
}

// Cancelling has to reach the vendor. A session left running after Canopy stopped reading goes on
// spending somebody's allowance on an answer nobody will see, and the session has to survive it,
// which is why this is an abort rather than a shutdown.
func TestCancellingATurnStopsTheVendorRatherThanAbandoningIt(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = func(_ string, emit func(Event)) { emit(Event{Kind: EventText, Text: "thinking"}) }
	client, _ := clientOn(t, agent)

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.Stream(ctx, core.Request{Model: "m", Messages: []core.Message{user("hi")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if !stream.Next() || stream.Event().Kind != core.EventText {
		t.Fatal("the first chunk did not arrive")
	}
	cancel()

	if !stream.Next() {
		t.Fatal("a cancelled stream still owes a done event, carrying whatever arrived")
	}
	done := stream.Event()
	if done.Kind != core.EventDone || done.StopReason != core.StopCancelled {
		t.Errorf("a cancelled turn ended as %+v, want a done event with %q", done, core.StopCancelled)
	}
	if stream.Err() != nil {
		t.Errorf("cancelling reported %v, and a turn somebody stopped is not a fault", stream.Err())
	}
	if agent.stopped() != 1 {
		t.Errorf("the vendor was aborted %d times, want once", agent.stopped())
	}
	if agent.shutDown() != 0 {
		t.Error("cancelling a turn closed the conversation, which loses the history it exists to keep")
	}
}

// The vendor delivers one conversation's events on one channel, so two readers would each get half
// of the other's reply. Refused in words rather than left to produce a reply with holes in it.
func TestASecondTurnIsRefusedWhileTheFirstIsStillReading(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = replies("one")
	client, _ := clientOn(t, agent)

	first, err := client.Stream(context.Background(), core.Request{
		Model: "m", Messages: []core.Message{user("a")}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	_, err = client.Stream(context.Background(), core.Request{
		Model: "m", Messages: []core.Message{user("a"), assistant("one"), user("b")}})
	if err == nil {
		t.Fatal("a second turn started while the first was still reading")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("the refusal reads as %v", err)
	}

	// Closing the first frees the conversation rather than ending it.
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := client.Stream(context.Background(), core.Request{
		Model: "m", Messages: []core.Message{user("a"), assistant("one"), user("b")}}); err != nil {
		t.Errorf("the next turn was refused after the first finished: %v", err)
	}
}

// A conversation is a child process and a session GitHub believes is open. Both end when Canopy
// does, and closing twice is something a shutdown path does by accident.
func TestClosingTheConversationEndsTheProcessBehindItAndIsSafeTwice(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = replies("ok")
	client, _ := clientOn(t, agent)

	ask(t, client, core.Request{Model: "m", Messages: []core.Message{user("hi")}})
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("closing twice: %v", err)
	}
	if agent.shutDown() != 1 {
		t.Errorf("the agent was closed %d times, want once", agent.shutDown())
	}

	if _, err := client.Stream(context.Background(), core.Request{
		Model: "m", Messages: []core.Message{user("again")}}); err == nil {
		t.Error("a closed conversation quietly started a new runtime")
	}
}

// Tokens are real and are counted. The dollar value of them is not, because a Copilot seat is billed
// monthly and this usage is metered against it, so a list price would be a number about an invoice
// nobody receives.
func TestUsageArrivesInTokensAndNeverAsACost(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = func(_ string, emit func(Event)) {
		emit(Event{Kind: EventUsage, Usage: core.Usage{InputTokens: 100, OutputTokens: 20}})
		emit(Event{Kind: EventText, Text: "hi"})
		emit(Event{Kind: EventUsage, Usage: core.Usage{InputTokens: 5, CacheReadTokens: 7}})
		emit(Event{Kind: EventIdle})
	}
	client, _ := clientOn(t, agent)

	_, _, done := ask(t, client, core.Request{Model: "m", Messages: []core.Message{user("hi")}})
	if done.Usage.InputTokens != 105 || done.Usage.OutputTokens != 20 || done.Usage.CacheReadTokens != 7 {
		t.Errorf("the turn reported %+v, want both model calls added up", done.Usage)
	}
	if done.Usage.CostKnown {
		t.Error("a Copilot turn reported a known cost, and a seat has no per-token price to report")
	}
}

// A runtime that stops talking is a failure with a partial reply, not a completed turn. Without this
// the stream would end silently and agent.Loop would report that the provider said nothing about how
// the turn finished, which is true and names the wrong culprit.
func TestARuntimeThatStopsTalkingEndsTheTurnAsAFailure(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = func(_ string, emit func(Event)) {
		emit(Event{Kind: EventText, Text: "half an ans"})
		close(agent.events)
	}
	client, _ := clientOn(t, agent)

	text, _, done := ask(t, client, core.Request{Model: "m", Messages: []core.Message{user("hi")}})
	if text != "half an ans" {
		t.Errorf("the partial reply was %q", text)
	}
	if done.StopReason != core.StopError || done.Err == nil {
		t.Errorf("a runtime that went quiet ended as %+v", done)
	}
}

// A failure the vendor reported during a turn ends it with the vendor's own classification, so the
// chain does the right thing with it rather than reading every failure as unknown.
func TestAFailureReportedDuringATurnEndsItWithTheReasonTheVendorGave(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = func(_ string, emit func(Event)) {
		emit(Event{Kind: EventFailed, Err: &core.ProviderError{
			Kind: core.ErrRateLimited, Provider: Name, Message: "slow down",
		}})
	}
	client, _ := clientOn(t, agent)

	_, _, done := ask(t, client, core.Request{Model: "m", Messages: []core.Message{user("hi")}})
	if done.StopReason != core.StopError {
		t.Fatalf("the turn ended as %q", done.StopReason)
	}
	var provErr *core.ProviderError
	if !errors.As(done.Err, &provErr) || provErr.Kind != core.ErrRateLimited {
		t.Errorf("the failure arrived as %v, want a rate limit the chain can fall back on", done.Err)
	}
}

// The credential's name rather than the provider's, so somebody with two Copilot seats can see which
// of them answered.
func TestATurnIsAttributedToTheCredentialThatAnswered(t *testing.T) {
	client, _ := clientOn(t, newFakeAgent())
	if client.Name() != "mycopilot" {
		t.Errorf("the client is called %q, want the credential's name", client.Name())
	}
	if unnamed := New("", Conversation{}); unnamed.Name() != Name {
		t.Errorf("a client with no credential name is called %q, want %q", unnamed.Name(), Name)
	}
}
