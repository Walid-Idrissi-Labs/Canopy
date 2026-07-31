package keys

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Distinctive enough that finding either of them in a file is unambiguous rather than a substring
// that happened to match.
const (
	plantedAccess  = "gho_PLANTED-ACCESS-TOKEN-DO-NOT-LEAK"
	plantedRefresh = "ghr_PLANTED-REFRESH-TOKEN-DO-NOT-LEAK"
)

func expiry(t *testing.T) *time.Time {
	t.Helper()
	at := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	return &at
}

func signedIn(account string, at *time.Time) SignIn {
	return SignIn{Kind: KindSignedIn, Account: account, ExpiresAt: at}
}

func bothTokens() Tokens {
	return Tokens{Access: core.NewSecret(plantedAccess), Refresh: core.NewSecret(plantedRefresh)}
}

// The account, the expiry and the kind are readable facts and the tokens are not. Asserted against
// the file's bytes rather than against the struct read back, because a round trip through the store
// would pass just as well if the tokens had been written down and read up again.
func TestASignedInCredentialKeepsItsAccountAndExpiryAndWritesNoTokenToDisk(t *testing.T) {
	store, path := newTestStore(t)
	ref := core.KeyRef{Name: "copilot", Provider: core.ProviderAnthropic}

	at := expiry(t)
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: ref}, signedIn("walid@example.invalid", at), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the metadata file: %v", err)
	}
	for _, token := range []string{plantedAccess, plantedRefresh} {
		if strings.Contains(string(data), token) {
			t.Fatalf("the metadata file contains a token:\n%s", data)
		}
	}
	for _, want := range []string{"signed-in", "walid@example.invalid", "2026-08-01T09:30:00Z"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the metadata file should record %q:\n%s", want, data)
		}
	}

	in, err := store.SignIn(ref)
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if in.Kind != KindSignedIn {
		t.Errorf("kind = %q, want %q", in.Kind, KindSignedIn)
	}
	if in.Account != "walid@example.invalid" {
		t.Errorf("account = %q", in.Account)
	}
	if in.ExpiresAt == nil || !in.ExpiresAt.Equal(*at) {
		t.Errorf("expiry = %v, want %v", in.ExpiresAt, at)
	}

	tokens, err := store.Tokens(ref)
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if tokens.Access.Reveal() != plantedAccess || tokens.Refresh.Reveal() != plantedRefresh {
		t.Error("the tokens did not come back")
	}
}

// Signing in again is rotation. It buys new tokens, not a new credential, so everything that
// belongs to the credential rather than to the tokens has to survive it, exactly as it survives
// Put replacing a pasted secret.
func TestSigningInAgainKeepsWhatRotatingAPastedKeyKeepsAndReplacesTheTokens(t *testing.T) {
	store, _ := newTestStore(t)
	ref := core.KeyRef{Name: "copilot", Provider: core.ProviderAnthropic}
	meta := core.KeyMetadata{Ref: ref}

	first, err := store.PutSignIn(meta, signedIn("walid@example.invalid", expiry(t)), bothTokens())
	if err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	if err := store.MarkUsed(ref); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	if err := store.AddModel(ref, "gpt-5.2-codex", "GPT-5.2 Codex"); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	if err := store.SetRate(ref, core.KeyRate{InputPerMTok: 1.25, OutputPerMTok: 10}); err != nil {
		t.Fatalf("SetRate: %v", err)
	}

	store.SetClock(func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) })
	later := time.Date(2026, 8, 2, 9, 30, 0, 0, time.UTC)
	second, err := store.PutSignIn(meta, signedIn("walid@example.invalid", &later), Tokens{
		Access:  core.NewSecret("gho_SECOND-ACCESS"),
		Refresh: core.NewSecret("ghr_SECOND-REFRESH"),
	})
	if err != nil {
		t.Fatalf("PutSignIn again: %v", err)
	}

	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("signing in again changed the creation date from %v to %v",
			first.CreatedAt, second.CreatedAt)
	}
	if second.LastUsedAt == nil {
		t.Error("signing in again forgot when the credential was last used")
	}
	if second.Rate.InputPerMTok != 1.25 || second.Rate.OutputPerMTok != 10 {
		t.Errorf("signing in again dropped the rate: %+v", second.Rate)
	}
	models, err := store.Models(ref)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0].Name != "GPT-5.2 Codex" {
		t.Errorf("signing in again lost the models its owner added: %+v", models)
	}

	tokens, err := store.Tokens(ref)
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if tokens.Access.Reveal() != "gho_SECOND-ACCESS" || tokens.Refresh.Reveal() != "ghr_SECOND-REFRESH" {
		t.Error("signing in again did not replace the tokens")
	}
	in, err := store.SignIn(ref)
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if in.ExpiresAt == nil || !in.ExpiresAt.Equal(later) {
		t.Errorf("the expiry did not move to the new one: %v", in.ExpiresAt)
	}

	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("signing in again created a duplicate, got %d entries", len(all))
	}
}

