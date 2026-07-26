package core

import (
	"strings"
	"testing"
)

func TestValidateKeyName(t *testing.T) {
	valid := []string{"claude", "kimi", "minimax", "k", "claude-work", "gpt_4", "agent1", "a1"}
	for _, name := range valid {
		if err := ValidateKeyName(name); err != nil {
			t.Errorf("ValidateKeyName(%q) = %v, want nil", name, err)
		}
	}

	invalid := map[string]string{
		"":                      "empty",
		"Claude":                "uppercase",
		"my key":                "space",
		"-leading":              "leading dash",
		"has/slash":             "slash",
		"has.dot":               "dot",
		strings.Repeat("a", 32): "too long",
	}
	for name, why := range invalid {
		if err := ValidateKeyName(name); err == nil {
			t.Errorf("ValidateKeyName(%q) should fail (%s)", name, why)
		}
	}
}

// The name constraint is a safety feature rather than tidiness. A name is displayed, logged, put
// into events and written into transcripts, so a key named after its own value would travel
// everywhere the name does. Real credentials fail the pattern, which turns a paste into an error
// instead of a permanent leak.
func TestKeyNameRejectsSomethingShapedLikeACredential(t *testing.T) {
	pasted := []string{
		"sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"sk-proj-BBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		"ghp_CCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",
	}
	for _, value := range pasted {
		if err := ValidateKeyName(value); err == nil {
			t.Errorf("a pasted credential %q... was accepted as a key name", value[:10])
		}
	}
}

// A rejection must not echo back the thing it rejected, because errors get logged.
func TestKeyNameErrorDoesNotEchoTheValue(t *testing.T) {
	pasted := "sk-ant-api03-SECRETVALUE-THAT-MUST-NOT-APPEAR"
	err := ValidateKeyName(pasted)
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if strings.Contains(err.Error(), pasted) {
		t.Errorf("the error echoed the rejected value: %s", err)
	}
	if strings.Contains(err.Error(), "SECRETVALUE") {
		t.Errorf("the error echoed part of the rejected value: %s", err)
	}
}

func TestKeyRefValidate(t *testing.T) {
	good := KeyRef{Name: "claude", Provider: ProviderAnthropic}
	if err := good.Validate(); err != nil {
		t.Errorf("valid ref rejected: %v", err)
	}
	if err := (KeyRef{Name: "claude", Provider: "gemini"}).Validate(); err == nil {
		t.Error("unknown provider should be rejected")
	}
	if err := (KeyRef{Provider: ProviderAnthropic}).Validate(); err == nil {
		t.Error("missing name should be rejected")
	}
	if !(KeyRef{}).IsZero() {
		t.Error("the zero ref is zero")
	}
}

func TestKeyMetadataRequiresBaseURLWhereNeeded(t *testing.T) {
	openaiRef := KeyRef{Name: "kimi", Provider: ProviderOpenAICompatible}

	if err := (KeyMetadata{Ref: openaiRef}).Validate(); err == nil {
		t.Error("an openai-compatible key without a base URL should be rejected, since there is " +
			"no sensible default endpoint to fall back to")
	}
	if err := (KeyMetadata{Ref: openaiRef, BaseURL: "https://example.invalid/v1"}).Validate(); err != nil {
		t.Errorf("valid metadata rejected: %v", err)
	}
	if err := (KeyMetadata{Ref: KeyRef{Name: "claude", Provider: ProviderAnthropic}}).Validate(); err != nil {
		t.Errorf("anthropic needs no base URL: %v", err)
	}
}

func TestTrustLevelOrdering(t *testing.T) {
	if !TrustBroad.AtLeast(TrustReadOnly) {
		t.Error("broad is at least read-only")
	}
	if TrustReadOnly.AtLeast(TrustStandard) {
		t.Error("read-only is not at least standard")
	}
	if !TrustStandard.AtLeast(TrustStandard) {
		t.Error("a level is at least itself")
	}
}

// An unrecognised trust level must reduce what an agent can do, never grant it something. A typo
// in a config file should fail closed.
func TestUnknownTrustLevelFailsClosed(t *testing.T) {
	bogus := TrustLevel("superuser")

	if bogus.Valid() {
		t.Error("an invented level is not valid")
	}
	for _, real := range AllTrustLevels() {
		if bogus.AtLeast(real) {
			t.Errorf("unknown level claimed to be at least %q", real)
		}
	}
	if bogus.AllowsWrites() || bogus.AllowsShell() || bogus.AllowsDestructiveGit() {
		t.Error("an unknown level must permit nothing")
	}

	profile := AgentProfile{Name: "p", Key: KeyRef{Name: "claude", Provider: ProviderAnthropic}, Model: "m", Trust: bogus}
	if got := profile.EffectiveTrust(); got != TrustReadOnly {
		t.Errorf("a profile with an unknown trust level resolved to %q, want read-only. "+
			"An unrecognised value must not quietly grant the usual amount.", got)
	}
}

