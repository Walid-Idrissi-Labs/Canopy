package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/copilot"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// gitHubThatSignsAnybodyIn is github.com, as much of it as the command line touches.
func gitHubThatSignsAnybodyIn(t *testing.T, login string) copilot.Vendor {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONTo(w, map[string]any{
			"device_code":      "DEVICE",
			"user_code":        "ABCD-1234",
			"verification_uri": "https://github.com/login/device",
			"expires_in":       900,
			"interval":         1,
		})
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONTo(w, map[string]any{"access_token": "gho_A-REAL-LOOKING-TOKEN"})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONTo(w, map[string]any{"login": login})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return copilot.Vendor{
		HTTP:     server.Client(),
		ClientID: "Iv23liCANOPYSOWN",
		Endpoints: copilot.Endpoints{
			DeviceCode: server.URL + "/login/device/code",
			Token:      server.URL + "/login/oauth/access_token",
			API:        server.URL,
		},
	}
}

func writeJSONTo(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// copilotRouteOn wires the Copilot route to a temporary store and a scripted GitHub.
func copilotRouteOn(t *testing.T, login string) (*keys.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "keys.json")
	store := keys.NewStore(keys.NewMemoryBackend(), path)

	originalStore := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = originalStore })

	originalRoutes := signInRoutes
	signInRoutes = routeSet{copilotSignIn{store: store, vendor: gitHubThatSignsAnybodyIn(t, login)}}
	t.Cleanup(func() { signInRoutes = originalRoutes })

	return store, path
}

// The acceptance clause about what the sign-in produces, from the surface a person actually uses.
func TestSigningInToCopilotShowsACodeAndAPageAndEndsAsTheGitHubLogin(t *testing.T) {
	store, path := copilotRouteOn(t, "walid")

	var out bytes.Buffer
	if err := runKeys([]string{"signin", "mycopilot"}, &out); err != nil {
		t.Fatalf("signing in: %v", err)
	}

	said := out.String()
	for _, want := range []string{"ABCD-1234", "https://github.com/login/device", "walid"} {
		if !strings.Contains(said, want) {
			t.Errorf("the sign-in never showed %q:\n%s", want, said)
		}
	}

	in, err := store.SignIn(core.KeyRef{Name: "mycopilot"})
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if in.Kind != keys.KindSignedIn {
		t.Errorf("the credential is a %q", in.Kind)
	}
	if in.Account != "walid" {
		t.Errorf("the credential belongs to %q, want the GitHub login", in.Account)
	}
	if in.Route != copilot.Route {
		t.Errorf("the credential records route %q, and without it nothing can tell it from a Codex "+
			"one later", in.Route)
	}

	tokens, err := store.Tokens(core.KeyRef{Name: "mycopilot"})
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if tokens.Access.Reveal() != "gho_A-REAL-LOOKING-TOKEN" {
		t.Error("the token is not behind the credential")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading keys.json: %v", err)
	}
	if strings.Contains(string(raw), "gho_") {
		t.Errorf("a token reached keys.json, which S-01 exists to prevent:\n%s", raw)
	}
}

