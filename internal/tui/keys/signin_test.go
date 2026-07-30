package keys

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The sign-in branch, tested the way the rest of this screen is: messages in, rendered frames out.
//
// The canary rule from model_test.go applies here with more force rather than less. A route hands
// this screen an account name and a device code, both of which are drawn, and neither of which may
// ever be a token. The fake below is therefore given a token-shaped value it never reveals, and the
// assertions check the frames rather than the model's fields.

// fakeRoute is a way in with a device code, which is the shape every headless flow takes.
var fakeRoute = Route{
	ID:     "copilot",
	Label:  "GitHub Copilot",
	Detail: "a GitHub account with a Copilot seat",
	Caveat: "usage is billed to your seat rather than to an API key",
}

// fakeSignIn is a build's worth of routes with no vendor behind them.
type fakeSignIn struct {
	routes   []Route
	beginErr error

	// begun records which route was asked for and under what name, so a test can hold that the
	// wizard passed on the name somebody typed rather than one of its own.
	begun []string

	attempt *fakeAttempt
}

func (f *fakeSignIn) Routes() []Route { return f.routes }

func (f *fakeSignIn) Begin(route Route, name string) (Attempt, error) {
	f.begun = append(f.begun, route.ID+"/"+name)
	if f.beginErr != nil {
		return nil, f.beginErr
	}
	f.attempt.name = name
	return f.attempt, nil
}

// fakeAttempt is one sign-in in flight, and the only thing in these tests that owns a credential.
//
// It stores the credential itself, which is the contract Attempt.Wait describes and the reason the
// screen never holds a token: by the time this returns, the thing with the token has finished with
// it.
type fakeAttempt struct {
	prompt Prompt
	waitFn func() error

	// release gates Wait, so a test can hold the screen in the waiting state and look at it.
	// stopped is closed by Cancel, which is how a real route's context would end the same wait.
	release chan struct{}
	stopped chan struct{}

	store   *stubStore
	name    string
	account string

	mu        sync.Mutex
	once      sync.Once
	cancelled bool
	stored    bool
}

func newAttempt(store *stubStore, account string, prompt Prompt) *fakeAttempt {
	released := make(chan struct{})
	close(released)
	return &fakeAttempt{
		store: store, account: account, prompt: prompt,
		release: released, stopped: make(chan struct{}),
	}
}

func (a *fakeAttempt) Prompt() Prompt { return a.prompt }

func (a *fakeAttempt) Wait() (Outcome, error) {
	select {
	case <-a.release:
	case <-a.stopped:
		return Outcome{}, errors.New("the sign-in was cancelled")
	}
	if a.waitFn != nil {
		if err := a.waitFn(); err != nil {
			return Outcome{}, err
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelled {
		return Outcome{}, errors.New("the sign-in was cancelled")
	}

	expires := time.Now().Add(time.Hour)
	a.store.keys = append(a.store.keys, core.KeyMetadata{
		Ref: core.KeyRef{Name: a.name, Provider: core.ProviderOpenAICompatible},
	})
	if a.store.identities == nil {
		a.store.identities = map[string]Identity{}
	}
	a.store.identities[a.name] = Identity{
		Kind: KindSignedIn, Account: a.account, ExpiresAt: &expires,
	}
	a.stored = true

	return Outcome{
		Name:     a.name,
		Identity: Identity{Kind: KindSignedIn, Account: a.account, ExpiresAt: &expires},
	}, nil
}

// Cancel undoes a sign-in that has already completed as well as stopping one that has not, which is
// what the interface asks of it and the half that is easy to leave out.
func (a *fakeAttempt) Cancel() {
	a.once.Do(func() { close(a.stopped) })

	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancelled = true
	if !a.stored {
		return
	}
	_ = a.store.Remove(core.KeyRef{Name: a.name})
	delete(a.store.identities, a.name)
	a.stored = false
}

func (a *fakeAttempt) didStore() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stored
}

// runCmd runs a command and hands its message back to the model, which is what the Bubble Tea
// runtime does and what these tests have to do in its place.
//
// Bounded rather than blocking forever, so a command that never returns fails as a test rather than
// as a suite that hangs.
func runCmd(t *testing.T, m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run, so nothing was ever asked of the vendor")
	}
	msg := collect(t, cmd)
	if msg == nil {
		return m, nil
	}
	return m.Update(msg)
}

