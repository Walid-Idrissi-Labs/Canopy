package tui_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
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

// stubEngine stands in for the session engine. The app level tests are about routing and chrome,
// not about conversations, so it answers with an empty session and records nothing.
type stubEngine struct {
	session core.Session
	sent    []string
}

func (e *stubEngine) Session(string) (core.Session, bool) { return e.session, true }

func (e *stubEngine) Send(_, prompt string) (string, error) {
	e.sent = append(e.sent, prompt)
	return "turn-1", nil
}

func (e *stubEngine) Cancel(string) {}

func (e *stubEngine) Events(uint64) <-chan core.Event { return make(chan core.Event) }

func (e *stubEngine) Compact(context.Context, string) (session.CompactionResult, error) {
	return session.CompactionResult{}, nil
}

func (e *stubEngine) Apply(string, session.CompactionResult) error { return nil }

func (e *stubEngine) Pending(string) (session.Prompt, bool) { return session.Prompt{}, false }

func (e *stubEngine) Answer(string, bool, bool) bool { return false }

// launch builds the application past the splash and at a known size, which is the state every
// test below actually cares about. Tests should not wait on a timer.
func launch(store core.SnapshotStore, keyStore keysui.Store) tea.Model {
	return launchWith(store, keyStore, &stubEngine{})
}

func launchWith(store core.SnapshotStore, keyStore keysui.Store, engine chat.Engine) tea.Model {
	app := tui.NewApp(store, keyStore, engine, "myproject", "claude").DismissSplash()
	next, _ := app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next
}

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

	app := launch(store, &fakeKeyStore{}).(tui.App)

	if app.Screen() != "keys" {
		t.Errorf("first run opened on %q, want the credential screen", app.Screen())
	}
	view := plain(app.View())
	if !strings.Contains(view, "No credentials yet") {
		t.Errorf("the empty state should explain itself:\n%s", view)
	}
}

// Chat is home. Opening on the dashboard would put the least common activity first and make Canopy
// look like something you watch rather than something you talk to.
func TestWithKeysOpensOnChat(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launch(store, withOneKey()).(tui.App)

	if app.Screen() != "chat" {
		t.Errorf("opened on %q, want chat", app.Screen())
	}
	view := plain(app.View())
	if !strings.Contains(view, "Canopy") || !strings.Contains(view, "Type a message") {
		t.Errorf("the chat screen should introduce itself:\n%s", view)
	}
}

func TestSwitchingBetweenScreens(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launch(store, withOneKey())

	// Navigation out of chat is on control keys, because every printable key belongs to the message
	// box. A plain "K" has to be typeable in a message.
	app = key(app, "ctrl+k")
	if app.(tui.App).Screen() != "keys" {
		t.Fatal("ctrl+k should open the credential screen")
	}
	if !strings.Contains(plain(app.View()), "claude") {
		t.Error("the credential should be listed")
	}

	app, _ = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if app.(tui.App).Screen() != "chat" {
		t.Error("esc should go back to chat, which is home")
	}

	app = key(app, "ctrl+d")
	if app.(tui.App).Screen() != "dashboard" {
		t.Fatal("ctrl+d should open the agents view")
	}
	app, _ = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if app.(tui.App).Screen() != "chat" {
		t.Error("esc should return to chat from the dashboard too")
	}
}

// "k" is move-up on the dashboard, so it may only mean "open credentials" where there is nothing
// to move around in. Otherwise a user navigating a list would be thrown onto another screen.
func TestLowercaseKDoesNotHijackNavigation(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := key(launch(store, withOneKey()), "ctrl+d")

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

	app := key(launch(store, withOneKey()), "ctrl+d")
	waiting := app.(tui.App).SubscribeCmd()

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

	app := launch(store, &fakeKeyStore{})
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
	app := launch(store, keyStore)

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

// Every screen has to say how to get to the credential screen, since a key is the one thing
// without which nothing else works.
func TestEveryScreenSaysHowToReachCredentials(t *testing.T) {
	store := fake.New()
	defer store.Close()

	chatView := plain(launch(store, withOneKey()).View())
	if !strings.Contains(chatView, "keys") {
		t.Errorf("chat should say how to reach credentials:\n%s", chatView)
	}

	dashboard := plain(key(launch(store, withOneKey()), "ctrl+d").View())
	if !strings.Contains(dashboard, "credentials") {
		t.Errorf("the dashboard should say how to reach credentials:\n%s", dashboard)
	}
}

// The splash is the first thing anyone sees, so it has to appear, get out of the way on its own,
// and get out of the way faster if the user is quicker than the timer.
func TestSplashAppearsAndClears(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")
	if app.Screen() != "splash" {
		t.Fatalf("launched on %q, want the splash", app.Screen())
	}
	if !strings.Contains(plain(app.View()), "Canopy") {
		t.Errorf("the splash should show the name:\n%s", plain(app.View()))
	}

	// Any key dismisses it, and is swallowed rather than acted on: the first keystroke after
	// launch is usually impatience, not a command.
	next := key(app, "j")
	if next.(tui.App).Screen() != "chat" {
		t.Errorf("a keystroke should dismiss the splash and land on chat, got %q",
			next.(tui.App).Screen())
	}
	// And is swallowed rather than typed into the message box, or the first impatient keypress
	// would end up in the message somebody is about to write.
	if got := next.(tui.App).ChatInput(); got != "" {
		t.Errorf("the dismissing keystroke landed in the input box as %q", got)
	}
}

func TestSplashGoesToCredentialsWhenThereAreNone(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := key(tui.NewApp(store, &fakeKeyStore{}, &stubEngine{}, "myproject", ""), "j")
	if app.(tui.App).Screen() != "keys" {
		t.Errorf("with no credentials the splash should lead to the credential screen, got %q",
			app.(tui.App).Screen())
	}
}

// The application fills the terminal and reflows, rather than printing a few lines wherever it
// happens to be.
func TestLayoutFillsAndReflows(t *testing.T) {
	store := fake.New()
	defer store.Close()

	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {200, 60}} {
		app := tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude").DismissSplash()
		next, _ := app.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})

		lines := strings.Split(plain(next.View()), "\n")
		if len(lines) < size.h-2 {
			t.Errorf("at %dx%d the view is %d lines, which does not fill the terminal",
				size.w, size.h, len(lines))
		}
		for i, line := range lines {
			if width := len([]rune(line)); width > size.w {
				t.Errorf("at %dx%d line %d is %d columns wide:\n%s", size.w, size.h, i, width, line)
			}
		}
	}
}

// Refusing to draw below a minimum is the honest option. A squeezed layout produces wrapped,
// overlapping output that reads as a rendering bug, and the user cannot tell that apart from the
// program being broken.
func TestTooSmallSaysSoRatherThanRenderingBadly(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude").DismissSplash()
	next, _ := app.Update(tea.WindowSizeMsg{Width: 30, Height: 8})

	view := plain(next.View())
	if !strings.Contains(view, "too small") {
		t.Errorf("a tiny terminal should say so:\n%s", view)
	}
	if strings.Contains(view, "WORKSPACE") {
		t.Error("the table should not be drawn into a space it cannot fit")
	}
}
