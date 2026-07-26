package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
)

// scriptedStream replays a fixed set of events, which is how the drain logic gets tested without a
// network or a credential.
type scriptedStream struct {
	events []core.StreamEvent
	i      int
	err    error
	closed bool
}

func (s *scriptedStream) Next() bool {
	if s.i >= len(s.events) {
		return false
	}
	s.i++
	return true
}
func (s *scriptedStream) Event() core.StreamEvent { return s.events[s.i-1] }
func (s *scriptedStream) Err() error              { return s.err }
func (s *scriptedStream) Close() error            { s.closed = true; return nil }

func text(t string) core.StreamEvent {
	return core.StreamEvent{Kind: core.EventText, Text: t}
}

func done(reason core.StopReason) core.StreamEvent {
	return core.StreamEvent{
		Kind:       core.EventDone,
		StopReason: reason,
		Usage:      core.Usage{InputTokens: 10, OutputTokens: 20},
	}
}

// unpriced stands in for the pricing table in tests about how a turn is reported, since what a
// turn cost is a separate question from how it ended.
func unpriced(usage core.Usage) (core.Usage, string) { return usage, "" }

func TestDrainWritesTheReply(t *testing.T) {
	var out bytes.Buffer
	stream := &scriptedStream{events: []core.StreamEvent{
		text("Hello"), text(", "), text("world"), done(core.StopEndTurn),
	}}

	if err := drain(stream, &out, unpriced); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "Hello, world" {
		t.Errorf("output = %q, want the concatenated reply", got)
	}
}

// A refusal arrives as a successful response with possibly empty content. Printing the text and
// exiting zero would present a declined request as an answered one.
func TestRefusalIsReportedAsAFailure(t *testing.T) {
	var out bytes.Buffer
	stream := &scriptedStream{events: []core.StreamEvent{done(core.StopRefusal)}}

	err := drain(stream, &out, unpriced)
	if err == nil {
		t.Fatal("a refused request must not exit successfully")
	}
	if !strings.Contains(err.Error(), "declined") {
		t.Errorf("the error should say the request was declined, got %q", err)
	}
}

// A truncated reply looks complete on screen, which is the whole problem. The exit status is the
// only thing that distinguishes it.
func TestTruncatedReplyIsReportedAsIncomplete(t *testing.T) {
	var out bytes.Buffer
	stream := &scriptedStream{events: []core.StreamEvent{
		text("this answer stops mid"), done(core.StopMaxTokens),
	}}

	err := drain(stream, &out, unpriced)
	if err == nil {
		t.Fatal("a truncated reply is not a complete answer")
	}
	if !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("the error should say it is incomplete, got %q", err)
	}
	if !strings.Contains(out.String(), "this answer stops mid") {
		t.Error("the partial reply should still be shown, it is what the user paid for")
	}
}

func TestInterruptedReplyIsMarked(t *testing.T) {
	var out bytes.Buffer
	stream := &scriptedStream{events: []core.StreamEvent{
		text("partial"), done(core.StopCancelled),
	}}

	if err := drain(stream, &out, unpriced); err == nil {
		t.Fatal("a cancelled turn should not exit successfully")
	}
	if !strings.Contains(out.String(), "interrupted") {
		t.Errorf("the partial reply should be marked as interrupted:\n%s", out.String())
	}
}

// A stream that ends without a done event is a bug in the provider adapter. Exiting zero would
// present an answer nobody received as a success.
func TestMissingDoneEventIsAnError(t *testing.T) {
	var out bytes.Buffer
	stream := &scriptedStream{events: []core.StreamEvent{text("hello")}}

	err := drain(stream, &out, unpriced)
	if err == nil {
		t.Fatal("a stream with no done event did not complete")
	}
	if !strings.Contains(err.Error(), "how it finished") {
		t.Errorf("the error should say what was missing, got %q", err)
	}
}

func TestStreamErrorIsSurfaced(t *testing.T) {
	var out bytes.Buffer
	boom := errors.New("connection reset")
	stream := &scriptedStream{events: []core.StreamEvent{text("part")}, err: boom}

	if err := drain(stream, &out, unpriced); !errors.Is(err, boom) {
		t.Errorf("the underlying failure should reach the caller, got %v", err)
	}
}

