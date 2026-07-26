package tui_test

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// fakeKeyStore is the credential half, with no keychain involved.
type fakeKeyStore struct {
	keys []core.KeyMetadata
}

func (f *fakeKeyStore) List() ([]core.KeyMetadata, error) { return f.keys, nil }

func (f *fakeKeyStore) Put(meta core.KeyMetadata, secret core.Secret) (core.KeyMetadata, error) {
	meta.Fingerprint = secret.Fingerprint()
	meta.CreatedAt = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	f.keys = append(f.keys, meta)
	return meta, nil
}

func (f *fakeKeyStore) Remove(ref core.KeyRef) error {
	remaining := f.keys[:0]
	for _, k := range f.keys {
		if k.Ref.Name != ref.Name {
			remaining = append(remaining, k)
		}
	}
	f.keys = remaining
	return nil
}

func (f *fakeKeyStore) BackendName() string        { return "test" }
func (f *fakeKeyStore) UsingInsecureBackend() bool { return false }

func withOneKey() *fakeKeyStore {
	return &fakeKeyStore{keys: []core.KeyMetadata{{
		Ref:         core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic},
		Fingerprint: "abc123def456",
		CreatedAt:   time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	}}}
}

// The first thing a new user sees. An empty dashboard would be technically correct and useless:
// what they need is the one action that makes the rest of the program work.
func TestFirstRunWithNoKeysOpensOnCredentials(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := tui.NewApp(store, &fakeKeyStore{})

	if app.Screen() != "keys" {
		t.Errorf("first run opened on %q, want the credential screen", app.Screen())
	}
	view := plain(app.View())
	if !strings.Contains(view, "No credentials yet") {
		t.Errorf("the empty state should explain itself:\n%s", view)
	}
}

func TestWithKeysOpensOnTheDashboard(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := tui.NewApp(store, withOneKey())

	if app.Screen() != "dashboard" {
		t.Errorf("opened on %q, want the dashboard", app.Screen())
	}
	if !strings.Contains(plain(app.View()), "feat-login") {
		t.Error("the dashboard should be showing")
	}
}

func TestSwitchingBetweenScreens(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var app tea.Model = tui.NewApp(store, withOneKey())

	app = key(app, "K")
	if app.(tui.App).Screen() != "keys" {
		t.Fatal("K should open the credential screen")
	}
	if !strings.Contains(plain(app.View()), "claude") {
		t.Error("the credential should be listed")
	}

	app, _ = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if app.(tui.App).Screen() != "dashboard" {
		t.Error("esc should go back to the dashboard")
	}
}

// "k" is move-up on the dashboard, so it may only mean "open credentials" where there is nothing
// to move around in. Otherwise a user navigating a list would be thrown onto another screen.
func TestLowercaseKDoesNotHijackNavigation(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var app tea.Model = tui.NewApp(store, withOneKey())

	app = key(app, "j")
	app = key(app, "k")
	if app.(tui.App).Screen() != "dashboard" {
		t.Error("k while navigating a populated dashboard should move the cursor, not switch screen")
	}
}

// The dashboard keeps consuming engine events while another screen is in front, or it would be
// stale the moment you came back to it.
func TestDashboardKeepsUpdatingBehindTheCredentialScreen(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var app tea.Model = tui.NewApp(store, withOneKey())
	waiting := app.(tui.App).Init()

	app = key(app, "K")
	if app.(tui.App).Screen() != "keys" {
		t.Fatal("precondition failed")
	}

	if err := store.Touch("ws-refactor-api"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	app, _ = app.Update(nextMsg(t, waiting))

	app, _ = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !strings.Contains(plain(app.View()), "STALE") {
		t.Errorf("the dashboard missed an event while another screen was in front:\n%s",
			plain(app.View()))
	}
}

// Every keystroke belongs to the field being edited, including q and esc, or a credential
// containing them could never be typed.
func TestKeystrokesAreNotStolenWhileTyping(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var app tea.Model = tui.NewApp(store, &fakeKeyStore{})
	app = key(app, "a") // start adding

	for _, letter := range []string{"q", "e", "s", "c"} {
		app = key(app, letter)
		if app.(tui.App).Screen() != "keys" {
			t.Fatalf("typing %q left the credential screen", letter)
		}
	}

	app, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Error("esc while typing should cancel the field, not quit the program")
	}
	if app.(tui.App).Screen() != "keys" {
		t.Error("esc while typing should stay on the credential screen")
	}
}

// A credential added in the interface has to actually reach the store, which is the whole point of
// A1-05 existing alongside the command line path.
func TestAddingAKeyInTheInterfaceReachesTheStore(t *testing.T) {
	store := fake.New()
	defer store.Close()

	keyStore := &fakeKeyStore{}
	var app tea.Model = tui.NewApp(store, keyStore)

	app = key(app, "a")
	for _, r := range "kimi" {
		app = key(app, string(r))
	}
	app, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter}) // anthropic
	for _, r := range "sk-value-typed-in-the-tui" {
		app = key(app, string(r))
	}
	app, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if len(keyStore.keys) != 1 {
		t.Fatalf("got %d stored credentials, want 1", len(keyStore.keys))
	}
	if keyStore.keys[0].Ref.Name != "kimi" {
		t.Errorf("stored name = %q", keyStore.keys[0].Ref.Name)
	}
	if strings.Contains(plain(app.View()), "sk-value-typed") {
		t.Error("the typed value is visible after storing")
	}
}

func TestDashboardShowsHowToReachCredentials(t *testing.T) {
	store := fake.New()
	defer store.Close()

	if !strings.Contains(plain(tui.NewApp(store, withOneKey()).View()), "credentials") {
		t.Error("the dashboard should say how to get to the credential screen")
	}
}
