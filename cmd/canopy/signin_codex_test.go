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

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/codex"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// The ChatGPT route, driven end to end on a machine that has no Codex.
//
// Every fixture below is an invented vendor. That is not a compromise: the acceptance is about what
// somebody is told before they sign in and what is stored after they do, and a test that needed a
// real ChatGPT plan could only ever check half of it, on one laptop, at somebody's expense. The
// protocol half is held against a real JSON-RPC peer in internal/provider/codex, and against a real
// Codex in that package's live tests.

// fakeCodex answers as a Codex that is signed in, or refuses the way one that is not would.
type fakeCodex struct {
	account codex.Account
	limits  codex.Limits

	prompt    codex.Prompt
	beginErr  error
	waitErr   error
	waitDelay time.Duration

	mu        sync.Mutex
	cancelled bool
	started   []codex.LoginMode
}

func (f *fakeCodex) Begin(ctx context.Context, mode codex.LoginMode) (codexLogin, error) {
	f.mu.Lock()
	f.started = append(f.started, mode)
	f.mu.Unlock()

	if f.beginErr != nil {
		return nil, f.beginErr
	}
	return &fakeLogin{vendor: f, stopped: make(chan struct{})}, nil
}

func (f *fakeCodex) Limits(ctx context.Context) (codex.Account, codex.Limits, error) {
	if f.beginErr != nil {
		return codex.Account{}, codex.Limits{}, f.beginErr
	}
	return f.account, f.limits, nil
}

func (f *fakeCodex) wasCancelled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled
}

// fakeLogin honours the one part of the real contract that a lazier fake would get wrong: Cancel
// unblocks Wait. The real Login does that by stopping the app server, which breaks the read the wait
// is sitting on, and a fake that only set a flag would let this test pass against an implementation
// where somebody pressing escape leaves the wizard stuck on the sign-in step forever.
type fakeLogin struct {
	vendor  *fakeCodex
	stopped chan struct{}
	once    sync.Once
}

func (l *fakeLogin) Prompt() codex.Prompt { return l.vendor.prompt }

func (l *fakeLogin) Wait(ctx context.Context) (codex.Account, error) {
	if l.vendor.waitDelay > 0 {
		select {
		case <-time.After(l.vendor.waitDelay):
		case <-l.stopped:
			return codex.Account{}, codex.ErrSignInStopped
		case <-ctx.Done():
			return codex.Account{}, ctx.Err()
		}
	}
	if l.vendor.waitErr != nil {
		return codex.Account{}, l.vendor.waitErr
	}
	return l.vendor.account, nil
}

func (l *fakeLogin) Cancel() {
	l.vendor.mu.Lock()
	l.vendor.cancelled = true
	l.vendor.mu.Unlock()
	l.once.Do(func() { close(l.stopped) })
}

func signedInCodex() *fakeCodex {
	reset := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	return &fakeCodex{
		account: codex.Account{Kind: "chatgpt", Email: "someone@example.com", Plan: "pro"},
		limits: codex.Limits{
			Plan: "pro",
			Primary: &codex.Window{
				UsedPercent: 42, Duration: 5 * time.Hour, ResetsAt: reset,
			},
		},
		prompt: codex.Prompt{
			URL:  "https://auth.openai.com/codex/device",
			Code: "ABCD-1234",
		},
	}
}

// codexRouteOn wires the ChatGPT route to a temporary store and an invented vendor.
func codexRouteOn(t *testing.T, vendor *fakeCodex) (*keys.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "keys.json")
	store := keys.NewStore(keys.NewMemoryBackend(), path)

	originalStore := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = originalStore })

	originalRoutes := signInRoutes
	signInRoutes = codexSignIn{store: store, vendor: vendor}
	t.Cleanup(func() { signInRoutes = originalRoutes })

	return store, path
}

