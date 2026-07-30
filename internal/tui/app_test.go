package tui_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
	agentsui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/agents"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// fakeKeyStore is the credential half, with no keychain involved.
type fakeKeyStore struct {
	keys  []core.KeyMetadata
	added map[string][]catalog.Model
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
	// sessions is for the few tests that need more than one, and so need the stub to be able to
	// tell them apart. Everything else uses the single session above and does not care which ID it
	// is asked for.
	sessions map[string]core.Session
	sent     []string
	// compacted and asked count the two calls that reach a provider without being a message, so a
	// test can assert that a key started neither.
	compacted int
	asked     []string
	agents    []session.AgentStatus
	added     []session.Agent
	using     [2]string
	created   int
	asking    bool
	waiting   []session.Waiting
	// useErr is what UseCredential answers with, for the tests about a switch the engine declines.
	// It refuses mid answer, and what the application does with a refusal is its own decision.
	useErr  error
	mode    core.Mode
	steered []string
	undone  []string
}

func (e *stubEngine) Session(id string) (core.Session, bool) {
	if e.sessions != nil {
		s, ok := e.sessions[id]
		return s, ok
	}
	return e.session, true
}

func (e *stubEngine) Send(_, prompt string) (string, error) {
	e.sent = append(e.sent, prompt)
	return "turn-1", nil
}

func (e *stubEngine) Cancel(string) {}

func (e *stubEngine) Events(uint64) <-chan core.Event { return make(chan core.Event) }

// Counted, not just answered. "No unconfirmed keystroke starts a paid call" is a claim about
// whether this was reached at all, and a stub that quietly returned an empty result would let every
// key in the table look innocent.
func (e *stubEngine) Compact(context.Context, string) (session.CompactionResult, error) {
	e.compacted++
	return session.CompactionResult{}, nil
}

func (e *stubEngine) Apply(string, session.CompactionResult) error { return nil }

func (e *stubEngine) Pending(string) (session.Prompt, bool) {
	if !e.asking {
		return session.Prompt{}, false
	}
	return session.Prompt{Request: permission.Request{Tool: "run_command", Kind: core.ToolExecute}}, true
}

func (e *stubEngine) Answer(string, bool, bool) bool { return false }

func (e *stubEngine) PendingAll() []session.Waiting { return e.waiting }

// The stub implements the agents view's engine too, so the app level tests exercise the real path
// rather than a screen that was never constructed.
func (e *stubEngine) AgentStatuses() []session.AgentStatus { return e.agents }

func (e *stubEngine) AddAgent(_ context.Context, agent session.Agent) (session.Agent, error) {
	if agent.Name == "" {
		return session.Agent{}, errors.New("an agent needs a name")
	}
	agent.SessionID = "session-" + agent.Name
	e.added = append(e.added, agent)
	e.agents = append(e.agents, session.AgentStatus{Agent: agent})
	return agent, nil
}

func (e *stubEngine) UseCredential(_, keyName, model string) error {
	if e.useErr != nil {
		return e.useErr
	}
	e.using = [2]string{keyName, model}
	// The conversation follows, because the screens read the model off the session. A stub that
	// recorded the call and left the session where it was would let a header claiming the old model
	// pass every assertion.
	e.session.KeyName, e.session.Model = keyName, model
	return nil
}

// The mode is what the box shows, so this holds a real one rather than answering a constant.
func (e *stubEngine) Mode(string) core.Mode {
	if e.mode.Name == "" {
		return core.ModeForTrust(core.TrustStandard)
	}
	return e.mode
}

func (e *stubEngine) SetMode(_ string, mode core.Mode) error {
	e.mode = mode
	return nil
}

// No ceiling here, so every mode is reachable and the key can offer all of them.
func (e *stubEngine) ModeUnusable(string, core.Mode) error { return nil }

func (e *stubEngine) Fork(_, _ string) (core.Session, error) {
	return core.Session{ID: "session-forked"}, nil
}

func (e *stubEngine) Trail() *permission.Trail { return nil }