func TestReadPromptFromArguments(t *testing.T) {
	got, err := readPrompt([]string{"hello", "there"})
	if err != nil {
		t.Fatalf("readPrompt: %v", err)
	}
	if got != "hello there" {
		t.Errorf("prompt = %q, want the joined arguments", got)
	}
}

func TestAskHelp(t *testing.T) {
	var out bytes.Buffer
	if err := runAsk([]string{"-h"}, &out); err != nil {
		t.Fatalf("runAsk -h: %v", err)
	}
	for _, want := range []string{"canopy ask", "-key", "-effort"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help should mention %q", want)
		}
	}
}

func TestAskRejectsUnknownEffort(t *testing.T) {
	var out bytes.Buffer
	err := runAsk([]string{"-effort", "extreme", "hi"}, &out)
	if err == nil {
		t.Fatal("an unknown effort should be rejected before any request is made")
	}
	if !strings.Contains(err.Error(), "low, medium, high") {
		t.Errorf("the error should list the valid levels, got %q", err)
	}
}

// The credential decides which API is spoken. Getting this wrong would mean sending an Anthropic
// request to somebody's NVIDIA endpoint, which fails in a way that reads like a broken key.
func TestClientIsChosenByTheCredential(t *testing.T) {
	anthropicKey := core.KeyMetadata{
		Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic},
	}
	client, err := newClient(anthropicKey, core.NewSecret("x"), "")
	if err != nil {
		t.Fatalf("an anthropic key with no model should work, since it has a default: %v", err)
	}
	if client.Name() != "anthropic" {
		t.Errorf("client = %q, want anthropic", client.Name())
	}

	compatible := core.KeyMetadata{
		Ref:     core.KeyRef{Name: "nemotron", Provider: core.ProviderOpenAICompatible},
		BaseURL: "https://integrate.api.nvidia.com/v1",
	}
	client, err = newClient(compatible, core.NewSecret("x"), "some/model")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	// Named after the key, so usage and errors attribute to "nemotron" rather than to a URL or to a
	// generic label shared by every endpoint in this family.
	if client.Name() != "nemotron" {
		t.Errorf("client = %q, want the key name", client.Name())
	}
}

// Guessing a model name for somebody else's gateway produces a 404 that reads like a broken key.
func TestOpenAICompatibleRequiresAModel(t *testing.T) {
	meta := core.KeyMetadata{
		Ref:     core.KeyRef{Name: "nemotron", Provider: core.ProviderOpenAICompatible},
		BaseURL: "https://integrate.api.nvidia.com/v1",
	}
	_, err := newClient(meta, core.NewSecret("x"), "")
	if err == nil {
		t.Fatal("a compatible key with no model should be refused, not guessed at")
	}
	if !strings.Contains(err.Error(), "-model") {
		t.Errorf("the error should say how to fix it, got %q", err)
	}
}

func TestUnknownProviderIsRefused(t *testing.T) {
	meta := core.KeyMetadata{Ref: core.KeyRef{Name: "k", Provider: core.Provider("mystery")}}
	if _, err := newClient(meta, core.NewSecret("x"), "m"); err == nil {
		t.Fatal("an unknown provider should be refused rather than defaulted to one of them")
	}
}

// The credential decides the price as well as the API, because what a turn costs depends on which
// endpoint answered it.
func TestUsageIsPricedFromTheCredential(t *testing.T) {
	anthropicPrice := pricer(pricing.NewModelID(core.ProviderAnthropic, "", "claude-opus-5"))
	usage, note := anthropicPrice(core.Usage{InputTokens: 1_000_000, OutputTokens: 100_000})
	if !usage.CostKnown {
		t.Fatal("a known model on a known provider should be priced")
	}
	if usage.CostUSD <= 0 {
		t.Errorf("cost = %v, want a real figure", usage.CostUSD)
	}
	_ = note

	// A hosted gateway sets its own prices, so this one reports why it has no figure rather than
	// showing $0.0000, which would read as free.
	hosted := pricer(pricing.NewModelID(
		core.ProviderOpenAICompatible, "https://integrate.api.nvidia.com/v1", "some/model"))
	usage, note = hosted(core.Usage{InputTokens: 1000, OutputTokens: 100})
	if usage.CostKnown {
		t.Error("an endpoint with no recorded rate must not report a cost")
	}
	if note == "" {
		t.Error("an unpriced turn should say why, otherwise it reads as a broken tool")
	}
}