func collect(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("the command never returned, so the sign-in is waiting on something nobody released")
		return nil
	}
}

// signInWizard walks as far as the sign-in step and returns the model waiting on the vendor.
func signInWizard(t *testing.T, m Model, name string) (Model, tea.Cmd) {
	t.Helper()
	m = key(m, "a")
	m = typeRunes(m, name)
	m = press(m, tea.KeyEnter)

	// Down past the two providers a credential can be pasted for, onto the first route.
	m = key(m, "j")
	m = key(m, "j")
	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.mode != modeSignIn {
		t.Fatalf("choosing a route landed on mode %v, want the sign-in step", m.mode)
	}
	return m, cmd
}

// The S-06 acceptance criterion: somebody with no credentials adds a subscription one through the
// wizard and is never shown a secret prompt.
func TestASubscriptionIsAddedThroughTheWizardWithoutASecretPromptAnywhere(t *testing.T) {
	store := &stubStore{}
	attempt := newAttempt(store, "octocat", Prompt{
		URL: "https://github.com/login/device", Code: "WDJB-MJHT",
	})
	service := &fakeSignIn{routes: []Route{fakeRoute}, attempt: attempt}

	m := NewWithSignIn(store, service)
	if !m.IsEmpty() {
		t.Fatal("a fresh store has no credentials")
	}

	m, cmd := signInWizard(t, m, "copilot-seat")
	assertNoSecretPrompt(t, m)

	m, cmd = runCmd(t, m, cmd) // Begin answered with the device code.
	assertNoSecretPrompt(t, m)
	m, _ = runCmd(t, m, cmd) // The vendor confirmed.

	if len(service.begun) != 1 || service.begun[0] != "copilot/copilot-seat" {
		t.Fatalf("the wizard asked for %v, want the route and the name that was typed", service.begun)
	}
	if m.mode != modeList {
		t.Fatalf("a finished sign-in landed on mode %v, want back at the list", m.mode)
	}
	if m.IsEmpty() {
		t.Fatal("the credential is not listed after signing in")
	}

	// Selected, not merely stored. The wizard has to end where the person walking it thinks it
	// ended, or the next message runs on whichever credential the resolver happens to prefer.
	chosen, picked := m.Chosen()
	if !picked || chosen != "copilot-seat" {
		t.Errorf("Chosen() = %q %v after signing in", chosen, picked)
	}

	view := plain(m.View())
	if !strings.Contains(view, "octocat") {
		t.Errorf("the account signed in as is not on screen:\n%s", view)
	}
	if strings.Contains(view, "value") || strings.Contains(view, "*****") {
		t.Errorf("a secret prompt appeared somewhere in the sign-in branch:\n%s", view)
	}
}

// assertNoSecretPrompt holds the whole point of the branch: no frame of it asks for a value.
func assertNoSecretPrompt(t *testing.T, m Model) {
	t.Helper()
	if m.mode == modeSecret {
		t.Fatal("the sign-in branch reached the secret prompt")
	}
	view := plain(m.View())
	for _, forbidden := range []string{"the value is not shown", "  value "} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("the sign-in branch showed a secret prompt (%q):\n%s", forbidden, view)
		}
	}
}

// A machine with no browser is a normal way to run a coding agent, so what a person needs is a page
// to visit and a code to type, as text they can read off the screen.
func TestTheSignInStepShowsTheCodeAndTheAddressForAMachineWithNoBrowser(t *testing.T) {
	store := &stubStore{}
	attempt := newAttempt(store, "octocat", Prompt{
		URL: "https://github.com/login/device", Code: "WDJB-MJHT",
	})
	service := &fakeSignIn{routes: []Route{fakeRoute}, attempt: attempt}

	m, cmd := signInWizard(t, NewWithSignIn(store, service), "copilot-seat")
	m, _ = runCmd(t, m, cmd)

	view := plain(m.View())
	for _, want := range []string{"https://github.com/login/device", "WDJB-MJHT", "Waiting for"} {
		if !strings.Contains(view, want) {
			t.Errorf("the sign-in step is missing %q:\n%s", want, view)
		}
	}
	// And what it costs, before there is a credential to regret. A caveat that arrives afterwards is
	// a caveat about a decision somebody has already made.
	if !strings.Contains(view, "billed to your seat") {
		t.Errorf("the route's caveat is not shown before anything is stored:\n%s", view)
	}
}