func TestTrustCapabilities(t *testing.T) {
	cases := []struct {
		level                         TrustLevel
		writes, shell, destructiveGit bool
	}{
		{TrustReadOnly, false, false, false},
		{TrustConfined, true, false, false},
		{TrustStandard, true, true, false},
		{TrustBroad, true, true, true},
	}
	for _, tc := range cases {
		if got := tc.level.AllowsWrites(); got != tc.writes {
			t.Errorf("%q AllowsWrites() = %v, want %v", tc.level, got, tc.writes)
		}
		if got := tc.level.AllowsShell(); got != tc.shell {
			t.Errorf("%q AllowsShell() = %v, want %v", tc.level, got, tc.shell)
		}
		if got := tc.level.AllowsDestructiveGit(); got != tc.destructiveGit {
			t.Errorf("%q AllowsDestructiveGit() = %v, want %v", tc.level, got, tc.destructiveGit)
		}
	}
}

func TestProfileValidate(t *testing.T) {
	key := KeyRef{Name: "claude", Provider: ProviderAnthropic}
	other := KeyRef{Name: "kimi", Provider: ProviderOpenAICompatible}

	good := AgentProfile{Name: "sonnet", Key: key, Model: "some-model", Trust: TrustStandard}
	if err := good.Validate(); err != nil {
		t.Errorf("valid profile rejected: %v", err)
	}

	bad := map[string]AgentProfile{
		"no name":            {Key: key, Model: "m"},
		"no key":             {Name: "p", Model: "m"},
		"no model":           {Name: "p", Key: key},
		"unknown trust":      {Name: "p", Key: key, Model: "m", Trust: "root"},
		"negative budget":    {Name: "p", Key: key, Model: "m", BudgetUSD: -1},
		"negative maxtokens": {Name: "p", Key: key, Model: "m", MaxTokens: -1},
		"bad fallback":       {Name: "p", Key: key, Model: "m", Fallbacks: []KeyRef{{Name: "x", Provider: "nope"}}},

		// Falling back to the key that just failed accomplishes nothing and hides the real
		// problem behind a retry.
		"self fallback": {Name: "p", Key: key, Model: "m", Fallbacks: []KeyRef{key}},
	}
	for why, profile := range bad {
		if err := profile.Validate(); err == nil {
			t.Errorf("profile with %s should be rejected", why)
		}
	}

	withFallback := AgentProfile{Name: "p", Key: key, Model: "m", Fallbacks: []KeyRef{other}}
	if err := withFallback.Validate(); err != nil {
		t.Errorf("a distinct fallback is fine: %v", err)
	}
}

func TestProfileDefaultsAndKeyChain(t *testing.T) {
	key := KeyRef{Name: "claude", Provider: ProviderAnthropic}
	other := KeyRef{Name: "kimi", Provider: ProviderOpenAICompatible}

	if got := (AgentProfile{Name: "p", Key: key, Model: "m"}).EffectiveTrust(); got != DefaultTrust {
		t.Errorf("unset trust resolved to %q, want the default %q", got, DefaultTrust)
	}

	profile := AgentProfile{Name: "p", Key: key, Model: "m", Fallbacks: []KeyRef{other}}
	chain := profile.KeyChain()
	if len(chain) != 2 || chain[0] != key || chain[1] != other {
		t.Errorf("KeyChain() = %v, want the primary first then fallbacks", chain)
	}

	// The chain must be a copy. Handing out the profile's own slice would let a caller reorder
	// which credential gets used.
	chain[0] = other
	if profile.Key != key {
		t.Error("KeyChain returned a slice aliasing the profile's own state")
	}
}

func TestProviderBaseURLRequirement(t *testing.T) {
	if ProviderAnthropic.RequiresBaseURL() {
		t.Error("anthropic has a known endpoint")
	}
	if !ProviderOpenAICompatible.RequiresBaseURL() {
		t.Error("openai-compatible is defined by its endpoint, so it needs one")
	}
	if ProviderAnthropic.Valid() != true || Provider("gemini").Valid() != false {
		t.Error("provider validity is wrong")
	}
}
