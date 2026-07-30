package tui_test

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// Signing in seen from the application rather than from the screen.
//
// The screen's own tests hold that the wizard reaches a credential without a secret prompt. What can
// only be asserted here is the half the screen deliberately does not own: which credential the
// conversation actually runs on. The credential screen states a preference and the application
// applies it, and a sign-in ends on a message from a vendor rather than on a keystroke, which is the
// case that arrangement had never been asked to handle.

// stubSignIn is a route with a device code and nothing behind it.
type stubSignIn struct {
	store   *fakeKeyStore
	account string
}

func (s *stubSignIn) Routes() []keysui.Route {
	return []keysui.Route{{
		ID:     "claude-code",
		Label:  "Claude, through your own Claude Code",
		Detail: "a Claude Code installation you have already signed in to",
	}}
}

func (s *stubSignIn) Begin(_ keysui.Route, name string) (keysui.Attempt, error) {
	return &stubAttempt{store: s.store, name: name, account: s.account}, nil
}

type stubAttempt struct {
	store   *fakeKeyStore
	name    string
	account string
}

func (a *stubAttempt) Prompt() keysui.Prompt {
	return keysui.Prompt{Doing: "looking for Claude Code"}
}

func (a *stubAttempt) Wait() (keysui.Outcome, error) {
	identity := keysui.Identity{Kind: keysui.KindDelegated, Account: a.account}
	a.store.keys = append(a.store.keys, core.KeyMetadata{
		Ref: core.KeyRef{Name: a.name, Provider: core.ProviderAnthropic},
	})
	if a.store.identities == nil {
		a.store.identities = map[string]keysui.Identity{}
	}
	a.store.identities[a.name] = identity
	return keysui.Outcome{Name: a.name, Identity: identity}, nil
}

func (a *stubAttempt) Cancel() {}

// launchSigningIn opens the application on an empty credential store with one route available,
// which is the machine this whole phase exists for: a subscription and no API key.
func launchSigningIn(t *testing.T, keyStore *fakeKeyStore, engine tui.Engine) tui.App {
	t.Helper()

	store := fake.New()
	t.Cleanup(func() { store.Close() })

	app := tui.NewAppConfigured(store, keyStore, engine, "myproject", "", tui.AppOptions{
		Session: "session-1",
		SignIn:  &stubSignIn{store: keyStore, account: "walid@example.com"},
	})
	next, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(tui.App)
}

// step runs a command and feeds its message back, which is what the runtime does.
func step(t *testing.T, app tui.App, cmd tea.Cmd) (tui.App, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command, so the sign-in never reached the vendor")
	}
	msg := nextMsg(t, cmd)
	if msg == nil {
		return app, nil
	}
	next, out := app.Update(msg)
	return next.(tui.App), out
}

// The S-06 acceptance criterion: somebody with no credentials signs one in through the wizard and
// the conversation's next message runs on it with no further keystroke.
//
// The keystroke is the part worth being careful about. A sign-in finishes on a message from a
// vendor, and the application used to look at what the credential screen had chosen only while
// handling a key, so the choice sat unapplied until the person pressed something unrelated.
func TestTheConversationRunsOnASignedInCredentialWithNoFurtherKeystroke(t *testing.T) {
	keyStore := &fakeKeyStore{}
	engine := &stubEngine{session: core.Session{ID: "session-1"}}
	app := launchSigningIn(t, keyStore, engine)

	if app.Screen() != "keys" {
		t.Fatalf("a run with no credentials opened on %q", app.Screen())
	}

	var cmd tea.Cmd
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("a")},
		{Type: tea.KeyRunes, Runes: []rune("claude-code")},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyEnter},
	} {
		next, out := app.Update(key)
		app, cmd = next.(tui.App), out
	}

	// From here nothing is typed. The vendor answers, and the vendor answering is the only thing
	// that happens between the route being chosen and the conversation running on the credential.
	app, cmd = step(t, app, cmd) // Begin answered.
	app, _ = step(t, app, cmd)   // The sign-in completed.

	if engine.using != [2]string{"claude-code", ""} {
		t.Fatalf("the conversation runs on %v after signing in, want the credential just signed in to",
			engine.using)
	}
	view := plain(app.View())
	if !strings.Contains(view, "now the credential for this conversation") {
		t.Errorf("the switch was not acknowledged:\n%s", view)
	}
	if !strings.Contains(view, "walid@example.com") {
		t.Errorf("the account signed in as is not on screen:\n%s", view)
	}
}

