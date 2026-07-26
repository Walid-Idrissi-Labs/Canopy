// Package anthropic talks to the Anthropic Messages API.
//
// Built on the official SDK rather than hand rolled: it already streams, takes a context for
// cancellation, and ships typed errors and model constants, so hand rolling would mean tracking
// API changes ourselves for nothing. See DECISIONS.md D-30.
//
// This package is the only place that knows what the Anthropic wire format looks like. Nothing
// above it should be able to tell which vendor answered.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// DefaultModel is used when a profile does not name one.
const DefaultModel = "claude-opus-5"

// DefaultMaxTokens is the response cap when a request does not set one.
//
// Generous on purpose. The cap covers thinking and answer together, and thinking is on by default
// on current models, so a value sized for the answer alone truncates mid sentence. Everything here
// streams, so a large cap costs nothing but headroom.
const DefaultMaxTokens = 32000

// Client is an Anthropic provider.
type Client struct {
	sdk    sdk.Client
	secret core.Secret
}

var _ core.ProviderClient = (*Client)(nil)

// New builds a client for a credential.
//
// The secret is revealed once, here, and handed straight to the SDK. That is the whole window in
// which it exists as an ordinary string in this package.
func New(secret core.Secret) *Client {
	return &Client{
		sdk:    sdk.NewClient(option.WithAPIKey(secret.Reveal())),
		secret: secret,
	}
}

func (c *Client) Name() string { return "anthropic" }

// Stream sends a request and returns the response as it arrives.
func (c *Client) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	if err := req.Validate(); err != nil {
		return nil, &core.ProviderError{
			Kind: core.ErrInvalidRequest, Provider: c.Name(), Message: err.Error(), Err: err,
		}
	}

	params, err := c.buildParams(req)
	if err != nil {
		return nil, err
	}

	return &stream{
		inner:  c.sdk.Messages.NewStreaming(ctx, params),
		client: c,
		ctx:    ctx,
	}, nil
}

func (c *Client) buildParams(req core.Request) (sdk.MessageNewParams, error) {
	model := req.Model
	if model == "" {
		model = DefaultModel
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}

	params := sdk.MessageNewParams{
		Model:     sdk.Model(model),
		MaxTokens: int64(maxTokens),
	}

	if req.System != "" {
		// The cache breakpoint goes on the system prompt because it is the largest stable prefix,
		// and everything after it is what varies per turn.
		params.System = []sdk.TextBlockParam{{
			Text:         req.System,
			CacheControl: sdk.NewCacheControlEphemeralParam(),
		}}
	}

	if effort := mapEffort(req.Effort); effort != "" {
		params.OutputConfig = sdk.OutputConfigParam{Effort: effort}
	}

	// Thinking is on by default on current models, so an unset field means it thinks. Only the
	// explicit opt out needs saying.
	if req.DisableThinking {
		params.Thinking = sdk.ThinkingConfigParamUnion{
			OfDisabled: &sdk.ThinkingConfigDisabledParam{},
		}
	}

	messages, err := c.buildMessages(req.Messages)
	if err != nil {
		return sdk.MessageNewParams{}, err
	}
	params.Messages = messages

	if tools := buildTools(req.Tools); len(tools) > 0 {
		params.Tools = tools
	}

	// Sampling parameters are deliberately never set. Current models reject temperature, top_p and
	// top_k with a 400, and AgentProfile still carries a Temperature field from A1-01. Dropping it
	// here rather than trusting every caller to remember is the point of having a provider layer.

	return params, nil
}

func (c *Client) buildMessages(messages []core.Message) ([]sdk.MessageParam, error) {
	out := make([]sdk.MessageParam, 0, len(messages))

	for i, msg := range messages {
		blocks := make([]sdk.ContentBlockParamUnion, 0, 1+len(msg.ToolCalls)+len(msg.ToolResults))

		// Tool results come first in a user turn. The API expects them at the head of the message
		// answering the assistant's calls.
		for _, result := range msg.ToolResults {
			blocks = append(blocks, sdk.NewToolResultBlock(result.CallID, result.Content, result.IsError))
		}
		if msg.Text != "" {
			blocks = append(blocks, sdk.NewTextBlock(msg.Text))
		}
		for _, call := range msg.ToolCalls {
			var input any
			if len(call.Input) > 0 {
				if err := json.Unmarshal(call.Input, &input); err != nil {
					return nil, &core.ProviderError{
						Kind:     core.ErrInvalidRequest,
						Provider: c.Name(),
						Message:  fmt.Sprintf("tool call %q has invalid JSON input", call.Name),
						Err:      err,
					}
				}
			}
			blocks = append(blocks, sdk.ContentBlockParamUnion{
				OfToolUse: &sdk.ToolUseBlockParam{ID: call.ID, Name: call.Name, Input: input},
			})
		}

		if len(blocks) == 0 {
			return nil, &core.ProviderError{
				Kind:     core.ErrInvalidRequest,
				Provider: c.Name(),
				Message:  fmt.Sprintf("message %d is empty", i),
			}
		}

		switch msg.Role {
		case core.RoleUser:
			out = append(out, sdk.NewUserMessage(blocks...))
		case core.RoleAssistant:
			out = append(out, sdk.NewAssistantMessage(blocks...))
		default:
			return nil, &core.ProviderError{
				Kind:     core.ErrInvalidRequest,
				Provider: c.Name(),
				Message:  fmt.Sprintf("message %d has unknown role %q", i, msg.Role),
			}
		}
	}

	return out, nil
}

