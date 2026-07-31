package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/codex"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/copilot"
)

// Which vendor gets asked about which credential.
//
// routeSet dispatches on the route recorded with a credential, and falls back to asking each vendor
// in turn when there is none, which is a credential stored before the field existed. Every Report in
// this build reads the machine or a token and none of them can tell from the answer whose credential
// the question was about, so the fallback was reporting one subscription's facts under another
// credential's name. The fix is that a route refuses a credential that is not its own, and these
// hold it for every route rather than for the one it was noticed on.

// routelessCredentials stores one credential per route with no route recorded on any of them.
func routelessCredentials(t *testing.T) (*keys.Store, routeSet) {
	t.Helper()

	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))

	originalStore := openKeyStore
	openKeyStore = func() (*keys.Store, error) { return store, nil }
	t.Cleanup(func() { openKeyStore = originalStore })

	// No Route on any of them, deliberately: that is what makes routeSet fall back to asking
	// everybody, which is the path being tested.
	if _, err := store.PutSignIn(
		core.KeyMetadata{
			Ref:     core.KeyRef{Name: "mycopilot", Provider: core.ProviderOpenAICompatible},
			BaseURL: copilot.BaseURL,
		},
		keys.SignIn{Kind: keys.KindSignedIn, Account: "walid"},
		keys.Tokens{Access: core.NewSecret("gho_TOKEN")},
	); err != nil {
		t.Fatalf("storing the Copilot credential: %v", err)
	}
	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "myclaude", Provider: core.ProviderAnthropic}},
		keys.SignIn{Kind: keys.KindDelegated, Account: "walid@example.com"},
		keys.Tokens{},
	); err != nil {
		t.Fatalf("storing the Claude credential: %v", err)
	}
	if _, err := store.PutSignIn(
		core.KeyMetadata{
			Ref:     core.KeyRef{Name: "mychatgpt", Provider: core.ProviderOpenAICompatible},
			BaseURL: codex.BaseURL,
		},
		keys.SignIn{Kind: keys.KindDelegated, Account: "someone@example.com"},
		keys.Tokens{},
	); err != nil {
		t.Fatalf("storing the ChatGPT credential: %v", err)
	}

	routes := routeSet{
		copilotSignIn{store: store, vendor: gitHubThatSignsAnybodyIn(t, "walid")},
		claudeCode{store: store, discovery: signedInMachine()},
		codexSignIn{store: store, vendor: signedInCodex()},
	}

	originalRoutes := signInRoutes
	signInRoutes = routes
	t.Cleanup(func() { signInRoutes = originalRoutes })

	return store, routes
}

func TestNoRouteAnswersAboutACredentialThatBelongsToAnotherVendor(t *testing.T) {
	// Read before the swap, so this notices a route that landed in the build without landing here.
	built, ok := signInRoutes.(routeSet)
	if !ok {
		t.Fatalf("this build's routes are a %T, and this test drives a routeSet", signInRoutes)
	}

	store, routes := routelessCredentials(t)
	if len(routes) != len(built) {
		t.Errorf("this build offers %d route registries and this test drives %d, so one of them is "+
			"not being asked whether it answers about other people's credentials",
			len(built), len(routes))
	}

	// Which member of routes above owns each credential.
	owners := map[string]int{"mycopilot": 0, "myclaude": 1, "mychatgpt": 2}
	for name, owner := range owners {
		meta, err := store.Metadata(core.KeyRef{Name: name})
		if err != nil {
			t.Fatalf("Metadata(%q): %v", name, err)
		}

		for i, member := range routes {
			reporter, ok := member.(reportsOnCredentials)
			if !ok {
				continue
			}
			report, err := reporter.Report(context.Background(), meta)
			if i == owner {
				if err != nil {
					t.Errorf("the %T route refused its own credential %q: %v", member, name, err)
				}
				continue
			}
			if err == nil {
				t.Errorf("the %T route answered about %q, which is not its credential, and said it "+
					"belongs to %q at %s. `canopy keys test %s` would print another subscription's "+
					"facts under this credential's name",
					member, name, report.Account, report.Vendor, name)
			}
		}
	}
}

