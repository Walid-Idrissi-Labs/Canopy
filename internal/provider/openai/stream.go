package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// stream reads a server sent event response and converts it to core events.
//
// Tool calls arrive as fragments spread across many chunks: an index, sometimes an id, sometimes a
// name, and the arguments a few characters at a time. They are accumulated and emitted whole at the
// end, because a half received argument string is not something a caller can act on.
type stream struct {
	ctx      context.Context
	client   *Client
	resp     *http.Response
	scanner  *bufio.Scanner
	current  core.StreamEvent
	pending  []core.StreamEvent
	partials map[int]*partialCall
	usage    core.Usage
	reason   string
	stalls   *stallGuard
	err      error
	finished bool
	closed   bool
}

type partialCall struct {
	id   string
	name string
	args strings.Builder
}

var _ core.Stream = (*stream)(nil)

// StallTimeout is how long a stream may produce nothing before it is treated as dead.
//
// A provider that accepts a request and then goes silent is not a hypothetical: NVIDIA NIM does it
// under load, and without this the turn waits on the HTTP client's timeout, which is half an hour.
// An agent hung for half an hour looks like an agent thinking, which is the worst way to fail.
//
// Generous, because a long reasoning turn genuinely produces nothing for a while before its first
// token. Two minutes of complete silence is a connection that is not coming back.
const StallTimeout = 2 * time.Minute

func newStream(
	ctx context.Context, client *Client, resp *http.Response, stalls *stallGuard,
) *stream {
	scanner := bufio.NewScanner(resp.Body)
	// A single SSE line can carry a large tool argument fragment, and the default 64k limit would
	// truncate it into invalid JSON. Failing loudly later is worse than allocating here.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	return &stream{
		ctx:      ctx,
		client:   client,
		resp:     resp,
		scanner:  scanner,
		partials: map[int]*partialCall{},
		stalls:   stalls,
	}
}

// stallGuard cancels a request that has stopped producing anything.
//
// It cancels the request's own context rather than setting a read deadline, because a deadline can
// only be set on a connection and an `http.Response.Body` is not one: the standard library has
// already wrapped it, so the type assertion that looks like it would work quietly does not, and the
// guard becomes a no-op that reads like a fix. Cancelling the context genuinely unblocks the read.
//
// The context it cancels is derived from the caller's, so a stall and a user pressing escape are
// distinguishable: the caller's context is untouched by a stall, and `fired` says which happened.
type stallGuard struct {
	mu      sync.Mutex
	timer   *time.Timer
	fired   bool
	stopped bool
}

func newStallGuard(cancel context.CancelFunc, timeout time.Duration) *stallGuard {
	guard := &stallGuard{}
	guard.timer = time.AfterFunc(timeout, func() {
		guard.mu.Lock()
		if guard.stopped {
			guard.mu.Unlock()
			return
		}
		guard.fired = true
		guard.mu.Unlock()
		cancel()
	})
	return guard
}

// touch restarts the clock, called every time something arrives.
func (g *stallGuard) touch() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopped || g.fired {
		return
	}
	g.timer.Reset(StallTimeout)
}

// Fired reports whether the request was cancelled for going silent rather than by its caller.
func (g *stallGuard) Fired() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fired
}

// stop ends the watchdog, so a finished stream does not leave a timer running.
func (g *stallGuard) stop() {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
	g.timer.Stop()
}

func (s *stream) Next() bool {
	for {
		if len(s.pending) > 0 {
			s.current = s.pending[0]
			s.pending = s.pending[1:]
			return true
		}
		if s.finished {
			return false
		}

		if err := s.ctx.Err(); err != nil {
			s.finishWith(core.StopCancelled, nil)
			continue
		}

		if !s.scanner.Scan() {
			// The context is checked again here, after the read rather than only before it.
			//
			// Scan blocks, so cancelling while it is waiting unblocks it with a transport error
			// rather than at the check above. Reporting that as a failure would mark every turn the
			// user stopped as a turn that broke, and a stopped turn and a failed one need different
			// words on screen and lead somewhere different.
			if err := s.ctx.Err(); err != nil {
				s.finishWith(core.StopCancelled, nil)
				continue
			}
			if err := s.scanner.Err(); err != nil {
				s.err = s.client.classifyTransport(err)
				s.finishWith(core.StopError, s.err)
				continue
			}
			s.flush()
			continue
		}

		// Something arrived, so the connection is alive and the clock resets.
		s.stalls.touch()
		s.handleLine(strings.TrimSpace(s.scanner.Text()))
	}
}