// The whole screen has to stay answerable while a vendor takes its time, because Bubble Tea runs one
// goroutine and a wait held inside a handler would freeze every screen in the program.
func TestTheScreenStaysAnswerableWhileASignInIsWaiting(t *testing.T) {
	store := &stubStore{}
	attempt := newAttempt(store, "octocat", Prompt{Code: "WDJB-MJHT"})
	attempt.release = make(chan struct{}) // nothing releases this
	service := &fakeSignIn{routes: []Route{fakeRoute}, attempt: attempt}

	m, cmd := signInWizard(t, NewWithSignIn(store, service), "copilot-seat")
	m, cmd = runCmd(t, m, cmd)

	// The wait is now running on its own goroutine and will not finish. The screen still draws and
	// still takes a key, which is the property being asserted.
	waiting := make(chan tea.Msg, 1)
	go func() { waiting <- cmd() }()

	if view := plain(m.View()); !strings.Contains(view, "WDJB-MJHT") {
		t.Errorf("the screen stopped drawing while waiting:\n%s", view)
	}

	m, cancel := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeList {
		t.Fatalf("escape during a wait landed on mode %v, want the list", m.mode)
	}
	if cancel == nil {
		t.Fatal("escape during a wait returned no command, so the attempt is still running")
	}
	if msg := collect(t, cancel); msg != nil {
		t.Errorf("cancelling produced a message: %v", msg)
	}
	if !attempt.cancelledNow() {
		t.Error("escape did not reach the attempt")
	}

	select {
	case <-waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("the wait never ended after the attempt was cancelled")
	}
}

func (a *fakeAttempt) cancelledNow() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancelled
}

// The S-06 acceptance criterion: cancelling at the sign-in step stores nothing and leaves no partial
// record.
func TestCancellingAtTheSignInStepStoresNothingAndLeavesNoPartialRecord(t *testing.T) {
	store := &stubStore{}
	attempt := newAttempt(store, "octocat", Prompt{Code: "WDJB-MJHT"})
	attempt.release = make(chan struct{})
	service := &fakeSignIn{routes: []Route{fakeRoute}, attempt: attempt}

	m, cmd := signInWizard(t, NewWithSignIn(store, service), "copilot-seat")
	m, _ = runCmd(t, m, cmd)

	m, cancel := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	collect(t, cancel)

	if len(store.keys) != 0 {
		t.Errorf("cancelling left a credential behind: %+v", store.keys)
	}
	if attempt.didStore() {
		t.Error("cancelling left the sign-in stored")
	}
	// And nothing half written in the screen either, which is cancelDraft's job for a draft and is
	// the same job here.
	if m.draftName != "" || m.draftRoute.ID != "" || !m.prompt.IsZero() || m.signingIn {
		t.Errorf("cancelling left a partial sign-in on the model: name=%q route=%q prompt=%+v",
			m.draftName, m.draftRoute.ID, m.prompt)
	}
	if _, picked := m.Chosen(); picked {
		t.Error("a cancelled sign-in still selected a credential")
	}
	if view := plain(m.View()); !strings.Contains(view, "Cancelled") {
		t.Errorf("the cancellation is not visible:\n%s", view)
	}
}