func (e *stubEngine) Undo(_ context.Context, _, turnID string) error {
	e.undone = append(e.undone, turnID)
	return nil
}

// Create hands back a session that is genuinely different from the one before it, since a stub
// returning the same ID every time would make "the screen moved to the new conversation" pass
// without the screen having moved anywhere.
func (e *stubEngine) Create(keyName, model string) core.Session {
	e.created++
	created := core.Session{
		ID:      fmt.Sprintf("session-%d", e.created+1),
		KeyName: keyName,
		Model:   model,
	}
	// The stub has one session, so the new one becomes what Session returns. Real conversations are
	// all kept; this only has to be able to tell them apart.
	e.session = created
	return created
}

// launch builds the application at a known size, which is the state every test below actually cares
// about.
func launch(store core.SnapshotStore, keyStore keysui.Store) tea.Model {
	return launchWith(store, keyStore, &stubEngine{})
}

func launchWith(store core.SnapshotStore, keyStore keysui.Store, engine tui.Engine) tea.Model {
	// The conversation is named, so these tests count only the conversations they start themselves.
	// Left empty the application would open a new one, which is right in the product and would put
	// every "how many were created" assertion below one out.
	return launchSession(store, keyStore, engine, "session-1")
}

func launchSession(
	store core.SnapshotStore, keyStore keysui.Store, engine tui.Engine, sessionID string,
) tea.Model {
	app := tui.NewAppConfigured(store, keyStore, engine, "myproject", "claude",
		tui.AppOptions{Session: sessionID})
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
	// The name, and what to do next. The second used to be a line of its own on the welcome block
	// and is the frame's footer now, which was already saying the same thing one row lower: two
	// lists of the keys is one list that goes stale the first time the other is edited.
	if !strings.Contains(view, "Canopy") || !strings.Contains(view, "enter send") {
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
	if app.(tui.App).Screen() != "agents" {
		t.Fatalf("ctrl+d opened %q, want the agents view", app.(tui.App).Screen())
	}
	app, _ = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if app.(tui.App).Screen() != "chat" {
		t.Error("esc should return to chat from the agents view too")
	}

	// The worktree monitor is a different question from the agent list: one is about what the
	// agents are doing and the other about what state the code is in.
	app = key(key(app, "ctrl+d"), "w")
	if app.(tui.App).Screen() != "dashboard" {
		t.Errorf("w opened %q, want the worktree monitor", app.(tui.App).Screen())
	}
}

// "k" is move-up on the dashboard, so it may only mean "open credentials" where there is nothing
// to move around in. Otherwise a user navigating a list would be thrown onto another screen.
func TestLowercaseKDoesNotHijackNavigation(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := key(key(launch(store, withOneKey()), "ctrl+d"), "w")

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

	app := key(key(launch(store, withOneKey()), "ctrl+d"), "w")
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

	// The agents view was exempt while its footer had no room, and Phase M put K on it. Leaving the
	// exemption behind would have quietly stopped testing a screen that had started advertising the
	// key, which is the worst of both: no coverage and a comment saying none was wanted.
	agentsView := plain(key(launch(store, withOneKey()), "ctrl+d").View())
	if !strings.Contains(agentsView, "credentials") {
		t.Errorf("the agents view should say how to reach credentials:\n%s", agentsView)
	}

	dashboard := plain(key(key(launch(store, withOneKey()), "ctrl+d"), "w").View())
	if !strings.Contains(dashboard, "credentials") {
		t.Errorf("the worktree monitor should say how to reach credentials:\n%s", dashboard)
	}
}

// Footer hints are dropped from the right when they do not fit, and eighty columns is where that
// starts to bite. The two that have to survive are how to get help and how to reach a credential,
// because everything else in the program is downstream of those and somebody who cannot find them
// is stuck on the first screen.
func TestTheChatFooterKeepsHelpAndCredentialsAtEightyColumns(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")
	narrow, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	view := plain(narrow.(tui.App).View())
	footer := lastLine(view)
	for _, hint := range []string{"? help", "ctrl+k keys"} {
		if !strings.Contains(footer, hint) {
			t.Errorf("the footer at eighty columns dropped %q: %q", hint, footer)
		}
	}
	if len([]rune(footer)) > 80 {
		t.Errorf("the footer is %d columns wide, so the frame wraps: %q", len([]rune(footer)), footer)
	}
}

func lastLine(view string) string {
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	return lines[len(lines)-1]
}

// There is no launch screen, and the first keystroke belongs to whoever typed it.
//
// This replaces the pair of tests that checked the splash appeared and cleared. It was shown for
// nine hundred milliseconds before the application arrived, which is a delay between somebody typing
// a command and reaching the thing they typed it for. The name and the mark did not go with it: they
// are on the screen a conversation opens on, which is usable while it is being looked at.
//
// Worth keeping as a test rather than deleting outright, because the failure this guards against is
// specific. The splash swallowed the first keystroke on purpose, so somebody impatient did not land
// somewhere they had not asked for. With it gone that keystroke has to reach the message box, or the
// first character of the first message is dropped on every run.
func TestThereIsNoLaunchScreenAndTheFirstKeystrokeCounts(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")
	if app.Screen() != "chat" {
		t.Fatalf("launched on %q, want a conversation straight away", app.Screen())
	}
	if !strings.Contains(plain(app.View()), "Canopy") {
		t.Errorf("the opening screen does not show the name:\n%s", plain(app.View()))
	}

	next := key(app, "j")
	if got := next.(tui.App).ChatInput(); got != "j" {
		t.Errorf("the first keystroke reached the box as %q, so a character was swallowed", got)
	}
}

// With no credential the credential screen is still what comes first, which was true before and is
// the one case where a form beats a conversation: an agent with no key can be talked to and cannot
// answer, and finding that out by typing a message is a worse introduction than being asked.
func TestWithNoCredentialsTheCredentialScreenComesFirst(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := tui.NewApp(store, &fakeKeyStore{}, &stubEngine{}, "myproject", "")
	if app.Screen() != "keys" {
		t.Errorf("with no credentials the application opened on %q", app.Screen())
	}
}

// The application fills the terminal and reflows, rather than printing a few lines wherever it
// happens to be.
func TestLayoutFillsAndReflows(t *testing.T) {
	store := fake.New()
	defer store.Close()

	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}, {200, 60}} {
		app := tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")
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

	app := tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")
	next, _ := app.Update(tea.WindowSizeMsg{Width: 30, Height: 8})

	view := plain(next.View())
	if !strings.Contains(view, "too small") {
		t.Errorf("a tiny terminal should say so:\n%s", view)
	}
	if strings.Contains(view, "WORKSPACE") {
		t.Error("the table should not be drawn into a space it cannot fit")
	}
}

