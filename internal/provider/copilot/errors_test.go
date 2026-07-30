package copilot

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The acceptance clause about a GitHub account with no Copilot seat, and the reason it is a clause.
//
// The natural outcome is a 403, classified as an authentication failure, which core documents as
// never retry and never fall back and which sends somebody to replace a credential that is
// completely fine. The credential is fine. The account behind it has no seat, and a seat is bought
// rather than fixed.
func TestAnAccountWithNoCopilotSeatIsToldThatRatherThanThatItsCredentialIsWrong(t *testing.T) {
	said := []error{
		errors.New("failed to create session: no Copilot subscription found for this user"),
		errors.New("403: user does not have access to Copilot"),
		fmt.Errorf("wrapped: %w", errors.New("access type is free_limited_copilot")),
	}
	for _, err := range said {
		classified := classify(err)
		if !errors.Is(classified, ErrNoSeat) {
			t.Errorf("%v was not recognised as a missing seat", err)
			continue
		}
		var provErr *core.ProviderError
		if !errors.As(classified, &provErr) {
			t.Errorf("%v did not become a provider error", err)
			continue
		}
		if !strings.Contains(provErr.Message, "no active Copilot subscription") {
			t.Errorf("the message is %q, and it has to name the actual problem", provErr.Message)
		}
		if !strings.Contains(provErr.Message, "github.com/settings/copilot") {
			t.Errorf("the message does not say where a seat comes from: %q", provErr.Message)
		}
		if strings.Contains(strings.ToLower(provErr.Message), "check your credential") {
			t.Errorf("the message sends somebody to replace a credential that is fine: %q", provErr.Message)
		}
	}
}

// The same thing arriving the other way, as a session error event during a turn rather than as a
// failed call. The vendor gives a category there, which is a better answer than anything read out of
// a message, and the message is still checked because "authorization" covers both a missing
// subscription and a repository somebody may not touch.
func TestAMissingSeatIsRecognisedWhenItArrivesDuringATurnToo(t *testing.T) {
	status := int32(403)
	err := sessionError(&sdk.SessionErrorData{
		ErrorType:  "authorization",
		Message:    "no Copilot subscription on this account",
		StatusCode: &status,
	})
	if !errors.Is(err, ErrNoSeat) {
		t.Fatalf("a session error about a subscription was classified as %v", err)
	}
	var provErr *core.ProviderError
	if !errors.As(err, &provErr) || provErr.StatusCode != 403 {
		t.Errorf("the failure lost the status the vendor gave: %v", err)
	}
}

// The vendor's own categories decide, because the actions they imply differ: a rate limit moves to
// the next credential, an authentication failure must never fall through, and a context limit is
// compacted rather than retried. Getting these wrong is not a worse message, it is a different
// action taken on somebody's behalf.
func TestTheVendorsOwnCategoriesDecideWhatHappensNext(t *testing.T) {
	for _, tc := range []struct {
		errorType string
		want      core.ProviderErrorKind
		fallback  bool
	}{
		{"authentication", core.ErrAuthentication, false},
		{"authorization", core.ErrAuthentication, false},
		{"quota", core.ErrRateLimited, true},
		{"rate_limit", core.ErrRateLimited, true},
		{"context_limit", core.ErrContextLength, false},
		{"query", core.ErrInvalidRequest, false},
		{"something new", core.ErrUnknown, false},
	} {
		err := sessionError(&sdk.SessionErrorData{ErrorType: tc.errorType, Message: "detail"})
		var provErr *core.ProviderError
		if !errors.As(err, &provErr) {
			t.Errorf("%q did not become a provider error", tc.errorType)
			continue
		}
		if provErr.Kind != tc.want {
			t.Errorf("%q classified as %q, want %q", tc.errorType, provErr.Kind, tc.want)
		}
		if provErr.AllowsFallback() != tc.fallback {
			t.Errorf("%q allows fallback = %v, want %v", tc.errorType, provErr.AllowsFallback(), tc.fallback)
		}
		if !strings.Contains(provErr.Message, "detail") {
			t.Errorf("%q lost what the vendor actually said: %q", tc.errorType, provErr.Message)
		}
	}
}

// The vendor's fine-grained code is carried through as well as its category, because "quota
// exceeded" and "billing not configured" are the same category and different problems.
func TestTheVendorsOwnErrorCodeSurvivesIntoTheMessage(t *testing.T) {
	code := "billing_not_configured"
	err := sessionError(&sdk.SessionErrorData{
		ErrorType: "quota", Message: "cannot run", ErrorCode: &code,
	})
	if !strings.Contains(err.Error(), code) {
		t.Errorf("the code the vendor gave did not survive: %v", err)
	}
}

// A failure that is already classified is left alone rather than being reclassified by reading its
// own printed message, which is how a rate limit becomes an unknown failure one layer up.
func TestAFailureThatIsAlreadyClassifiedIsNotClassifiedAgain(t *testing.T) {
	original := &core.ProviderError{Kind: core.ErrOverloaded, Provider: Name, Message: "busy"}
	got := classify(original)
	var provErr *core.ProviderError
	if !errors.As(got, &provErr) || provErr.Kind != core.ErrOverloaded {
		t.Errorf("an already classified failure became %v", got)
	}
}

// A runtime that will not start because the binary is gone says what to install, whichever layer
// notices. FindCLI catches it first, and this is the path where something else did.
func TestARuntimeThatCouldNotStartForWantOfABinarySaysWhatToInstall(t *testing.T) {
	err := startFailure(errors.New(`failed to start CLI server: exec: "copilot": executable file not found in $PATH`))
	if !strings.Contains(err.Error(), "@github/copilot") {
		t.Errorf("the failure does not say what to install: %v", err)
	}

	other := startFailure(errors.New("failed to start CLI server: permission denied"))
	if strings.Contains(other.Error(), "@github/copilot") {
		t.Errorf("a runtime that was present and would not run was reported as absent: %v", other)
	}
}

// The permission decision constants this package depends on exist and mean what it thinks. A rename
// in the SDK would otherwise be a compile error in one place and a silently different decision here.
func TestTheRejectionThisPackageSendsIsTheVendorsOwnRejection(t *testing.T) {
	feedback := "no"
	var decision rpc.PermissionDecision = &rpc.PermissionDecisionReject{Feedback: &feedback}
	if decision.Kind() != rpc.PermissionDecisionKindReject {
		t.Errorf("the rejection reports itself as %q", decision.Kind())
	}
	decision = &rpc.PermissionDecisionApproveOnce{}
	if decision.Kind() != rpc.PermissionDecisionKindApproveOnce {
		t.Errorf("the approval reports itself as %q", decision.Kind())
	}
}
