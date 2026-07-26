package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The rule this whole type exists to enforce: a refused, truncated, cancelled or failed turn is
// not a finished one. Presenting any of them as complete is the chat equivalent of a stale green.
func TestOnlyEndTurnAndToolUseAreComplete(t *testing.T) {
	complete := map[StopReason]bool{
		StopEndTurn: true,
		StopToolUse: true,
	}
	for _, reason := range []StopReason{
		StopEndTurn, StopToolUse, StopMaxTokens, StopRefusal, StopCancelled, StopError,
	} {
		if got := reason.Complete(); got != complete[reason] {
			t.Errorf("%q Complete() = %v, want %v", reason, got, complete[reason])
		}
	}
}

// A refusal arrives as a successful response with possibly empty content. It is a stop reason,
// never an error, and treating it as one would send a caller retrying something that will be
// refused again.
func TestRefusalIsAStopReasonNotAnError(t *testing.T) {
	if StopRefusal.Complete() {
		t.Error("a refused turn produced no answer, so it is not complete")
	}
	for _, kind := range []ProviderErrorKind{
		ErrAuthentication, ErrRateLimited, ErrOverloaded, ErrContextLength,
		ErrInvalidRequest, ErrNetwork, ErrCancelled, ErrUnknown,
	} {
		if string(kind) == string(StopRefusal) {
			t.Error("refusal must not also be an error kind, or callers will handle it twice")
		}
	}
}

func TestRetryableErrors(t *testing.T) {
	retryable := map[ProviderErrorKind]bool{
		ErrRateLimited: true,
		ErrOverloaded:  true,
		ErrNetwork:     true,
	}
	for _, kind := range []ProviderErrorKind{
		ErrAuthentication, ErrRateLimited, ErrOverloaded, ErrContextLength,
		ErrInvalidRequest, ErrNetwork, ErrCancelled, ErrUnknown,
	} {
		if got := kind.Retryable(); got != retryable[kind] {
			t.Errorf("%q Retryable() = %v, want %v", kind, got, retryable[kind])
		}
	}
}

// Falling back is narrower than retrying, and the difference is the whole point.
//
// A wrong key must never route to another credential: the user would be billed elsewhere, possibly
// answered by a weaker model, and never told the key was wrong. A network blip says nothing about
// the credential either, so it is retryable but not a fallback trigger.
func TestOnlyLoadFailuresAllowFallback(t *testing.T) {
	allowed := map[ProviderErrorKind]bool{
		ErrRateLimited: true,
		ErrOverloaded:  true,
	}
	for _, kind := range []ProviderErrorKind{
		ErrAuthentication, ErrRateLimited, ErrOverloaded, ErrContextLength,
		ErrInvalidRequest, ErrNetwork, ErrCancelled, ErrUnknown,
	} {
		if got := kind.AllowsFallback(); got != allowed[kind] {
			t.Errorf("%q AllowsFallback() = %v, want %v", kind, got, allowed[kind])
		}
	}
	if ErrAuthentication.AllowsFallback() {
		t.Error("a bad key must be fixed, not routed around")
	}
	if !ErrNetwork.Retryable() || ErrNetwork.AllowsFallback() {
		t.Error("a network failure is retryable but says nothing about the credential")
	}
}

func TestRequestValidate(t *testing.T) {
	good := Request{
		Model:    "claude-opus-5",
		Messages: []Message{{Role: RoleUser, Text: "hello"}},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("valid request rejected: %v", err)
	}

	bad := map[string]Request{
		"no model":          {Messages: []Message{{Role: RoleUser}}},
		"no messages":       {Model: "m"},
		"assistant first":   {Model: "m", Messages: []Message{{Role: RoleAssistant}}},
		"unknown effort":    {Model: "m", Messages: []Message{{Role: RoleUser}}, Effort: "extreme"},
		"negative maxtoken": {Model: "m", Messages: []Message{{Role: RoleUser}}, MaxTokens: -1},
	}
	for why, req := range bad {
		if err := req.Validate(); err == nil {
			t.Errorf("request with %s should be rejected", why)
		}
	}
}

