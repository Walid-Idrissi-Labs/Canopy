package copilot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
)

// fakeGitHub is github.com, as much of it as this route touches.
type fakeGitHub struct {
	mu sync.Mutex

	// pendingPolls is how many times the token endpoint answers "not yet" before it answers.
	pendingPolls int
	// tokenBody replaces the successful answer, for the refusals.
	tokenBody map[string]string
	// deviceBody replaces the device code answer.
	deviceBody map[string]string
	// login is who /user says the token belongs to. Empty makes /user fail.
	login string

	deviceRequests []url.Values
	tokenRequests  []url.Values
	polls          int
}

func (g *fakeGitHub) serve(t *testing.T) (Vendor, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		g.mu.Lock()
		g.deviceRequests = append(g.deviceRequests, r.PostForm)
		body := g.deviceBody
		g.mu.Unlock()

		if body == nil {
			body = map[string]string{
				"device_code":      "DEVICE-CODE",
				"user_code":        "WXYZ-1234",
				"verification_uri": "https://github.com/login/device",
				"expires_in":       "900",
				"interval":         "1",
			}
		}
		writeJSON(w, body)
	})

	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		g.mu.Lock()
		g.tokenRequests = append(g.tokenRequests, r.PostForm)
		g.polls++
		pending := g.polls <= g.pendingPolls
		body := g.tokenBody
		g.mu.Unlock()

		if pending {
			writeJSON(w, map[string]string{"error": "authorization_pending"})
			return
		}
		if body == nil {
			body = map[string]string{"access_token": "gho_THE-TOKEN", "token_type": "bearer"}
		}
		writeJSON(w, body)
	})

	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		login := g.login
		g.mu.Unlock()
		if login == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(w, map[string]string{"login": login})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return Vendor{
		HTTP:     server.Client(),
		ClientID: "Iv23liCANOPYSOWN",
		Endpoints: Endpoints{
			DeviceCode: server.URL + "/login/device/code",
			Token:      server.URL + "/login/oauth/access_token",
			API:        server.URL,
		},
	}, server
}

func writeJSON(w http.ResponseWriter, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	parts := make([]string, 0, len(body))
	for key, value := range body {
		parts = append(parts, `"`+key+`":`+quoteOrNumber(value))
	}
	_, _ = w.Write([]byte("{" + strings.Join(parts, ",") + "}"))
}

// quoteOrNumber lets a fixture write expires_in as a number and everything else as a string, which
// is the shape GitHub actually sends.
func quoteOrNumber(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
		return value
	}
	return `"` + value + `"`
}

// hurry makes an attempt poll without waiting, and records the intervals it was asked to wait for.
//
// The interval is what the assertion is about, so holding it directly beats inferring it from how
// long a test took, which is the kind of assertion that passes on one machine and fails on another.
func hurry(attempt *Attempt) func() []time.Duration {
	var mu sync.Mutex
	waited := []time.Duration{}
	attempt.tick = func(d time.Duration) <-chan time.Time {
		mu.Lock()
		waited = append(waited, d)
		mu.Unlock()
		ready := make(chan time.Time, 1)
		ready <- time.Now()
		return ready
	}
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), waited...)
	}
}

func (g *fakeGitHub) asked() []url.Values {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]url.Values(nil), g.deviceRequests...)
}

// The first acceptance clause. On a machine with no GitHub credentials, signing in produces a code
// and a page, waits, and completes when the person authorises.
func TestSigningInProducesACodeAndAPageAndCompletesWhenThePersonAuthorises(t *testing.T) {
	github := &fakeGitHub{pendingPolls: 2, login: "walid"}
	vendor, _ := github.serve(t)

	attempt, err := vendor.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	prompt := attempt.Prompt()
	if prompt.UserCode != "WXYZ-1234" {
		t.Errorf("the code to type is %q", prompt.UserCode)
	}
	if prompt.VerificationURI != "https://github.com/login/device" {
		t.Errorf("the page to open is %q", prompt.VerificationURI)
	}
	if prompt.ExpiresAt.IsZero() {
		t.Error("the code has no stated expiry, so nothing can say when it stops working")
	}
	if prompt.Interval < pollFloor {
		t.Errorf("the poll interval is %s, and anything under %s is a tight loop against somebody's "+
			"account", prompt.Interval, pollFloor)
	}

	waited := hurry(attempt)
	grant, err := attempt.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if len(waited()) != 3 {
		t.Errorf("the flow polled %d times, want the two pending answers and the one that arrived",
			len(waited()))
	}
	for _, gap := range waited()[1:] {
		if gap < pollFloor {
			t.Errorf("a poll waited %s, and anything under %s is a tight loop against somebody's "+
				"account", gap, pollFloor)
		}
	}
	if grant.Account != "walid" {
		t.Errorf("the grant belongs to %q, want the GitHub login", grant.Account)
	}
	if grant.Tokens.Access.Reveal() != "gho_THE-TOKEN" {
		t.Error("the access token did not come back")
	}
	if grant.ExpiresAt != nil {
		t.Errorf("an expiry was invented for a token GitHub said nothing about: %v", grant.ExpiresAt)
	}

	asked := github.asked()
	if len(asked) != 1 {
		t.Fatalf("the device endpoint was asked %d times", len(asked))
	}
	if asked[0].Get("client_id") != "Iv23liCANOPYSOWN" {
		t.Errorf("the sign-in was started as client %q, and Canopy must use its own",
			asked[0].Get("client_id"))
	}
	scope := asked[0].Get("scope")
	for _, want := range Scopes {
		if !strings.Contains(scope, want) {
			t.Errorf("the scope %q left out %q", scope, want)
		}
	}
}