// Help is reachable from everywhere and leaves on any key, because somebody who opened it by
// accident should not have to find the one key that closes it.
func TestHelpOpensFromAnyScreenAndLeavesOnAnyKey(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := tui.NewApp(store, withOneKey(), &stubEngine{}, "myproject", "claude")

	// From chat with a message already started, where a question mark belongs to the message box
	// and must not open anything. Stealing a character out of the middle of a sentence somebody is
	// writing is the thing this rule exists to prevent.
	started, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("what is this")})
	typed, _ := started.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if typed.(tui.App).Screen() == "help" {
		t.Error("a question mark typed into a message opened the help screen")
	}
	if !strings.Contains(typed.(tui.App).ChatInput(), "?") {
		t.Errorf("the question mark did not reach the message box: %q", typed.(tui.App).ChatInput())
	}

	// From chat with nothing typed, where there is no message for it to be part of. Chat is the
	// screen the program opens on, and while this did not work the one key that lists every other
	// key could not be pressed from home.
	fromHome, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if fromHome.(tui.App).Screen() != "help" {
		t.Errorf("? on an empty message box landed on %q", fromHome.(tui.App).Screen())
	}

	// From the agents view, where it is not being typed into anything.
	agents, _ := app.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	opened, _ := agents.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	if opened.(tui.App).Screen() != "help" {
		t.Fatalf("? from the agents view landed on %q", opened.(tui.App).Screen())
	}
	if !strings.Contains(plain(opened.(tui.App).View()), "send") {
		t.Error("the overlay is on screen but does not list the bindings")
	}

	closed, _ := opened.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	if closed.(tui.App).Screen() != "agents" {
		t.Errorf("leaving help landed on %q, want where it was opened from", closed.(tui.App).Screen())
	}
}

