package session

import (
	"context"
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
	resolver := NewKeyResolver(storeWithCopilot(t), "test")
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
	resolver := NewKeyResolver(storeWithCopilot(t), "test")
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
	resolver := NewKeyResolver(store, "test")
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
//
// The session behind each one is a fake that records being closed, and that is the whole difference
// between this and the version of it that shipped. That one asserted the resolver's own map was
// empty afterwards, which a shutdown that dropped every client without closing it satisfies
// perfectly: the map is the bookkeeping, and the process and the vendor's session are the thing.
func TestClosingTheResolverEndsEveryConversationItWasHolding(t *testing.T) {
	resolver := NewKeyResolver(storeWithCopilot(t), "test")
	sessions := copilotSessionsAreFakes(resolver)

	for _, id := range []string{"one", "two", "three"} {
		client, _, err := resolver.ResolveFor(id, "mycopilot", "m")
		if err != nil {
			t.Fatalf("ResolveFor(%q): %v", id, err)
		}
		// A turn each, so there is a session to leave open rather than only a client to forget.
		runOneTurn(t, client)
	}
	if opened := sessions.opened(); opened != 3 {
		t.Fatalf("%d sessions were opened for three conversations", opened)
	}

	resolver.Close()

	if closed := sessions.closed(); closed != 3 {
		t.Errorf("%d of the three sessions were closed by the shutdown. Each one left open is a "+
			"`copilot` process still resident and a session GitHub believes is live", closed)
	}
	if held := resolver.vendors.copilots.Live(); held != 0 {
		t.Errorf("%d conversations survived the shutdown", held)
	}
	if _, _, err := resolver.ResolveFor("four", "mycopilot", "m"); err == nil {
		t.Error("a shut down resolver quietly started a new runtime")
	}
}

// runOneTurn drives a client through one complete turn, so that a session exists behind it.
func runOneTurn(t *testing.T, client core.ProviderClient) {
	t.Helper()

	stream, err := client.Stream(context.Background(), core.Request{
		Model:    "m",
		Messages: []core.Message{{Role: core.RoleUser, Text: "hello"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for stream.Next() {
		_ = stream.Event()
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("closing the stream: %v", err)
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

	resolver := NewKeyResolver(store, "test")
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
	if held := resolver.vendors.copilots.Live(); held != 0 {
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

// Everything that builds a client on this route ends what it built.
//
// The build this fixes claimed exactly that and delivered it for the main conversation alone. An
// aside, a compaction and `canopy ask` each resolve without a conversation, and each one used to get
// a client that nothing in the program could reach afterwards: the stream was closed, the session was
// not, and a `copilot` process stayed resident with a session GitHub believed was open. Driven
// through the engine rather than through the resolver, because the engine is what actually asks.
func TestAnAsideAndACompactionEndTheSessionsTheyOpen(t *testing.T) {
	resolver := NewKeyResolver(storeWithCopilot(t), "test")
	sessions := copilotSessionsAreFakes(resolver)

	engine := New(resolver)
	t.Cleanup(engine.Close)

	conversation := engine.Create("mycopilot", "gpt-5.2-codex")
	for range keepRecentTurns + 2 {
		turnID, err := engine.Send(conversation.ID, "question")
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if turn := waitForTurn(t, engine, conversation.ID, turnID); turn.State != core.TurnComplete {
			t.Fatalf("a turn on the fake Copilot ended as %s: %s", turn.State, turn.Error)
		}
	}
	if live := resolver.vendors.copilots.Live(); live != 1 {
		t.Fatalf("%d clients are held for one conversation", live)
	}

	if _, err := engine.Aside(context.Background(), conversation.ID, "what is this about"); err != nil {
		t.Fatalf("Aside: %v", err)
	}
	if _, err := engine.Compact(context.Background(), conversation.ID); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if opened := sessions.opened(); opened != 3 {
		t.Fatalf("%d sessions were opened, want one for the conversation and one each for the aside "+
			"and the compaction", opened)
	}
	if closed := sessions.closed(); closed != 2 {
		t.Errorf("%d of them were closed, want the aside's and the compaction's. Each one that is "+
			"not is a resident CLI process and a session GitHub believes is open, for the life of "+
			"the program", closed)
	}
	if live := resolver.vendors.copilots.Live(); live != 1 {
		t.Errorf("%d clients are held after two one-off questions, want only the conversation's", live)
	}

	engine.Close()
	if closed := sessions.closed(); closed != 3 {
		t.Errorf("%d sessions were closed after the engine shut down, want all three", closed)
	}
}

// copilotSessions counts the vendor sessions a test opened and closed.
type copilotSessions struct {
	mu     sync.Mutex
	agents []*fakeCopilotAgent
}

func (s *copilotSessions) opened() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.agents)
}

func (s *copilotSessions) closed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	shut := 0
	for _, agent := range s.agents {
		if agent.shutDown() {
			shut++
		}
	}
	return shut
}

// copilotSessionsAreFakes points a resolver's Copilot pool at fake sessions.
//
// The pool is what a client cannot be built outside of, so replacing it is how this route is driven
// on a machine with no Copilot CLI and no seat. Reaching into the resolver rather than adding an
// option to NewVendors, because a way to swap the vendor from outside the package would be a way for
// the program to do it too.
func copilotSessionsAreFakes(resolver *KeyResolver) *copilotSessions {
	sessions := &copilotSessions{}
	resolver.vendors.copilots = copilot.NewClients(
		copilot.WithOpener(func(context.Context, copilot.Conversation) (copilot.Agent, error) {
			agent := &fakeCopilotAgent{events: make(chan copilot.Event, 16)}
			sessions.mu.Lock()
			defer sessions.mu.Unlock()
			sessions.agents = append(sessions.agents, agent)
			return agent, nil
		}))
	return sessions
}

// fakeCopilotAgent answers everything with one line, and remembers being closed.
type fakeCopilotAgent struct {
	events chan copilot.Event

	mu     sync.Mutex
	closed bool
}

func (a *fakeCopilotAgent) Send(_ context.Context, _ string) error {
	a.events <- copilot.Event{Kind: copilot.EventText, Text: "an answer"}
	a.events <- copilot.Event{Kind: copilot.EventIdle}
	return nil
}

func (a *fakeCopilotAgent) Answer(context.Context, string, string, string) error { return nil }
func (a *fakeCopilotAgent) Events() <-chan copilot.Event                         { return a.events }
func (a *fakeCopilotAgent) Abort(context.Context) error                          { return nil }

func (a *fakeCopilotAgent) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.closed = true
		close(a.events)
	}
	return nil
}

func (a *fakeCopilotAgent) shutDown() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}