func TestEffortValidity(t *testing.T) {
	if !EffortDefault.Valid() {
		t.Error("an unset effort is valid and means the provider default")
	}
	for _, e := range AllEfforts() {
		if !e.Valid() {
			t.Errorf("%q should be valid", e)
		}
	}
	if Effort("extreme").Valid() {
		t.Error("the effort vocabulary is closed")
	}
}

func TestUsageAdd(t *testing.T) {
	a := Usage{InputTokens: 100, OutputTokens: 50, CostUSD: 0.01, CostKnown: true}
	b := Usage{InputTokens: 200, OutputTokens: 25, CostUSD: 0.02, CostKnown: true}

	got := a.Add(b)
	if got.InputTokens != 300 || got.OutputTokens != 75 {
		t.Errorf("token totals wrong: %+v", got)
	}
	if !got.CostKnown {
		t.Error("two known costs sum to a known cost")
	}
}

// A total is only as trustworthy as its least trustworthy part. Summing a known cost with an
// unknown one and presenting the result as a figure would put a wrong number on screen, which is
// worse than showing none.
func TestUnknownCostPoisonsTheTotal(t *testing.T) {
	known := Usage{CostUSD: 0.01, CostKnown: true}
	unknown := Usage{CostUSD: 0, CostKnown: false}

	if known.Add(unknown).CostKnown {
		t.Error("a total including an unpriced turn is not a known total")
	}
	if unknown.Add(known).CostKnown {
		t.Error("order should not matter")
	}
	if (Usage{}).CostKnown {
		t.Error("the zero usage has no known cost, it is not free")
	}
}

func TestProviderErrorWrapping(t *testing.T) {
	underlying := errors.New("connection reset")
	err := &ProviderError{
		Kind:     ErrNetwork,
		Provider: "anthropic",
		Message:  "request failed",
		Err:      underlying,
	}

	if !errors.Is(err, underlying) {
		t.Error("the underlying error should be reachable with errors.Is")
	}
	if !err.Retryable() {
		t.Error("a network failure is retryable")
	}
	for _, want := range []string{"anthropic", "network", "request failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message should mention %q, got %q", want, err.Error())
		}
	}
}

// ProviderClient is deliberately not named Provider: that name is already the vendor enum on
// KeyRef, and two Providers in one package would be a coin flip at every call site.
func TestProviderClientIsSatisfiable(t *testing.T) {
	var _ ProviderClient = (*stubProvider)(nil)
}

type stubProvider struct{}

func (*stubProvider) Name() string { return "stub" }
func (*stubProvider) Stream(context.Context, Request) (Stream, error) {
	return nil, errors.New("not implemented")
}

// Add has no identity element, so folding a list from Usage{} would carry an unknown cost into
// every total. This is the correct way to add turns up, and the reason it exists.
func TestSumDoesNotStartFromAnUnpricedZero(t *testing.T) {
	turns := []Usage{
		{InputTokens: 100, OutputTokens: 10, CostUSD: 0.01, CostKnown: true},
		{InputTokens: 200, OutputTokens: 20, CostUSD: 0.02, CostKnown: true},
	}

	total := Sum(turns...)
	if !total.CostKnown {
		t.Error("every turn was priced, so the total is priced")
	}
	if total.InputTokens != 300 || total.OutputTokens != 30 {
		t.Errorf("token totals wrong: %+v", total)
	}
	if total.CostUSD != 0.03 {
		t.Errorf("cost = %v, want 0.03", total.CostUSD)
	}

	// One unpriced turn still poisons the total, which is the property from TestUnknownCostPoisons.
	withUnknown := Sum(turns[0], Usage{InputTokens: 5, CostKnown: false})
	if withUnknown.CostKnown {
		t.Error("a total containing an unpriced turn is not a known total")
	}

	if Sum().CostKnown {
		t.Error("nothing to add is not a priced zero")
	}
	if Sum(turns[0]) != turns[0] {
		t.Error("one turn sums to itself")
	}
}
