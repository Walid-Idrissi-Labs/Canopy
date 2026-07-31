package copilot

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	sdk "github.com/github/copilot-sdk/go"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Name is what this provider is called wherever a turn is attributed.
const Name = "copilot"

// noSeat is the sentence somebody with a GitHub account and no Copilot subscription gets.
//
// It exists because the alternative is what they would otherwise see: a 403, classified as an
// authentication failure, which core documents as never retry and never fall back and which sends
// somebody to replace a credential that is completely fine. The credential is fine. The account
// behind it has no seat, and that is bought rather than fixed.
const noSeat = "this GitHub account has no active Copilot subscription, so there is nothing for a " +
	"turn to run against. The sign-in itself worked and the credential is not the problem: a seat is " +
	"bought at https://github.com/settings/copilot, or granted by an organisation"

// ErrNoSeat marks the failure above so a caller can act on it rather than only print it.
var ErrNoSeat = errors.New("no Copilot subscription on this account")

// seatPhrases are how the vendor says it, and are matched rather than parsed.
//
// String matching is not a design anybody would choose and it is what is available. The Go SDK
// defines no error type for this and no constant for any of the strings its runtime produces: a
// failure arrives as a JSON-RPC error whose message the CLI wrote, or as a session error event whose
// errorType is a string documented only in a field comment. Both are checked, and the structured
// half is checked first so that a vendor who adds a code later makes this better rather than
// leaving it stuck on prose.
var seatPhrases = []string{
	"no copilot subscription",
	"not entitled",
	"copilot subscription required",
	"copilot access",
	"does not have access to copilot",
	"individual_disabled",
	"copilot_not_enabled",
	"free_limited_copilot",
}

// looksLikeNoSeat reports whether a message is the vendor saying there is no seat.
func looksLikeNoSeat(message string) bool {
	lowered := strings.ToLower(message)
	for _, phrase := range seatPhrases {
		if strings.Contains(lowered, phrase) {
			return true
		}
	}
	return false
}

// classify turns whatever the SDK returned into something a caller can act on.
//
// Everything that reaches here has already failed, so the question is only which of core's kinds it
// is, and the kinds differ in what they cause: authentication stops the chain dead, rate limited and
// overloaded move to the next credential, and the rest are reported. Getting it wrong is not a worse
// message, it is a different action taken on somebody's behalf.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var already *core.ProviderError
	if errors.As(err, &already) {
		return err
	}

	message := err.Error()
	if looksLikeNoSeat(message) {
		return &core.ProviderError{
			Kind:     core.ErrAuthentication,
			Provider: Name,
			Message:  noSeat,
			Err:      errors.Join(ErrNoSeat, err),
		}
	}

	lowered := strings.ToLower(message)
	switch {
	case strings.Contains(lowered, "rate limit") || strings.Contains(lowered, "rate_limit"):
		return &core.ProviderError{
			Kind:       core.ErrRateLimited,
			Provider:   Name,
			Message:    core.WithDetail("GitHub Copilot is rate limiting this account", message),
			StatusCode: http.StatusTooManyRequests,
			Err:        err,
		}
	case strings.Contains(lowered, "quota"):
		return &core.ProviderError{
			Kind:     core.ErrRateLimited,
			Provider: Name,
			Message: core.WithDetail(
				"this Copilot subscription has used its allowance for the period", message),
			Err: err,
		}
	case strings.Contains(lowered, "unauthorized") || strings.Contains(lowered, "authentication") ||
		strings.Contains(lowered, "bad credentials"):
		return &core.ProviderError{
			Kind:     core.ErrAuthentication,
			Provider: Name,
			Message:  core.WithDetail("GitHub rejected the sign-in for this credential", message),
			Err:      err,
		}
	case strings.Contains(lowered, "context") && strings.Contains(lowered, "limit"):
		return &core.ProviderError{
			Kind:     core.ErrContextLength,
			Provider: Name,
			Message:  core.WithDetail("the conversation is longer than the model will take", message),
			Err:      err,
		}
	default:
		return &core.ProviderError{
			Kind:     core.ErrUnknown,
			Provider: Name,
			Message:  core.WithDetail("the Copilot runtime failed", message),
			Err:      err,
		}
	}
}

// sessionError turns a session error event into the same vocabulary.
//
// This one has structure to work with, so it uses it: errorType is the vendor's own category and is
// a better answer than anything read out of the message. The message is still checked for the seat
// case, because "authorization" covers both a missing subscription and a repository somebody may not
// touch and only the first has a remedy worth naming.
func sessionError(data *sdk.SessionErrorData) error {
	status := 0
	if data.StatusCode != nil {
		status = int(*data.StatusCode)
	}

	if looksLikeNoSeat(data.Message) || looksLikeNoSeat(codeOf(data)) {
		return &core.ProviderError{
			Kind:       core.ErrAuthentication,
			Provider:   Name,
			Message:    noSeat,
			StatusCode: status,
			Err:        ErrNoSeat,
		}
	}

	kind := core.ErrUnknown
	advice := "the Copilot runtime ended the turn"
	switch data.ErrorType {
	case "authentication":
		kind, advice = core.ErrAuthentication, "GitHub rejected the sign-in for this credential"
	case "authorization":
		kind, advice = core.ErrAuthentication, "GitHub refused this account access to what the turn needed"
	case "quota":
		kind, advice = core.ErrRateLimited, "this Copilot subscription has used its allowance for the period"
	case "rate_limit":
		kind, advice = core.ErrRateLimited, "GitHub Copilot is rate limiting this account"
	case "context_limit":
		kind, advice = core.ErrContextLength, "the conversation is longer than the model will take"
	case "query":
		kind, advice = core.ErrInvalidRequest, "the Copilot runtime refused the request"
	}

	return &core.ProviderError{
		Kind:       kind,
		Provider:   Name,
		Message:    core.WithDetail(advice, strings.TrimSpace(data.Message+" "+codeOf(data))),
		StatusCode: status,
		Err:        errors.New(data.ErrorType),
	}
}

func codeOf(data *sdk.SessionErrorData) string {
	if data.ErrorCode == nil {
		return ""
	}
	return *data.ErrorCode
}

// startFailure explains a runtime that would not start.
//
// The SDK's own answer to an absent binary is Go's exec error wrapped in "failed to start CLI
// server", which names a thing the user has never heard of and does not say to install it. FindCLI
// catches that before the SDK is asked, so anything reaching here started with a binary present and
// failed for some other reason, and the message says which of the two happened rather than merging
// them.
func startFailure(err error) error {
	if strings.Contains(err.Error(), "executable file not found") {
		return missingCLI()
	}
	return classify(fmt.Errorf("the Copilot CLI would not start: %w", err))
}