func (f *fakeKeyStore) SetModel(ref core.KeyRef, model string) error {
	for i := range f.keys {
		if f.keys[i].Ref.Name == ref.Name {
			f.keys[i].Model = model
			return nil
		}
	}
	return errors.New("no such credential")
}

func (f *fakeKeyStore) Models(ref core.KeyRef) ([]catalog.Model, error) {
	return f.added[ref.Name], nil
}

func (f *fakeKeyStore) AddModel(ref core.KeyRef, id, name string) error {
	if f.added == nil {
		f.added = map[string][]catalog.Model{}
	}
	f.added[ref.Name] = append(f.added[ref.Name], catalog.Model{ID: id, Name: name})
	return nil
}

// The panic this asserts against was reachable in about four keystrokes from a fresh launch: open
// the agents view, press n, type a name, press enter. The agents view was constructed as a zero
// value, so its engine was nil, its list silently showed nothing, and creating an agent
// dereferenced the nil interface and killed the program.
func TestCreatingAnAgentFromTheInterfaceWorks(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{}
	app := launchWith(store, withOneKey(), engine)

	next, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if next.(tui.App).Screen() != "agents" {
		t.Fatalf("ctrl+d landed on %q", next.(tui.App).Screen())
	}

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("n")},
		{Type: tea.KeyRunes, Runes: []rune("worker")},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune("y")},
	} {
		next, _ = next.(tui.App).Update(key)
	}

	if len(engine.added) != 1 {
		t.Fatalf("%d agents created, want 1", len(engine.added))
	}
	created := engine.added[0]
	if created.Name != "worker" {
		t.Errorf("the agent is called %q", created.Name)
	}
	// The credential and the directory come from the application, and an agent created without
	// them fails on its first message rather than at creation, which is a much worse place to
	// find out.
	if created.KeyName != "claude" {
		t.Errorf("the new agent has credential %q, so it would fail on its first message", created.KeyName)
	}
	if created.Dir != "myproject" {
		t.Errorf("the new agent has working directory %q", created.Dir)
	}
}