// The race worth caring about: the vendor confirms in the moment between somebody pressing escape
// and the cancellation arriving. A credential nobody knows they have is worse than one that failed
// to appear.
func TestCancellingASignInThatHadAlreadySucceededTakesTheCredentialBackOut(t *testing.T) {
	store := &stubStore{}
	attempt := newAttempt(store, "octocat", Prompt{Code: "WDJB-MJHT"})
	service := &fakeSignIn{routes: []Route{fakeRoute}, attempt: attempt}

	m, cmd := signInWizard(t, NewWithSignIn(store, service), "copilot-seat")
	m, cmd = runCmd(t, m, cmd)

	// The sign-in completes, but its answer is held rather than delivered, which is the state the
	// screen is in for as long as the message is in the runtime's queue.
	done := collect(t, cmd)
	if !attempt.didStore() {
		t.Fatal("the fake never stored anything, so there is nothing to take back out")
	}

	m, cancel := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	collect(t, cancel)

	if len(store.keys) != 0 {
		t.Errorf("the credential survived a cancellation that raced it: %+v", store.keys)
	}

	// And the answer, arriving late, is not taken for a live one.
	m, _ = m.Update(done)
	if _, picked := m.Chosen(); picked {
		t.Error("an answer from a cancelled sign-in selected a credential")
	}
	if view := plain(m.View()); strings.Contains(view, "Signed") {
		t.Errorf("an answer from a cancelled sign-in was announced:\n%s", view)
	}
}

// Cancel, then start another. The first vendor's answer must not be taken for the second's, which is
// what the attempt number is for.
func TestAnAnswerFromACancelledSignInIsNotTakenForTheNextOne(t *testing.T) {
	store := &stubStore{}
	first := newAttempt(store, "first-account", Prompt{Code: "AAAA-AAAA"})
	first.release = make(chan struct{})
	service := &fakeSignIn{routes: []Route{fakeRoute}, attempt: first}

	m, cmd := signInWizard(t, NewWithSignIn(store, service), "one")
	m, waitCmd := runCmd(t, m, cmd)
	_ = waitCmd

	m, cancel := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	collect(t, cancel)

	// A second sign-in, under a different name, which completes.
	second := newAttempt(store, "second-account", Prompt{Code: "BBBB-BBBB"})
	service.attempt = second
	m, cmd = signInWizard(t, m, "two")
	m, cmd = runCmd(t, m, cmd)
	m, _ = runCmd(t, m, cmd)

	if chosen, _ := m.Chosen(); chosen != "two" {
		t.Fatalf("the second sign-in selected %q", chosen)
	}
	// Now the first one's answer arrives. It belongs to nothing.
	stale := signInDoneMsg{attemptID: 1, outcome: Outcome{Name: "one"}}
	m, _ = m.Update(stale)

	if chosen, _ := m.Chosen(); chosen != "two" {
		t.Errorf("a stale answer moved the selection to %q", chosen)
	}
	if view := plain(m.View()); strings.Contains(view, "first-account") {
		t.Errorf("a stale answer reached the screen:\n%s", view)
	}
}

// A route that cannot start on this machine says so and leaves the other routes where they are.
// Dropping back to the list would make somebody retype the name to try the next one.
func TestARouteThatCannotStartLeavesTheOtherRoutesWhereTheyAre(t *testing.T) {
	store := &stubStore{}
	service := &fakeSignIn{
		routes:   []Route{fakeRoute},
		beginErr: errors.New("no browser and no device flow on this machine"),
	}

	m, cmd := signInWizard(t, NewWithSignIn(store, service), "copilot-seat")
	m, _ = runCmd(t, m, cmd)

	if m.mode != modeProvider {
		t.Fatalf("a failed start landed on mode %v, want the route list", m.mode)
	}
	if m.draftName != "copilot-seat" {
		t.Errorf("the name was thrown away, leaving %q", m.draftName)
	}
	if len(store.keys) != 0 {
		t.Errorf("a failed start stored something: %+v", store.keys)
	}
	if view := plain(m.View()); !strings.Contains(view, "no browser") {
		t.Errorf("the reason it could not start is not shown:\n%s", view)
	}
}

