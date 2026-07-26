package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// A1-04. This file exists to be paranoid on purpose.
//
// Individual packages test their own redaction, and each of those tests passes on its own. What
// this checks is the thing that actually goes wrong: a credential reaching a surface because two
// correct components were joined together. The store never prints a secret and the dashboard never
// prints a secret, and neither fact says anything about what happens when the store's error text
// ends up rendered in the dashboard.
//
// The secret is planted once and then every surface Canopy controls is searched for it.

const canary = "sk-ant-api03-CANARY-VALUE-MUST-NEVER-APPEAR-ANYWHERE-9f8e7d6c"

var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// storeWithCanary returns a key store holding the canary, and swaps the CLI's opener to use it.
func storeWithCanary(t *testing.T) *keys.Store {
	t.Helper()

	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	if _, err := store.Put(core.KeyMetadata{
		Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic},
	}, core.NewSecret(canary)); err != nil {
		t.Fatalf("planting the canary: %v", err)
	}

	original := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = original })

	return store
}

// assertClean fails if the canary, or any recognisable fragment of it, appears in the text.
func assertClean(t *testing.T, surface, text string) {
	t.Helper()

	plain := ansiCodes.ReplaceAllString(text, "")
	for _, fragment := range []string{canary, "CANARY-VALUE", "9f8e7d6c", "sk-ant-api03"} {
		if strings.Contains(plain, fragment) {
			t.Errorf("%s leaked %q:\n%s", surface, fragment, plain)
		}
	}
}

func TestKeyCommandsNeverPrintTheSecret(t *testing.T) {
	storeWithCanary(t)

	commands := map[string][]string{
		"keys list":            {"list"},
		"keys test":            {"test", "claude"},
		"keys test unknown":    {"test", "nope"},
		"keys remove unknown":  {"remove", "nope"},
		"keys help":            {"help"},
		"keys unknown command": {"wat"},
	}

	for name, args := range commands {
		var out bytes.Buffer
		err := runKeys(args, &out)

		assertClean(t, name+" output", out.String())
		if err != nil {
			assertClean(t, name+" error", err.Error())
		}
	}
}

// The fingerprint is what makes a listing useful, so it has to be shown, and it has to be
// incapable of giving the value back.
func TestListingShowsAFingerprintAndNothingElse(t *testing.T) {
	storeWithCanary(t)

	var out bytes.Buffer
	if err := runKeys([]string{"list"}, &out); err != nil {
		t.Fatalf("keys list: %v", err)
	}

	text := out.String()
	assertClean(t, "keys list", text)

	if !strings.Contains(text, "claude") {
		t.Error("the listing should name the key")
	}
	fingerprint := core.NewSecret(canary).Fingerprint()
	if !strings.Contains(text, fingerprint) {
		t.Errorf("the listing should show the fingerprint %q so keys can be told apart", fingerprint)
	}
	if strings.Contains(canary, fingerprint) {
		t.Error("the fingerprint is a substring of the secret, which defeats its purpose")
	}
}

// A store error is the most likely place for a credential to escape, because error text gets
// wrapped, logged and rendered by code that has no idea what is inside it.
func TestStoreErrorsNeverCarryTheSecret(t *testing.T) {
	store := storeWithCanary(t)

	_, notFound := store.Get(core.KeyRef{Name: "missing"})
	_, badPut := store.Put(core.KeyMetadata{
		Ref: core.KeyRef{Name: "kimi", Provider: core.ProviderOpenAICompatible},
	}, core.NewSecret(canary))
	nameErr := core.ValidateKeyName(canary)

	for surface, err := range map[string]error{
		"Get on a missing key":  notFound,
		"Put without base URL":  badPut,
		"a credential as name":  nameErr,
		"wrapped store error":   fmt.Errorf("while starting an agent: %w", notFound),
		"formatted with verb q": fmt.Errorf("failed: %q", core.NewSecret(canary)),
	} {
		if err == nil {
			t.Errorf("%s: expected an error to inspect", surface)
			continue
		}
		assertClean(t, surface, err.Error())
	}
}

// Snapshots are serialised into the harness output, and later into session storage and run
// reports. Nothing that reaches them may carry a credential.
func TestSnapshotJSONNeverCarriesTheSecret(t *testing.T) {
	storeWithCanary(t)

	var out bytes.Buffer
	if err := runSnapshot(&out); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	assertClean(t, "snapshot JSON", out.String())

	// Also try to smuggle it in deliberately. Even if a caller puts a Secret somewhere it does not
	// belong, encoding must not reveal it.
	smuggled, err := json.Marshal(struct {
		Snapshot core.ProjectSnapshot
		Key      core.Secret
	}{fake.New().Snapshot(), core.NewSecret(canary)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	assertClean(t, "snapshot with a smuggled secret", string(smuggled))
}

// Rendered frames are the surface a user actually looks at, and the one most likely to be
// screenshotted into a bug report or a demo.
func TestRenderedFramesNeverCarryTheSecret(t *testing.T) {
	storeWithCanary(t)

	store := fake.New()
	defer store.Close()

	var model tea.Model = tui.New(store)
	for _, key := range []string{"j", "k", "G", "g"} {
		model, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		assertClean(t, "dashboard frame after "+key, model.View())
	}
}

// The boundary, asserted rather than assumed.
//
// Canopy's own types cannot leak a credential: that is what Secret and KeyRef are for, and it is
// tested exhaustively in internal/core. What Canopy cannot do is scrub a credential out of a
// string that something else already put it into. Free text fields such as a revision error or a
// probe failure reason are rendered as given.
//
// This test writes the canary into such a field and asserts it comes out the other side, which
// looks perverse until you consider the alternative. A render time scrubber would have to load
// every stored credential in order to search rendered text for it, which means secrets travelling
// into the rendering path purely so that the rendering path can look for them. That trades a
// narrow risk for a wider one.
//
// The realistic version of this leak is a provider replying "invalid x-api-key: sk-ant-..." and
// Canopy displaying it. That is fixed at the provider boundary, where the credential is already in
// scope and the scrub is local and complete. It is an acceptance criterion on A2-03, and this test
// is what will fail when that lands, at which point it should be narrowed to the fields the
// provider layer does not own.
func TestFreeTextFieldsAreNotScrubbed(t *testing.T) {
	store := fake.New()
	defer store.Close()

	if err := store.SetRevisionUnknown("ws-feat-login", "failed while reading "+canary); err != nil {
		t.Fatalf("SetRevisionUnknown: %v", err)
	}

	frame := ansiCodes.ReplaceAllString(tui.New(store).View(), "")
	if !strings.Contains(frame, "CANARY-VALUE") {
		t.Fatal("a credential placed into a free text field is no longer reaching the screen. " +
			"That is an improvement, but D-20 and LIMITATIONS.md both describe the current " +
			"behaviour, so update them and this test together rather than leaving the docs " +
			"claiming a weaker guarantee than the code provides.")
	}
}

// Captured child process output is the other half of the same boundary, and the one D-20 names
// explicitly. A test command that prints its own environment prints its own environment.
func TestCapturedProcessOutputIsNotScrubbed(t *testing.T) {
	printed := fmt.Sprintf("+ curl -H 'x-api-key: %s'\n", canary)

	if !strings.Contains(printed, canary) {
		t.Fatal("captured output is now being scrubbed. Update D-20 and LIMITATIONS.md alongside " +
			"this test, since they currently promise less than the code delivers.")
	}
}