// The acceptance clause about signing in, from the surface a person actually uses.
func TestSigningInThroughChatGPTStoresADelegatedCredentialWithNoTokenBehindIt(t *testing.T) {
	vendor := signedInCodex()
	store, path := codexRouteOn(t, vendor)

	var out bytes.Buffer
	if err := runKeys([]string{"signin", "chatgpt", "-route", codexDeviceRouteID}, &out); err != nil {
		t.Fatalf("signing in through Codex: %v", err)
	}

	if !strings.Contains(out.String(), "someone@example.com") {
		t.Errorf("the account was not reported:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "ABCD-1234") {
		t.Errorf("the code was never shown, so nobody could have completed it:\n%s", out.String())
	}

	in, err := store.SignIn(core.KeyRef{Name: "chatgpt"})
	if err != nil {
		t.Fatalf("reading the stored credential: %v", err)
	}
	if in.Kind != keys.KindDelegated {
		t.Errorf("the credential stored as %q, want %q: the app server holds the grant and Canopy "+
			"never sees it", in.Kind, keys.KindDelegated)
	}
	if in.Route != codex.Route {
		t.Errorf("the credential recorded route %q, want %q, which is what both places that build "+
			"a client key on", in.Route, codex.Route)
	}
	if in.ExpiresAt != nil {
		t.Error("an expiry was recorded for a grant Canopy does not hold and cannot renew")
	}

	// Asserted against the file's bytes rather than the struct, which is S-01's rule and matters
	// most on the route that involves a real OAuth flow.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading keys.json: %v", err)
	}
	for _, forbidden := range []string{"token", "Bearer", "eyJ"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("keys.json contains %q, and this route exists on the grounds that Canopy holds "+
				"no ChatGPT credential:\n%s", forbidden, body)
		}
	}
}

// The acceptance clause about the 429 caveat.
func TestTheQuotaCaveatIsShownBeforeAnythingIsStored(t *testing.T) {
	vendor := signedInCodex()
	// The sign-in never finishes, so anything printed was printed before a credential existed.
	vendor.waitErr = errors.New("nobody completed it")
	store, _ := codexRouteOn(t, vendor)

	var out bytes.Buffer
	err := runKeys([]string{"signin", "chatgpt", "-route", codexRouteID}, &out)
	if err == nil {
		t.Fatal("a sign-in nobody completed was reported as a success")
	}

	said := out.String()
	if !strings.Contains(said, "429") {
		t.Errorf("the 429 report was not mentioned before the sign-in. It is an open, unanswered "+
			"report that this route may draw on a smaller allowance than the ChatGPT app, and the "+
			"honest place for a maybe is in front of the decision it would change:\n%s", said)
	}
	if !strings.Contains(said, "permission gate is not in that path") {
		t.Errorf("the delegated-turn caveat was not shown before signing in:\n%s", said)
	}

	if _, err := store.Metadata(core.KeyRef{Name: "chatgpt"}); err == nil {
		t.Error("a credential was stored by a sign-in that failed")
	}
}

// The acceptance clause about rate limits reaching a surface a user can see, and S-07's
// reportsOnCredentials finally having something to report.
func TestKeysTestOnAChatGPTCredentialAsksOpenAIRatherThanReadingBackTheRecord(t *testing.T) {
	vendor := signedInCodex()
	store, _ := codexRouteOn(t, vendor)

	var signin bytes.Buffer
	if err := runKeys([]string{"signin", "chatgpt", "-route", codexRouteID}, &signin); err != nil {
		t.Fatalf("signing in: %v", err)
	}
	_ = store

	var out bytes.Buffer
	if err := runKeys([]string{"test", "chatgpt"}, &out); err != nil {
		t.Fatalf("testing the credential: %v", err)
	}

	said := out.String()
	if strings.Contains(said, "No vendor was contacted") {
		t.Errorf("`keys test` still says no vendor was contacted. account/rateLimits/read is exactly "+
			"the answer S-07 built reportsOnCredentials for, and it is a fact about the "+
			"subscription rather than a fact about the file:\n%s", said)
	}
	for _, want := range []string{"someone@example.com", "pro", "42% used"} {
		if !strings.Contains(said, want) {
			t.Errorf("`keys test` does not report %q:\n%s", want, said)
		}
	}
	if !strings.Contains(said, "none is correct") {
		t.Errorf("the empty keychain half was not described as correct, so it reads as damage:\n%s",
			said)
	}
}

// A vendor that cannot be reached is said to be unreachable, not answered for.
func TestKeysTestRefusesToInventAnAnswerWhenOpenAICannotBeAsked(t *testing.T) {
	vendor := signedInCodex()
	store, _ := codexRouteOn(t, vendor)

	if err := runKeys([]string{"signin", "chatgpt", "-route", codexRouteID}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in: %v", err)
	}
	_ = store

	vendor.beginErr = errors.New("the codex cli is not installed")

	var out bytes.Buffer
	if err := runKeys([]string{"test", "chatgpt"}, &out); err != nil {
		t.Fatalf("testing the credential: %v", err)
	}
	said := out.String()
	if !strings.Contains(said, "could not be asked") {
		t.Errorf("`keys test` did not say the vendor was unreachable, so a stored account reads as "+
			"a checked one:\n%s", said)
	}
	if !strings.Contains(said, "not installed") {
		t.Errorf("`keys test` did not say what was wrong:\n%s", said)
	}
}