// A deleted credential that leaves its refresh token behind is a live credential nothing lists and
// nobody will think to revoke.
func TestRemovingASignedInCredentialTakesItsTokensWithIt(t *testing.T) {
	backend := NewMemoryBackend()
	store := NewStore(backend, filepath.Join(t.TempDir(), "keys.json"))
	ref := core.KeyRef{Name: "copilot", Provider: core.ProviderAnthropic}

	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: ref}, signedIn("walid@example.invalid", expiry(t)), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	if err := store.Remove(ref); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := backend.Get("copilot"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the tokens are still in the backend after removal, got %v", err)
	}
	if _, err := store.SignIn(ref); !errors.Is(err, ErrNotFound) {
		t.Errorf("the record is still there after removal, got %v", err)
	}
}

// Metadata and tokens live in different places, so they can disagree, and the report has to be the
// one the pasted path already gives: this is recorded, there is nothing behind it, somebody removed
// it outside Canopy. Reported as absent instead, the user goes looking for a credential they can
// still see listed.
func TestASignInWithNoTokensBehindItIsReportedAsDamageNotAsAbsence(t *testing.T) {
	backend := NewMemoryBackend()
	store := NewStore(backend, filepath.Join(t.TempDir(), "keys.json"))
	ref := core.KeyRef{Name: "copilot", Provider: core.ProviderAnthropic}

	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: ref}, signedIn("walid@example.invalid", expiry(t)), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}

	// The keychain entry deleted from outside Canopy, which is a thing people do.
	if err := backend.Delete("copilot"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := store.Tokens(ref)
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrNotFound) {
		t.Error("a sign-in with nothing behind it is not the same as a credential that is not there")
	}
	for _, want := range []string{"copilot", "outside Canopy", "walid@example.invalid", "Sign in again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}

	// Still listed, because the record is genuinely still there. Hiding it would be its own lie.
	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("got %d entries, want the record to still be visible", len(all))
	}
}

// The two halves of the store are reached by two calls, and asking the wrong one is answered rather
// than served. A caller reaching for Get wants something to put in a header, and a sign-in has no
// such thing.
func TestASignedInCredentialIsNotHandedOutAsAPastedSecret(t *testing.T) {
	store, _ := newTestStore(t)
	signInRef := core.KeyRef{Name: "copilot", Provider: core.ProviderAnthropic}
	pastedRef := core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}

	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: signInRef}, signedIn("walid@example.invalid", expiry(t)), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Get(signInRef)
	if err == nil {
		t.Fatalf("Get on a sign-in returned %q", got.Reveal())
	}
	for _, want := range []string{"copilot", "signed in as", "walid@example.invalid"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should mention %q, got: %v", want, err)
		}
	}

	if _, err := store.Tokens(pastedRef); err == nil {
		t.Error("Tokens on a pasted credential returned tokens")
	} else if !strings.Contains(err.Error(), "pasted") {
		t.Errorf("the refusal should say the credential was pasted, got: %v", err)
	}

	// And the pasted one is untouched by any of this.
	secret, err := store.Get(pastedRef)
	if err != nil {
		t.Fatalf("Get on the pasted key: %v", err)
	}
	if secret.Reveal() != planted {
		t.Error("the pasted credential came back wrong")
	}
}