// Constraint 4 in one assertion. The provider fork lives in two places on purpose, and the failure
// mode is that one of them learns a route and the other does not, so a credential works in the
// interface and not at the terminal or the other way round.
func TestBothWaysOfBuildingAClientAgreeAboutWhichRouteACredentialTakes(t *testing.T) {
	store, _ := copilotRouteOn(t, "walid")

	if err := runKeys([]string{"signin", "mycopilot"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in: %v", err)
	}
	if _, err := store.Put(
		core.KeyMetadata{Ref: core.KeyRef{Name: "pasted", Provider: core.ProviderAnthropic}},
		core.NewSecret("sk-ant-notreal"),
	); err != nil {
		t.Fatalf("storing a pasted credential: %v", err)
	}

	resolver := session.NewKeyResolver(store)
	for _, name := range []string{"mycopilot", "pasted"} {
		meta, err := store.Metadata(core.KeyRef{Name: name})
		if err != nil {
			t.Fatalf("Metadata(%q): %v", name, err)
		}

		viaCommand, commandID, commandErr := clientFor(store, meta, "some-model")
		viaInterface, interfaceID, interfaceErr := resolver.Resolve(name, "some-model")
		if (commandErr == nil) != (interfaceErr == nil) {
			t.Fatalf("%q: the command said %v and the interface said %v", name, commandErr, interfaceErr)
		}
		if commandErr != nil {
			continue
		}
		if viaCommand.Name() != viaInterface.Name() {
			t.Errorf("%q: the command reached %q and the interface reached %q",
				name, viaCommand.Name(), viaInterface.Name())
		}
		if commandID.Delegated != interfaceID.Delegated {
			t.Errorf("%q: the two surfaces disagree about whether a turn on it can be priced", name)
		}
		if closer, ok := viaCommand.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		if closer, ok := viaInterface.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	}
	resolver.Close()
}

// A Copilot turn is billed against a seat and metered per prompt, so a figure derived from token
// counts would be arithmetic presented as somebody's spend. Unpriced rather than free, which are
// different claims and only one of them is true.
func TestATurnOnACopilotSeatIsReportedAsUnpricedRatherThanAsFree(t *testing.T) {
	store, _ := copilotRouteOn(t, "walid")
	if err := runKeys([]string{"signin", "mycopilot"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in: %v", err)
	}
	meta, err := store.Metadata(core.KeyRef{Name: "mycopilot"})
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}

	client, id, err := clientFor(store, meta, "gpt-5.2-codex")
	if err != nil {
		t.Fatalf("clientFor: %v", err)
	}
	if closer, ok := client.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
	if !id.Delegated {
		t.Fatal("a Copilot turn is priced like an API call, and nobody receives that invoice")
	}

	usage, reason := pricer(id)(core.Usage{InputTokens: 1000, OutputTokens: 500})
	if usage.CostKnown {
		t.Errorf("a cost of $%.4f was reported for a turn metered against a seat", usage.CostUSD)
	}
	if reason == "" {
		t.Error("no reason was given for the missing figure, which reads as an omission")
	}
}

// Two routes and one credential each, and `keys test` has to ask the right vendor about each. Asking
// every registry in turn is how a Copilot credential gets reported on by what was found on the
// machine about Claude Code.
func TestKeysTestAsksTheVendorACredentialActuallyBelongsTo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.json")
	store := keys.NewStore(keys.NewMemoryBackend(), path)

	originalStore := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = originalStore })

	originalRoutes := signInRoutes
	signInRoutes = routeSet{
		copilotSignIn{store: store, vendor: gitHubThatSignsAnybodyIn(t, "walid")},
		claudeCode{store: store, discovery: signedInMachine()},
	}
	t.Cleanup(func() { signInRoutes = originalRoutes })

	if err := runKeys([]string{"signin", "mycopilot", "-route", "copilot"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Copilot: %v", err)
	}
	if err := runKeys([]string{"signin", "myclaude", "-route", "claude-code"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}

	var copilotOut bytes.Buffer
	if err := runKeys([]string{"test", "mycopilot"}, &copilotOut); err != nil {
		t.Fatalf("`keys test` on the Copilot credential: %v", err)
	}
	if !strings.Contains(copilotOut.String(), "GitHub says") {
		t.Errorf("the Copilot credential was not reported on by GitHub:\n%s", copilotOut.String())
	}
	if strings.Contains(copilotOut.String(), "Claude Code") {
		t.Errorf("the Copilot credential was reported on by the Claude route:\n%s", copilotOut.String())
	}

	var claudeOut bytes.Buffer
	if err := runKeys([]string{"test", "myclaude"}, &claudeOut); err != nil {
		t.Fatalf("`keys test` on the delegated credential: %v", err)
	}
	if !strings.Contains(claudeOut.String(), "Claude Code on this machine") {
		t.Errorf("the delegated credential was not reported on by the Claude route:\n%s",
			claudeOut.String())
	}
}

// The route list is what somebody sees when they ask how to sign in, and the question behind that is
// usually whether they are allowed to. Both permitted routes have to appear, and each has to say
// what has to already be true rather than what it is.
func TestBothBuiltRoutesAreOfferedAndEachSaysWhatItNeeds(t *testing.T) {
	offered := routeSet{copilotSignIn{}, claudeCode{}}.Routes()
	if len(offered) != 2 {
		t.Fatalf("%d routes are offered", len(offered))
	}

	byID := map[string]keysui.Route{}
	for _, route := range offered {
		byID[route.ID] = route
	}
	copilotRoute, ok := byID[copilot.Route]
	if !ok {
		t.Fatalf("the Copilot route is not offered: %v", byID)
	}
	if !strings.Contains(copilotRoute.Detail, "Copilot seat") {
		t.Errorf("the Copilot route's detail is %q, and it has to say what has to already be true",
			copilotRoute.Detail)
	}
	if !strings.Contains(copilotRoute.Detail, "@github/copilot") {
		t.Errorf("the Copilot route does not name the runtime it needs: %q", copilotRoute.Detail)
	}
	if !strings.Contains(copilotRoute.Caveat, "permission gate") {
		t.Errorf("the caveat says nothing about whose gate is in force: %q", copilotRoute.Caveat)
	}
	if _, ok := byID["claude-code"]; !ok {
		t.Fatalf("the Claude route stopped being offered when the second route landed: %v", byID)
	}
}

// A route nobody built is still named, because "am I allowed to" is the question behind the one
// being asked. ChatGPT is S-05's and is not here yet.
func TestNamingARouteThisBuildDoesNotHaveSaysWhichOnesItDoes(t *testing.T) {
	copilotRouteOn(t, "walid")

	err := runKeys([]string{"signin", "mysub", "-route", "chatgpt"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a route that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), copilot.Route) {
		t.Errorf("the refusal does not say what this build offers: %v", err)
	}
}
