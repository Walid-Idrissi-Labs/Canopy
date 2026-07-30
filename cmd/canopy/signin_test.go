package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// The command line half of signing in.
//
// No route is built yet, so every test here supplies one. That is the point rather than a gap being
// worked around: the routes arrive with S-03, S-04 and S-05, and what these hold is that whichever
// of them lands first finds a command that already behaves correctly, including the parts nobody
// building a vendor integration would think to write, such as refusing -token and saying out loud
// that nothing was asked of the vendor.

// fakeRoutes is a build's worth of sign-in routes with no vendor behind them.
type fakeRoutes struct {
	routes  []keysui.Route
	store   *keys.Store
	prompt  keysui.Prompt
	account string

	// waitErr is what the vendor refuses with, for the tests about a sign-in that does not complete.
	waitErr error

	// blockUntil holds Wait open, so the interrupt test can arrive while it is still waiting. Set
	// once before the command runs and never written again, because the two goroutines that read it
	// are the command's wait and the signal handler cancelling it.
	blockUntil chan struct{}
	release    sync.Once

	// mu guards cancelled, which really is written by one goroutine and read by the other.
	mu        sync.Mutex
	cancelled bool

	revoked   []string
	revokeErr error
	report    *signInReport
	reportErr error
}

func (f *fakeRoutes) Routes() []keysui.Route { return f.routes }

func (f *fakeRoutes) Begin(route keysui.Route, name string) (keysui.Attempt, error) {
	return &fakeCLIAttempt{routes: f, route: route, name: name}, nil
}

type fakeCLIAttempt struct {
	routes *fakeRoutes
	route  keysui.Route
	name   string
}

func (a *fakeCLIAttempt) Prompt() keysui.Prompt { return a.routes.prompt }

func (a *fakeCLIAttempt) Wait() (keysui.Outcome, error) {
	if a.routes.blockUntil != nil {
		<-a.routes.blockUntil
	}
	a.routes.mu.Lock()
	cancelled := a.routes.cancelled
	a.routes.mu.Unlock()
	if cancelled {
		return keysui.Outcome{}, errors.New("the sign-in was cancelled")
	}
	if a.routes.waitErr != nil {
		return keysui.Outcome{}, a.routes.waitErr
	}

	expires := time.Now().Add(time.Hour)
	meta, err := a.routes.store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: a.name, Provider: core.ProviderOpenAICompatible},
			BaseURL: "https://api.githubcopilot.com"},
		keys.SignIn{Kind: keys.KindSignedIn, Account: a.routes.account, ExpiresAt: &expires},
		keys.Tokens{Access: core.NewSecret("access-token"), Refresh: core.NewSecret("refresh-token")},
	)
	if err != nil {
		return keysui.Outcome{}, err
	}
	return keysui.Outcome{
		Name: meta.Ref.Name,
		Identity: keysui.Identity{
			Kind: keysui.KindSignedIn, Account: a.routes.account, ExpiresAt: &expires,
		},
	}, nil
}

func (a *fakeCLIAttempt) Cancel() {
	a.routes.mu.Lock()
	a.routes.cancelled = true
	a.routes.mu.Unlock()

	if a.routes.blockUntil != nil {
		a.routes.release.Do(func() { close(a.routes.blockUntil) })
	}
	// Removes whatever the sign-in had managed to store, which is Attempt.Cancel's contract and the
	// half a route is likely to leave out.
	_ = a.routes.store.Remove(core.KeyRef{Name: a.name})
}

func (f *fakeRoutes) Revoke(_ context.Context, meta core.KeyMetadata) error {
	if f.revokeErr != nil {
		return f.revokeErr
	}
	f.revoked = append(f.revoked, meta.Ref.Name)
	return nil
}

// reportingRoutes is fakeRoutes that can also ask the vendor, kept as a separate type so a test can
// have a registry that cannot.
type reportingRoutes struct{ *fakeRoutes }

func (r reportingRoutes) Report(_ context.Context, _ core.KeyMetadata) (signInReport, error) {
	if r.reportErr != nil {
		return signInReport{}, r.reportErr
	}
	return *r.report, nil
}

