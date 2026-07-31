package session

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/acp"
)

// Resolving a credential that drives somebody else's agent.
//
// In package session rather than session_test, because delegatedAgent is what makes these runnable
// on a machine with no Claude Code and it is unexported for the reason every other seam like it is.

func machineWith(t *testing.T, present map[string]string, says string) {
	t.Helper()

	original := delegatedAgent
	delegatedAgent = acp.Discovery{
		LookPath: func(name string) (string, error) {
			if path, ok := present[name]; ok {
				return path, nil
			}
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		},
		Status: func(ctx context.Context, cli string) (string, error) { return says, nil },
		Getenv: func(string) string { return "" },
	}
	t.Cleanup(func() { delegatedAgent = original })
}

func storeWithADelegatedCredential(t *testing.T) *keys.Store {
	t.Helper()

	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	_, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		keys.SignIn{Kind: keys.KindDelegated, Account: "walid@example.com"},
		keys.Tokens{},
	)
	if err != nil {
		t.Fatalf("storing a delegated credential: %v", err)
	}
	return store
}

func TestADelegatedCredentialResolvesToTheDelegatedRouteRatherThanToTheAnthropicApi(t *testing.T) {
	machineWith(t, map[string]string{
		"claude":           "/usr/local/bin/claude",
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
	}, `{"loggedIn":true,"authMethod":"claude.ai","email":"walid@example.com","subscriptionType":"max"}`)

	client, id, err := NewKeyResolver(storeWithADelegatedCredential(t), "test").Resolve("claude", "sonnet")
	if err != nil {
		t.Fatalf("resolving a delegated credential: %v", err)
	}

	if got := client.Name(); got != "claude-code" {
		t.Errorf("the resolved client is %q, want the delegated route", got)
	}
	if !id.Delegated {
		t.Error("the resolved model identity is not marked delegated, so a turn on a plan that is " +
			"billed monthly would be given a per-token price")
	}
}

// The cost clause, held where the figure would actually be produced.
func TestATurnOnADelegatedCredentialIsUnpricedRatherThanFree(t *testing.T) {
	machineWith(t, map[string]string{
		"claude":           "/usr/local/bin/claude",
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
	}, `{"loggedIn":true,"authMethod":"claude.ai","email":"walid@example.com","subscriptionType":"max"}`)

	// A model that the dated table does price, so this is the case where a figure was available and
	// is deliberately not shown.
	_, id, err := NewKeyResolver(storeWithADelegatedCredential(t), "test").Resolve("claude", "claude-sonnet-5")
	if err != nil {
		t.Fatalf("resolving a delegated credential: %v", err)
	}

	priced, reason := pricing.Apply(id, core.Usage{InputTokens: 1000, OutputTokens: 1000})
	if priced.CostKnown {
		t.Errorf("a delegated turn was priced at $%v", priced.CostUSD)
	}
	if priced.CostUSD != 0 {
		t.Errorf("a delegated turn carries a cost of %v", priced.CostUSD)
	}
	if !strings.Contains(reason, "signed in to yourself") {
		t.Errorf("the missing figure was not explained: %q", reason)
	}
	if priced.InputTokens != 1000 || priced.OutputTokens != 1000 {
		t.Error("pricing dropped the token counts, which are real and are the only thing Canopy " +
			"can honestly report about what a delegated turn consumed")
	}
}

func TestAMachineWithoutClaudeCodeSaysSoWhenTheCredentialIsUsedRatherThanFailingLater(t *testing.T) {
	machineWith(t, map[string]string{}, "")

	_, _, err := NewKeyResolver(storeWithADelegatedCredential(t), "test").Resolve("claude", "")
	if err == nil {
		t.Fatal("a delegated credential resolved on a machine with no Claude Code")
	}
	if !strings.Contains(err.Error(), `key "claude" delegates to Claude Code`) {
		t.Errorf("the failure does not say which credential it was about: %v", err)
	}
	if !strings.Contains(err.Error(), "claude.com/claude-code") {
		t.Errorf("the failure does not name what to install: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "exec:") {
		t.Errorf("a missing installation surfaced as an exec error: %v", err)
	}
}

func TestAPastedCredentialStillResolvesTheWayItAlwaysDid(t *testing.T) {
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	if _, err := store.Put(
		core.KeyMetadata{Ref: core.KeyRef{Name: "api", Provider: core.ProviderAnthropic}},
		core.NewSecret("sk-ant-not-a-real-key"),
	); err != nil {
		t.Fatalf("storing a pasted credential: %v", err)
	}

	client, id, err := NewKeyResolver(store, "test").Resolve("api", "claude-sonnet-5")
	if err != nil {
		t.Fatalf("resolving a pasted credential: %v", err)
	}
	if client.Name() == "claude-code" {
		t.Error("a pasted Anthropic credential was routed through the delegated agent")
	}
	if id.Delegated {
		t.Error("a pasted credential was marked delegated, so its turns would stop being priced")
	}
	if priced, _ := pricing.Apply(id, core.Usage{OutputTokens: 1_000_000}); !priced.CostKnown {
		t.Error("a pasted credential stopped being priced")
	}
}
