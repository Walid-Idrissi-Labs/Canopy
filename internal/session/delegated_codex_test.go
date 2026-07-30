package session

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/codex"
)

// Resolving the ChatGPT credential, which is the second delegated route and the one that proves the
// fork has to be on the route rather than on the provider.
//
// In package session rather than session_test, because delegatedCodex is what makes these runnable
// on a machine with no Codex and it is unexported for the reason every other seam like it is.

func codexMachineWith(t *testing.T, present map[string]string) {
	t.Helper()

	original := delegatedCodex
	delegatedCodex = codex.Discovery{
		LookPath: func(name string) (string, error) {
			if path, ok := present[name]; ok {
				return path, nil
			}
			return "", errors.New("executable file not found in $PATH")
		},
		Getenv:   func(string) string { return "" },
		HomeDir:  func() (string, error) { return t.TempDir(), nil },
		ReadFile: func(string) ([]byte, error) { return nil, errors.New("nothing there") },
	}
	t.Cleanup(func() { delegatedCodex = original })
}

func storeWithACodexCredential(t *testing.T) *keys.Store {
	t.Helper()

	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	_, err := store.PutSignIn(
		core.KeyMetadata{
			Ref:     core.KeyRef{Name: "chatgpt", Provider: core.ProviderOpenAICompatible},
			BaseURL: codex.BaseURL,
		},
		keys.SignIn{
			Kind:    keys.KindDelegated,
			Account: "someone@example.com (pro)",
			Route:   codex.Route,
		},
		keys.Tokens{},
	)
	if err != nil {
		t.Fatalf("storing a delegated ChatGPT credential: %v", err)
	}
	return store
}

func TestACodexCredentialResolvesToTheAppServerRatherThanToAnOpenAIEndpoint(t *testing.T) {
	codexMachineWith(t, map[string]string{"codex": "/usr/local/bin/codex"})

	client, id, err := NewKeyResolver(storeWithACodexCredential(t)).Resolve("chatgpt", "")
	if err != nil {
		t.Fatalf("resolving the ChatGPT credential: %v", err)
	}

	if got := client.Name(); got != "codex" {
		t.Fatalf("the credential resolved to a %q client. It is openai-compatible by provider and "+
			"carries a chatgpt.com base URL, so a switch on provider alone would have built an HTTP "+
			"client and posted an empty Authorization header to it", got)
	}
	if !id.Delegated {
		t.Error("the turn was not marked delegated, so a dollar figure derived from token counts " +
			"would appear beside a turn metered against a monthly plan")
	}
}

// The regression this fork exists to prevent: two delegated routes, two different vendors.
func TestTheTwoDelegatedRoutesDoNotResolveToEachOthersAgents(t *testing.T) {
	codexMachineWith(t, map[string]string{"codex": "/usr/local/bin/codex"})
	machineWith(t, map[string]string{
		"claude":           "/usr/local/bin/claude",
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
	}, `{"loggedIn":true,"authMethod":"claude.ai","email":"walid@example.com","subscriptionType":"max"}`)

	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	if _, err := store.PutSignIn(
		core.KeyMetadata{
			Ref:     core.KeyRef{Name: "chatgpt", Provider: core.ProviderOpenAICompatible},
			BaseURL: codex.BaseURL,
		},
		keys.SignIn{Kind: keys.KindDelegated, Account: "someone@example.com", Route: codex.Route},
		keys.Tokens{},
	); err != nil {
		t.Fatalf("storing the ChatGPT credential: %v", err)
	}
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		keys.SignIn{Kind: keys.KindDelegated, Account: "walid@example.com"},
		keys.Tokens{},
	); err != nil {
		t.Fatalf("storing the Claude credential: %v", err)
	}

	resolver := NewKeyResolver(store)
	for name, want := range map[string]string{"chatgpt": "codex", "claude": "claude-code"} {
		client, _, err := resolver.Resolve(name, "")
		if err != nil {
			t.Fatalf("resolving %q: %v", name, err)
		}
		if got := client.Name(); got != want {
			t.Errorf("the %q credential resolved to a %q client, want %q. Both are delegated and "+
				"they reach different vendors, so the fork has to be on the route", name, got, want)
		}
	}
}

// A machine with no Codex says so where the turn was asked for, not as an exec error later.
func TestAMachineWithoutCodexSaysSoWhenTheCredentialIsUsed(t *testing.T) {
	codexMachineWith(t, nil)

	_, _, err := NewKeyResolver(storeWithACodexCredential(t)).Resolve("chatgpt", "")
	if err == nil {
		t.Fatal("a credential whose agent is not installed resolved to a working client")
	}
	if !errors.Is(err, codex.ErrCodexMissing) {
		t.Errorf("the failure was %v, want it to wrap ErrCodexMissing", err)
	}
	for _, want := range []string{"chatgpt", "@openai/codex"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not mention %q, so it names neither the credential nor the "+
				"remedy: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "no such file") {
		t.Errorf("an exec error reached the surface: %v", err)
	}
}
