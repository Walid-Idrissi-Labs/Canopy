package session

import (
	"reflect"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/codex"
)

// What Canopy calls itself when it drives somebody else's agent.
//
// Both delegated routes put a version in their handshake, and it travels upstream: ACP sends it as
// the client's version and Codex sends it beside the originator, which is what OpenAI see. The build
// this fixes had two ways of building these clients, and only one of them passed the version, so a
// turn started from the interface reported Canopy "dev" from a tagged release while the same turn
// started with `canopy ask` reported the tag. There is now one way, and it takes the version as an
// argument rather than as a setting, which is what stops the next path from leaving it out.

// versionSentBy reads the version a delegated client will report.
//
// Read off the client because neither vendor package exposes it: the value only becomes visible in a
// handshake with a real agent, and a test that needed one would be a test nobody runs. It is
// deliberately not compared against a constant this file also passes in, which would be a test that
// cannot fail; the version below is a value neither provider would ever produce on its own, and both
// of them default to "dev" when nobody says otherwise.
func versionSentBy(t *testing.T, client core.ProviderClient) string {
	t.Helper()

	field := reflect.ValueOf(client).Elem().FieldByName("version")
	if !field.IsValid() || field.Kind() != reflect.String {
		t.Fatalf("a %T carries no version field, so this test can no longer see what is sent",
			client)
	}
	return field.String()
}

func TestBothDelegatedRoutesTellTheVendorTheVersionCanopyWasBuiltWith(t *testing.T) {
	const built = "1.4.2"

	machineWith(t, map[string]string{
		"claude":           "/usr/local/bin/claude",
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
	}, `{"loggedIn":true,"authMethod":"claude.ai","email":"walid@example.com","subscriptionType":"max"}`)
	codexMachineWith(t, map[string]string{"codex": "/usr/local/bin/codex"})

	vendors := NewVendors(built)
	t.Cleanup(vendors.Close)

	routes := map[string]keys.SignIn{
		"claude-code": {Kind: keys.KindDelegated, Account: "walid@example.com"},
		"codex":       {Kind: keys.KindDelegated, Account: "someone@example.com", Route: codex.Route},
	}
	for name, in := range routes {
		client, _, err := vendors.Delegated(
			core.KeyMetadata{Ref: core.KeyRef{Name: name, Provider: core.ProviderAnthropic}}, in, "")
		if err != nil {
			t.Fatalf("building the %s client: %v", name, err)
		}
		if got := versionSentBy(t, client); got != built {
			t.Errorf("the %s route tells the vendor Canopy is version %q, and this build is %q. A "+
				"release that reports itself as %q upstream is a bug report nobody can place",
				name, got, built, got)
		}
	}
}

// The same claim from the other end: a resolver, which is what the interface holds, reports what it
// was built with rather than the provider's own default.
func TestTheInterfacesResolverReportsTheBuildsVersionRatherThanTheProvidersDefault(t *testing.T) {
	const built = "1.4.2"

	machineWith(t, map[string]string{
		"claude":           "/usr/local/bin/claude",
		"claude-agent-acp": "/usr/local/bin/claude-agent-acp",
	}, `{"loggedIn":true,"authMethod":"claude.ai","email":"walid@example.com","subscriptionType":"max"}`)

	resolver := NewKeyResolver(storeWithADelegatedCredential(t), built)
	t.Cleanup(resolver.Close)

	client, _, err := resolver.Resolve("claude", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := versionSentBy(t, client); got != built {
		t.Errorf("a turn started in the interface tells Claude Code that Canopy is %q, want %q",
			got, built)
	}
}
