package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/acp"
)

// The Claude route, driven end to end on a machine that has no Claude Code.
//
// Every fixture below is an invented machine. That is not a compromise: the acceptance is about what
// happens with and without an installation, and a test that needed one could only ever check half of
// it, on one laptop, at the cost of somebody's subscription.

// claudeMachine builds a Discovery that answers as if a machine were set up a particular way.
func claudeMachine(present map[string]string, says string) acp.Discovery {
	return acp.Discovery{
		LookPath: func(name string) (string, error) {
			if path, ok := present[name]; ok {
				return path, nil
			}
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		},
		Status: func(ctx context.Context, cli string) (string, error) { return says, nil },
		Getenv: func(string) string { return "" },
	}
}

func signedInMachine() acp.Discovery {
	return claudeMachine(map[string]string{
		"claude":           "/usr/local/bin/claude",
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
	}, `{"loggedIn":true,"authMethod":"claude.ai","email":"walid@example.com","subscriptionType":"max"}`)
}

// claudeRouteOn wires the Claude route to a temporary store and a scripted machine.
func claudeRouteOn(t *testing.T, machine acp.Discovery) (*keys.Store, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "keys.json")
	store := keys.NewStore(keys.NewMemoryBackend(), path)

	originalStore := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = originalStore })

	originalRoutes := signInRoutes
	signInRoutes = claudeCode{store: store, discovery: machine}
	t.Cleanup(func() { signInRoutes = originalRoutes })

	return store, path
}

func TestAddingTheClaudeCredentialFindsClaudeCodeAndReportsTheAccountItIsSignedInAs(t *testing.T) {
	store, path := claudeRouteOn(t, signedInMachine())

	var out bytes.Buffer
	if err := runKeys([]string{"signin", "claude"}, &out); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}

	if !strings.Contains(out.String(), "walid@example.com") {
		t.Errorf("the account was not reported:\n%s", out.String())
	}

	in, err := store.SignIn(core.KeyRef{Name: "claude"})
	if err != nil {
		t.Fatalf("reading the stored credential: %v", err)
	}
	if in.Kind != keys.KindDelegated {
		t.Errorf("the stored credential is %q, want delegated", in.Kind)
	}
	if in.Account != "walid@example.com" {
		t.Errorf("the stored account is %q", in.Account)
	}
	if in.ExpiresAt != nil {
		t.Error("a delegated credential was given an expiry, and it has no token of its own to expire")
	}

	// The clause that carries the decision: nothing was written that could be a credential. Read off
	// the file's bytes rather than the struct, which is how S-01 holds the same property.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading keys.json: %v", err)
	}
	for _, forbidden := range []string{"sk-ant", "token", "access", "refresh", "canopyGrant"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("keys.json contains %q:\n%s", forbidden, raw)
		}
	}
}

// The keychain half must be empty, because `canopy keys test` says it is and says empty is correct.
func TestADelegatedClaudeCredentialLeavesTheKeychainHalfEmpty(t *testing.T) {
	backend := keys.NewMemoryBackend()
	path := filepath.Join(t.TempDir(), "keys.json")
	store := keys.NewStore(backend, path)

	originalStore := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = originalStore })

	originalRoutes := signInRoutes
	signInRoutes = claudeCode{store: store, discovery: signedInMachine()}
	t.Cleanup(func() { signInRoutes = originalRoutes })

	if err := runKeys([]string{"signin", "claude"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}

	if _, err := backend.Get("claude"); err == nil {
		t.Error("a delegated credential put something in the keychain, and the whole reason this " +
			"route is permitted is that Canopy holds nothing of the user's on it")
	}
}

func TestTestingADelegatedCredentialSaysSomethingTrueRatherThanReportingAMissingSecret(t *testing.T) {
	claudeRouteOn(t, signedInMachine())

	if err := runKeys([]string{"signin", "claude"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}

	var out bytes.Buffer
	if err := runKeys([]string{"test", "claude"}, &out); err != nil {
		t.Fatalf("`keys test` on a delegated credential failed: %v", err)
	}

	said := out.String()
	for _, want := range []string{
		"walid@example.com",
		"none, and none is correct",
		"max",
		"/usr/local/bin/claude-agent-acp",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("`keys test` does not say %q:\n%s", want, said)
		}
	}
	for _, forbidden := range []string{"does not match its record", "corrupt", "damage"} {
		if strings.Contains(said, forbidden) {
			t.Errorf("an empty keychain half was reported as %q:\n%s", forbidden, said)
		}
	}
}

