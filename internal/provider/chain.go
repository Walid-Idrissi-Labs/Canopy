package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Link is one option in a fallback chain.
type Link struct {
	// Name is what the user called this credential. It goes on screen when the chain moves past it,
	// so it has to be the name they chose rather than a provider or a URL.
	Name string
	// Client is the provider to try.
	Client core.ProviderClient
	// Model overrides the request's model for this link, since the second choice is rarely the same
	// model as the first. Empty leaves the request's own model alone.
	Model string
}

// Chain tries several providers in order.
//
// The point is to survive the moment several agents run at once, which is exactly when providers
// start shedding load. It is not a general retry: retryable and fallback-able are different sets,
// and the difference is the whole design.
//
//   - **Overload and rate limits fall through.** Somewhere else can answer this.
//   - **Authentication failures do not.** A wrong key is a thing to fix, and quietly billing the
//     next key instead would hide the problem and spend somebody's money doing it.
//   - **Invalid requests do not.** The next provider will reject it too, for the same reason.
//   - **Cancellation does not.** The user asked for it to stop.
//
// Nothing here is silent. Every fallback emits a notice before the replacement stream continues, so
// the transcript records that the answer came from somewhere other than where it was addressed.
// Being billed on a different key, and possibly answered by a weaker model, without being told,
// would be the kind of convenience that costs trust.
type Chain struct {
	links []Link
}

var _ core.ProviderClient = (*Chain)(nil)

// NewChain builds a fallback chain. The first link is the primary.
func NewChain(links ...Link) *Chain { return &Chain{links: links} }

// Name reports the chain's members, so an error names the chain rather than whichever link
// happened to answer.
func (c *Chain) Name() string {
	if len(c.links) == 0 {
		return "empty chain"
	}
	names := make([]string, len(c.links))
	for i, link := range c.links {
		names[i] = link.Name
	}
	return strings.Join(names, " then ")
}

// Stream tries each link in turn.
func (c *Chain) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	if len(c.links) == 0 {
		return nil, &core.ProviderError{
			Kind:     core.ErrInvalidRequest,
			Provider: c.Name(),
			Message:  "a fallback chain needs at least one provider",
		}
	}

	s := &chainStream{chain: c, ctx: ctx, req: req}
	if err := s.advance(); err != nil {
		return nil, err
	}
	return s, nil
}

// fallbackAllowed decides whether somewhere else is worth trying.
//
// Defaults to no. An unrecognised error is one nobody has reasoned about, and routing around it
// would spend money on a guess.
func fallbackAllowed(err error) bool {
	var provErr *core.ProviderError
	if !errors.As(err, &provErr) {
		return false
	}
	return provErr.Kind.AllowsFallback()
}

func reasonOf(err error) string {
	var provErr *core.ProviderError
	if errors.As(err, &provErr) {
		return string(provErr.Kind)
	}
	if err == nil {
		return "no reason given"
	}
	return "unknown"
}

// chainStream is whichever link is currently answering, plus the notices about the ones that could
// not.
//
// It has to watch the stream rather than only the call that opened it. The Anthropic SDK hands back
// a stream immediately and reports an overload on the first read, so a chain that only checked the
// constructor's error would sit there having never fallen back at all, which is the exact case this
// exists for.
type chainStream struct {
	chain *Chain
	ctx   context.Context
	req   core.Request

	inner  core.Stream
	active Link
	next   int

	pending []core.StreamEvent
	current core.StreamEvent

	// delivered records that some part of the answer has already reached the caller. After that
	// point the chain stops falling back, whatever goes wrong: a replacement stream starts its
	// answer from the beginning, and splicing that onto a half delivered one would produce a reply
	// that reads as though the model contradicted itself mid sentence.
	delivered bool
	done      bool
	err       error
}

var _ core.Stream = (*chainStream)(nil)

// advance opens the next usable link, noting each one it walks past.
//
// Returns an error only for a failure nobody should route around, or when the links run out. In
// both cases the error is the last one seen, since that is the one describing why there is no
// answer.
func (s *chainStream) advance() error {
	for s.next < len(s.chain.links) {
		link := s.chain.links[s.next]
		s.next++

		attempt := s.req
		if link.Model != "" {
			attempt.Model = link.Model
		}

		stream, err := link.Client.Stream(s.ctx, attempt)
		if err == nil {
			s.inner = stream
			s.active = link
			return nil
		}
		if !s.canFallBack(err) {
			return err
		}
		s.note(link, err)
	}

	return &core.ProviderError{
		Kind:     core.ErrOverloaded,
		Provider: s.chain.Name(),
		Message:  "every provider in the chain was unavailable",
	}
}

// canFallBack reports whether the chain should move on from a failure.
func (s *chainStream) canFallBack(err error) bool {
	return s.next < len(s.chain.links) && !s.delivered && fallbackAllowed(err)
}

// note queues the record of a fallback, naming both ends of it.
func (s *chainStream) note(from Link, err error) {
	to := s.chain.links[s.next]
	s.pending = append(s.pending, core.StreamEvent{
		Kind: core.EventNotice,
		Text: fmt.Sprintf("%s could not take this turn (%s), so it went to %s instead",
			from.Name, reasonOf(err), to.Name),
	})
}

func (s *chainStream) Next() bool {
	for {
		if len(s.pending) > 0 {
			s.current = s.pending[0]
			s.pending = s.pending[1:]
			return true
		}
		if s.done {
			return false
		}

		if !s.inner.Next() {
			s.done = true
			s.err = s.inner.Err()
			return false
		}

		event := s.inner.Event()

		// A stream that failed before saying anything is still a turn that can be taken elsewhere.
		// The failed done event is swallowed rather than delivered, because the caller is about to
		// get a real one from whichever link answers.
		if event.Kind == core.EventDone && event.StopReason == core.StopError &&
			s.canFallBack(event.Err) {
			failed := s.active
			_ = s.inner.Close()
			s.note(failed, event.Err)

			if err := s.advance(); err != nil {
				// Nowhere left to go. The turn ends as it would have anyway, with the notices
				// explaining the detour still queued in front of it.
				s.pending = append(s.pending, core.StreamEvent{
					Kind:       core.EventDone,
					StopReason: core.StopError,
					Usage:      event.Usage,
					Err:        err,
				})
				s.done = true
				s.err = err
			}
			continue
		}

		switch event.Kind {
		case core.EventText, core.EventThinking, core.EventToolCall:
			s.delivered = true
		}
		if event.Kind == core.EventDone {
			s.done = true
		}

		s.current = event
		return true
	}
}

func (s *chainStream) Event() core.StreamEvent { return s.current }

func (s *chainStream) Err() error { return s.err }

func (s *chainStream) Close() error {
	if s.inner == nil {
		return nil
	}
	return s.inner.Close()
}
