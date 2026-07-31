package session

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider"
)

// The message a first run ends on, so the key it names has to be the key that works.
//
// It said "press k", which opens the credential screen from the worktree monitor and does nothing
// at all in a conversation, which is where somebody reads this. A message that names the wrong key
// teaches people that the program's instructions are not worth following.
func TestTheEmptyCredentialMessageNamesTheKeyThatOpensTheList(t *testing.T) {
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))

	_, _, err := NewKeyResolver(store, "test").Resolve("", "claude-opus-5")
	if err == nil {
		t.Fatal("resolving with nothing stored should refuse and say what to do about it")
	}
	if !strings.Contains(err.Error(), "press ctrl+k") {
		t.Errorf("the message is %q, and ctrl+k is what opens the credential screen", err)
	}
}

// countingSource is a token endpoint that records whether it was asked for anything.
type countingSource struct {
	calls  atomic.Int64
	access string
}

func (s *countingSource) Name() string { return "the fake vendor" }

func (s *countingSource) Refresh(context.Context, keys.SignIn, keys.Tokens) (keys.Renewal, error) {
	s.calls.Add(1)
	at := time.Now().Add(time.Hour)
	return keys.Renewal{Tokens: keys.Tokens{Access: core.NewSecret(s.access)}, ExpiresAt: &at}, nil
}

// signedIn stores one credential somebody signed in to, pointing at a test server, and returns a
// resolver that renews it against source.
func signedIn(
	t *testing.T, baseURL string, expiresIn time.Duration, source keys.TokenSource,
) (*keys.Store, *KeyResolver) {
	t.Helper()
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	ref := core.KeyRef{Name: "copilot", Provider: core.ProviderOpenAICompatible}
	// The real clock, deliberately, so what decides here is keys.RefreshMargin as shipped rather
	// than a window the test moved to suit itself.
	expires := time.Now().Add(expiresIn)
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: ref, BaseURL: baseURL},
		keys.SignIn{Kind: keys.KindSignedIn, Account: "walid@example.invalid", ExpiresAt: &expires},
		keys.Tokens{
			Access:  core.NewSecret("gho_THE-OLD-ACCESS-TOKEN"),
			Refresh: core.NewSecret("ghr_THE-REFRESH-TOKEN"),
		},
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}

	resolver := NewKeyResolver(store, "test")
	resolver.Renews(func(core.KeyMetadata, keys.SignIn) (keys.TokenSource, bool) {
		return source, true
	})
	return store, resolver
}

func oneMessage() core.Request {
	return core.Request{
		Model:    "gpt-5.2-codex",
		Messages: []core.Message{{Role: core.RoleUser, Text: "hello"}},
	}
}

// The end of the path this task exists for. A credential close enough to expiry to be a risk is
// renewed while the client is being built, so the token that leaves the machine is the one that was
// just bought rather than the one that was about to stop working.
func TestASignedInCredentialReachesTheProviderWithTheTokenItJustRenewed(t *testing.T) {
	var mu sync.Mutex
	var sent string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sent = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	source := &countingSource{access: "gho_THE-NEW-ACCESS-TOKEN"}
	store, resolver := signedIn(t, srv.URL, time.Minute, source)

	client, _, err := resolver.Resolve("copilot", "gpt-5.2-codex")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	stream, err := client.Stream(context.Background(), oneMessage())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	mu.Lock()
	got := sent
	mu.Unlock()
	if got != "Bearer gho_THE-NEW-ACCESS-TOKEN" {
		t.Errorf("the request carried %q, want the renewed token", got)
	}
	if source.calls.Load() != 1 {
		t.Errorf("the vendor was asked %d times, want once", source.calls.Load())
	}

	tokens, err := store.Tokens(core.KeyRef{Name: "copilot"})
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if tokens.Access.Reveal() != "gho_THE-NEW-ACCESS-TOKEN" {
		t.Error("the renewed token was used and not written down")
	}
	// The vendor issued no new refresh token, which means keep the one you have.
	if tokens.Refresh.Reveal() != "ghr_THE-REFRESH-TOKEN" {
		t.Errorf("the refresh token became %q", tokens.Refresh.Reveal())
	}
}

// The claim that renewing ahead of the request is what protects: because no expired token can reach
// the wire, a 401 that comes back still means exactly what core says it means. Nothing renews in
// response to it, nothing retries it, and the chain does not bill the next credential instead.
func TestA401FromALiveTokenIsStillTerminalAndIsNotAnsweredByRenewing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad credentials"}}`)
	}))
	defer srv.Close()

	source := &countingSource{access: "gho_THE-NEW-ACCESS-TOKEN"}
	store, resolver := signedIn(t, srv.URL, time.Hour, source)

	client, _, err := resolver.Resolve("copilot", "gpt-5.2-codex")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	spare := &scriptedClient{name: "spare", events: []core.StreamEvent{{Kind: core.EventText, Text: "hi"}}}
	chain := provider.NewChain(
		provider.Link{Name: "copilot", Client: client},
		provider.Link{Name: "spare", Client: spare},
	)

	_, err = chain.Stream(context.Background(), oneMessage())
	if err == nil {
		t.Fatal("a rejected credential produced a stream")
	}
	var provErr *core.ProviderError
	if !errors.As(err, &provErr) {
		t.Fatalf("the failure is %v, want a provider error", err)
	}
	if provErr.Kind != core.ErrAuthentication {
		t.Errorf("a 401 classified as %q, want %q", provErr.Kind, core.ErrAuthentication)
	}
	if len(spare.History()) != 0 {
		t.Error("the chain billed the next credential after the first one was rejected")
	}
	if source.calls.Load() != 0 {
		t.Errorf("a rejection caused %d renewals, and renewing is not an answer to one",
			source.calls.Load())
	}

	tokens, err := store.Tokens(core.KeyRef{Name: "copilot"})
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if tokens.Access.Reveal() != "gho_THE-OLD-ACCESS-TOKEN" {
		t.Error("a rejection replaced the stored token")
	}
}
