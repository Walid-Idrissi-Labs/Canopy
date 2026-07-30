package codex

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The tests here talk to a real Codex, and they are skipped unless asked for.
//
// They exist for the reason internal/session/live_test.go gives, and the reason is sharp on this
// route. Every other test in this package is scripted against a fake app server written from the
// same reading of the protocol as the code it exercises, so a misreading makes both wrong together
// and the suite still passes. This is the only thing that can catch that, because the peer is the
// real binary and the schema is not consulted by either side.
//
// The turn costs the person running it a small amount of their own plan's usage, which is why it is
// opt in rather than merely slow:
//
//	CANOPY_LIVE_CODEX=1 go test ./internal/provider/codex/ -run Live -v
//
// Nothing is passed in. The account, the binary and the grant all come from the machine, which is
// the whole shape of this route: there is no credential for a test to be given.

func liveInstall(t *testing.T) Installation {
	t.Helper()
	if os.Getenv("CANOPY_LIVE_CODEX") == "" {
		t.Skip("set CANOPY_LIVE_CODEX=1 to run against the Codex on this machine")
	}
	found, err := Discovery{}.Find()
	if err != nil {
		t.Fatalf("finding Codex: %v", err)
	}
	t.Logf("driving %s, with its state in %s", found.Binary, found.Home)
	return found
}

func TestLiveADelegatedTurnReachesTheUsersOwnCodex(t *testing.T) {
	found := liveInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	client := New(found, WithWorkspace(t.TempDir()), WithVersion("0.1.0-test"))
	stream, err := client.Stream(ctx, core.Request{
		Messages: []core.Message{{
			Role: core.RoleUser,
			Text: "Reply with exactly the word: pong. Do not run any commands.",
		}},
	})
	if err != nil {
		t.Fatalf("starting a real delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var reply strings.Builder
	var opening string
	var usage core.Usage
	var stop core.StopReason

	for stream.Next() {
		event := stream.Event()
		switch event.Kind {
		case core.EventText:
			reply.WriteString(event.Text)
		case core.EventNotice:
			if opening == "" {
				opening = event.Text
			}
			t.Logf("notice: %s", event.Text)
		case core.EventToolCall:
			t.Fatal("a real delegated turn handed back a tool call, which internal/agent/loop.go " +
				"would invoke, running the vendor's tool a second time")
		case core.EventDone:
			usage, stop = event.Usage, event.StopReason
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatalf("the real turn failed: %v", err)
	}

	t.Logf("stop=%s reply=%q usage=%+v", stop, strings.TrimSpace(reply.String()), usage)

	if stop != core.StopEndTurn {
		t.Errorf("the turn ended as %q, want %q", stop, core.StopEndTurn)
	}
	if strings.TrimSpace(reply.String()) == "" {
		t.Error("a real turn produced no reply at all")
	}
	if !strings.Contains(opening, "ChatGPT") {
		t.Errorf("the first notice was %q, want the sentence about whose plan this runs on", opening)
	}
	if usage.CostKnown {
		t.Error("a real subscription-billed turn claimed to know what it cost")
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		t.Error("a real turn reported no tokens at all, so the usage notification was missed")
	}
}

// The claim this whole route stands on, checked against the real binary rather than against a fake
// that was written to agree with it.
func TestLiveTheAppServerIdentifiesCanopyAsCanopy(t *testing.T) {
	found := liveInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	session, err := start(ctx, spawn(found, t.TempDir()), "0.1.0-test")
	if err != nil {
		t.Fatalf("the handshake with a real app server failed: %v", err)
	}
	defer func() { _ = session.Close() }()

	t.Logf("the app server will identify this client upstream as: %s", session.userAgent)
	if !strings.HasPrefix(session.userAgent, Originator+"/") {
		t.Errorf("a real app server said it would identify Canopy as %q, and the whole "+
			"defensibility of this route rests on that being %q", session.userAgent, Originator)
	}
	for _, theirs := range []string{"codex_cli_rs", "codex_vscode", "codex_atlas"} {
		if strings.HasPrefix(session.userAgent, theirs) {
			t.Errorf("a real app server would identify Canopy as %q, which belongs to another client",
				theirs)
		}
	}
}

// The answer `canopy keys test` gives, taken from the real vendor.
func TestLiveThePlansLimitsComeBackFromOpenAI(t *testing.T) {
	found := liveInstall(t)

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	vendor := Vendor{Discovery: Discovery{}, Version: "0.1.0-test"}
	_ = found

	account, limits, err := vendor.Limits(ctx)
	if err != nil {
		t.Fatalf("asking a real app server for the account and its limits: %v", err)
	}
	t.Logf("account=%+v limits=%+v", account, limits)

	if account.Email == "" {
		t.Error("a real signed-in Codex reported no account, so two subscriptions on one machine " +
			"would be two rows nobody can tell apart")
	}
	if limits.Primary == nil {
		t.Error("no rate limit window came back, and that window is the whole reason " +
			"`canopy keys test` on this route can say something a fingerprint never could")
	} else {
		t.Logf("primary window: %s", limits.Primary)
	}
}