// Choosing a credential has to reach the conversation. Before there was any way to choose, the
// screen was somewhere to add and remove keys and nowhere to pick one.
func TestChoosingACredentialReachesTheConversation(t *testing.T) {
	store := fake.New()
	defer store.Close()

	keyStore := &fakeKeyStore{keys: []core.KeyMetadata{
		{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		{Ref: core.KeyRef{Name: "nim", Provider: core.ProviderOpenAICompatible}, Model: "some/model"},
	}}
	engine := &stubEngine{}
	app := launchWith(store, keyStore, engine)

	next, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if next.(tui.App).Screen() != "keys" {
		t.Fatalf("ctrl+k landed on %q", next.(tui.App).Screen())
	}
	next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	chosen, _ := next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = chosen

	if engine.using[0] != "nim" {
		t.Errorf("the conversation is on %q, want the credential that was chosen", engine.using[0])
	}
	if engine.using[1] != "some/model" {
		t.Errorf("the model is %q, so the session would send an empty model field", engine.using[1])
	}
}

// Several credentials stored and none chosen is not the same as none stored, and it used to land on
// the chat, where the only way to find out was to type a message and watch it fail.
func TestWithNoCredentialChosenTheKeyScreenComesFirst(t *testing.T) {
	store := fake.New()
	defer store.Close()

	keyStore := &fakeKeyStore{keys: []core.KeyMetadata{
		{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		{Ref: core.KeyRef{Name: "nim", Provider: core.ProviderOpenAICompatible}, Model: "some/model"},
	}}

	app := tui.NewApp(store, keyStore, &stubEngine{}, "myproject", "")
	if got := app.Screen(); got != "keys" {
		t.Errorf("with nothing chosen the app opened on %q, want the credential screen", got)
	}

	// And with one chosen it opens on the conversation, which is the home screen.
	chosen := tui.NewApp(store, keyStore, &stubEngine{}, "myproject", "nim")
	if got := chosen.Screen(); got != "chat" {
		t.Errorf("with a credential chosen the app opened on %q", got)
	}
}

func (e *stubEngine) Steer(_, guidance string) error {
	e.steered = append(e.steered, guidance)
	return nil
}

func (e *stubEngine) Aside(_ context.Context, _, question string) (string, error) {
	e.asked = append(e.asked, question)
	return "", nil
}

func (e *stubEngine) Asides(string) []session.Aside { return nil }

func (e *stubEngine) Steering(string) []string { return nil }

func (s *stubEngine) Tools() (*core.ToolRegistry, bool) { return nil, false }

// twoKeys is a credential on each provider: one the catalog knows a lineup for, and one pointed at
// a gateway nobody here has heard of, which is the pair the picker has to draw honestly.
func twoKeys() *fakeKeyStore {
	return &fakeKeyStore{keys: []core.KeyMetadata{
		{
			Ref:         core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic},
			Model:       "claude-opus-5",
			Fingerprint: "abc123def456",
		},
		{
			Ref:     core.KeyRef{Name: "nim", Provider: core.ProviderOpenAICompatible},
			BaseURL: "https://api.moonshot.cn/v1",
		},
	}}
}

// onOpus is the conversation the picker tests start from: a real credential and a real model, so
// "what is it on now" has an answer to mark and to move away from.
func onOpus() *stubEngine {
	return &stubEngine{session: core.Session{ID: "session-1", KeyName: "claude", Model: "claude-opus-5"}}
}

func openPicker(t *testing.T, keyStore keysui.Store, engine tui.Engine) tui.App {
	t.Helper()

	store := fake.New()
	t.Cleanup(func() { store.Close() })

	app := launchWith(store, keyStore, engine)
	typed, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/model")})
	sent, cmd := typed.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("/model produced no command, so nothing was asked of the application")
	}
	opened, _ := sent.(tui.App).Update(cmd())

	if opened.(tui.App).Screen() != "model" {
		t.Fatalf("/model landed on %q", opened.(tui.App).Screen())
	}
	return opened.(tui.App)
}

// The command has to be in the menu, or the screen is reachable only by people who already know it
// is there, which is the same as it not existing.
func TestTheModelCommandIsOfferedInTheSlashMenu(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launchWith(store, twoKeys(), onOpus())
	menu, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})

	if !strings.Contains(plain(menu.(tui.App).View()), "model") {
		t.Errorf("a bare slash does not offer /model:\n%s", plain(menu.(tui.App).View()))
	}
}

// Opening it costs nothing. A screen that could change what you run by being looked at is one people
// stop opening, and esc is the key everything else in this program leaves by.
func TestOpeningTheModelPickerAndLeavingChangesNothing(t *testing.T) {
	engine := onOpus()
	app := openPicker(t, twoKeys(), engine)

	// Moved around first, because a cursor that has been walked is where an accidental apply would
	// come from if leaving applied anything.
	moved, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	left, _ := moved.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEsc})

	if left.(tui.App).Screen() != "chat" {
		t.Errorf("esc from the picker landed on %q", left.(tui.App).Screen())
	}
	if engine.using != [2]string{} {
		t.Errorf("leaving the picker changed the conversation to %v", engine.using)
	}
}

// The current model is marked, every credential gets a section with its provider on the header, and
// a key with nothing to offer says so rather than disappearing.
func TestThePickerMarksWhereYouAreAndKeepsEmptySections(t *testing.T) {
	app := openPicker(t, twoKeys(), onOpus())
	view := plain(app.View())

	for _, want := range []string{
		"claude (anthropic)",
		"* Claude Opus 5",
		"claude-opus-5",
		"Claude Sonnet 5",
		"nim (openai-compatible)",
		"api.moonshot.cn",
		"none set",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the picker is missing %q:\n%s", want, view)
		}
	}
}