// The refusal protocol is not weakened by the message path. The engine refuses mid answer, and a
// credential that was signed in successfully but not switched to has to say exactly that: stored,
// not selected.
func TestASignInRefusedByTheConversationIsStoredAndNotClaimed(t *testing.T) {
	keyStore := &fakeKeyStore{}
	engine := &stubEngine{session: core.Session{ID: "session-1"}}
	engine.useErr = errors.New("this session is mid answer, so wait for it to finish or stop it first")
	app := launchSigningIn(t, keyStore, engine)

	var cmd tea.Cmd
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("a")},
		{Type: tea.KeyRunes, Runes: []rune("claude-code")},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyEnter},
	} {
		next, out := app.Update(key)
		app, cmd = next.(tui.App), out
	}
	app, cmd = step(t, app, cmd)
	app, _ = step(t, app, cmd)

	if engine.using != [2]string{} {
		t.Fatalf("a refused credential reached the conversation as %v", engine.using)
	}
	if len(keyStore.keys) != 1 {
		t.Fatalf("the credential was not stored: %+v", keyStore.keys)
	}
	view := plain(app.View())
	for _, want := range []string{`stored "claude-code"`, "not selected", "mid answer"} {
		if !strings.Contains(view, want) {
			t.Errorf("the screen lost %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "now the credential") {
		t.Errorf("the refused switch is presented as applied:\n%s", view)
	}

	// And the refusal is disarmed, so a later unrelated key does not silently retry it once the turn
	// that caused it has ended.
	engine.useErr = nil
	after, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if engine.using != [2]string{} {
		t.Errorf("an unrelated later key retried the refused switch as %v", engine.using)
	}
	_ = after
}

// The S-06 acceptance criterion: a vendor that chooses the model says so where the picker would
// otherwise be empty.
//
// The two states look identical from the picker's side, and only one of them is worth a person's
// time: "Canopy has no lineup for this endpoint" is something to fix, and "somebody else's agent
// picks the model inside its own loop" is not.
func TestThePickerSaysTheVendorChoosesRatherThanShowingAnEmptyList(t *testing.T) {
	keyStore := &fakeKeyStore{
		keys: []core.KeyMetadata{{
			Ref: core.KeyRef{Name: "claude-code", Provider: core.ProviderOpenAICompatible},
		}},
		identities: map[string]keysui.Identity{
			"claude-code": {Kind: keysui.KindDelegated, Account: "walid@example.com"},
		},
	}

	app := openPicker(t, keyStore, onOpus())
	view := plain(app.View())

	if !strings.Contains(view, "the vendor's own agent chooses the model on this credential") {
		t.Errorf("the picker did not say who chooses:\n%s", view)
	}
	if strings.Contains(view, "none set, press ctrl+k") {
		t.Errorf("the picker told somebody to set a model that cannot be set:\n%s", view)
	}
	// The row that takes anything typed is still there. D-46 rule 1 has no exception for a claim
	// about somebody else's agent.
	if !strings.Contains(view, "something else, type it") {
		t.Errorf("the section lost its typed row:\n%s", view)
	}
}

// A credential nobody signed in to keeps the words it has always had, so the note above is a fact
// about delegated credentials rather than a new phrasing for every empty section.
func TestAnEndpointWithNoLineupStillSaysNoneSet(t *testing.T) {
	keyStore := &fakeKeyStore{
		keys: []core.KeyMetadata{{
			Ref:     core.KeyRef{Name: "nim", Provider: core.ProviderOpenAICompatible},
			BaseURL: "https://api.moonshot.cn/v1",
		}},
	}

	view := plain(openPicker(t, keyStore, onOpus()).View())
	if !strings.Contains(view, "none set, press ctrl+k") {
		t.Errorf("an endpoint with no lineup lost its own words:\n%s", view)
	}
	if strings.Contains(view, "the vendor's own agent chooses") {
		t.Errorf("a pasted credential was described as delegated:\n%s", view)
	}
}

// The credential screen's frames have to fit the width most terminals open at, sign-in included.
func TestTheSignInStepFitsEightyColumnsInsideTheApplicationFrame(t *testing.T) {
	keyStore := &fakeKeyStore{}
	app := launchSigningIn(t, keyStore, &stubEngine{session: core.Session{ID: "session-1"}})

	var cmd tea.Cmd
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("a")},
		{Type: tea.KeyRunes, Runes: []rune("claude-code")},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyRunes, Runes: []rune("j")},
		{Type: tea.KeyEnter},
	} {
		next, out := app.Update(key)
		app, cmd = next.(tui.App), out
	}
	app, _ = step(t, app, cmd)

	view := plain(app.View())
	for _, line := range strings.Split(view, "\n") {
		if len([]rune(line)) > 80 {
			t.Errorf("a line is %d columns wide at eighty:\n%s", len([]rune(line)), line)
		}
	}
	if !strings.Contains(view, "looking for Claude Code") {
		t.Errorf("the wait says nothing about what is happening:\n%s", view)
	}
	if !strings.Contains(view, "esc cancel") {
		t.Errorf("the footer does not say how to stop:\n%s", view)
	}
}
