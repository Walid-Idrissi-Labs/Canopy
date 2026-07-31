package keys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

const planted = "sk-ant-api03-PLANTED-SECRET-DO-NOT-LEAK"

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	s := NewStore(NewMemoryBackend(), path)
	s.SetClock(func() time.Time { return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC) })
	return s, path
}

func anthropic(name string) core.KeyMetadata {
	return core.KeyMetadata{Ref: core.KeyRef{Name: name, Provider: core.ProviderAnthropic}}
}

func TestPutAndGet(t *testing.T) {
	store, _ := newTestStore(t)

	meta, err := store.Put(anthropic("claude"), core.NewSecret(planted))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if meta.Fingerprint == "" {
		t.Error("stored metadata should carry a fingerprint")
	}
	if meta.CreatedAt.IsZero() {
		t.Error("stored metadata should carry a creation time")
	}

	got, err := store.Get(core.KeyRef{Name: "claude"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Reveal() != planted {
		t.Error("the value came back wrong")
	}
}

// The whole point of this package: what it writes to disk must not contain the credential.
func TestNothingOnDiskContainsTheSecret(t *testing.T) {
	store, path := newTestStore(t)

	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the metadata file: %v", err)
	}
	if strings.Contains(string(data), planted) {
		t.Fatalf("the metadata file contains the credential:\n%s", data)
	}
	if !strings.Contains(string(data), "claude") {
		t.Error("the metadata file should contain the key name")
	}
}

func TestMetadataFileIsNotWorldReadable(t *testing.T) {
	store, path := newTestStore(t)
	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("metadata file mode is %o, want 600. It names which providers and endpoints "+
			"someone uses, which is nobody else's business even without the secrets.", perm)
	}
}

func TestPutReplacesButKeepsTheCreationDate(t *testing.T) {
	store, _ := newTestStore(t)

	first, err := store.Put(anthropic("claude"), core.NewSecret(planted))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	store.SetClock(func() time.Time { return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) })
	second, err := store.Put(anthropic("claude"), core.NewSecret("rotated-value"))
	if err != nil {
		t.Fatalf("Put again: %v", err)
	}

	// Rotating a credential is not creating a new one, and losing the date would hide how long a
	// key has actually been in use.
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("rotation changed the creation date from %v to %v", first.CreatedAt, second.CreatedAt)
	}
	if second.Fingerprint == first.Fingerprint {
		t.Error("a rotated credential should have a new fingerprint")
	}

	got, err := store.Get(core.KeyRef{Name: "claude"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Reveal() != "rotated-value" {
		t.Error("rotation did not replace the value")
	}

	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("rotation created a duplicate, got %d entries", len(all))
	}
}

// Metadata and secrets live in different places, so they can disagree. That case must be reported
// as what it is, not as a plain absence, or the user goes looking for a key they can see listed.
func TestMissingSecretWithPresentMetadataIsExplained(t *testing.T) {
	backend := NewMemoryBackend()
	store := NewStore(backend, filepath.Join(t.TempDir(), "keys.json"))

	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Simulate the credential being deleted from the keychain outside Canopy.
	if err := backend.Delete("claude"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := store.Get(core.KeyRef{Name: "claude"})
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"claude", "outside Canopy", "keys add"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}

	// It is still listed, because the metadata is genuinely still there. Hiding it would be its
	// own small lie.
	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("got %d entries, want the record to still be visible", len(all))
	}
}