// The S-06 acceptance criterion: the list row says which account it is signed in as and when the
// grant expires.
func TestTheListRowNamesTheAccountAndWhenTheGrantExpires(t *testing.T) {
	expires := time.Date(2099, 3, 4, 17, 45, 0, 0, time.Local)
	store := &stubStore{
		keys: []core.KeyMetadata{
			{Ref: core.KeyRef{Name: "seat", Provider: core.ProviderOpenAICompatible}, Model: "gpt-5"},
			{Ref: core.KeyRef{Name: "kimi", Provider: core.ProviderOpenAICompatible},
				Model: "moonshot-v1-8k", Fingerprint: "a1b2c3d4e5f6"},
		},
		identities: map[string]Identity{
			"seat": {Kind: KindSignedIn, Account: "octocat", ExpiresAt: &expires},
		},
	}

	view := plain(New(store).View())
	if !strings.Contains(view, "signed in as octocat") {
		t.Errorf("the row does not say whose account it is:\n%s", view)
	}
	if !strings.Contains(view, "2099-03-04 17:45") {
		t.Errorf("the row does not say when the grant expires:\n%s", view)
	}
	// The credential nobody signed in to keeps the row it has always had.
	if !strings.Contains(view, "a1b2c3d4e5f6") {
		t.Errorf("a pasted credential lost its fingerprint:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "kimi") && strings.Contains(line, "signed in") {
			t.Errorf("a pasted credential claimed to be signed in:\n%s", line)
		}
	}
}

// A lapsed grant says lapsed in the word as well as in the colour, because a terminal with no colour
// is the terminal this has to be readable on. D-10.
func TestALapsedGrantSaysSoInWordsAndNotOnlyInColour(t *testing.T) {
	expired := time.Now().Add(-time.Hour)
	store := &stubStore{
		keys: []core.KeyMetadata{
			{Ref: core.KeyRef{Name: "seat", Provider: core.ProviderOpenAICompatible}},
		},
		identities: map[string]Identity{
			"seat": {Kind: KindSignedIn, Account: "octocat", ExpiresAt: &expired},
		},
	}

	view := plain(New(store).View())
	if !strings.Contains(view, "lapsed") {
		t.Errorf("a lapsed grant does not say so:\n%s", view)
	}
	if !strings.Contains(view, "sign in again") {
		t.Errorf("a lapsed grant does not say what fixes it:\n%s", view)
	}
}

// A delegated credential holds no token of the user's at all, which is the reason D-51 permits that
// route. The row says so rather than looking like a sign-in with a missing expiry.
func TestADelegatedRowSaysCanopyHoldsNothingOfTheUsers(t *testing.T) {
	store := &stubStore{
		keys: []core.KeyMetadata{
			{Ref: core.KeyRef{Name: "claude-code", Provider: core.ProviderAnthropic}},
		},
		identities: map[string]Identity{
			"claude-code": {Kind: KindDelegated, Account: "walid@example.com"},
		},
	}

	view := plain(New(store).View())
	if !strings.Contains(view, "the vendor's own agent, which holds the credential") {
		t.Errorf("the delegated row does not say who holds the credential:\n%s", view)
	}
	// And its model column is not an instruction to go and set something that cannot be set.
	if strings.Contains(view, "none set, press m") {
		t.Errorf("a delegated credential was told to set a model:\n%s", view)
	}
	if !strings.Contains(view, "the vendor chooses") {
		t.Errorf("the delegated row does not say who picks the model:\n%s", view)
	}
}

// A build with no routes offers exactly what it offered before phase S, rather than an empty list of
// ways in that would read as something being broken.
func TestAWizardWithNoRoutesOffersExactlyWhatItOfferedBefore(t *testing.T) {
	store := &stubStore{}
	m := NewWithSignIn(store, &fakeSignIn{})

	m = key(m, "a")
	m = typeRunes(m, "claude")
	m = press(m, tea.KeyEnter)

	if got := len(m.providerRows()); got != len(core.AllProviders()) {
		t.Fatalf("the provider step offers %d rows with no routes available", got)
	}
	m = press(m, tea.KeyEnter)
	if m.mode != modeSecret {
		t.Fatalf("anthropic landed on mode %v, want the secret prompt it has always used", m.mode)
	}
}

// The routes are under the providers rather than instead of them, because a person still pasting an
// API key must find the two rows exactly where they have always been.
func TestTheRouteRowsComeAfterTheProvidersAndSayWhatTheyNeed(t *testing.T) {
	store := &stubStore{}
	service := &fakeSignIn{routes: []Route{fakeRoute}, attempt: newAttempt(store, "octocat", Prompt{})}

	m := NewWithSignIn(store, service)
	m = key(m, "a")
	m = typeRunes(m, "seat")
	m = press(m, tea.KeyEnter)

	view := plain(m.View())
	anthropic := strings.Index(view, string(core.ProviderAnthropic))
	route := strings.Index(view, "GitHub Copilot")
	if anthropic < 0 || route < 0 || route < anthropic {
		t.Fatalf("the routes are not under the providers:\n%s", view)
	}
	if !strings.Contains(view, "a GitHub account with a Copilot seat") {
		t.Errorf("the route does not say what it needs:\n%s", view)
	}
}

