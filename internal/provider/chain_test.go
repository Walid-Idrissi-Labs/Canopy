package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// fakeClient answers with a script, or refuses in a named way.
type fakeClient struct {
	name string
	// openErr is returned by Stream itself, the way an OpenAI compatible client reports a bad status.
	openErr error
	// events is what the stream yields, which is how the Anthropic SDK reports a failure: the stream
	// opens fine and the trouble arrives on the first read.
	events []core.StreamEvent
	// calls counts how many turns reached this client, so a test can tell "did not fall back" from
	// "fell back and came back".
	calls int
	model string
}

func (f *fakeClient) Name() string { return f.name }

func (f *fakeClient) Stream(_ context.Context, req core.Request) (core.Stream, error) {
	f.calls++
	f.model = req.Model
	if f.openErr != nil {
		return nil, f.openErr
	}
	return &scriptedStream{events: f.events}, nil
}

type scriptedStream struct {
	events  []core.StreamEvent
	current core.StreamEvent
	closed  bool
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
func (s *scriptedStream) Close() error            { s.closed = true; return nil }

func provErr(kind core.ProviderErrorKind) error {
	return &core.ProviderError{Kind: kind, Provider: "fake", Message: string(kind)}
}

func answered(text string) []core.StreamEvent {
	return []core.StreamEvent{
		{Kind: core.EventText, Text: text},
		{Kind: core.EventDone, StopReason: core.StopEndTurn},
	}
}

func failedWith(kind core.ProviderErrorKind) []core.StreamEvent {
	return []core.StreamEvent{
		{Kind: core.EventDone, StopReason: core.StopError, Err: provErr(kind)},
	}
}

func request() core.Request {
	return core.Request{Model: "primary-model", Messages: []core.Message{{Role: core.RoleUser, Text: "hi"}}}
}

func collect(t *testing.T, s core.Stream) (text, notices []string, final core.StreamEvent) {
	t.Helper()
	for s.Next() {
		event := s.Event()
		switch event.Kind {
		case core.EventText:
			text = append(text, event.Text)
		case core.EventNotice:
			notices = append(notices, event.Text)
		case core.EventDone:
			final = event
		}
	}
	return text, notices, final
}

func TestPrimaryAnswersAndNothingElseIsTouched(t *testing.T) {
	primary := &fakeClient{name: "claude", events: answered("hello")}
	backup := &fakeClient{name: "kimi", events: answered("should not run")}

	chain := NewChain(Link{Name: "claude", Client: primary}, Link{Name: "kimi", Client: backup})
	stream, err := chain.Stream(context.Background(), request())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	text, notices, final := collect(t, stream)

	if strings.Join(text, "") != "hello" {
		t.Errorf("text = %v", text)
	}
	if len(notices) != 0 {
		t.Errorf("a turn nobody fell back on needs no notices, got %v", notices)
	}
	if backup.calls != 0 {
		t.Errorf("the backup was called %d times on a turn the primary answered", backup.calls)
	}
	if final.StopReason != core.StopEndTurn {
		t.Errorf("stop reason = %q", final.StopReason)
	}
}

// The case the whole thing exists for: several agents at once is exactly when providers shed load.
func TestOverloadFallsThrough(t *testing.T) {
	primary := &fakeClient{name: "claude", openErr: provErr(core.ErrOverloaded)}
	backup := &fakeClient{name: "kimi", events: answered("picked it up")}

	chain := NewChain(Link{Name: "claude", Client: primary}, Link{Name: "kimi", Client: backup})
	stream, err := chain.Stream(context.Background(), request())
	if err != nil {
		t.Fatalf("an overloaded primary should not lose the turn: %v", err)
	}
	text, notices, final := collect(t, stream)

	if strings.Join(text, "") != "picked it up" {
		t.Errorf("text = %v", text)
	}
	if final.StopReason != core.StopEndTurn {
		t.Errorf("stop reason = %q, want a completed turn", final.StopReason)
	}
	if len(notices) != 1 {
		t.Fatalf("%d notices, want 1: a silent fallback bills a different key without saying so", len(notices))
	}
	// Both ends named, because "something went wrong somewhere" is not something anyone can act on.
	if !strings.Contains(notices[0], "claude") || !strings.Contains(notices[0], "kimi") {
		t.Errorf("the notice should name what failed and what answered, got %q", notices[0])
	}
}

// The Anthropic SDK opens the stream and reports the overload on the first read, so a chain that
// only watched the constructor would never fall back at all.
func TestFailureDuringTheStreamAlsoFallsThrough(t *testing.T) {
	primary := &fakeClient{name: "claude", events: failedWith(core.ErrOverloaded)}
	backup := &fakeClient{name: "kimi", events: answered("picked it up")}

	chain := NewChain(Link{Name: "claude", Client: primary}, Link{Name: "kimi", Client: backup})
	stream, err := chain.Stream(context.Background(), request())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	text, notices, final := collect(t, stream)

	if strings.Join(text, "") != "picked it up" {
		t.Errorf("text = %v, want the backup's answer", text)
	}
	if len(notices) != 1 {
		t.Errorf("%d notices, want 1", len(notices))
	}
	// The failed turn's done event must not reach the caller, since a real one is coming.
	if final.StopReason != core.StopEndTurn {
		t.Errorf("stop reason = %q, want the successful turn's", final.StopReason)
	}
	if final.Err != nil {
		t.Errorf("the failed link's error leaked into a successful turn: %v", final.Err)
	}
}

// A wrong key is a thing to fix. Quietly billing the next one instead would hide the problem and
// spend somebody's money doing it.
func TestAuthenticationFailuresDoNotFallThrough(t *testing.T) {
	for _, kind := range []core.ProviderErrorKind{
		core.ErrAuthentication,
		core.ErrInvalidRequest,
		core.ErrContextLength,
		core.ErrCancelled,
	} {
		primary := &fakeClient{name: "claude", openErr: provErr(kind)}
		backup := &fakeClient{name: "kimi", events: answered("should not run")}

		chain := NewChain(Link{Name: "claude", Client: primary}, Link{Name: "kimi", Client: backup})
		_, err := chain.Stream(context.Background(), request())

		if err == nil {
			t.Errorf("%s was routed around instead of surfaced", kind)
		}
		if backup.calls != 0 {
			t.Errorf("%s reached the backup, so a problem that needs fixing got hidden", kind)
		}

		var provider *core.ProviderError
		if errors.As(err, &provider) && provider.Kind != kind {
			t.Errorf("the surfaced error was %q, want the original %q", provider.Kind, kind)
		}
	}
}

// Splicing a second provider's answer onto a half delivered one would read as the model
// contradicting itself mid sentence.
func TestNoFallbackOnceTheAnswerHasStarted(t *testing.T) {
	primary := &fakeClient{name: "claude", events: []core.StreamEvent{
		{Kind: core.EventText, Text: "here is the first half"},
		{Kind: core.EventDone, StopReason: core.StopError, Err: provErr(core.ErrOverloaded)},
	}}
	backup := &fakeClient{name: "kimi", events: answered("a completely different answer")}

	chain := NewChain(Link{Name: "claude", Client: primary}, Link{Name: "kimi", Client: backup})
	stream, err := chain.Stream(context.Background(), request())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	text, _, final := collect(t, stream)

	if backup.calls != 0 {
		t.Error("the chain fell back after part of the answer was already on screen")
	}
	if strings.Join(text, "") != "here is the first half" {
		t.Errorf("text = %v, want only what was actually delivered", text)
	}
	if final.StopReason != core.StopError {
		t.Errorf("stop reason = %q, want the failure to be reported rather than papered over",
			final.StopReason)
	}
}

// The second choice is rarely the same model as the first.
func TestALinkMayOverrideTheModel(t *testing.T) {
	primary := &fakeClient{name: "claude", openErr: provErr(core.ErrRateLimited)}
	backup := &fakeClient{name: "kimi", events: answered("ok")}

	chain := NewChain(
		Link{Name: "claude", Client: primary},
		Link{Name: "kimi", Client: backup, Model: "kimi-k2"},
	)
	stream, err := chain.Stream(context.Background(), request())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, _, _ = collect(t, stream)

	if backup.model != "kimi-k2" {
		t.Errorf("the backup was asked for %q, want its own model", backup.model)
	}
	if primary.model != "primary-model" {
		t.Errorf("the primary was asked for %q, want the request's own model", primary.model)
	}
}

func TestChainExhaustionReportsWhy(t *testing.T) {
	first := &fakeClient{name: "a", openErr: provErr(core.ErrOverloaded)}
	second := &fakeClient{name: "b", openErr: provErr(core.ErrRateLimited)}

	chain := NewChain(Link{Name: "a", Client: first}, Link{Name: "b", Client: second})
	_, err := chain.Stream(context.Background(), request())
	if err == nil {
		t.Fatal("a chain where nothing answered is a failed turn")
	}
	// The last link's own error, rather than a generic one, because that is what describes why
	// there is no answer.
	var provider *core.ProviderError
	if !errors.As(err, &provider) || provider.Kind != core.ErrRateLimited {
		t.Errorf("err = %v, want the last link's rate limit", err)
	}
}

func TestEmptyChainIsRefused(t *testing.T) {
	if _, err := NewChain().Stream(context.Background(), request()); err == nil {
		t.Fatal("a chain with no providers cannot answer anything and should say so")
	}
}

func TestNameListsTheChain(t *testing.T) {
	chain := NewChain(
		Link{Name: "claude", Client: &fakeClient{name: "anthropic"}},
		Link{Name: "kimi", Client: &fakeClient{name: "openai-compatible"}},
	)
	if got := chain.Name(); !strings.Contains(got, "claude") || !strings.Contains(got, "kimi") {
		t.Errorf("name = %q, want both members", got)
	}
}