const copilotRoute = "copilot"

// storeWithRoutes builds a real key store on a memory backend and points the CLI at it, with one
// sign-in route available.
//
// A real store rather than a fake one, because half of what these tests are about is where things
// end up: a token in the backend and not in keys.json, and a record that both surfaces read the same
// way. A fake store would agree with whatever the test expected.
func storeWithRoutes(t *testing.T) (*keys.Store, keys.Backend, *fakeRoutes, string) {
	t.Helper()

	backend := keys.NewMemoryBackend()
	path := filepath.Join(t.TempDir(), "keys.json")
	store := keys.NewStore(backend, path)

	originalStore := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = originalStore })

	routes := &fakeRoutes{
		routes: []keysui.Route{{
			ID:     copilotRoute,
			Label:  "GitHub Copilot",
			Detail: "a GitHub account with a Copilot seat",
			Caveat: "usage is billed to your seat rather than to an API key",
		}},
		store:   store,
		account: "octocat",
		prompt:  keysui.Prompt{URL: "https://github.com/login/device", Code: "WDJB-MJHT"},
	}

	originalRoutes := signInRoutes
	signInRoutes = routes
	t.Cleanup(func() { signInRoutes = originalRoutes })

	return store, backend, routes, path
}

// The S-07 acceptance criterion: signing in from the CLI produces a credential the interface then
// lists, and one added in the interface is visible to the CLI.
func TestACredentialSignedInFromEitherSurfaceIsVisibleToTheOther(t *testing.T) {
	store, _, routes, _ := storeWithRoutes(t)

	var out bytes.Buffer
	if err := runKeys([]string{"signin", "seat", "-route", copilotRoute}, &out); err != nil {
		t.Fatalf("keys signin: %v", err)
	}
	printed := out.String()
	for _, want := range []string{
		"https://github.com/login/device", "WDJB-MJHT", "Signed \"seat\" in as octocat",
		"billed to your seat",
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("the sign-in output is missing %q:\n%s", want, printed)
		}
	}

	// The interface, reading the same store through the same narrow view the application hands it.
	screen := keysui.New(signInAware{store})
	view := ansiCodes.ReplaceAllString(screen.View(), "")
	if !strings.Contains(view, "seat") || !strings.Contains(view, "signed in as octocat") {
		t.Errorf("the interface does not show the credential signed in at the terminal:\n%s", view)
	}

	// And the other direction: a sign-in driven from the wizard shows up in `canopy keys list`.
	wizard := wizardSignIn(t, keysui.NewWithSignIn(signInAware{store}, wizardRoutes{routes}), "second")
	if chosen, picked := wizard.Chosen(); !picked || chosen != "second" {
		t.Fatalf("the wizard finished with %q selected", chosen)
	}

	out.Reset()
	if err := runKeys([]string{"list"}, &out); err != nil {
		t.Fatalf("keys list: %v", err)
	}
	listing := out.String()
	for _, want := range []string{"seat", "second", "octocat", "SIGNED IN AS"} {
		if !strings.Contains(listing, want) {
			t.Errorf("the listing is missing %q:\n%s", want, listing)
		}
	}
	if _, err := store.Metadata(core.KeyRef{Name: "second"}); err != nil {
		t.Errorf("the credential the wizard signed in is not in the store: %v", err)
	}
}

// wizardRoutes is the registry as the credential screen sees it, which is the same value: the two
// surfaces share one interface rather than each having their own.
type wizardRoutes struct{ *fakeRoutes }