// Constraint 5, held over this route's own source rather than promised in a comment. A device flow
// exists precisely so that nothing has to listen, and a later edit that added a loopback callback
// would be the easiest thing in the world to do without noticing.
func TestNothingInThisRouteListensOnAPort(t *testing.T) {
	forbidden := []string{
		"net.Listen",
		"ListenAndServe",
		"http.Server{",
		"httptest.NewServer",
	}
	for _, file := range goFilesIn(t, ".") {
		if strings.HasSuffix(file.name, "_test.go") {
			continue
		}
		for _, banned := range forbidden {
			if strings.Contains(file.body, banned) {
				t.Errorf("%s contains %q, and this route exists because nothing has to listen",
					file.name, banned)
			}
		}
	}
}

// Canopy uses its own vendor identity or none. Reusing another editor's client id would make Canopy
// sign users in as a program they did not choose, and it is the one behaviour D-51 says turns a
// defensible route into an indefensible one.
func TestThisRouteUsesNobodyElsesClientIdAndSendsNobodyElsesVersion(t *testing.T) {
	// Assembled rather than written out, so that this test is not the thing that puts another
	// editor's client id into the repository it is meant to keep it out of.
	banned := []string{
		"Iv1." + "b507a08c87ecfe98", // the id every other Copilot client borrows
		"Editor-" + "Version",
		"Copilot-" + "Integration-Id",
		"Editor-" + "Plugin-Version",
	}
	for _, file := range goFilesIn(t, ".") {
		for _, phrase := range banned {
			if strings.Contains(file.body, phrase) {
				t.Errorf("%s mentions %q, which is impersonating another client", file.name, phrase)
			}
		}
	}
}

// A build with no registration behind it says what has to be registered rather than failing at the
// vendor with something about a missing client id.
func TestABuildWithNoGitHubAppSaysWhatHasToBeRegistered(t *testing.T) {
	t.Setenv(ClientIDEnvVar, "")

	_, err := Vendor{Endpoints: Endpoints{DeviceCode: "x", Token: "y", API: "z"}}.Begin(context.Background())
	if !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("a build with no client id failed with %v", err)
	}
	if !strings.Contains(err.Error(), ClientIDEnvVar) || !strings.Contains(err.Error(), "INSTALL.md") {
		t.Errorf("the failure does not say what to do: %v", err)
	}
}

