package session

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/copilot"
)

// storeWithCopilot puts one signed-in Copilot credential in a store.
func storeWithCopilot(t *testing.T) *keys.Store {
	t.Helper()
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	if _, err := store.PutSignIn(
		core.KeyMetadata{
			Ref:     core.KeyRef{Name: "mycopilot", Provider: core.ProviderOpenAICompatible},
			BaseURL: copilot.BaseURL,
		},
		keys.SignIn{Kind: keys.KindSignedIn, Account: "walid", Route: copilot.Route},
		keys.Tokens{Access: core.NewSecret("gho_TOKEN")},
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	return store
}

// The reason ResolveFor exists. GitHub's agent owns the conversation and cannot be handed a history,
// so a resolver that built a fresh client every turn would open a fresh session every turn, and
// every message after the first would be answered by an agent with amnesia while the transcript on
// screen said otherwise.
func TestOneConversationKeepsOneCopilotClientAcrossItsTurns(t *testing.T) {
	resolver := NewKeyResolver(storeWithCopilot(t))
	t.Cleanup(resolver.Close)

	first, _, err := resolver.ResolveFor("conversation-1", "mycopilot", "gpt-5.2-codex")
	if err != nil {
		t.Fatalf("ResolveFor: %v", err)
	}
	second, _, err := resolver.ResolveFor("conversation-1", "mycopilot", "gpt-5.2-codex")
	if err != nil {
		t.Fatalf("ResolveFor: %v", err)
	}
	if first != second {
		t.Error("a second turn on one conversation got a second session, which has never heard the first")
	}

	other, _, err := resolver.ResolveFor("conversation-2", "mycopilot", "gpt-5.2-codex")
	if err != nil {
		t.Fatalf("ResolveFor: %v", err)
	}
	if other == first {
		t.Error("two conversations shared one session, so each is reading the other's history")
	}
}

// An aside and a compaction resolve without a conversation, and on this route that is the right
// answer rather than a gap: an aside is a separate conversation by definition and a compaction is
// one question asked once about a transcript. Sharing the conversation's session would put a
// summarisation request into the middle of somebody's session.
func TestAnAsideGetsAClientOfItsOwnRatherThanTheConversationsSession(t *testing.T) {
	resolver := NewKeyResolver(storeWithCopilot(t))
	t.Cleanup(resolver.Close)

	held, _, err := resolver.ResolveFor("conversation-1", "mycopilot", "m")
	if err != nil {
		t.Fatalf("ResolveFor: %v", err)
	}
	aside, _, err := resolver.Resolve("mycopilot", "m")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if aside == held {
		t.Error("an aside was given the conversation's own session")
	}

	another, _, err := resolver.Resolve("mycopilot", "m")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if another == aside {
		t.Error("two asides shared one session, so every aside in the program reads the last one's")
	}
	if closer, ok := aside.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	if closer, ok := another.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

// A conversation signed in again as somebody else is a different subscription, and inheriting the
// previous person's session would be a turn billed to one account arriving in a session opened by
// another.
func TestSigningInAsSomebodyElseStartsANewConversationWithTheVendor(t *testing.T) {
	store := storeWithCopilot(t)
	resolver := NewKeyResolver(store)
	t.Cleanup(resolver.Close)

	before, _, err := resolver.ResolveFor("conversation-1", "mycopilot", "m")
	if err != nil {
		t.Fatalf("ResolveFor: %v", err)
	}

	if _, err := store.PutSignIn(
		core.KeyMetadata{
			Ref:     core.KeyRef{Name: "mycopilot", Provider: core.ProviderOpenAICompatible},
			BaseURL: copilot.BaseURL,
		},
		keys.SignIn{Kind: keys.KindSignedIn, Account: "somebody-else", Route: copilot.Route},
		keys.Tokens{Access: core.NewSecret("gho_ANOTHER-TOKEN")},
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}

	after, _, err := resolver.ResolveFor("conversation-1", "mycopilot", "m")
	if err != nil {
		t.Fatalf("ResolveFor: %v", err)
	}
	if after == before {
		t.Error("a credential signed in as a different account kept the previous account's session")
	}
}

// A held conversation is a child process on the machine and a session GitHub believes is open. Both
// end when Canopy does, and nothing else is going to clean up either.
func TestClosingTheResolverEndsEveryConversationItWasHolding(t *testing.T) {
	resolver := NewKeyResolver(storeWithCopilot(t))

	for _, id := range []string{"one", "two", "three"} {
		if _, _, err := resolver.ResolveFor(id, "mycopilot", "m"); err != nil {
			t.Fatalf("ResolveFor(%q): %v", id, err)
		}
	}
	resolver.Close()

	if len(resolver.sessions) != 0 {
		t.Errorf("%d conversations survived the shutdown", len(resolver.sessions))
	}
	if _, _, err := resolver.ResolveFor("four", "mycopilot", "m"); err == nil {
		t.Error("a shut down resolver quietly started a new runtime")
	}
}

// A pasted credential is unaffected by any of this, and asking for one twice can keep giving out a
// new client, because a stateless provider has nothing to remember.
func TestAPastedCredentialIsStillBuiltFreshEveryTurn(t *testing.T) {
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	if _, err := store.Put(
		core.KeyMetadata{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		core.NewSecret("sk-ant-notreal"),
	); err != nil {
		t.Fatalf("Put: %v", err)
	}

	resolver := NewKeyResolver(store)
	t.Cleanup(resolver.Close)

	first, _, err := resolver.ResolveFor("conversation-1", "claude", "claude-opus-5")
	if err != nil {
		t.Fatalf("ResolveFor: %v", err)
	}
	second, _, err := resolver.ResolveFor("conversation-1", "claude", "claude-opus-5")
	if err != nil {
		t.Fatalf("ResolveFor: %v", err)
	}
	if first == second {
		t.Error("a pasted credential is being held open, which is a resource nothing needs")
	}
	if len(resolver.sessions) != 0 {
		t.Error("a pasted credential was recorded as a conversation to shut down")
	}
}

// conversationAware is a resolver that records which conversation it was asked about.
type conversationAware struct {
	mu    sync.Mutex
	asked []string
	inner Resolver
}

func (r *conversationAware) Resolve(name, model string) (core.ProviderClient, pricing.ModelID, error) {
	return r.ResolveFor("", name, model)
}

func (r *conversationAware) ResolveFor(
	conversation, name, model string,
) (core.ProviderClient, pricing.ModelID, error) {
	r.mu.Lock()
	r.asked = append(r.asked, conversation)
	r.mu.Unlock()
	return r.inner.Resolve(name, model)
}

func (r *conversationAware) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.asked...)
}

// The engine has to pass the conversation on, or none of the above is reachable from a real turn.
// The optional interface is what keeps every other resolver, including four fakes in this package,
// working unchanged.
func TestTheEngineTellsAResolverWhichConversationATurnIsFor(t *testing.T) {
	client := &scriptedClient{name: "fake", events: reply("hello")}
	resolver := &conversationAware{inner: fixedResolver{client: client, id: anthropicID()}}

	engine := New(resolver)
	t.Cleanup(engine.Close)

	created := engine.Create("claude", "claude-opus-5")
	if _, err := engine.Send(created.ID, "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sessionID := created.ID

	deadline := time.After(10 * time.Second)
	for {
		if seen := resolver.seen(); len(seen) > 0 {
			if seen[0] != sessionID {
				t.Errorf("the resolver was asked about conversation %q, want %q", seen[0], sessionID)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("the turn never reached the resolver")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