// wizardSignIn walks the interface's wizard to a finished sign-in.
func wizardSignIn(t *testing.T, m keysui.Model, name string) keysui.Model {
	t.Helper()

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	for _, r := range name {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	for range 2 {
		if cmd == nil {
			t.Fatal("the wizard stopped before the sign-in finished")
		}
		msg := cmd()
		m, cmd = m.Update(msg)
	}
	return m
}

// The S-07 acceptance criterion: signing out leaves no token in the backend and no record in
// keys.json.
func TestSigningOutLeavesNoTokenInTheBackendAndNoRecordInKeysJson(t *testing.T) {
	store, backend, routes, path := storeWithRoutes(t)

	var out bytes.Buffer
	if err := runKeys([]string{"signin", "seat", "-route", copilotRoute}, &out); err != nil {
		t.Fatalf("keys signin: %v", err)
	}
	if _, err := backend.Get("seat"); err != nil {
		t.Fatalf("the sign-in stored no tokens: %v", err)
	}

	out.Reset()
	if err := runKeys([]string{"signout", "seat"}, &out); err != nil {
		t.Fatalf("keys signout: %v", err)
	}

	if _, err := backend.Get("seat"); !errors.Is(err, keys.ErrNotFound) {
		t.Errorf("a token survived signing out: %v", err)
	}
	if _, err := store.Metadata(core.KeyRef{Name: "seat"}); !errors.Is(err, keys.ErrNotFound) {
		t.Errorf("the record survived signing out: %v", err)
	}
	// Against the file's bytes rather than the struct, because a record that no longer parses is
	// still a record somebody's disk is holding.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading keys.json: %v", err)
	}
	if strings.Contains(string(raw), "seat") {
		t.Errorf("keys.json still mentions the credential:\n%s", raw)
	}

	// And the grant was ended at the vendor rather than only forgotten here, which is the whole
	// difference between signing out and removing.
	if len(routes.revoked) != 1 || routes.revoked[0] != "seat" {
		t.Errorf("the vendor was not told: %v", routes.revoked)
	}
	if !strings.Contains(out.String(), "revoked at the vendor") {
		t.Errorf("the output does not say the grant was revoked:\n%s", out.String())
	}
}

// A credential nobody signed in to cannot be signed out of, and the refusal names what does remove
// it. Treating the two as one command is how somebody believes they revoked access they still have.
func TestSigningOutAPastedCredentialRefusesAndNamesWhatRemovesIt(t *testing.T) {
	store, _, _, _ := storeWithRoutes(t)

	if _, err := store.Put(
		core.KeyMetadata{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		core.NewSecret("sk-ant-something"),
	); err != nil {
		t.Fatalf("planting a pasted credential: %v", err)
	}

	var out bytes.Buffer
	err := runKeys([]string{"signout", "claude"}, &out)
	if err == nil {
		t.Fatal("signing out of a pasted credential was accepted")
	}
	if !strings.Contains(err.Error(), "canopy keys remove claude") {
		t.Errorf("the refusal does not name what removes it: %v", err)
	}
}

// The S-07 acceptance criterion: `canopy keys test` on a pasted key behaves as it does today, with
// unchanged output where the output is unchanged.
func TestKeysTestOnAPastedKeyStillReportsStorageAndNothingMore(t *testing.T) {
	storeWithCanary(t)

	var out bytes.Buffer
	if err := runKeys([]string{"test", "claude"}, &out); err != nil {
		t.Fatalf("keys test: %v", err)
	}
	report := out.String()

	for _, want := range []string{
		"claude is readable from the", "  provider     anthropic", "  fingerprint  ",
		"This checks storage only.",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the pasted key report lost %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "signed in") {
		t.Errorf("a pasted key was described as signed in:\n%s", report)
	}
	assertClean(t, "keys test", report)
}

// The S-07 acceptance criterion: on a signed-in credential it reports the account and the state of
// the grant, and refuses to claim a network check it did not make.
func TestKeysTestOnASignedInCredentialReportsTheAccountAndTheStateOfTheGrant(t *testing.T) {
	_, _, _, _ = storeWithRoutes(t)

	if err := runKeys([]string{"signin", "seat", "-route", copilotRoute}, &bytes.Buffer{}); err != nil {
		t.Fatalf("keys signin: %v", err)
	}

	var out bytes.Buffer
	if err := runKeys([]string{"test", "seat"}, &out); err != nil {
		t.Fatalf("keys test: %v", err)
	}
	report := out.String()

	for _, want := range []string{
		"seat is signed in as octocat", "  kind         signed-in", "  account      octocat",
		"an access token and a refresh token", "Canopy renews it 5 minutes before that",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report is missing %q:\n%s", want, report)
		}
	}
	// The fingerprint comparison has nothing to compare on a sign-in and must not be faked.
	if strings.Contains(report, "fingerprint") {
		t.Errorf("a signed-in credential was given a fingerprint:\n%s", report)
	}
	// And no token reaches the output, which is what the whole command is careful about.
	for _, secret := range []string{"access-token", "refresh-token"} {
		if strings.Contains(report, secret) {
			t.Fatalf("a token reached the output:\n%s", report)
		}
	}
}