// Picking under the same credential changes what the next request runs on, and the header says so.
// A change nothing on screen reflects is one somebody has to take on faith.
func TestPickingAModelMovesTheConversationAndTheHeaderSaysSo(t *testing.T) {
	engine := onOpus()
	app := openPicker(t, twoKeys(), engine)

	moved, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	applied, _ := moved.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})

	if applied.(tui.App).Screen() != "chat" {
		t.Errorf("applying landed on %q, want back where it was opened from", applied.(tui.App).Screen())
	}
	if engine.using[0] != "claude" || engine.using[1] != "claude-opus-4-8" {
		t.Fatalf("the conversation moved to %v, want the row under the one it was on", engine.using)
	}
	if !strings.Contains(plain(applied.(tui.App).View()), "claude-opus-4-8") {
		t.Errorf("the header does not name the model now in use:\n%s", plain(applied.(tui.App).View()))
	}
}

// A row under another section switches the credential as well, which is the case that makes this a
// picker rather than a list: two keys, two providers, one conversation.
func TestPickingUnderAnotherKeySwitchesCredentialAsWell(t *testing.T) {
	keyStore := twoKeys()
	keyStore.added = map[string][]catalog.Model{
		"nim": {{ID: "minimaxai/minimax-m2.7", Name: "MiniMax M2.7"}},
	}
	engine := onOpus()
	app := openPicker(t, keyStore, engine)

	// All the way down, which the cursor clamps at, and then one back up: the last row of every
	// section is the one that takes a typed model, so the last model row in the whole list is the
	// row above it, and that is the one the other credential offers.
	next := tea.Model(app)
	for range 20 {
		next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if !strings.Contains(plain(next.(tui.App).View()), "> ") {
		t.Fatal("the cursor left the list entirely")
	}
	applied, _ := next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})

	if engine.using[0] != "nim" || engine.using[1] != "minimaxai/minimax-m2.7" {
		t.Fatalf("the conversation moved to %v, want the model under the other credential", engine.using)
	}
	// The header follows both, since the credential is as much a fact about the next request as the
	// model is.
	if header := plain(applied.(tui.App).View()); !strings.Contains(header, "nim") {
		t.Errorf("the header does not name the credential now in use:\n%s", header)
	}
}

// Picking is about this conversation and nothing else. Rewriting the credential's recorded default
// would move every future conversation on that key because somebody tried something once.
func TestPickingAModelNeverRewritesTheKeysDefault(t *testing.T) {
	keyStore := twoKeys()
	app := openPicker(t, keyStore, onOpus())

	moved, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if _, cmd := moved.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		_ = cmd
	}

	if keyStore.keys[0].Model != "claude-opus-5" {
		t.Errorf("the key's own default moved to %q", keyStore.keys[0].Model)
	}
}

// With no colour at all the picker still says which model is in use and which row the cursor is on,
// because both are characters rather than hues. D-10.
func TestThePickerReadsWithNoColour(t *testing.T) {
	mono, ok := theme.ByName("mono")
	if !ok {
		t.Fatal("there is no colourless theme to check against")
	}
	theme.Set(mono)
	defer theme.Set(theme.Default)

	app := openPicker(t, twoKeys(), onOpus())
	view := plain(app.View())

	if !strings.Contains(view, "* Claude Opus 5") {
		t.Errorf("with no colour, nothing marks the model in use:\n%s", view)
	}
	if !strings.Contains(view, "> ") {
		t.Errorf("with no colour, nothing marks the row the cursor is on:\n%s", view)
	}
}