// Every new frame has to fit the width most terminals actually open at, with no colour to lean on.
func TestTheSignInStepRendersAtEightyColumnsWithNoColour(t *testing.T) {
	store := &stubStore{}
	attempt := newAttempt(store, "octocat", Prompt{
		URL: "https://github.com/login/device", Code: "WDJB-MJHT",
	})
	service := &fakeSignIn{routes: []Route{fakeRoute}, attempt: attempt}

	m := NewWithSignIn(store, service)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, cmd := signInWizard(t, m, "copilot-seat")
	m, _ = runCmd(t, m, cmd)

	for _, frame := range []string{m.View(), m.Body()} {
		for _, line := range strings.Split(plain(frame), "\n") {
			if len([]rune(line)) > 80 {
				t.Errorf("a line is %d columns wide at eighty:\n%s", len([]rune(line)), line)
			}
		}
	}

	// And the frame still carries every fact it needs once the colour is gone, which is what a
	// NO_COLOR terminal shows. D-10.
	stripped := plain(m.View())
	for _, want := range []string{"WDJB-MJHT", "https://github.com/login/device", "esc stops this"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("stripped of colour the sign-in step lost %q:\n%s", want, stripped)
		}
	}
}

// A list whose sign-ins could not be read is still a list. Hiding every credential over one row that
// would not answer is worse than a row with less on it.
func TestACredentialWhoseSignInCannotBeReadIsStillListed(t *testing.T) {
	store := &stubStore{
		keys: []core.KeyMetadata{
			{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		},
		identityErr: errors.New("the keychain is locked"),
	}

	view := plain(New(store).View())
	if !strings.Contains(view, "claude") {
		t.Errorf("the credential vanished because its sign-in could not be read:\n%s", view)
	}
}

// A sign-in that started after somebody had already escaped out of it is stopped, not merely
// ignored.
//
// The two are easy to confuse and cost different things. Dropping the message keeps the screen
// correct, which is what the sibling test above holds. But the attempt on the other end of it is a
// live device-code poll: on a real route it goes on asking the vendor every few seconds, for as long
// as the program runs, on behalf of somebody who pressed escape and believes they are done. Nothing
// on screen would ever show it.
//
// The window is ordinary rather than exotic. Begin talks to a vendor, so it is a command, and escape
// is deliberately live for the whole of that wait; cancel-then-retry produces this sequence every
// time the first route is slow.
func TestASignInThatArrivesAfterEscapeIsStoppedRatherThanLeftPolling(t *testing.T) {
	store := &stubStore{}
	abandoned := newAttempt(store, "nobody", Prompt{Code: "AAAA-AAAA"})
	abandoned.release = make(chan struct{})
	service := &fakeSignIn{routes: []Route{fakeRoute}, attempt: abandoned}

	m, cmd := signInWizard(t, NewWithSignIn(store, service), "one")
	_ = cmd

	// Escape first, so the attempt number moves on before the vendor has answered Begin.
	m, cancel := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cancel != nil {
		collect(t, cancel)
	}

	// Now Begin's answer turns up, carrying a live attempt that belongs to nobody.
	stale := signInStartedMsg{attemptID: 0, attempt: abandoned}
	next, out := m.Update(stale)
	m = next
	if out != nil {
		collect(t, out)
	}

	abandoned.mu.Lock()
	stopped := abandoned.cancelled
	abandoned.mu.Unlock()
	if !stopped {
		t.Error("the abandoned sign-in was dropped but never cancelled, so its device code goes on " +
			"polling the vendor for the life of the program on behalf of somebody who pressed escape")
	}
	if chosen, picked := m.Chosen(); picked {
		t.Errorf("a stale start selected %q", chosen)
	}
}