// The Cancel contract, on the first route in this build where somebody genuinely has time to change
// their mind.
func TestCancellingAChatGPTSignInStopsThePollingAndStoresNothing(t *testing.T) {
	vendor := signedInCodex()
	vendor.waitDelay = time.Hour
	store, _ := codexRouteOn(t, vendor)

	attempt, err := signInRoutes.Begin(keysRoute(t, codexDeviceRouteID), "chatgpt")
	if err != nil {
		t.Fatalf("starting the sign-in: %v", err)
	}

	waited := make(chan error, 1)
	go func() {
		_, err := attempt.Wait()
		waited <- err
	}()
	time.Sleep(20 * time.Millisecond)
	attempt.Cancel()

	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait never returned after Cancel")
	}

	if !vendor.wasCancelled() {
		t.Error("OpenAI was never told to stop, so an abandoned device code goes on being polled")
	}
	if _, err := store.Metadata(core.KeyRef{Name: "chatgpt"}); err == nil {
		t.Error("a cancelled sign-in left a credential behind")
	}
}

// The other half of the race, which S-06 wrote the Cancel contract for.
func TestCancellingAChatGPTSignInThatAlreadyCompletedRemovesTheCredential(t *testing.T) {
	vendor := signedInCodex()
	store, _ := codexRouteOn(t, vendor)

	attempt, err := signInRoutes.Begin(keysRoute(t, codexDeviceRouteID), "chatgpt")
	if err != nil {
		t.Fatalf("starting the sign-in: %v", err)
	}
	if _, err := attempt.Wait(); err != nil {
		t.Fatalf("the sign-in failed: %v", err)
	}
	if _, err := store.Metadata(core.KeyRef{Name: "chatgpt"}); err != nil {
		t.Fatalf("the sign-in stored nothing: %v", err)
	}

	attempt.Cancel()

	if _, err := store.Metadata(core.KeyRef{Name: "chatgpt"}); err == nil {
		t.Error("Cancel left the credential in place. OpenAI can confirm in the moment between " +
			"somebody pressing escape and this call arriving, and a credential nobody knows they " +
			"have is worse than one that failed to appear")
	}
}

// keysRoute finds a route by id from whatever the build offers.
func keysRoute(t *testing.T, id string) (route keysui.Route) {
	t.Helper()
	for _, offered := range signInRoutes.Routes() {
		if offered.ID == id {
			return offered
		}
	}
	t.Fatalf("this build offers no route called %q", id)
	return route
}

// The route reaches both surfaces, which is the whole reason S-06 and S-07 landed first.
func TestTheChatGPTRouteIsOfferedByTheBuildAndNamesWhatItNeeds(t *testing.T) {
	var found []string
	for _, route := range (routeSet{copilotSignIn{}, claudeCode{}, codexSignIn{}}).Routes() {
		found = append(found, route.ID)
	}

	for _, want := range []string{codexRouteID, codexDeviceRouteID} {
		var have bool
		for _, id := range found {
			have = have || id == want
		}
		if !have {
			t.Errorf("the build offers %v, without %q, so the wizard cannot reach it", found, want)
		}
	}

	for _, route := range (codexSignIn{}).Routes() {
		if route.Detail == "" {
			t.Errorf("route %q says nothing about what has to already be true, which is what "+
				"somebody choosing between routes is choosing on", route.ID)
		}
		if !route.VendorPicksModel {
			t.Errorf("route %q offers a model picker, and a delegated credential stores no model, "+
				"so the picker would be empty", route.ID)
		}
	}
}

// A route id nobody offers is refused with the list of the ones that exist.
func TestNamingAChatGPTRouteThatDoesNotExistSaysWhichOnesDo(t *testing.T) {
	codexRouteOn(t, signedInCodex())

	var out bytes.Buffer
	err := runKeys([]string{"signin", "chatgpt", "-route", "chatgpt-telepathy"}, &out)
	if err == nil {
		t.Fatal("a route nobody offers was accepted")
	}
	if !strings.Contains(err.Error(), codexRouteID) {
		t.Errorf("the refusal was %q, want it to list the routes that do exist", err)
	}
}