// The same thing from the surface somebody uses, which is where it would be noticed.
func TestKeysTestOnACredentialWithNoRouteStillAsksTheRightVendor(t *testing.T) {
	routelessCredentials(t)

	for name, want := range map[string]string{
		"mycopilot": "GitHub says",
		"myclaude":  "Claude Code on this machine",
		"mychatgpt": "the Codex app server on this machine",
	} {
		var out bytes.Buffer
		if err := runKeys([]string{"test", name}, &out); err != nil {
			t.Fatalf("`keys test %s`: %v", name, err)
		}
		if !strings.Contains(out.String(), want) {
			t.Errorf("`keys test %s` was not answered by %q:\n%s", name, want, out.String())
		}
		for other, sentence := range map[string]string{
			"mycopilot": "GitHub says",
			"myclaude":  "Claude Code on this machine",
			"mychatgpt": "the Codex app server on this machine",
		} {
			if other == name || !strings.Contains(out.String(), sentence) {
				continue
			}
			t.Errorf("`keys test %s` was answered by the vendor %q belongs to:\n%s",
				name, other, out.String())
		}
	}
}

// A route that this build no longer offers is still said plainly rather than answered by whoever
// happens to be next in the list.
func TestACredentialFromARouteThisBuildDroppedIsNotAnsweredBySomebodyElse(t *testing.T) {
	store, _ := routelessCredentials(t)

	if _, err := store.PutSignIn(
		core.KeyMetadata{Ref: core.KeyRef{Name: "gone", Provider: core.ProviderAnthropic}},
		keys.SignIn{Kind: keys.KindDelegated, Account: "walid@example.com", Route: "gemini-cli"},
		keys.Tokens{},
	); err != nil {
		t.Fatalf("storing a credential from a route nobody offers: %v", err)
	}

	var out bytes.Buffer
	err := runKeys([]string{"test", "gone"}, &out)
	if strings.Contains(out.String(), "Claude Code on this machine") {
		t.Errorf("a credential from a route this build does not have was reported on by Claude "+
			"Code:\n%s", out.String())
	}
	if err == nil && !strings.Contains(out.String(), "could not be asked") {
		t.Errorf("nothing said that the vendor was not asked:\n%s", out.String())
	}
}

// A route that reports by sending a token must not send it about somebody else's credential.
//
// The Copilot route is the only one of the three that reads a secret out of the keychain and puts it
// on the wire, so a mis-dispatch here is not a wrong answer, it is one vendor being handed another
// vendor's grant. It was safe before by accident: internal/keys refuses to hand tokens over for a
// delegated credential, so the read failed first. Accident is not the same as safe, and a route that
// stored real tokens under a route id this build does not know would have gone straight through.
func TestARouteThatReportsWithATokenRefusesToSendItForAnotherRoutesCredential(t *testing.T) {
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))

	// A signed-in credential holding real tokens, recorded against a route that is not Copilot's.
	if _, err := store.PutSignIn(
		core.KeyMetadata{
			Ref:     core.KeyRef{Name: "elsewhere", Provider: core.ProviderOpenAICompatible},
			BaseURL: "https://api.example.com",
		},
		keys.SignIn{Kind: keys.KindSignedIn, Account: "somebody", Route: "some-other-vendor"},
		keys.Tokens{Access: core.NewSecret("not-a-github-token")},
	); err != nil {
		t.Fatalf("storing the credential: %v", err)
	}

	meta, err := store.Metadata(core.KeyRef{Name: "elsewhere"})
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}

	route := copilotSignIn{store: store, vendor: gitHubThatSignsAnybodyIn(t, "walid")}
	if report, err := route.Report(context.Background(), meta); err == nil {
		t.Errorf("the Copilot route answered about a credential belonging to %q and said it belongs "+
			"to %q. To get that answer it sent that credential's access token to github.com",
			"some-other-vendor", report.Account)
	}
}
