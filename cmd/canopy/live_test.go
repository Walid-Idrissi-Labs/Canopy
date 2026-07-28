package main

// The live test. Opt in, because it spends money and needs a network.
//
// M-01 asks for something no unit test can give: a real model, deciding on its own to call a tool,
// against a real file system, with the result coming back through the whole stack. Everything below
// this line has been tested against fakes for nine phases, and fakes agree with whatever you tell
// them. The two bugs that shipped on 2026-07-26 both passed their tests.
//
// Run it with a stored credential:
//
//	CANOPY_LIVE_KEY=nemotron go test ./cmd/canopy -run TestLive -v
//
// It is skipped otherwise, so CI and `go test ./...` are unaffected.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

func TestLiveToolsReachTheProviderAndBack(t *testing.T) {
	keyName := os.Getenv("CANOPY_LIVE_KEY")
	if keyName == "" {
		t.Skip("set CANOPY_LIVE_KEY to the name of a stored credential to run this")
	}

	keyStore, err := openKeyStore()
	if err != nil {
		t.Fatalf("opening the key store: %v", err)
	}
	model := defaultModelFor(keyStore, keyName)
	if model == "" {
		t.Fatalf("credential %q has no model set, so the request would name nothing", keyName)
	}
	t.Logf("using credential %q with model %q", keyName, model)

	// A directory of its own, so the agent cannot touch the repository it is being tested from.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"),
		[]byte("the answer is 41\n"), 0o600); err != nil {
		t.Fatalf("seeding the workspace: %v", err)
	}

	resolver := session.NewKeyResolver(keyStore)
	engine := session.New(resolver)
	defer engine.Close()

	if err := attachTools(engine, dir, loadProject(dir)); err != nil {
		t.Fatalf("attaching tools: %v", err)
	}

	created := engine.Create(keyName, model)
	if _, err := engine.Send(created.ID,
		"Read the file notes.txt in the workspace, then write a file called answer.txt "+
			"containing the corrected number. The answer should be 42, not 41. "+
			"Use your tools. Do not ask me anything."); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Generous, because a real provider on a real network is not fast and this is not a latency
	// test. A turn that has not finished in three minutes has gone wrong in a way worth seeing.
	deadline := time.Now().Add(3 * time.Minute)
	var final core.Session
	for time.Now().Before(deadline) {
		got, ok := engine.Session(created.ID)
		if !ok {
			t.Fatal("the session disappeared")
		}
		if _, working := got.Active(); !working && len(got.Turns) > 0 {
			final = got
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(final.Turns) == 0 {
		t.Fatal("the turn never finished")
	}

	turn := final.Turns[len(final.Turns)-1]
	t.Logf("turn ended as %q after %d tool calls", turn.State, len(turn.ToolCalls))
	for i, call := range turn.ToolCalls {
		t.Logf("  call %d: %s %s", i+1, call.Name, string(call.Input))
	}
	for _, result := range turn.ToolResults {
		t.Logf("  result %s: error=%v in %s: %s",
			result.CallID, result.IsError, result.Duration, firstLineOf(result.Content))
	}
	if turn.Error != "" {
		t.Logf("  turn error: %s", turn.Error)
	}
	t.Logf("  reply: %s", turn.Text)

	if len(turn.ToolCalls) == 0 {
		t.Fatal("the model called no tools at all, so nothing about the tool path was exercised")
	}

	// The point of the whole exercise: a file that only exists because a model decided to make it.
	written, err := os.ReadFile(filepath.Join(dir, "answer.txt"))
	if err != nil {
		t.Errorf("the model did not write answer.txt: %v", err)
	} else if !strings.Contains(string(written), "42") {
		t.Errorf("answer.txt says %q", strings.TrimSpace(string(written)))
	}

	// And the other half of M-01: that a person watching could follow it. Rendered from the same
	// session the engine produced, so this is the real transcript rather than a constructed one.
	// No kind lookup, which is the honest thing here: this drives the engine directly rather than
	// through a chat model, so there is no registry to ask and the labels degrade to blanks.
	rendered := strings.Join(chat.Transcript(final, 100, ".", nil), "\n")
	for _, want := range []string{"read_file", "notes.txt"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the transcript does not mention %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "ok in") && !strings.Contains(rendered, "failed after") {
		t.Errorf("no tool call reports an outcome or a duration:\n%s", rendered)
	}
	t.Logf("transcript:\n%s", rendered)

}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