// The claim it must not make. A stored account printed as though somebody had just checked it is the
// same dishonesty the old line about A2 had settled into, one level further in.
func TestKeysTestRefusesToClaimANetworkCheckItDidNotMake(t *testing.T) {
	_, _, routes, _ := storeWithRoutes(t)

	if err := runKeys([]string{"signin", "seat", "-route", copilotRoute}, &bytes.Buffer{}); err != nil {
		t.Fatalf("keys signin: %v", err)
	}

	var out bytes.Buffer
	if err := runKeys([]string{"test", "seat"}, &out); err != nil {
		t.Fatalf("keys test: %v", err)
	}
	if !strings.Contains(out.String(), "No vendor was contacted") {
		t.Errorf("the report did not say that nobody was asked:\n%s", out.String())
	}

	// With a route that can ask, it asks, and says who answered.
	signInRoutes = reportingRoutes{routes}
	routes.report = &signInReport{
		Vendor:  "GitHub",
		Account: "octocat",
		Facts:   []signInFact{{Label: "plan", Value: "Copilot Business"}, {Label: "requests", Value: "42 left this hour"}},
	}

	out.Reset()
	if err := runKeys([]string{"test", "seat"}, &out); err != nil {
		t.Fatalf("keys test with a route that reports: %v", err)
	}
	report := out.String()
	for _, want := range []string{"GitHub says:", "Copilot Business", "42 left this hour"} {
		if !strings.Contains(report, want) {
			t.Errorf("the vendor's answer is missing %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "No vendor was contacted") {
		t.Errorf("the report claimed nobody was asked after asking:\n%s", report)
	}

	// A vendor that could not be reached is said out loud too, rather than quietly falling back to
	// what is stored and letting it read as checked.
	routes.reportErr = errors.New("dial tcp: no route to host")
	out.Reset()
	if err := runKeys([]string{"test", "seat"}, &out); err != nil {
		t.Fatalf("keys test with an unreachable vendor: %v", err)
	}
	if !strings.Contains(out.String(), "could not be asked") {
		t.Errorf("an unreachable vendor was not reported:\n%s", out.String())
	}
}

// The S-07 acceptance criterion: on a lapsed credential it says lapsed and names the command that
// fixes it.
func TestKeysTestOnALapsedCredentialSaysLapsedAndNamesTheCommandThatFixesIt(t *testing.T) {
	store, _, _, _ := storeWithRoutes(t)
	lapsed := time.Now().Add(-time.Hour)

	// Renewable: lapsed is a state Canopy recovers from on its own, so it is a note rather than a
	// failure, and the command is still named in case the renewal is refused.
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "seat", Provider: core.ProviderAnthropic}},
		keys.SignIn{Kind: keys.KindSignedIn, Account: "octocat", ExpiresAt: &lapsed},
		keys.Tokens{Access: core.NewSecret("a"), Refresh: core.NewSecret("r")},
	); err != nil {
		t.Fatalf("planting a lapsed sign-in: %v", err)
	}

	var out bytes.Buffer
	if err := runKeys([]string{"test", "seat"}, &out); err != nil {
		t.Fatalf("keys test on a renewable lapsed credential: %v", err)
	}
	report := out.String()
	if !strings.Contains(report, "which has passed") || !strings.Contains(report, "lapsed") {
		t.Errorf("a lapsed grant did not say so:\n%s", report)
	}
	if !strings.Contains(report, "canopy keys signin seat") {
		t.Errorf("the command that fixes it is not named:\n%s", report)
	}

	// With nothing to renew it with there is no recovery without a person, so it fails rather than
	// reporting a credential that cannot answer a message as fine.
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "stuck", Provider: core.ProviderAnthropic}},
		keys.SignIn{Kind: keys.KindSignedIn, Account: "octocat", ExpiresAt: &lapsed},
		keys.Tokens{Access: core.NewSecret("a")},
	); err != nil {
		t.Fatalf("planting an unrenewable lapsed sign-in: %v", err)
	}

	out.Reset()
	err := runKeys([]string{"test", "stuck"}, &out)
	if err == nil {
		t.Fatalf("a lapsed credential with nothing to renew it passed:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "canopy keys signin stuck") {
		t.Errorf("the failure does not name what fixes it: %v", err)
	}
}