func buildTools(tools []core.ToolDefinition) []sdk.ToolUnionParam {
	if len(tools) == 0 {
		return nil
	}
	out := make([]sdk.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		var schema sdk.ToolInputSchemaParam
		if len(tool.InputSchema) > 0 {
			// A schema that will not parse is a programming error in our own tool definitions, so
			// it falls through as an empty schema and the API rejects it loudly rather than being
			// silently dropped here.
			_ = json.Unmarshal(tool.InputSchema, &schema)
		}
		out = append(out, sdk.ToolUnionParam{OfTool: &sdk.ToolParam{
			Name:        tool.Name,
			Description: sdk.String(tool.Description),
			InputSchema: schema,
		}})
	}
	return out
}

func mapEffort(effort core.Effort) sdk.OutputConfigEffort {
	switch effort {
	case core.EffortLow:
		return sdk.OutputConfigEffortLow
	case core.EffortMedium:
		return sdk.OutputConfigEffortMedium
	case core.EffortHigh:
		return sdk.OutputConfigEffortHigh
	case core.EffortXHigh:
		return sdk.OutputConfigEffortXhigh
	case core.EffortMax:
		return sdk.OutputConfigEffortMax
	default:
		return ""
	}
}

// mapStopReason translates the API's stop reason.
//
// The refusal case is why this is a function rather than a cast. A refusal arrives as a successful
// response with possibly empty content, and a caller that reads content without checking would
// crash on it.
func mapStopReason(reason sdk.StopReason) core.StopReason {
	switch reason {
	case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence:
		return core.StopEndTurn
	case sdk.StopReasonToolUse:
		return core.StopToolUse
	case sdk.StopReasonMaxTokens:
		return core.StopMaxTokens
	case sdk.StopReasonRefusal:
		return core.StopRefusal
	case "":
		// A stream that ended without a stop reason did not finish. Reporting it as end-turn would
		// present a truncated answer as complete.
		return core.StopError
	default:
		return core.StopEndTurn
	}
}

// classify turns an SDK error into one Canopy can act on.
//
// The distinctions are the point: a rate limit means wait, a bad key means fix the credential, an
// overload means try elsewhere. Collapsing them into "request failed" would leave every caller
// guessing.
func (c *Client) classify(err error) *core.ProviderError {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.Canceled) {
		return &core.ProviderError{
			Kind: core.ErrCancelled, Provider: c.Name(), Message: "cancelled", Err: err,
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &core.ProviderError{
			Kind: core.ErrNetwork, Provider: c.Name(), Message: "timed out", Err: err,
		}
	}

	var apiErr *sdk.Error
	if !errors.As(err, &apiErr) {
		return &core.ProviderError{
			Kind:     core.ErrNetwork,
			Provider: c.Name(),
			Message:  c.scrub(err.Error()),
			Err:      err,
		}
	}

	out := &core.ProviderError{
		Provider:   c.Name(),
		StatusCode: apiErr.StatusCode,
		Message:    c.scrub(safeMessage(apiErr)),
		Err:        err,
	}

	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		out.Kind = core.ErrAuthentication
		out.Message = "the credential was rejected. Check it with `canopy keys test`, or add it again"
	case http.StatusNotFound:
		out.Kind = core.ErrInvalidRequest
		out.Message = "unknown model or endpoint. Check the model name on the profile"
	case http.StatusRequestEntityTooLarge:
		out.Kind = core.ErrContextLength
		out.Message = "the request is too large. Compact the conversation or shorten the input"
	case http.StatusTooManyRequests:
		out.Kind = core.ErrRateLimited
		out.Message = "rate limited"
	case 529:
		out.Kind = core.ErrOverloaded
		out.Message = "the provider is overloaded"
	case http.StatusBadRequest:
		out.Kind = core.ErrInvalidRequest
		// A context length failure arrives as an ordinary 400, and it needs a different response
		// from every other 400: compact the conversation rather than fix the request.
		if strings.Contains(strings.ToLower(safeMessage(apiErr)), "context") {
			out.Kind = core.ErrContextLength
		}
	default:
		if apiErr.StatusCode >= 500 {
			out.Kind = core.ErrOverloaded
		} else {
			out.Kind = core.ErrUnknown
		}
	}

	return out
}

// safeMessage reads an SDK error's text without trusting it not to panic.
//
// The SDK's Error method dereferences the HTTP response it was built from, and an error
// constructed without one takes the process down. That is a poor trade: a provider failure is
// already a bad moment, and turning it into a crash loses the session and everything in it. A
// generic message is a far better outcome than no process.
func safeMessage(err *sdk.Error) (msg string) {
	defer func() {
		if recover() != nil {
			msg = fmt.Sprintf("provider returned status %d", err.StatusCode)
		}
	}()
	return err.Error()
}

// scrub removes the credential from text before it leaves this package.
//
// A1-04 found that free text fields render verbatim, so a provider replying "invalid x-api-key:
// sk-ant-..." would land on screen and in any screenshot of it. This is the right layer to fix it:
// the credential is already in scope here, so the scrub is local and complete, whereas doing it at
// render time would mean loading every stored key so the renderer could search for it.
func (c *Client) scrub(text string) string {
	if c.secret.IsZero() {
		return text
	}
	value := c.secret.Reveal()
	if value == "" || !strings.Contains(text, value) {
		return text
	}
	return strings.ReplaceAll(text, value, core.Redacted)
}