func TestTestingADelegatedCredentialLooksAtTheMachineAgainRatherThanReadingBackWhatWasStored(t *testing.T) {
	store, _ := claudeRouteOn(t, signedInMachine())

	if err := runKeys([]string{"signin", "claude"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}

	// Somebody signed out of Claude Code after adding the credential.
	signInRoutes = claudeCode{store: store, discovery: claudeMachine(map[string]string{
		"claude":           "/usr/local/bin/claude",
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
	}, `{"loggedIn":false}`)}

	var out bytes.Buffer
	if err := runKeys([]string{"test", "claude"}, &out); err != nil {
		t.Fatalf("`keys test` failed rather than reporting: %v", err)
	}
	if !strings.Contains(out.String(), "The vendor could not be asked") {
		t.Errorf("a signed-out Claude Code was not reported:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Run `claude` and sign in") {
		t.Errorf("the remedy was not named:\n%s", out.String())
	}
}

func TestASecondAccountOnOneMachineIsSaidRatherThanAbsorbed(t *testing.T) {
	store, _ := claudeRouteOn(t, signedInMachine())

	if err := runKeys([]string{"signin", "claude"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}

	signInRoutes = claudeCode{store: store, discovery: claudeMachine(map[string]string{
		"claude":           "/usr/local/bin/claude",
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
	}, `{"loggedIn":true,"authMethod":"claude.ai","email":"someone-else@example.com","subscriptionType":"pro"}`)}

	var out bytes.Buffer
	if err := runKeys([]string{"test", "claude"}, &out); err != nil {
		t.Fatalf("`keys test` failed: %v", err)
	}
	if !strings.Contains(out.String(), "was added for walid@example.com") {
		t.Errorf("a changed account was absorbed silently:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Turns run as someone-else@example.com") {
		t.Errorf("the output does not say which subscription is actually used:\n%s", out.String())
	}
}

func TestAMachineWithoutClaudeCodeIsToldWhatToInstallAndIsNotOfferedASignIn(t *testing.T) {
	claudeRouteOn(t, claudeMachine(map[string]string{}, ""))

	var out bytes.Buffer
	err := runKeys([]string{"signin", "claude"}, &out)
	if err == nil {
		t.Fatal("a machine with no Claude Code stored a delegated credential")
	}

	said := err.Error()
	if !strings.Contains(said, "claude.com/claude-code") {
		t.Errorf("the failure does not name what to install: %v", err)
	}
	if !strings.Contains(said, "signed in to yourself") {
		t.Errorf("the failure does not say Canopy will not sign anybody in: %v", err)
	}
	for _, forbidden := range []string{"oauth", "authoriz", "paste your"} {
		if strings.Contains(strings.ToLower(said), forbidden) {
			t.Errorf("the failure offers %q as a way in: %v", forbidden, err)
		}
	}
}

func TestAMachineWithNoBridgeIsToldToInstallTheBridge(t *testing.T) {
	claudeRouteOn(t, claudeMachine(map[string]string{"claude": "/usr/local/bin/claude"},
		`{"loggedIn":true,"authMethod":"claude.ai","email":"walid@example.com","subscriptionType":"max"}`))

	err := runKeys([]string{"signin", "claude"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("a machine with no bridge stored a delegated credential")
	}
	if !strings.Contains(err.Error(), "@agentclientprotocol/claude-agent-acp") {
		t.Errorf("the failure does not name the package: %v", err)
	}
}

// The route is offered before the wizard has anything else, and it has to say what it is before
// somebody chooses it.
func TestTheClaudeRouteSaysWhatItNeedsAndWhatItGivesUp(t *testing.T) {
	routes := claudeCode{}.Routes()
	if len(routes) != 1 {
		t.Fatalf("the Claude registry offers %d routes", len(routes))
	}
	route := routes[0]

	if route.ID != "claude-code" {
		t.Errorf("the route id is %q", route.ID)
	}
	if !route.VendorPicksModel {
		t.Error("the route does not say the vendor picks the model, so the model picker would offer " +
			"a choice that changes nothing")
	}
	for _, want := range []string{
		"does not sign you in to Claude",
		"never holds a Claude credential",
		"own tools under its own permissions",
		"permission gate is not in that path",
	} {
		if !strings.Contains(route.Caveat, want) {
			t.Errorf("the caveat does not say %q:\n%s", want, route.Caveat)
		}
	}
	if !strings.Contains(route.Detail, "signed in") {
		t.Errorf("the route does not say what has to already be true: %s", route.Detail)
	}
}

// Cancel has to undo a sign-in that completed in the moment between the keystroke and the call.
func TestCancellingAClaudeSignInThatAlreadyCompletedRemovesTheCredential(t *testing.T) {
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))
	route := claudeCode{store: store, discovery: signedInMachine()}

	attempt, err := route.Begin(route.Routes()[0], "claude")
	if err != nil {
		t.Fatalf("beginning the sign-in: %v", err)
	}

	if _, err := attempt.Wait(); err != nil {
		t.Fatalf("the sign-in failed: %v", err)
	}
	if _, err := store.Metadata(core.KeyRef{Name: "claude"}); err != nil {
		t.Fatalf("the sign-in stored nothing: %v", err)
	}

	attempt.Cancel()

	if _, err := store.Metadata(core.KeyRef{Name: "claude"}); err == nil {
		t.Error("cancelling left a working credential behind, which is a credential nobody knows " +
			"they have")
	}
}

func TestCancellingAClaudeSignInBeforeItFinishesStoresNothing(t *testing.T) {
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))

	// The search blocks until the test lets it through, which is the window Cancel exists for.
	released := make(chan struct{})
	machine := signedInMachine()
	inner := machine.Status
	machine.Status = func(ctx context.Context, cli string) (string, error) {
		<-released
		return inner(ctx, cli)
	}

	route := claudeCode{store: store, discovery: machine}
	attempt, err := route.Begin(route.Routes()[0], "claude")
	if err != nil {
		t.Fatalf("beginning the sign-in: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var waitErr error
	go func() {
		defer wg.Done()
		_, waitErr = attempt.Wait()
	}()

	attempt.Cancel()
	close(released)
	wg.Wait()

	if waitErr == nil {
		t.Fatal("a cancelled sign-in reported success")
	}
	if _, err := store.Metadata(core.KeyRef{Name: "claude"}); err == nil {
		t.Error("a cancelled sign-in stored a credential")
	}
}

func TestSigningOutOfADelegatedClaudeCredentialRevokesNothingAndSaysSo(t *testing.T) {
	claudeRouteOn(t, signedInMachine())

	if err := runKeys([]string{"signin", "claude"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}

	var out bytes.Buffer
	if err := runKeys([]string{"signout", "claude"}, &out); err != nil {
		t.Fatalf("signing out: %v", err)
	}
	if !strings.Contains(out.String(), "Nothing was revoked anywhere") {
		t.Errorf("signing out implied something was revoked at the vendor:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "still signed in as far as the vendor is concerned") {
		t.Errorf("signing out did not say Claude Code is untouched:\n%s", out.String())
	}
}

// A delegated credential is listed with the account and with no model of Canopy's choosing.
func TestADelegatedCredentialListsAsDelegatedAndLetsTheVendorChooseTheModel(t *testing.T) {
	claudeRouteOn(t, signedInMachine())

	if err := runKeys([]string{"signin", "claude"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}

	var out bytes.Buffer
	if err := runKeys([]string{"list"}, &out); err != nil {
		t.Fatalf("listing credentials: %v", err)
	}
	for _, want := range []string{"walid@example.com (delegated)", "the vendor chooses"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the list does not say %q:\n%s", want, out.String())
		}
	}
}

// The record on disk is the one thing that outlives the process, so what it contains is the promise.
func TestWhatIsWrittenToDiskForADelegatedCredentialIsThreeFactsAndNoCredential(t *testing.T) {
	_, path := claudeRouteOn(t, signedInMachine())

	if err := runKeys([]string{"signin", "claude"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading keys.json: %v", err)
	}
	var records []map[string]any
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatalf("keys.json is not a list of records: %v\n%s", err, raw)
	}
	if len(records) != 1 {
		t.Fatalf("keys.json holds %d records", len(records))
	}

	record := records[0]
	if record["kind"] != "delegated" {
		t.Errorf("the record's kind is %v", record["kind"])
	}
	if record["account"] != "walid@example.com" {
		t.Errorf("the record's account is %v", record["account"])
	}
	if _, ok := record["expiresAt"]; ok {
		t.Error("the record carries an expiry for a credential with no token to expire")
	}
	// The field is always written, which is S-01's shape and not this route's to change. What must be
	// true is that it holds nothing: a fingerprint is a summary of a stored value, and there is no
	// stored value here to summarise.
	if got := record["fingerprint"]; got != "" {
		t.Errorf("the record carries the fingerprint %v of a value that does not exist", got)
	}
}

// A conversation on a delegated credential must not open naming a model Canopy chose, because
// choosing it changes nothing: the agent Canopy drives picks its own.
func TestAConversationOnADelegatedCredentialStartsWithNoModelOfCanopysChoosing(t *testing.T) {
	store, _ := claudeRouteOn(t, signedInMachine())

	if err := runKeys([]string{"signin", "claude"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("signing in through Claude Code: %v", err)
	}
	if got := defaultModelFor(store, "claude"); got != "" {
		t.Errorf("a delegated credential opened a session on %q, which has no effect on a single "+
			"message and is a model name on screen that nothing honours", got)
	}

	// The regression that matters beside it: an ordinary Anthropic key still gets the default.
	if _, err := store.Put(
		core.KeyMetadata{Ref: core.KeyRef{Name: "api", Provider: core.ProviderAnthropic}},
		core.NewSecret("sk-ant-not-a-real-key"),
	); err != nil {
		t.Fatalf("storing a pasted credential: %v", err)
	}
	if got := defaultModelFor(store, "api"); got == "" {
		t.Error("a pasted Anthropic credential stopped getting this build's default model")
	}
}
