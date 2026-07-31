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
)

// Which credential a new conversation runs on, and where that answer comes from.
//
// The two halves are tested together because they only work together. The default is read from what
// was last used, and until now nothing in the interface ever wrote that down: `canopy ask` did, and
// the program people actually run did not. Either half alone leaves you on the credential screen.

// storeWith builds a key store holding the named credentials.
func storeWith(t *testing.T, names ...string) *keys.Store {
	t.Helper()
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	for _, name := range names {
		meta := core.KeyMetadata{Ref: core.KeyRef{Name: name, Provider: core.ProviderAnthropic}}
		if _, err := store.Put(meta, core.NewSecret("sk-"+name)); err != nil {
			t.Fatalf("storing %s: %v", name, err)
		}
	}
	return store
}

// The one stored credential is the default whether or not it has ever answered, because there is
// nothing else it could be.
func TestTheOnlyCredentialIsTheDefault(t *testing.T) {
	if got := NewKeyResolver(storeWith(t, "claude"), "test").DefaultKeyName(); got != "claude" {
		t.Errorf("DefaultKeyName = %q, want claude", got)
	}
}

// Adding a second credential used to cost you the first one's place, because the default was defined
// as "the only one" rather than "the one you use". Every launch after that opened on the credential
// list instead of a conversation, and picking one there was forgotten the moment the program exited:
// there was no way to say "this one, from now on" that survived a restart.
func TestTheDefaultCredentialIsTheOneLastUsed(t *testing.T) {
	store := storeWith(t, "glm", "nemotron")
	resolver := NewKeyResolver(store, "test")

	// A clock of its own, because the two uses below are microseconds apart on a fast machine and
	// milliseconds apart on a coarse one, and a test that depends on which is a test that fails on
	// somebody else's laptop for a reason that has nothing to do with credentials.
	now := time.Unix(1_700_000_000, 0)
	store.SetClock(func() time.Time { return now })

	if got := resolver.DefaultKeyName(); got != "" {
		t.Errorf("DefaultKeyName = %q, and with neither one used there is nothing to go on", got)
	}

	resolver.MarkUsed("nemotron")
	if got := resolver.DefaultKeyName(); got != "nemotron" {
		t.Fatalf("DefaultKeyName = %q, want nemotron, the one that answered", got)
	}

	// And switching is a switch, not a suggestion: the credential used most recently is the one the
	// next run opens on, or changing key in the interface would last exactly one session.
	now = now.Add(time.Hour)
	resolver.MarkUsed("glm")
	if got := resolver.DefaultKeyName(); got != "glm" {
		t.Errorf("DefaultKeyName = %q, want glm, which is the one used since", got)
	}
}

// A credential that answers under no particular name is still a credential that answered. Without
// this the single key case recorded nothing, and `canopy keys list` showed "never used" beside the
// key every conversation on the machine had been running on.
func TestUsingTheObviousCredentialRecordsWhichOneItWas(t *testing.T) {
	store := storeWith(t, "claude")
	NewKeyResolver(store, "test").MarkUsed("")

	meta, err := store.Metadata(core.KeyRef{Name: "claude"})
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.LastUsedAt == nil {
		t.Error("the credential the turn resolved to should be recorded as used")
	}
}

// markingResolver is a resolver that can also record which credential answered, which is the pair of
// abilities the real one has.
type markingResolver struct {
	client core.ProviderClient

	mu   sync.Mutex
	used []string
}

func (r *markingResolver) Resolve(string, string) (core.ProviderClient, pricing.ModelID, error) {
	return r.client, anthropicID(), nil
}

func (r *markingResolver) MarkUsed(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.used = append(r.used, name)
}

func (r *markingResolver) marked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.used...)
}

// The engine is what has to write the record the default reads, because a turn is the only place
// that knows a credential worked.
func TestACredentialThatAnsweredIsRecordedAsUsed(t *testing.T) {
	resolver := &markingResolver{client: &scriptedClient{name: "claude", events: reply("hello")}}
	e := New(resolver)
	defer e.Close()

	session := e.Create("nemotron", "claude-opus-5")
	turnID, err := e.Send(session.ID, "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, session.ID, turnID)

	marked := resolver.marked()
	if len(marked) != 1 || marked[0] != "nemotron" {
		t.Errorf("recorded %v as used, want the credential the turn ran on", marked)
	}
}

// A credential that could not be reached is not one to remember. Recording it would aim the next
// launch at the one credential known not to work, which is worse than having no default at all.
func TestACredentialThatFailedIsNotRecordedAsUsed(t *testing.T) {
	resolver := &markingResolver{
		client: &scriptedClient{name: "claude", openErr: context.DeadlineExceeded},
	}
	e := New(resolver)
	defer e.Close()

	session := e.Create("nemotron", "claude-opus-5")
	turnID, err := e.Send(session.ID, "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, session.ID, turnID)

	if marked := resolver.marked(); len(marked) != 0 {
		t.Errorf("recorded %v as used, and nothing answered", marked)
	}
}
