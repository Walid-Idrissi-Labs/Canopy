package acp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The one test here that talks to a real Claude Code, and it is skipped unless asked for.
//
// It exists for the reason internal/session/live_test.go gives, and the reason is sharper on this
// route than anywhere else. Every other test in this package is scripted against a fake agent that
// was written from the same reading of the ACP schema as the code it exercises, so a misreading makes
// both wrong together and the suite still passes. This is the only thing that can catch that, because
// the peer is the real bridge and the schema is not consulted by either side.
//
// It costs the person running it a small amount of their own plan's usage, which is why it is opt in
// rather than merely slow:
//
//	CANOPY_LIVE_CLAUDE_CODE=1 go test ./internal/provider/acp/ -run Live -v
//
// Nothing is passed in. The account, the installation and the bridge all come from the machine, which
// is the whole shape of this route: there is no credential for a test to be given.
func TestLiveADelegatedTurnReachesTheUsersOwnClaudeCode(t *testing.T) {
	if os.Getenv("CANOPY_LIVE_CLAUDE_CODE") == "" {
		t.Skip("set CANOPY_LIVE_CLAUDE_CODE=1 to run a real turn against the Claude Code on this machine")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	found, err := Discovery{}.Find(ctx)
	if err != nil {
		t.Fatalf("finding Claude Code: %v", err)
	}
	t.Logf("delegating to %s as %s, through %s", found.CLI, found.Account, found.Bridge)

	client := New(found, WithWorkspace(t.TempDir()))
	stream, err := client.Stream(ctx, core.Request{
		Messages: []core.Message{{
			Role: core.RoleUser,
			Text: "Reply with exactly the word: pong. Do not use any tools.",
		}},
	})
	if err != nil {
		t.Fatalf("starting a delegated turn: %v", err)
	}
	defer func() { _ = stream.Close() }()

	got := drain(t, stream)
	if got.err != nil {
		t.Fatalf("the delegated turn failed: %v", got.err)
	}
	if !strings.Contains(strings.ToLower(got.text), "pong") {
		t.Errorf("the reply was %q", got.text)
	}
	if got.stop != core.StopEndTurn {
		t.Errorf("the turn stopped with %q", got.stop)
	}
	if got.usage.CostKnown {
		t.Error("a real delegated turn claimed to know what it cost")
	}
	t.Logf("tokens: %+v", got.usage)
	for _, notice := range got.notices {
		t.Logf("notice: %s", notice)
	}
}