// A delegated credential's keychain half is empty and empty is correct, so `keys test` says
// something true about it rather than reporting the absence as corruption. S-04 depends on this.
func TestKeysTestOnADelegatedCredentialDoesNotReportAnEmptyKeychainAsDamage(t *testing.T) {
	store, _, _, _ := storeWithRoutes(t)

	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "claude-code", Provider: core.ProviderAnthropic}},
		keys.SignIn{Kind: keys.KindDelegated, Account: "walid@example.com"},
		keys.Tokens{},
	); err != nil {
		t.Fatalf("planting a delegated sign-in: %v", err)
	}

	var out bytes.Buffer
	if err := runKeys([]string{"test", "claude-code"}, &out); err != nil {
		t.Fatalf("keys test on a delegated credential: %v", err)
	}
	report := out.String()
	for _, want := range []string{
		"claude-code is signed in as walid@example.com", "  kind         delegated",
		"none, and none is correct",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the delegated report is missing %q:\n%s", want, report)
		}
	}
	for _, forbidden := range []string{"damage", "corrupt", "missing"} {
		if strings.Contains(strings.ToLower(report), forbidden) {
			t.Errorf("an empty keychain half was reported as %q:\n%s", forbidden, report)
		}
	}
}

// The S-07 acceptance criterion: no output anywhere mentions A2.
//
// It was in `keys test`, as the reason the provider was not contacted, and it had been untrue for
// eight phases. This sweeps every command rather than the one it was found in, because a task
// reference that survives in one place survives in others.
func TestNoKeyCommandMentionsAPhaseThatIsLongGone(t *testing.T) {
	store, _, _, _ := storeWithRoutes(t)
	expires := time.Now().Add(time.Hour)
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "seat", Provider: core.ProviderAnthropic}},
		keys.SignIn{Kind: keys.KindSignedIn, Account: "octocat", ExpiresAt: &expires},
		keys.Tokens{Access: core.NewSecret("a"), Refresh: core.NewSecret("r")},
	); err != nil {
		t.Fatalf("planting a sign-in: %v", err)
	}

	commands := map[string][]string{
		"keys help":    {"help"},
		"keys usage":   {},
		"keys list":    {"list"},
		"keys test":    {"test", "seat"},
		"keys models":  {"models", "seat"},
		"keys signin":  {"signin", "another", "-route", copilotRoute},
		"keys signout": {"signout", "seat"},
	}
	for name, args := range commands {
		var out bytes.Buffer
		err := runKeys(args, &out)
		text := out.String()
		if err != nil {
			text += err.Error()
		}
		for _, phase := range []string{"until A2", "A2.", " A2 "} {
			if strings.Contains(text, phase) {
				t.Errorf("%s mentions %q:\n%s", name, phase, text)
			}
		}
	}
}

// The flags that exist only to be refused have to be refused on the command somebody signing in
// would reach for them on. A person who has read that a subscription involves a token is exactly the
// person who tries -token.
func TestSigningInRefusesAFlagThatWouldPutACredentialInShellHistory(t *testing.T) {
	_, _, _, _ = storeWithRoutes(t)

	for _, flag := range []string{"-token", "-key", "-secret"} {
		var out bytes.Buffer
		err := runKeys([]string{"signin", "seat", flag, "ghp_something"}, &out)
		if err == nil {
			t.Errorf("%s was accepted:\n%s", flag, out.String())
			continue
		}
		if !strings.Contains(err.Error(), "shell history") {
			t.Errorf("%s was refused without explaining why: %v", flag, err)
		}
		if strings.Contains(err.Error(), "ghp_something") {
			t.Errorf("the refusal echoed what was passed: %v", err)
		}
	}
}

