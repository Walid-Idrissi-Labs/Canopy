package keys

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