// The Claude route in D-51 is permitted precisely because Canopy holds no credential of the user's
// on it, so a delegated credential's empty half is the correct state and must not read as damage.
func TestADelegatedSignInHoldsNoTokensAndIsNotDamage(t *testing.T) {
	store, path := newTestStore(t)
	ref := core.KeyRef{Name: "claude-code", Provider: core.ProviderAnthropic}

	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: ref},
		SignIn{Kind: KindDelegated, Account: "walid@example.invalid"},
		Tokens{},
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}

	in, err := store.SignIn(ref)
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if in.Kind != KindDelegated {
		t.Errorf("kind = %q, want %q", in.Kind, KindDelegated)
	}
	if in.Account != "walid@example.invalid" {
		t.Errorf("account = %q", in.Account)
	}
	if in.ExpiresAt != nil {
		t.Errorf("a delegated sign-in has no token to expire, got %v", in.ExpiresAt)
	}

	_, err = store.Tokens(ref)
	if err == nil {
		t.Fatal("a delegated sign-in produced tokens")
	}
	// The distinction the kind exists for: this says Canopy holds none, not that some went missing.
	if strings.Contains(err.Error(), "outside Canopy") {
		t.Errorf("an empty half by design was reported as damage: %v", err)
	}
	if !strings.Contains(err.Error(), "delegated") {
		t.Errorf("the answer should name what this credential is, got: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the metadata file: %v", err)
	}
	if !strings.Contains(string(data), "delegated") {
		t.Errorf("the record does not say what kind it is:\n%s", data)
	}
}

// The guarantee that makes the delegated route defensible is enforced here rather than promised in
// a document, because a document cannot refuse.
func TestADelegatedSignInRefusesToBeGivenTokens(t *testing.T) {
	store, _ := newTestStore(t)
	ref := core.KeyRef{Name: "claude-code", Provider: core.ProviderAnthropic}

	_, err := store.PutSignIn(
		core.KeyMetadata{Ref: ref},
		SignIn{Kind: KindDelegated, Account: "walid@example.invalid"},
		bothTokens(),
	)
	if err == nil {
		t.Fatal("a delegated credential accepted the user's tokens")
	}
	if !strings.Contains(err.Error(), "holds no credential") {
		t.Errorf("the refusal should say why, got: %v", err)
	}

	if _, err := store.SignIn(ref); !errors.Is(err, ErrNotFound) {
		t.Errorf("the refused credential was stored anyway, got %v", err)
	}
}

// Both halves are one entry under one name, so a credential cannot be a pasted secret and a sign-in
// at the same time and converting either way leaves nothing behind to be found later.
func TestAPastedSecretAndASignInCannotBothBeTrueOfOneCredential(t *testing.T) {
	backend := NewMemoryBackend()
	store := NewStore(backend, filepath.Join(t.TempDir(), "keys.json"))
	ref := core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}

	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: ref}, signedIn("walid@example.invalid", expiry(t)), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}

	stored, err := backend.Get("claude")
	if err != nil {
		t.Fatalf("reading the backend: %v", err)
	}
	if strings.Contains(stored, planted) {
		t.Error("the pasted secret is still in the backend after the credential was signed in")
	}
	meta, err := store.Metadata(ref)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.Fingerprint != "" {
		t.Errorf("the pasted secret's fingerprint outlived it: %q", meta.Fingerprint)
	}

	// And back the other way.
	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put after the sign-in: %v", err)
	}
	stored, err = backend.Get("claude")
	if err != nil {
		t.Fatalf("reading the backend: %v", err)
	}
	if strings.Contains(stored, plantedRefresh) {
		t.Error("the refresh token is still in the backend after the credential was pasted over")
	}
	in, err := store.SignIn(ref)
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if in.Kind != KindPasted || in.Account != "" || in.ExpiresAt != nil {
		t.Errorf("a pasted credential still claims to be signed in: %+v", in)
	}
	got, err := store.Get(ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Reveal() != planted {
		t.Error("the pasted value did not come back")
	}
}

// The record is written before the backend entry is read, so the two can be one change apart. A
// sign-in's tokens served as if somebody had pasted them would go on the wire as a bearer value,
// come back 401, and be classified as a wrong key, which is documented as never retry and never
// fall back. The user would then be sent to replace a credential that was never the problem.
func TestTokensLeftInTheBackendAreNotServedAsAPastedSecret(t *testing.T) {
	backend := NewMemoryBackend()
	store := NewStore(backend, filepath.Join(t.TempDir(), "keys.json"))
	ref := core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}

	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// The split state an older build or an external backend edit can leave behind.
	if err := store.writeTokens("claude", bothTokens()); err != nil {
		t.Fatalf("writeTokens: %v", err)
	}

	got, err := store.Get(ref)
	if err == nil {
		t.Fatalf("Get returned %q", got.Reveal())
	}
	for _, want := range []string{"claude", "stopped halfway", "keys add"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
}