func (s *stream) handleLine(line string) {
	if line == "" || !strings.HasPrefix(line, "data:") {
		return
	}
	payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if payload == "" {
		return
	}
	if payload == "[DONE]" {
		s.flush()
		return
	}

	var chunk struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning_content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			PromptDetails    *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}

	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		// One unreadable chunk is not worth ending a turn over. Providers in this family emit
		// keepalives and vendor specific frames that are not part of the spec.
		return
	}

	if chunk.Usage != nil {
		s.usage.InputTokens = chunk.Usage.PromptTokens
		s.usage.OutputTokens = chunk.Usage.CompletionTokens
		if chunk.Usage.PromptDetails != nil {
			s.usage.CacheReadTokens = chunk.Usage.PromptDetails.CachedTokens
		}
	}

	for _, choice := range chunk.Choices {
		if choice.FinishReason != "" {
			s.reason = choice.FinishReason
		}
		if text := choice.Delta.Content; text != "" {
			s.pending = append(s.pending, core.StreamEvent{Kind: core.EventText, Text: text})
		}
		// Some providers in this family stream reasoning on a separate field. Surfacing it as
		// thinking rather than as reply text keeps it out of the answer.
		if thinking := choice.Delta.Reasoning; thinking != "" {
			s.pending = append(s.pending, core.StreamEvent{Kind: core.EventThinking, Text: thinking})
		}

		for _, call := range choice.Delta.ToolCalls {
			partial, ok := s.partials[call.Index]
			if !ok {
				partial = &partialCall{}
				s.partials[call.Index] = partial
			}
			if call.ID != "" {
				partial.id = call.ID
			}
			if call.Function.Name != "" {
				partial.name = call.Function.Name
			}
			partial.args.WriteString(call.Function.Arguments)
		}
	}
}

// flush emits the accumulated tool calls and the terminal event.
func (s *stream) flush() {
	indices := make([]int, 0, len(s.partials))
	for index := range s.partials {
		indices = append(indices, index)
	}
	// Emitted in index order, because map iteration is random and a caller executing tool calls
	// should see them in the order the model asked for them.
	sort.Ints(indices)

	for _, index := range indices {
		partial := s.partials[index]
		if partial.name == "" {
			continue
		}
		args := partial.args.String()
		if args == "" {
			args = "{}"
		}
		s.pending = append(s.pending, core.StreamEvent{
			Kind: core.EventToolCall,
			ToolCall: &core.ToolCall{
				ID:    partial.id,
				Name:  partial.name,
				Input: []byte(args),
			},
		})
	}

	s.finishWith(mapFinishReason(s.reason), nil)
}

// finishWith queues the terminal event.
//
// Always runs, on every path including failure and cancellation. The done event is the last thing a
// caller hears about the turn, and without it a failed stream looks identical to one still running.
func (s *stream) finishWith(reason core.StopReason, err error) {
	s.finished = true
	s.pending = append(s.pending, core.StreamEvent{
		Kind:       core.EventDone,
		StopReason: reason,
		Usage:      s.usage,
		Err:        err,
	})
}

func (s *stream) Event() core.StreamEvent { return s.current }

func (s *stream) Err() error { return s.err }

func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.stalls.stop()
	if s.resp == nil || s.resp.Body == nil {
		return nil
	}
	// Drained before closing so the connection can be reused. An abandoned body forces a new TCP
	// handshake on the next request, which is a real cost when several agents are running.
	_, _ = io.Copy(io.Discard, io.LimitReader(s.resp.Body, 1<<20))
	if err := s.resp.Body.Close(); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
		return err
	}
	return nil
}