// GitHub says how much slower to poll and ignoring it gets the device code rejected outright a few
// polls later, which would read as the person having done something wrong.
func TestGitHubAskingForSlowerPollingIsObeyedRatherThanIgnored(t *testing.T) {
	github := &fakeGitHub{
		login:     "walid",
		tokenBody: map[string]string{"error": "slow_down", "interval": "17"},
	}
	vendor, _ := github.serve(t)

	attempt, err := vendor.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	waited := hurry(attempt)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Three polls is enough to see the new interval taken up and kept.
		for {
			if len(waited()) > 3 {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	if _, err := attempt.Wait(ctx); err == nil {
		t.Fatal("a vendor that only ever says slow_down produced a grant")
	}

	for i, gap := range waited()[1:] {
		if gap != 17*time.Second {
			t.Errorf("poll %d waited %s after GitHub asked for 17s, and ignoring that is how a "+
				"device code gets rejected outright a few polls later", i+2, gap)
		}
	}
}

// The three ways a device flow ends badly, each said in its own words. "Expired" and "refused" have
// different remedies and merging them into one failure wastes a specific amount of somebody's time.
func TestEachWayASignInEndsBadlyIsSaidInItsOwnWords(t *testing.T) {
	for _, tc := range []struct {
		vendorSays string
		wants      string
	}{
		{"expired_token", "expired before it was entered"},
		{"access_denied", "refused on GitHub"},
		{"incorrect_client_credentials", "GitHub refused the sign-in"},
	} {
		github := &fakeGitHub{login: "walid", tokenBody: map[string]string{"error": tc.vendorSays}}
		vendor, _ := github.serve(t)

		attempt, err := vendor.Begin(context.Background())
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		_, err = attempt.Wait(context.Background())
		if err == nil {
			t.Fatalf("%q produced a grant", tc.vendorSays)
		}
		if !strings.Contains(err.Error(), tc.wants) {
			t.Errorf("%q was reported as %v, want something containing %q", tc.vendorSays, err, tc.wants)
		}
	}
}

// S-01 requires the account a grant belongs to, because two seats on one machine are otherwise two
// rows nobody can tell apart. A token GitHub will not name an owner for cannot be stored as one.
func TestAGrantGitHubWillNotNameAnOwnerForIsRefusedRatherThanStoredAnonymously(t *testing.T) {
	github := &fakeGitHub{}
	vendor, _ := github.serve(t)

	attempt, err := vendor.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := attempt.Wait(context.Background()); err == nil {
		t.Fatal("a grant with no account behind it was accepted")
	} else if !strings.Contains(err.Error(), "whose account it is") {
		t.Errorf("the refusal reads as %v", err)
	}
}

// Cancelling stops the polling. A device code left polling is a request every few seconds for as
// long as the program runs, on behalf of somebody who pressed escape.
func TestCancellingAWaitStopsPollingOnBehalfOfSomebodyWhoLeft(t *testing.T) {
	github := &fakeGitHub{pendingPolls: 1000, login: "walid"}
	vendor, _ := github.serve(t)

	attempt, err := vendor.Begin(context.Background())
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := attempt.Wait(context.Background())
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	attempt.Cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled wait produced a grant")
		}
		if !strings.Contains(err.Error(), "nothing was stored") {
			t.Errorf("cancelling reported %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling did not stop the wait")
	}

	// Cancel is called from a keystroke and from a signal handler, and both can arrive.
	attempt.Cancel()
}

// Refresh follows S-02, driven through keys.Refresher rather than called directly, because the thing
// worth proving is that this route plugs into the seam S-02 built rather than that an HTTP call
// works.
func TestARenewalGoesThroughTheRefresherAndReplacesTheStoredToken(t *testing.T) {
	github := &fakeGitHub{
		login: "walid",
		tokenBody: map[string]string{
			"access_token":  "gho_THE-NEW-TOKEN",
			"refresh_token": "ghr_THE-NEW-REFRESH",
			"expires_in":    "28800",
		},
	}
	vendor, _ := github.serve(t)
	vendor.ClientSecret = "a-secret-the-maintainer-supplied"

	store, meta := storedGrantExpiring(t, time.Now().Add(time.Minute))
	refresher := keys.NewRefresher(store)
	refresher.Renews(vendor.Sources())

	secret, err := refresher.Credential(meta)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if secret.Reveal() != "gho_THE-NEW-TOKEN" {
		t.Errorf("the request would carry %q, want the renewed token", secret.Reveal())
	}

	tokens, err := store.Tokens(meta.Ref)
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if tokens.Refresh.Reveal() != "ghr_THE-NEW-REFRESH" {
		t.Errorf("the refresh token was not replaced: %q", tokens.Refresh.Reveal())
	}
}

// The registration Canopy recommends issues tokens that do not expire, so nothing is renewed and no
// client secret is needed. That is the ordinary case and it has to cost nothing.
func TestAGrantWithNoStatedExpiryIsNeverRenewed(t *testing.T) {
	github := &fakeGitHub{login: "walid"}
	vendor, _ := github.serve(t)

	store, meta := storedGrantExpiring(t, time.Time{})
	refresher := keys.NewRefresher(store)
	refresher.Renews(vendor.Sources())

	secret, err := refresher.Credential(meta)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if secret.Reveal() != "gho_THE-OLD-TOKEN" {
		t.Errorf("a token with no stated expiry was replaced with %q", secret.Reveal())
	}
	github.mu.Lock()
	polls := github.polls
	github.mu.Unlock()
	if polls != 0 {
		t.Errorf("GitHub was asked to renew %d times on a grant that never expires", polls)
	}
}

// GitHub renews only for an app that can prove who it is, which needs a secret a public program
// cannot ship. Reported as a lapsed sign-in rather than as something to retry, because a missing
// secret does not become present by waiting, and it names the thing that can actually be changed.
func TestRenewingWithoutAClientSecretSaysWhatToChangeRatherThanAskingForAnotherTry(t *testing.T) {
	github := &fakeGitHub{login: "walid"}
	vendor, _ := github.serve(t)
	t.Setenv(ClientSecretEnvVar, "")

	_, err := vendor.Refresh(context.Background(), keys.SignIn{}, keys.Tokens{
		Access:  core.NewSecret("gho_OLD"),
		Refresh: core.NewSecret("ghr_OLD"),
	})
	if !errors.Is(err, keys.ErrSignInLapsed) {
		t.Fatalf("a renewal with no secret reported %v, want a lapsed sign-in", err)
	}
	if !strings.Contains(err.Error(), ClientSecretEnvVar) ||
		!strings.Contains(err.Error(), "do not expire") {
		t.Errorf("the failure does not name either fix: %v", err)
	}
}

// A vendor that refuses the renewal has ended the grant and only signing in again helps. A vendor
// nobody could reach has said nothing about it, and telling that person to sign in again throws
// away a working refresh token to do it.
func TestARefusedRenewalIsLapsedAndAnUnreachableVendorIsNot(t *testing.T) {
	refused := &fakeGitHub{login: "walid", tokenBody: map[string]string{"error": "bad_refresh_token"}}
	vendor, _ := refused.serve(t)
	vendor.ClientSecret = "secret"

	_, err := vendor.Refresh(context.Background(), keys.SignIn{}, keys.Tokens{
		Access: core.NewSecret("a"), Refresh: core.NewSecret("r")})
	if !errors.Is(err, keys.ErrSignInLapsed) {
		t.Errorf("a refused renewal reported %v, want a lapsed sign-in", err)
	}

	unreachable := Vendor{
		ClientID:     "id",
		ClientSecret: "secret",
		Endpoints:    Endpoints{DeviceCode: "http://127.0.0.1:1/d", Token: "http://127.0.0.1:1/t", API: "http://127.0.0.1:1"},
	}
	_, err = unreachable.Refresh(context.Background(), keys.SignIn{}, keys.Tokens{
		Access: core.NewSecret("a"), Refresh: core.NewSecret("r")})
	if err == nil {
		t.Fatal("an unreachable vendor renewed a grant")
	}
	if errors.Is(err, keys.ErrSignInLapsed) {
		t.Errorf("a dropped connection was reported as a lapsed sign-in: %v", err)
	}
}

// Only credentials that came this way renew here. Copilot and a future Codex are both
// openai-compatible, so a source keyed on the provider would hand one of them the other's token
// endpoint.
func TestOnlyACredentialThatCameThisWayIsRenewedHere(t *testing.T) {
	sources := Vendor{}.Sources()
	if _, ok := sources(core.KeyMetadata{}, keys.SignIn{Route: Route}); !ok {
		t.Error("a Copilot credential found no way to renew")
	}
	for _, route := range []string{"", "claude-code", "codex"} {
		if _, ok := sources(core.KeyMetadata{}, keys.SignIn{Route: route}); ok {
			t.Errorf("a credential from route %q was claimed by the Copilot route", route)
		}
	}
}

// storedGrantExpiring puts one signed-in Copilot credential in a store.
func storedGrantExpiring(t *testing.T, at time.Time) (*keys.Store, core.KeyMetadata) {
	t.Helper()
	store := keys.NewStore(keys.NewMemoryBackend(), t.TempDir()+"/keys.json")

	in := keys.SignIn{Kind: keys.KindSignedIn, Account: "walid", Route: Route}
	if !at.IsZero() {
		in.ExpiresAt = &at
	}
	meta, err := store.PutSignIn(
		core.KeyMetadata{
			Ref:     core.KeyRef{Name: "mycopilot", Provider: core.ProviderOpenAICompatible},
			BaseURL: BaseURL,
		},
		in,
		keys.Tokens{
			Access:  core.NewSecret("gho_THE-OLD-TOKEN"),
			Refresh: core.NewSecret("ghr_THE-OLD-REFRESH"),
		},
	)
	if err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	return store, meta
}