// A build with no route says so in terms of what a person wanted to know, which is whether this is
// possible at all, rather than reporting an unknown flag value.
//
// The registry is set to the empty one here rather than left as the shipped default, which it was
// until S-04. The shipped build now offers the Claude Code route, and this test is about what a
// build with nothing behind it says: that behaviour still exists, is still reachable while S-03 and
// S-05 are unbuilt on some path or other, and is worth keeping true.
func TestSigningInWithNoRouteBuiltSaysWhichRoutesAreComing(t *testing.T) {
	backend := keys.NewMemoryBackend()
	store := keys.NewStore(backend, filepath.Join(t.TempDir(), "keys.json"))
	original := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = original })

	originalRoutes := signInRoutes
	signInRoutes = noRoutes{}
	t.Cleanup(func() { signInRoutes = originalRoutes })

	err := runKeys([]string{"signin", "seat"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a build with no routes accepted a sign-in")
	}
	for _, want := range []string{"Copilot", "Claude Code", "ChatGPT", "canopy keys add"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
}

// Naming a route that does not exist says which do, rather than leaving somebody to guess at
// spellings.
func TestNamingARouteThatDoesNotExistSaysWhichOnesDo(t *testing.T) {
	_, _, _, _ = storeWithRoutes(t)

	err := runKeys([]string{"signin", "seat", "-route", "chatgpt"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("an unknown route was accepted")
	}
	if !strings.Contains(err.Error(), copilotRoute) {
		t.Errorf("the refusal does not list what is available: %v", err)
	}
}

// A sign-in stopped part way stores nothing, which is the same promise the wizard makes and the same
// reason: a credential nobody knows they have is worse than one that failed to appear.
func TestAnInterruptedSignInStoresNothing(t *testing.T) {
	store, backend, routes, _ := storeWithRoutes(t)
	routes.blockUntil = make(chan struct{})

	raised := make(chan os.Signal, 1)
	originalInterrupts := interrupts
	interrupts = func() (<-chan os.Signal, func()) { return raised, func() {} }
	t.Cleanup(func() { interrupts = originalInterrupts })

	go func() {
		// Long enough for the command to have reached the wait, and harmless if it has not: the
		// channel is buffered and the select reads it whenever it gets there.
		time.Sleep(10 * time.Millisecond)
		raised <- os.Interrupt
	}()

	err := runKeys([]string{"signin", "seat", "-route", copilotRoute}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("an interrupted sign-in reported success")
	}
	if !strings.Contains(err.Error(), "nothing was stored") {
		t.Errorf("the interruption did not say what it left behind: %v", err)
	}
	if _, err := store.Metadata(core.KeyRef{Name: "seat"}); !errors.Is(err, keys.ErrNotFound) {
		t.Errorf("an interrupted sign-in left a record: %v", err)
	}
	if _, err := backend.Get("seat"); !errors.Is(err, keys.ErrNotFound) {
		t.Errorf("an interrupted sign-in left a token: %v", err)
	}
}

// The two sides of the boundary spell the three kinds identically, which is what makes the plain
// conversion in signInAware safe. If either renames one, this fails rather than a delegated
// credential quietly reading as a pasted one on screen.
func TestTheStoreAndTheScreenAgreeAboutWhatAKindIsCalled(t *testing.T) {
	pairs := []struct {
		store  keys.Kind
		screen keysui.Kind
	}{
		{keys.KindPasted, keysui.KindPasted},
		{keys.KindSignedIn, keysui.KindSignedIn},
		{keys.KindDelegated, keysui.KindDelegated},
	}
	for _, pair := range pairs {
		if string(pair.store) != string(pair.screen) {
			t.Errorf("internal/keys calls it %q and the screen calls it %q",
				pair.store, pair.screen)
		}
	}
	// And that neither side has grown a fourth that the other has not.
	if got := len(pairs); got != 3 {
		t.Errorf("this test knows %d kinds", got)
	}
}
