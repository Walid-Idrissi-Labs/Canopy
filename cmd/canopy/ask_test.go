package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
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

func TestDrainWritesTheReply(t *testing.T) {
	var out bytes.Buffer
	stream := &scriptedStream{events: []core.StreamEvent{
		text("Hello"), text(", "), text("world"), done(core.StopEndTurn),
	}}

	if err := drain(stream, &out); err != nil {
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

	err := drain(stream, &out)
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

	err := drain(stream, &out)
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

	if err := drain(stream, &out); err == nil {
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

	err := drain(stream, &out)
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

	if err := drain(stream, &out); !errors.Is(err, boom) {
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