func TestGetUnknownKey(t *testing.T) {
	store, _ := newTestStore(t)

	_, err := store.Get(core.KeyRef{Name: "nope"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := store.Metadata(core.KeyRef{Name: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Metadata: want ErrNotFound, got %v", err)
	}
	if err := store.Remove(core.KeyRef{Name: "nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove: want ErrNotFound, got %v", err)
	}
}

func TestRemove(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Remove(core.KeyRef{Name: "claude"}); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := store.Get(core.KeyRef{Name: "claude"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("the key should be gone, got %v", err)
	}
	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("got %d entries after removal, want 0", len(all))
	}
}

func TestListIsSortedAndComplete(t *testing.T) {
	store, _ := newTestStore(t)

	for _, name := range []string{"zeta", "claude", "minimax"} {
		if _, err := store.Put(anthropic(name), core.NewSecret("value-"+name)); err != nil {
			t.Fatalf("Put %s: %v", name, err)
		}
	}

	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := make([]string, len(all))
	for i, m := range all {
		got[i] = m.Ref.Name
	}
	want := []string{"claude", "minimax", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List order = %v, want %v", got, want)
		}
	}
}

func TestPutValidates(t *testing.T) {
	store, _ := newTestStore(t)

	cases := map[string]struct {
		meta   core.KeyMetadata
		secret core.Secret
	}{
		"bad name": {
			core.KeyMetadata{Ref: core.KeyRef{Name: "Not Valid", Provider: core.ProviderAnthropic}},
			core.NewSecret(planted),
		},
		"unknown provider": {
			core.KeyMetadata{Ref: core.KeyRef{Name: "x", Provider: "gemini"}},
			core.NewSecret(planted),
		},
		"empty secret": {anthropic("claude"), core.NewSecret("")},
		"openai-compatible without a base URL": {
			core.KeyMetadata{Ref: core.KeyRef{Name: "kimi", Provider: core.ProviderOpenAICompatible}},
			core.NewSecret(planted),
		},
	}

	for why, tc := range cases {
		if _, err := store.Put(tc.meta, tc.secret); err == nil {
			t.Errorf("Put with %s should fail", why)
		}
	}
}

func TestOpenAICompatibleRoundTrip(t *testing.T) {
	store, _ := newTestStore(t)

	meta := core.KeyMetadata{
		Ref:     core.KeyRef{Name: "kimi", Provider: core.ProviderOpenAICompatible},
		BaseURL: "https://example.invalid/v1",
	}
	if _, err := store.Put(meta, core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := store.Metadata(core.KeyRef{Name: "kimi"})
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if got.BaseURL != meta.BaseURL {
		t.Errorf("BaseURL = %q, want %q", got.BaseURL, meta.BaseURL)
	}
	if got.Ref.Provider != core.ProviderOpenAICompatible {
		t.Errorf("provider = %q", got.Ref.Provider)
	}
}

func TestMarkUsed(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	before, err := store.Metadata(core.KeyRef{Name: "claude"})
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if before.LastUsedAt != nil {
		t.Error("a fresh key has never been used")
	}

	if err := store.MarkUsed(core.KeyRef{Name: "claude"}); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	after, err := store.Metadata(core.KeyRef{Name: "claude"})
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if after.LastUsedAt == nil {
		t.Error("LastUsedAt should be set after use")
	}
}

func TestSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	backend := NewMemoryBackend()

	first := NewStore(backend, path)
	if _, err := first.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// A second store over the same backend and path is what a restart looks like.
	second := NewStore(backend, path)
	got, err := second.Get(core.KeyRef{Name: "claude"})
	if err != nil {
		t.Fatalf("Get after restart: %v", err)
	}
	if got.Reveal() != planted {
		t.Error("the credential did not survive a restart")
	}
}

func TestCorruptMetadataIsReportedNotIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}

	store := NewStore(NewMemoryBackend(), path)
	_, err := store.List()
	if err == nil {
		t.Fatal("a corrupt metadata file should be an error, not an empty list. Silently " +
			"returning nothing would look identical to having no keys, and the user would add " +
			"them all again over the top of a broken file.")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("the error should name the file, got: %v", err)
	}
}

func TestFileBackendIsOptInAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	fileStore := NewStore(&fileBackend{path: filepath.Join(dir, "credentials.json")}, filepath.Join(dir, "keys.json"))
	memStore := NewStore(NewMemoryBackend(), filepath.Join(dir, "keys2.json"))

	if !fileStore.UsingInsecureBackend() {
		t.Error("the file backend must report itself as insecure, and keep reporting it. A one " +
			"time warning is seen by the person who set it up, not by the person who later " +
			"assumes their keys are in the keychain.")
	}
	if memStore.UsingInsecureBackend() {
		t.Error("only the file backend is the insecure one")
	}
	if !strings.Contains(fileStore.BackendName(), "insecure") {
		t.Errorf("the backend name should say what it is, got %q", fileStore.BackendName())
	}
}