// Canopy renews nothing on this route, because it holds nothing to renew.
func TestNothingOnTheChatGPTRouteIsRenewedByCanopyBecauseCanopyHoldsNoToken(t *testing.T) {
	vendor := signedInCodex()
	store, _ := codexRouteOn(t, vendor)

	if err := runKeys([]string{"signin", "chatgpt", "-route", codexRouteID}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in: %v", err)
	}

	meta, err := store.Metadata(core.KeyRef{Name: "chatgpt"})
	if err != nil {
		t.Fatalf("reading the credential: %v", err)
	}

	// The refresher is the one place S-02 put "how old is too old", and a delegated credential is
	// refused by it in internal/keys' own words rather than falling through to a renewal that has
	// no token to send. The app server renews its own grant; that is the point of the route.
	refresher := keys.NewRefresher(store)
	refresher.Renews(signInSources())
	if _, err := refresher.Credential(meta); err == nil {
		t.Error("the refresher handed out a credential for a route Canopy holds no token on. " +
			"Whatever it returned would be empty, and it would be sent as an Authorization header")
	}

	// And nothing in the build claims to know how to renew one.
	in, err := store.SignIn(meta.Ref)
	if err != nil {
		t.Fatalf("reading the sign-in: %v", err)
	}
	if _, ok := signInSources()(meta, in); ok {
		t.Error("a token source was registered for the ChatGPT route. There is no token here to " +
			"renew: the grant lives in ~/.codex and the app server renews it without being asked")
	}
}

// Both bugs below were found by running the built command against a real Codex rather than by
// reading the code, which is the argument for doing that before ticking a task.

// The route ids and the route recorded on a credential have to be the same string, because
// routeSet dispatches `canopy keys test` by matching one against the other.
func TestTheChatGPTRouteIdIsTheOneRecordedOnItsCredentials(t *testing.T) {
	vendor := signedInCodex()
	store, _ := codexRouteOn(t, vendor)

	if err := runKeys([]string{"signin", "chatgpt", "-route", codexDeviceRouteID}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in: %v", err)
	}
	in, err := store.SignIn(core.KeyRef{Name: "chatgpt"})
	if err != nil {
		t.Fatalf("reading the credential: %v", err)
	}

	// The whole registry, not this member alone, because the dispatch that matters is routeSet's.
	if !offers(codexSignIn{}, in.Route) {
		t.Fatalf("a credential signed in through the device route recorded route %q, and no route "+
			"id in this registry matches it. routeSet.Report matches the recorded route against the "+
			"ids its members offer, so `canopy keys test` on this credential reports that no route "+
			"in this build can say anything about it", in.Route)
	}
}

// The stored account is an identity, so it must not move when the plan does.
func TestAnAccountWhosePlanChangedIsNotReportedAsADifferentAccount(t *testing.T) {
	vendor := signedInCodex()
	store, _ := codexRouteOn(t, vendor)

	if err := runKeys([]string{"signin", "chatgpt", "-route", codexRouteID}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in: %v", err)
	}

	in, err := store.SignIn(core.KeyRef{Name: "chatgpt"})
	if err != nil {
		t.Fatalf("reading the credential: %v", err)
	}
	if in.Account != "someone@example.com" {
		t.Errorf("the credential recorded the account as %q. Folding the plan into it makes an "+
			"identity that moves like a clock, which is the objection S-01 raised against recording "+
			"a fingerprint", in.Account)
	}

	// Same person, upgraded plan. Nothing about who the turns run as has changed.
	vendor.account.Plan = "pro"
	vendor.limits.Plan = "pro"

	var out bytes.Buffer
	if err := runKeys([]string{"test", "chatgpt"}, &out); err != nil {
		t.Fatalf("testing the credential: %v", err)
	}
	if strings.Contains(out.String(), "Turns run as") {
		t.Errorf("an upgraded plan was reported as a different account, which would fire on every "+
			"credential on the day somebody changed plan:\n%s", out.String())
	}

	// A genuinely different account still says so, or the check above would be satisfied by
	// deleting the note.
	vendor.account.Email = "somebody-else@example.com"
	out.Reset()
	if err := runKeys([]string{"test", "chatgpt"}, &out); err != nil {
		t.Fatalf("testing the credential: %v", err)
	}
	if !strings.Contains(out.String(), "somebody-else@example.com") {
		t.Errorf("Codex being signed in as somebody else was not reported, and turns on this "+
			"credential run as them:\n%s", out.String())
	}
}