func TestAFailedFirstSignInRemovesTheGrantItCouldNotRecord(t *testing.T) {
	backend := NewMemoryBackend()
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("making metadata saving fail: %v", err)
	}
	store := NewStore(backend, path)

	_, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "copilot", Provider: core.ProviderAnthropic}},
		signedIn("walid@example.invalid", expiry(t)), bothTokens(),
	)
	if err == nil {
		t.Fatal("a sign-in whose metadata could not be saved reported success")
	}
	if _, backendErr := backend.Get("copilot"); !errors.Is(backendErr, ErrNotFound) {
		t.Fatalf("the failed sign-in left a grant in the backend: %v", backendErr)
	}
}

func TestAFailedReplacementRestoresThePreviousGrant(t *testing.T) {
	for name, replacement := range map[string]Tokens{
		"another signed-in account": {
			Access: core.NewSecret("gho_NEW-ACCOUNT"), Refresh: core.NewSecret("ghr_NEW-ACCOUNT"),
		},
		"a delegated account with no tokens": {},
	} {
		t.Run(name, func(t *testing.T) {
			backend := NewMemoryBackend()
			path := filepath.Join(t.TempDir(), "keys.json")
			store := NewStore(backend, path)
			meta := core.KeyMetadata{
				Ref: core.KeyRef{Name: "subscription", Provider: core.ProviderAnthropic},
			}
			if _, err := store.PutSignIn(
				meta, signedIn("old@example.invalid", expiry(t)), bothTokens(),
			); err != nil {
				t.Fatalf("storing the previous sign-in: %v", err)
			}
			previous, err := backend.Get("subscription")
			if err != nil {
				t.Fatalf("reading the previous grant: %v", err)
			}

			if err := os.Remove(path); err != nil {
				t.Fatalf("removing the metadata file: %v", err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatalf("making metadata saving fail: %v", err)
			}

			kind := KindSignedIn
			if replacement.IsZero() {
				kind = KindDelegated
			}
			_, err = store.PutSignIn(meta, SignIn{
				Kind: kind, Account: "new@example.invalid",
			}, replacement)
			if err == nil {
				t.Fatal("a replacement whose metadata could not be saved reported success")
			}
			after, backendErr := backend.Get("subscription")
			if backendErr != nil {
				t.Fatalf("the previous grant was not restored: %v", backendErr)
			}
			if after != previous {
				t.Error("the failed replacement left the new account's grant behind the old account metadata")
			}
		})
	}
}

// A sign-in with no account is two identical rows the moment somebody has a second subscription,
// and telling them apart is the only reason the field exists.
func TestASignInWithoutTheAccountItBelongsToIsRefused(t *testing.T) {
	store, _ := newTestStore(t)
	ref := core.KeyRef{Name: "copilot", Provider: core.ProviderAnthropic}

	cases := map[string]struct {
		in     SignIn
		tokens Tokens
	}{
		"no account":            {signedIn("", expiry(t)), bothTokens()},
		"only whitespace":       {signedIn("   ", expiry(t)), bothTokens()},
		"no access token":       {signedIn("walid@example.invalid", expiry(t)), Tokens{}},
		"a kind nobody defines": {SignIn{Kind: "oauth", Account: "walid"}, bothTokens()},
		"the pasted kind":       {SignIn{Kind: KindPasted, Account: "walid"}, bothTokens()},
	}
	for why, tc := range cases {
		if _, err := store.PutSignIn(core.KeyMetadata{Ref: ref}, tc.in, tc.tokens); err == nil {
			t.Errorf("PutSignIn with %s should fail", why)
		}
	}
}

// Kind sits in the keys record rather than becoming a third core.Provider, so signing in adds no
// provider and internal/core is untouched by any of this.
func TestSigningInAddsNoProvider(t *testing.T) {
	if got := core.AllProviders(); len(got) != 2 {
		t.Errorf("core knows %d providers, want the two it knew before sign-ins existed: %v",
			len(got), got)
	}

	store, _ := newTestStore(t)
	ref := core.KeyRef{Name: "copilot", Provider: core.ProviderOpenAICompatible}
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: ref, BaseURL: "https://api.example.invalid/v1"},
		signedIn("walid@example.invalid", expiry(t)), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	meta, err := store.Metadata(ref)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.Ref.Provider != core.ProviderOpenAICompatible {
		t.Errorf("provider = %q, want the existing one", meta.Ref.Provider)
	}

	// The provider contract is unchanged too, so a sign-in that needs an endpoint is refused
	// without one exactly as a pasted key is.
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "nobase", Provider: core.ProviderOpenAICompatible}},
		signedIn("walid@example.invalid", expiry(t)), bothTokens(),
	); err == nil {
		t.Error("a sign-in on a provider that needs a base URL was stored without one")
	}
}