func TestFileBackendRoundTripAndPermissions(t *testing.T) {
	dir := t.TempDir()
	secretsPath := filepath.Join(dir, "credentials.json")
	store := NewStore(&fileBackend{path: secretsPath}, filepath.Join(dir, "keys.json"))

	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := store.Get(core.KeyRef{Name: "claude"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Reveal() != planted {
		t.Error("round trip failed")
	}

	info, err := os.Stat(secretsPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("credential file mode is %o, want 600", perm)
	}
}

func TestAtomicWriteLeavesNoDebris(t *testing.T) {
	store, path := newTestStore(t)
	for _, name := range []string{"a", "b", "c"} {
		if _, err := store.Put(anthropic(name), core.NewSecret("v-"+name)); err != nil {
			t.Fatalf("Put %s: %v", name, err)
		}
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("reading the directory: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// Correcting a price must not require re typing a secret. A flow that asks for one is a flow where
// people paste keys into shell history.
func TestSettingARateDoesNotNeedTheSecret(t *testing.T) {
	store, _ := newTestStore(t)

	ref := core.KeyRef{Name: "kimi", Provider: core.ProviderOpenAICompatible}
	if _, err := store.Put(
		core.KeyMetadata{Ref: ref, BaseURL: "https://api.moonshot.cn/v1"},
		core.NewSecret("sk-secret-value"),
	); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rate := core.KeyRate{InputPerMTok: 0.6, OutputPerMTok: 2.5}
	if err := store.SetRate(ref, rate); err != nil {
		t.Fatalf("SetRate: %v", err)
	}

	meta, err := store.Metadata(ref)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if meta.Rate != rate {
		t.Errorf("rate = %+v, want %+v", meta.Rate, rate)
	}

	// And the secret is untouched, which is the whole point of it being a separate call.
	secret, err := store.Get(ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if secret.Reveal() != "sk-secret-value" {
		t.Error("setting a rate disturbed the stored secret")
	}
}

// Rotating a key does not change what the endpoint charges, so dropping the price would turn a
// working cost figure into "unknown" for no reason the user could see.
func TestRotatingAKeyKeepsItsRate(t *testing.T) {
	store, _ := newTestStore(t)

	ref := core.KeyRef{Name: "kimi", Provider: core.ProviderOpenAICompatible}
	meta := core.KeyMetadata{Ref: ref, BaseURL: "https://api.moonshot.cn/v1"}
	if _, err := store.Put(meta, core.NewSecret("sk-first-value")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rate := core.KeyRate{InputPerMTok: 0.6, OutputPerMTok: 2.5}
	if err := store.SetRate(ref, rate); err != nil {
		t.Fatalf("SetRate: %v", err)
	}

	if _, err := store.Put(meta, core.NewSecret("sk-rotated-value")); err != nil {
		t.Fatalf("Put after rotation: %v", err)
	}

	after, err := store.Metadata(ref)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if after.Rate != rate {
		t.Errorf("rate after rotation = %+v, want it kept at %+v", after.Rate, rate)
	}
}

func TestSettingARateOnAMissingKeyIsAnError(t *testing.T) {
	store, _ := newTestStore(t)
	err := store.SetRate(core.KeyRef{Name: "nope"}, core.KeyRate{InputPerMTok: 1, OutputPerMTok: 2})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want not found", err)
	}
}

// A key holds models, plural, and the ones its owner added are its own.
func TestAKeyRemembersTheModelsItsOwnerAdded(t *testing.T) {
	store, _ := newTestStore(t)
	ref := core.KeyRef{Name: "nim"}
	if _, err := store.Put(core.KeyMetadata{
		Ref:     core.KeyRef{Name: "nim", Provider: core.ProviderOpenAICompatible},
		BaseURL: "https://api.moonshot.cn/v1",
		Model:   "moonshot-v1-8k",
	}, core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if models, err := store.Models(ref); err != nil || len(models) != 0 {
		t.Fatalf("a new key starts with models %+v, err %v", models, err)
	}

	if err := store.AddModel(ref, "minimaxai/minimax-m2.7", "MiniMax M2.7"); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	if err := store.AddModel(ref, "moonshot-v1-32k", ""); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	models, err := store.Models(ref)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v, want two", models)
	}
	// The name is kept beside the id rather than instead of it: the id is what goes on the wire.
	if models[0].ID != "minimaxai/minimax-m2.7" || models[0].Name != "MiniMax M2.7" {
		t.Errorf("the first model reads %+v", models[0])
	}
	if models[1].Name != "" {
		t.Errorf("a model added with no name grew one: %+v", models[1])
	}

	// Adding an id that is already there corrects its name rather than listing it twice.
	if err := store.AddModel(ref, "moonshot-v1-32k", "Moonshot 32k"); err != nil {
		t.Fatalf("AddModel again: %v", err)
	}
	models, _ = store.Models(ref)
	if len(models) != 2 || models[1].Name != "Moonshot 32k" {
		t.Errorf("re-adding an id gave %+v", models)
	}

	if err := store.RemoveModel(ref, "moonshot-v1-32k"); err != nil {
		t.Fatalf("RemoveModel: %v", err)
	}
	if models, _ := store.Models(ref); len(models) != 1 || models[0].ID != "minimaxai/minimax-m2.7" {
		t.Errorf("after removal the list is %+v", models)
	}

	// A miss is refused rather than passed over, since the usual cause is a typo and a silent
	// success leaves somebody believing they removed something they did not.
	if err := store.RemoveModel(ref, "never-added"); err == nil {
		t.Error("removing a model that was never added was reported as done")
	}
}

// The plural list is a fact about the credential, so rotating the secret must not cost it, the same
// way rotating does not cost the creation date or the rate.
func TestRotatingAKeyKeepsTheModelsItsOwnerAdded(t *testing.T) {
	store, _ := newTestStore(t)
	ref := core.KeyRef{Name: "claude"}
	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.AddModel(ref, "claude-something-unreleased", "The New One"); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	if _, err := store.Put(anthropic("claude"), core.NewSecret("sk-ant-rotated")); err != nil {
		t.Fatalf("Put after rotation: %v", err)
	}

	models, err := store.Models(ref)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0].Name != "The New One" {
		t.Errorf("models after rotation = %+v", models)
	}
}

// Every mutating method here reads the whole metadata file, changes one thing in it and writes all
// of it back, which is only safe because they take the store's lock first. SetModel was the one that
// did not, so a model selection landing at the same moment as any other write put back a copy of the
// file from before that write and the other change was gone.
func TestChangingTheModelDoesNotDiscardAConcurrentChange(t *testing.T) {
	store, _ := newTestStore(t)
	ref := core.KeyRef{Name: "claude"}
	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	const rounds = 32
	var wg sync.WaitGroup
	failures := make(chan error, rounds*2)
	for i := 0; i < rounds; i++ {
		id := fmt.Sprintf("model-%02d", i)
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := store.AddModel(ref, id, ""); err != nil {
				failures <- err
			}
		}()
		go func() {
			defer wg.Done()
			if err := store.SetModel(ref, id); err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("a concurrent write failed: %v", err)
	}

	models, err := store.Models(ref)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != rounds {
		t.Errorf("%d of %d added models survived, so selecting a model discarded work done "+
			"alongside it", len(models), rounds)
	}
}

// A keys.json written by the build before this one has no models field at all. It has to load with
// an empty list and lose nothing else, or an upgrade silently drops credentials.
func TestAKeysFileFromThePreviousBuildLoadsWithNothingLost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	previous := `[
  {
    "name": "claude",
    "provider": "anthropic",
    "model": "claude-opus-5",
    "fingerprint": "abcd1234",
    "createdAt": "2026-07-01T00:00:00Z",
    "inputPerMTok": 5,
    "outputPerMTok": 25
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
	if len(all) != 1 {
		t.Fatalf("%d keys loaded from a file written by the previous build", len(all))
	}
	if all[0].Model != "claude-opus-5" || all[0].Fingerprint != "abcd1234" {
		t.Errorf("the record lost something: %+v", all[0])
	}
	if all[0].Rate.InputPerMTok != 5 || all[0].Rate.OutputPerMTok != 25 {
		t.Errorf("the rate did not survive: %+v", all[0].Rate)
	}

	models, err := store.Models(core.KeyRef{Name: "claude"})
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("a file with no models field loaded %+v", models)
	}

	// And writing it back leaves the field absent rather than writing an empty array, so a file
	// that has never had a model added round trips to the same document.
	if err := store.SetRate(core.KeyRef{Name: "claude"}, core.KeyRate{InputPerMTok: 5, OutputPerMTok: 25}); err != nil {
		t.Fatalf("SetRate: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if strings.Contains(string(written), "models") {
		t.Errorf("an empty model list was written to disk:\n%s", written)
	}
}

// The store and the resolver have to agree about what counts as one model.
//
// Matching forgives case and punctuation, so two spellings of one id would be two rows the resolver
// then refuses to choose between, and the request would be refused with the same model listed twice
// as the alternatives. Refused here instead, where somebody is present to be told why, and refused
// rather than folded into the existing entry: what is stored goes on the wire exactly as typed, and
// an unknown provider's ids may well be case sensitive.
func TestASecondSpellingOfOneModelIsRefusedRatherThanStored(t *testing.T) {
	store, _ := newTestStore(t)
	ref := core.KeyRef{Name: "nim"}
	if _, err := store.Put(core.KeyMetadata{
		Ref:     core.KeyRef{Name: "nim", Provider: core.ProviderOpenAICompatible},
		BaseURL: "https://api.moonshot.cn/v1",
		Model:   "moonshot-v1-8k",
	}, core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := store.AddModel(ref, "minimaxai/minimax-m2.7", "MiniMax M2.7"); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	err := store.AddModel(ref, "MiniMaxAI/MiniMax-M2.7", "")
	if err == nil {
		t.Fatal("a second spelling of one id was stored beside the first")
	}
	// The refusal names what it collided with, or somebody has to go and find it themselves.
	if !strings.Contains(err.Error(), "minimaxai/minimax-m2.7") {
		t.Errorf("the refusal does not say what it collides with: %v", err)
	}

	models, err := store.Models(ref)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("the key offers %+v", models)
	}
	// And the one that is there is byte for byte what was typed, not a normalised version of it.
	if models[0].ID != "minimaxai/minimax-m2.7" {
		t.Errorf("the stored id reads %q", models[0].ID)
	}

	// The exact id is still the way to correct a name, which is a different thing from adding a
	// second spelling and must keep working.
	if err := store.AddModel(ref, "minimaxai/minimax-m2.7", "MiniMax"); err != nil {
		t.Fatalf("renaming through the exact id: %v", err)
	}
	if models, _ := store.Models(ref); len(models) != 1 || models[0].Name != "MiniMax" {
		t.Errorf("after renaming the key offers %+v", models)
	}
}

// Renaming keeps the credential and moves everything that names it. The name is the identifier the
// secret is filed under, so this is a move rather than an edit of a field, and the value has to come
// back out from under the new name unchanged.
func TestRenamingMovesTheCredentialAndItsSecret(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.Put(anthropic("kimi"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.SetModel(core.KeyRef{Name: "kimi"}, "claude-opus-5"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if err := store.AddModel(core.KeyRef{Name: "kimi"}, "some/model", "Some Model"); err != nil {
		t.Fatalf("AddModel: %v", err)
	}

	meta, err := store.Rename(core.KeyRef{Name: "kimi"}, "moonshot")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if meta.Ref.Name != "moonshot" {
		t.Errorf("the rename returned %q", meta.Ref.Name)
	}

	secret, err := store.Get(core.KeyRef{Name: "moonshot"})
	if err != nil {
		t.Fatalf("the credential is not readable under its new name: %v", err)
	}
	if secret.Reveal() != planted {
		t.Error("the value changed in the move")
	}

	// Everything recorded about it comes too. A rename that reset the model or forgot the models
	// somebody added by hand would be a remove and an add wearing another name.
	moved, err := store.Metadata(core.KeyRef{Name: "moonshot"})
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if moved.Model != "claude-opus-5" {
		t.Errorf("the model became %q", moved.Model)
	}
	if moved.Fingerprint == "" {
		t.Error("the fingerprint was lost, so the credential looks unverifiable")
	}
	added, err := store.Models(core.KeyRef{Name: "moonshot"})
	if err != nil || len(added) != 1 || added[0].ID != "some/model" {
		t.Errorf("the models added by hand did not come with it: %v, %v", added, err)
	}

	// And the old name stops resolving, which is the half that makes the callers holding
	// conversations have to follow it.
	if _, err := store.Metadata(core.KeyRef{Name: "kimi"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("the old name still resolves: %v", err)
	}
	if _, err := store.Get(core.KeyRef{Name: "kimi"}); err == nil {
		t.Error("the old name still reads a secret, so the same value is in the backend twice")
	}
}

func TestRenamingMovesASignedInGrantWithoutChangingItsIdentity(t *testing.T) {
	store, _ := newTestStore(t)
	oldRef := core.KeyRef{Name: "copilot", Provider: core.ProviderOpenAICompatible}
	wantTokens := bothTokens()
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: oldRef, BaseURL: "https://api.githubcopilot.com"},
		SignIn{Kind: KindSignedIn, Account: "walid", Route: "copilot"},
		wantTokens,
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}

	meta, err := store.Rename(oldRef, "work-copilot")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	newRef := meta.Ref
	in, err := store.SignIn(newRef)
	if err != nil {
		t.Fatalf("SignIn under new name: %v", err)
	}
	if in.Kind != KindSignedIn || in.Account != "walid" || in.Route != "copilot" {
		t.Errorf("sign-in metadata changed during rename: %+v", in)
	}
	gotTokens, err := store.Tokens(newRef)
	if err != nil {
		t.Fatalf("Tokens under new name: %v", err)
	}
	if gotTokens.Access.Reveal() != wantTokens.Access.Reveal() ||
		gotTokens.Refresh.Reveal() != wantTokens.Refresh.Reveal() {
		t.Error("the grant changed during rename")
	}
	if _, err := store.SignIn(oldRef); !errors.Is(err, ErrNotFound) {
		t.Errorf("the old signed-in name still resolves: %v", err)
	}
}

func TestRenamingADelegatedCredentialDoesNotInventABackendValue(t *testing.T) {
	store, _ := newTestStore(t)
	oldRef := core.KeyRef{Name: "claude-code", Provider: core.ProviderAnthropic}
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: oldRef},
		SignIn{Kind: KindDelegated, Account: "walid", Route: "claude-code"},
		Tokens{},
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}

	meta, err := store.Rename(oldRef, "max-plan")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	in, err := store.SignIn(meta.Ref)
	if err != nil {
		t.Fatalf("SignIn under new name: %v", err)
	}
	if in.Kind != KindDelegated || in.Account != "walid" || in.Route != "claude-code" {
		t.Errorf("delegated metadata changed during rename: %+v", in)
	}
	if _, err := store.backend.Get(meta.Ref.Name); !errors.Is(err, ErrNotFound) {
		t.Errorf("rename created a backend value for a delegated credential: %v", err)
	}
	if _, err := store.SignIn(oldRef); !errors.Is(err, ErrNotFound) {
		t.Errorf("the old delegated name still resolves: %v", err)
	}
}

// Two credentials with one name is one credential, and which of the two secrets survived would be
// decided by the order of a loop.
func TestRenamingRefusesANameAlreadyInUse(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := store.Put(anthropic("kimi"), core.NewSecret("second-value")); err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, err := store.Rename(core.KeyRef{Name: "kimi"}, "claude"); err == nil {
		t.Fatal("renaming onto a name already in use was allowed")
	}

	// And neither credential moved. A refusal that had already written half of itself would be
	// worse than the collision it was refusing.
	for name, want := range map[string]string{"claude": planted, "kimi": "second-value"} {
		got, err := store.Get(core.KeyRef{Name: name})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Reveal() != want {
			t.Errorf("%s now holds the wrong value", name)
		}
	}
}

// A name is validated on the way in here for the same reason it is on the way in to Put: it travels,
// into a file, into a conversation and into the backend's own account list.
func TestRenamingRefusesANameThatIsNotOne(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	for _, to := range []string{"", "  ", "Not A Name", strings.Repeat("x", 40)} {
		if _, err := store.Rename(core.KeyRef{Name: "claude"}, to); err == nil {
			t.Errorf("%q was accepted as a credential name", to)
		}
	}
	if _, err := store.Get(core.KeyRef{Name: "claude"}); err != nil {
		t.Errorf("a refused rename disturbed the credential: %v", err)
	}
}

// Renaming to the name it already has is what somebody who opened the field and changed their mind
// asks for, and failing them would be inventing a problem.
func TestRenamingToTheSameNameIsNotAnError(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	meta, err := store.Rename(core.KeyRef{Name: "claude"}, "claude")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if meta.Ref.Name != "claude" {
		t.Errorf("the no-op rename returned %q", meta.Ref.Name)
	}
	got, err := store.Get(core.KeyRef{Name: "claude"})
	if err != nil || got.Reveal() != planted {
		t.Errorf("the credential did not survive being renamed to itself: %v", err)
	}
}

// A record whose secret has gone is not a record that can be moved anywhere useful, and the message
// has to name the disagreement rather than report a rename failure.
func TestRenamingSaysWhenTheSecretIsGone(t *testing.T) {
	store, _ := newTestStore(t)

	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Removed behind the store's back, which is what happens when somebody deletes an entry in their
	// keychain application.
	if err := store.backend.Delete("claude"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err := store.Rename(core.KeyRef{Name: "claude"}, "anthropic")
	if err == nil {
		t.Fatal("a credential with no secret was renamed")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("the message does not say what is actually wrong: %v", err)
	}
}

type renameCleanupFailureBackend struct {
	Backend
	metadataPath string
	newName      string
}

func (b *renameCleanupFailureBackend) Set(account, secret string) error {
	if err := b.Backend.Set(account, secret); err != nil {
		return err
	}
	if account != b.newName {
		return nil
	}
	// The store has already loaded the old metadata before Set. Replacing its file with a directory
	// makes the following atomic rename fail, which puts the operation on its cleanup path.
	if err := os.Remove(b.metadataPath); err != nil {
		return err
	}
	return os.Mkdir(b.metadataPath, 0o700)
}

func (b *renameCleanupFailureBackend) Delete(account string) error {
	if account == b.newName {
		return errors.New("test backend refused cleanup")
	}
	return b.Backend.Delete(account)
}

// If writing the moved metadata and removing the newly copied secret both fail, returning only the
// metadata error hides a live credential copy under a name the store cannot list.
func TestRenameReportsAnOrphanWhenMetadataAndCleanupBothFail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	backend := &renameCleanupFailureBackend{
		Backend:      NewMemoryBackend(),
		metadataPath: path,
		newName:      "anthropic",
	}
	store := NewStore(backend, path)
	if _, err := store.Put(anthropic("claude"), core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	meta, err := store.Rename(core.KeyRef{Name: "claude"}, backend.newName)
	if meta.Ref.Name != "" {
		t.Fatalf("a failed metadata move returned a live rename: %+v", meta)
	}
	if err == nil {
		t.Fatal("the failed metadata write and cleanup were both hidden")
	}
	for _, want := range []string{"saving the rename", "cleanup also failed", "anthropic", "untracked copy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %q: %v", want, err)
		}
	}
	if _, err := backend.Get("claude"); err != nil {
		t.Errorf("the original credential was disturbed: %v", err)
	}
	if _, err := backend.Get("anthropic"); err != nil {
		t.Errorf("the error claimed an orphan but none remained: %v", err)
	}
}