// The corner of the frame answers "whose conversation am I in", which is the question somebody with
// several agents running has every time they look up, and it follows them as they move between them.
func TestTheCornerNamesTheConversationsAgent(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{
		sessions: map[string]core.Session{
			"session-1": {ID: "session-1", Turns: []core.Turn{{
				ID: "t1", State: core.TurnComplete,
				Request: core.Message{Role: core.RoleUser, Text: "hello"},
			}}},
			"session-7": {ID: "session-7"},
		},
	}
	app := tui.NewAppConfigured(store, withOneKey(), engine, "myproject", "claude",
		tui.AppOptions{Session: "session-1", Agent: "main"})
	sized, _ := app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	view := plain(sized.(tui.App).View())
	if !strings.Contains(view, "main") {
		t.Errorf("the conversation Canopy opened on is not named in the corner:\n%s", view)
	}
	// And it is not said twice: the facts row gave the name up when the title took it.
	if strings.Count(view, "main") != 1 {
		t.Errorf("the agent's name is on the header %d times:\n%s", strings.Count(view, "main"), view)
	}

	// Moving to a subagent's conversation moves the name with it.
	switched, _ := sized.(tui.App).Update(agentsui.SwitchMsg{SessionID: "session-7", AgentName: "worker-2"})
	moved := plain(switched.(tui.App).View())
	if !strings.Contains(moved, "worker-2") {
		t.Errorf("the corner still names the conversation that was left:\n%s", moved)
	}
	if strings.Contains(moved, "main") {
		t.Errorf("the corner names two agents at once:\n%s", moved)
	}
}

// A visitor panel can summarize a request, but it cannot approve one. Its only action asks the
// application to open the owning conversation, where the chat's full canonical prompt is the
// surface that receives y or a.
func TestASurfacedQuestionSwitchesToTheConversationThatOwnsIt(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{
		sessions: map[string]core.Session{
			"session-1": {ID: "session-1", Turns: []core.Turn{{
				ID: "t1", State: core.TurnComplete,
				Request: core.Message{Role: core.RoleUser, Text: "hello"},
			}}},
			"session-7": {ID: "session-7"},
		},
	}
	app := tui.NewAppConfigured(store, withOneKey(), engine, "myproject", "claude",
		tui.AppOptions{Session: "session-1", Agent: "main"})

	switched, _ := app.Update(chat.SwitchMsg{SessionID: "session-7", AgentName: "worker-2"})
	view := plain(switched.(tui.App).View())
	if !strings.Contains(view, "worker-2") || strings.Contains(view, "main") {
		t.Errorf("the surfaced question did not open its owning conversation:\n%s", view)
	}
}

// A pick the engine declines must change nothing at all, including the note the application keeps of
// which credential to start the next conversation on.
//
// It used to move regardless. The conversation correctly stayed where it was, and then ctrl+n opened
// a new one on the credential the refusal had just declined, with no model on it, so the first
// message failed at the far end for a reason nothing on this side explained.
func TestARefusedPickLeavesTheNextConversationWhereItWas(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := onOpus()
	engine.useErr = errors.New("this session is mid answer, so wait for it to finish or stop it first")

	// The pick has to be under the other credential, since that is the case the defect was visible
	// in: a refusal that moved the note to a key the conversation never reached.
	keyStore := twoKeys()
	keyStore.added = map[string][]catalog.Model{
		"nim": {{ID: "minimaxai/minimax-m2.7", Name: "MiniMax M2.7"}},
	}

	app := launchWith(store, keyStore, engine)
	typed, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/model")})
	sent, cmd := typed.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})
	opened, _ := sent.(tui.App).Update(cmd())

	// All the way down and one back up, which is the last model row: the one under the other key.
	next := tea.Model(opened)
	for range 20 {
		next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	refused, _ := next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})

	if engine.using != [2]string{} {
		t.Fatalf("a refused pick reached the conversation as %v", engine.using)
	}

	// The refusal is on screen rather than swallowed, or the screen looks broken.
	if view := plain(refused.(tui.App).View()); !strings.Contains(view, "mid answer") {
		t.Errorf("the refusal is not shown:\n%s", view)
	}

	before := engine.created
	started, _ := refused.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	_ = started

	if engine.created != before+1 {
		t.Fatalf("ctrl+n created %d conversations", engine.created-before)
	}
	created := engine.session
	if created.KeyName != "claude" || created.Model != "claude-opus-5" {
		t.Errorf("the new conversation opened on %q running %q, want the credential and model that "+
			"were in use before the refusal", created.KeyName, created.Model)
	}
}