// SignIn is the type that goes to a screen, so it must have nowhere to put a token, the same way
// core.KeyRef has nowhere to put a secret. TestPublishedTypesCarryNoSecrets holds the core types
// and cannot see this one, since core cannot import this package.
func TestTheFactsBehindASignInAreSafeToDisplay(t *testing.T) {
	secretType := reflect.TypeOf(core.Secret{})

	var walk func(reflect.Type, string, int)
	walk = func(typ reflect.Type, path string, depth int) {
		if typ == nil || depth > 4 {
			return
		}
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
			walk(typ.Elem(), path+"[]", depth+1)
		case reflect.Struct:
			if typ == secretType {
				t.Errorf("SignIn reaches a core.Secret at %s, and this type is displayed", path)
				return
			}
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				walk(f.Type, path+"."+f.Name, depth+1)
			}
		}
	}
	walk(reflect.TypeOf(SignIn{}), "SignIn", 0)

	// And the type that does hold them is not reachable from the metadata that gets published.
	metaType := reflect.TypeOf(core.KeyMetadata{})
	for i := 0; i < metaType.NumField(); i++ {
		if metaType.Field(i).Type == reflect.TypeOf(Tokens{}) {
			t.Errorf("core.KeyMetadata carries Tokens at field %q", metaType.Field(i).Name)
		}
	}
}

// A keys.json from the build before this one has no kind field on any record. Every credential in
// it has to load intact, and none may come back claiming to be something nobody signed in to.
func TestAKeysFileFromThePreviousBuildHasNoCredentialClaimingToBeSignedIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	previous := `[
  {
    "name": "claude",
    "provider": "anthropic",
    "model": "claude-opus-5",
    "fingerprint": "abcd1234",
    "createdAt": "2026-07-01T00:00:00Z"
  },
  {
    "name": "kimi",
    "provider": "openai-compatible",
    "baseUrl": "https://api.moonshot.cn/v1",
    "fingerprint": "efgh5678",
    "createdAt": "2026-07-02T00:00:00Z",
    "inputPerMTok": 0.6,
    "outputPerMTok": 2.5
  }
]`
	if err := os.WriteFile(path, []byte(previous), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}

	store := NewStore(NewMemoryBackend(), path)
	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("%d keys loaded from a file written by the previous build", len(all))
	}
	if all[0].Fingerprint != "abcd1234" || all[1].BaseURL != "https://api.moonshot.cn/v1" {
		t.Errorf("a record lost something: %+v", all)
	}

	for _, meta := range all {
		in, err := store.SignIn(meta.Ref)
		if err != nil {
			t.Fatalf("SignIn %s: %v", meta.Ref.Name, err)
		}
		if in.Kind != KindPasted {
			t.Errorf("%s came back as %q, but nobody signed it in", meta.Ref.Name, in.Kind)
		}
		if in.Kind.IsSignIn() {
			t.Errorf("%s reads as signed in", meta.Ref.Name)
		}
	}

	// And writing the file back leaves the kind absent rather than spelling out the default, so a
	// file that has never held a sign-in round trips to the same document.
	if err := store.SetModel(core.KeyRef{Name: "claude"}, "claude-opus-5"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	for _, unwanted := range []string{"kind", "account", "expiresAt"} {
		if strings.Contains(string(written), unwanted) {
			t.Errorf("a pasted credential was written with a %q field:\n%s", unwanted, written)
		}
	}
}
