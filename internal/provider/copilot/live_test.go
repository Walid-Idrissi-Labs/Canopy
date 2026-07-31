package copilot

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
)

// Live tests, which need a real Copilot seat and the vendor's CLI, and are skipped unless asked for.
//
// Everything else in this package is proved against a fake agent, and a fake agent is written from
// the same understanding of the SDK as the code it stands in for. If that understanding is wrong,
// both are wrong together and every test still passes. These are the only things here that can
// catch that, and they are also the only way to settle the two questions this route could not answer
// from a reading: which OAuth scopes GitHub actually requires, and whether ModeEmpty plus Canopy's
// allowlist really leaves the model with no vendor tools.
//
// Run them against a stored credential:
//
//	CANOPY_LIVE_COPILOT=mycopilot go test ./internal/provider/copilot/ -run Live -v
//
// The credential is read from the key store by name. No token is ever passed on a command line or
// written into this repository.
func liveCredential(t *testing.T) (keys.Tokens, string) {
	t.Helper()

	name := os.Getenv("CANOPY_LIVE_COPILOT")
	if name == "" {
		t.Skip("set CANOPY_LIVE_COPILOT to the name of a stored Copilot credential to run live tests")
	}
	if _, err := FindCLI(); err != nil {
		t.Skipf("the Copilot CLI is not on this machine: %v", err)
	}

	store, err := keys.Open()
	if err != nil {
		t.Fatalf("opening the key store: %v", err)
	}
	tokens, err := store.Tokens(core.KeyRef{Name: name})
	if err != nil {
		t.Fatalf("reading the tokens for %q: %v", name, err)
	}
	return tokens, os.Getenv("CANOPY_LIVE_COPILOT_MODEL")
}

// The acceptance clause that a turn runs against the user's own subscription and streams back, with
// nothing standing in for the vendor.
func TestLiveATurnRunsOnTheSubscriptionAndStreamsBack(t *testing.T) {
	tokens, model := liveCredential(t)

	clients := NewClients()
	t.Cleanup(func() { _ = clients.Close() })
	client, err := clients.Once("live",
		Conversation{Token: tokens.Access, Model: model, StateDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Once: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	stream, err := client.Stream(ctx, core.Request{
		Model:    model,
		Messages: []core.Message{{Role: core.RoleUser, Text: "Reply with exactly: canopy"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var reply strings.Builder
	var done core.StreamEvent
	for stream.Next() {
		event := stream.Event()
		if event.Kind == core.EventText {
			reply.WriteString(event.Text)
		}
		if event.Kind == core.EventDone {
			done = event
		}
	}
	if done.StopReason != core.StopEndTurn {
		t.Fatalf("the turn ended as %q: %v", done.StopReason, done.Err)
	}
	if !strings.Contains(strings.ToLower(reply.String()), "canopy") {
		t.Errorf("the reply was %q", reply.String())
	}
	t.Logf("usage: %+v", done.Usage)
	if done.Usage.CostKnown {
		t.Error("a Copilot turn reported a known cost, and a seat has no per-token price")
	}
}

// The claim ModeEmpty and the allowlist are there to make, asked of the model itself.
//
// Not a proof and it says so: a model can be wrong about its own tools. It is the strongest check
// available from outside GitHub's own harness, which verifies the same property by inspecting the
// chat completion request through a proxy, and it would catch the change that matters most, a
// release where the mode stops meaning what it means today.
func TestLiveTheModelIsGivenNoneOfTheVendorsOwnTools(t *testing.T) {
	tokens, model := liveCredential(t)

	clients := NewClients()
	t.Cleanup(func() { _ = clients.Close() })
	client, err := clients.Once("live",
		Conversation{Token: tokens.Access, Model: model, StateDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Once: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	stream, err := client.Stream(ctx, core.Request{
		Model: model,
		Messages: []core.Message{{Role: core.RoleUser, Text: "List the names of every tool you can " +
			"call, one per line, and nothing else. If you have none, reply with exactly: none"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var reply strings.Builder
	for stream.Next() {
		if event := stream.Event(); event.Kind == core.EventText {
			reply.WriteString(event.Text)
		}
	}

	said := strings.ToLower(reply.String())
	t.Logf("the model listed: %s", reply.String())
	for _, vendorTool := range []string{"bash", "powershell", "web_fetch", "str_replace"} {
		if strings.Contains(said, vendorTool) {
			t.Errorf("the model says it can call %q, so ModeEmpty and the allowlist are not doing "+
				"what this route depends on them doing", vendorTool)
		}
	}
}

// The scope question, which no amount of reading settles because GitHub documents no scope for
// Copilot at all. Run this with CANOPY_GITHUB_SCOPES narrowed and record what stopped working.
func TestLiveTheStoredGrantIsAcceptedForATurn(t *testing.T) {
	tokens, _ := liveCredential(t)

	login, err := Vendor{}.Login(context.Background(), tokens.Access)
	if err != nil {
		t.Fatalf("GitHub would not say whose token this is, which is what read:user is for: %v", err)
	}
	t.Logf("the stored grant belongs to %s, obtained with scopes %v", login, Scopes)
}
