package anthropic

import (
	"context"
	"encoding/json"

	sdk "github.com/anthropics/anthropic-sdk-go"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// stream adapts the SDK's event stream to the core contract.
//
// Two shape differences matter. The SDK emits fine grained deltas, while callers want whole tool
// calls; and the SDK's terminal signal is spread across several events, while callers want one
// done event carrying the stop reason and usage.
//
// Text streams live, because that is the point. Tool calls are emitted only once complete: a
// partially received tool input is not something a caller can act on, and handing over a half
// parsed one would invite exactly the wrong kind of optimism.
type stream struct {
	inner interface {
		Next() bool
		Current() sdk.MessageStreamEventUnion
		Err() error
		Close() error
	}
	client *Client
	ctx    context.Context

	// message accumulates the SDK's events, which is how the final stop reason, usage and
	// completed tool inputs become available.
	message sdk.Message

	current core.StreamEvent
	pending []core.StreamEvent

	native   *core.Native
	err      error
	finished bool
	closed   bool

	// searched marks the content blocks whose search was already reported.
	searched map[int64]bool
}

var _ core.Stream = (*stream)(nil)

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

		if !s.inner.Next() {
			s.finish()
			continue
		}

		event := s.inner.Current()
		if err := s.message.Accumulate(event); err != nil {
			s.err = s.client.classify(err)
			s.finishWith(core.StopError, s.err)
			continue
		}
		s.queueDeltas(event)
	}
}

// queueDeltas turns one SDK event into whatever core events it implies, which is often none.
func (s *stream) queueDeltas(event sdk.MessageStreamEventUnion) {
	// A search the provider ran for this reply is said where it happens, since its results arrive
	// inside the reply rather than as a tool call Canopy made.
	if start, ok := event.AsAny().(sdk.ContentBlockStartEvent); ok {
		if use, ok := start.ContentBlock.AsAny().(sdk.ServerToolUseBlock); ok && use.Name == "web_search" {
			s.pending = append(s.pending, core.StreamEvent{Kind: core.EventNotice, Text: "searching the web..."})
		}
		return
	}
	// The query is complete when its block is, and it is reported then: a reply that is cancelled
	// or fails later has still been billed for the searches it ran, and the audit trail should say so.
	if stop, ok := event.AsAny().(sdk.ContentBlockStopEvent); ok {
		s.reportSearch(stop.Index)
		return
	}
	delta, ok := event.AsAny().(sdk.ContentBlockDeltaEvent)
	if !ok {
		return
	}

	switch d := delta.Delta.AsAny().(type) {
	case sdk.TextDelta:
		if d.Text != "" {
			s.pending = append(s.pending, core.StreamEvent{Kind: core.EventText, Text: d.Text})
		}
	case sdk.ThinkingDelta:
		if d.Thinking != "" {
			s.pending = append(s.pending, core.StreamEvent{
				Kind: core.EventThinking, Text: d.Thinking,
			})
		}
	}
}

// finish handles the end of the underlying stream, whether it ended cleanly or failed.
func (s *stream) finish() {
	// Cancellation is checked before the stream's error, not after.
	//
	// Cancelling an in flight request unblocks the read with a transport error, so checking the
	// error first would classify every turn the user stopped as a turn that broke. A stopped turn
	// and a failed one need different words on screen and lead the reader somewhere different, and
	// this ordering is the whole difference between them.
	if err := s.ctx.Err(); err != nil {
		s.finishWith(core.StopCancelled, nil)
		return
	}
	if err := s.inner.Err(); err != nil {
		s.err = s.client.classify(err)
		s.finishWith(core.StopError, s.err)
		return
	}

	// Tool calls are emitted here rather than during streaming, because only now is each input
	// complete enough to be parsed.
	for _, block := range s.message.Content {
		use, ok := block.AsAny().(sdk.ToolUseBlock)
		if !ok {
			continue
		}
		s.pending = append(s.pending, core.StreamEvent{
			Kind: core.EventToolCall,
			ToolCall: &core.ToolCall{
				ID:    use.ID,
				Name:  use.Name,
				Input: []byte(use.JSON.Input.Raw()),
			},
		})
	}

	// Any search not yet reported, for a stream whose block ends went unseen.
	for i := range s.message.Content {
		s.reportSearch(int64(i))
	}
	s.native = s.nativeMessage()
	s.finishWith(mapStopReason(s.message.StopReason), nil)
}

// nativeMessage is the finished assistant message as the API's own JSON, thinking blocks and
// signatures included, so the next request can send back exactly what was received.
func (s *stream) nativeMessage() *core.Native {
	if len(s.message.Content) == 0 {
		return nil
	}
	data, err := json.Marshal(s.message.ToParam())
	if err != nil {
		return nil
	}
	// A reply cut off mid-thought carries a thinking block with no signature, and the API refuses
	// every later request that sends one back. The unsigned block goes; the rest of the reply stays.
	var message map[string]json.RawMessage
	var blocks []json.RawMessage
	if json.Unmarshal(data, &message) != nil || json.Unmarshal(message["content"], &blocks) != nil {
		return nil
	}
	kept := blocks[:0]
	for _, block := range blocks {
		var head struct{ Type, Signature string }
		if json.Unmarshal(block, &head) == nil && head.Type == "thinking" && head.Signature == "" {
			continue
		}
		kept = append(kept, block)
	}
	if len(kept) == 0 {
		return nil
	}
	if message["content"], err = json.Marshal(kept); err != nil {
		return nil
	}
	if data, err = json.Marshal(message); err != nil {
		return nil
	}
	return &core.Native{Provider: providerName, Data: data}
}

// finishWith queues the terminal event.
//
// It always runs, on every path including failure and cancellation, because the done event is the
// last thing a caller ever hears about the turn. Without it a failed stream looks identical to one
// still in progress, and usage that was already billed would go unaccounted for.
func (s *stream) finishWith(reason core.StopReason, err error) {
	s.finished = true
	s.pending = append(s.pending, core.StreamEvent{
		Kind:       core.EventDone,
		StopReason: reason,
		Usage:      s.usage(),
		Err:        err,
		Native:     s.native,
	})
}

func (s *stream) usage() core.Usage {
	u := s.message.Usage
	return core.Usage{
		InputTokens:      int(u.InputTokens),
		OutputTokens:     int(u.OutputTokens),
		CacheReadTokens:  int(u.CacheReadInputTokens),
		CacheWriteTokens: int(u.CacheCreationInputTokens),
		// Cost is left unknown here. Pricing is A2-05's job, and a zero that looks like a real
		// figure is worse than an absent one.
		CostKnown: false,
	}
}

func (s *stream) Event() core.StreamEvent { return s.current }

func (s *stream) Err() error { return s.err }

func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.inner.Close()
}

// reportSearch reports the search in a finished content block, with its query, once, so it reaches
// the audit trail as well as the screen.
func (s *stream) reportSearch(index int64) {
	if index < 0 || index >= int64(len(s.message.Content)) || s.searched[index] {
		return
	}
	use, ok := s.message.Content[index].AsAny().(sdk.ServerToolUseBlock)
	if !ok || use.Name != "web_search" {
		return
	}
	if s.searched == nil {
		s.searched = map[int64]bool{}
	}
	s.searched[index] = true
	var input struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal([]byte(use.JSON.Input.Raw()), &input)
	s.pending = append(s.pending, core.StreamEvent{Kind: core.EventNotice, Text: WebSearchNotice + input.Query})
}