// The picker is the last model surface that had no way to name something the lists have never heard
// of, which made it the one place a list Canopy ships could stand between somebody and the model
// they wanted. D-46 rule 1.
func TestThePickerTakesAModelItHasNeverHeardOf(t *testing.T) {
	engine := onOpus()
	app := openPicker(t, twoKeys(), engine)

	if !strings.Contains(plain(app.View()), "something else, type it") {
		t.Fatalf("the picker offers no way to type a model:\n%s", plain(app.View()))
	}

	// Down to the row that takes typing under the first credential. The cursor opens on the model
	// the conversation is running, which is the second of that credential's eight, so its typing row
	// is seven below.
	next := tea.Model(app)
	for range 7 {
		next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	opened, _ := next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Enter opens the field rather than applying, since there is nothing on it yet to apply.
	if engine.using != [2]string{} {
		t.Fatalf("the typing row applied something by itself: %v", engine.using)
	}

	typing := tea.Model(opened)
	for _, r := range "claude-something-unreleased" {
		typing, _ = typing.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Every key belongs to the field while it is up, including the ones that would otherwise move.
	if view := plain(typing.(tui.App).View()); !strings.Contains(view, "claude-something-unreleased") {
		t.Errorf("what was typed is not on screen:\n%s", view)
	}

	applied, _ := typing.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})

	if engine.using[0] != "claude" || engine.using[1] != "claude-something-unreleased" {
		t.Errorf("the typed model reached the conversation as %v", engine.using)
	}
	if applied.(tui.App).Screen() != "chat" {
		t.Errorf("applying a typed model landed on %q", applied.(tui.App).Screen())
	}
	if view := plain(applied.(tui.App).View()); !strings.Contains(view, "claude-something-unreleased") {
		t.Errorf("the header does not name the typed model:\n%s", view)
	}
}

// Esc while typing is the more local meaning: it puts the keyboard back on the list rather than
// leaving the screen, and nothing has been chosen either way.
func TestLeavingTheTypedRowChangesNothing(t *testing.T) {
	engine := onOpus()
	app := openPicker(t, twoKeys(), engine)

	next := tea.Model(app)
	for range 7 {
		next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "half-typed" {
		next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	back, _ := next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if back.(tui.App).Screen() != "model" {
		t.Errorf("esc while typing left the picker for %q", back.(tui.App).Screen())
	}
	if engine.using != [2]string{} {
		t.Errorf("abandoning a half typed model applied %v", engine.using)
	}
	if view := plain(back.(tui.App).View()); strings.Contains(view, "half-typed") {
		t.Errorf("what was abandoned is still on screen:\n%s", view)
	}

	// And a second esc leaves the picker, which is what it always meant.
	left, _ := back.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if left.(tui.App).Screen() != "chat" {
		t.Errorf("esc from the list landed on %q", left.(tui.App).Screen())
	}
}

// A credential with nothing to offer still says so, and can still be typed into, which is the state
// an unrecognised endpoint is in on the day it is added.
func TestAKeyWithNothingToOfferCanStillBeTypedInto(t *testing.T) {
	engine := onOpus()
	app := openPicker(t, twoKeys(), engine)

	view := plain(app.View())
	if !strings.Contains(view, "none set") {
		t.Fatalf("the empty section lost its warning:\n%s", view)
	}

	// The last row in the list belongs to that section, since it is the only row it has.
	next := tea.Model(app)
	for range 20 {
		next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})
	for _, r := range "moonshot-v1-32k" {
		next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	applied, _ := next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})

	if engine.using[0] != "nim" || engine.using[1] != "moonshot-v1-32k" {
		t.Errorf("the typed model reached the conversation as %v, want it under the empty key",
			engine.using)
	}
	// And it leaves the picker on the way, like any other choice taken from it.
	if screen := applied.(tui.App).Screen(); screen != "chat" {
		t.Errorf("applying a typed model under an empty key landed on %q", screen)
	}
}
