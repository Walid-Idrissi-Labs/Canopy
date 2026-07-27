package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/agent"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tools"
)

// The one test that proves an agent can actually do work.
//
// Everything else in this package runs against a scripted model, and a scripted model always calls
// the tool the script says it will with arguments the script chose. What it cannot tell us is
// whether a real model, given these tool descriptions and these schemas, reaches for the right tool
// and fills it in correctly. That is a property of the descriptions as much as of the code, and it
// is only observable against something that has not been told the answer.
//
//	CANOPY_LIVE_KEY=nim CANOPY_LIVE_MODEL=minimaxai/minimax-m2.7 go test ./internal/agent/ -run Live -v
//
// liveBudget is how long a live turn may take before the test gives up.
//
// Generous, and it has been raised once already. NVIDIA NIM is a free tier and is genuinely slow
// under load: the same request that answers in seventy seconds one minute takes four the next.
// Tightening this would make the suite fail for reasons that have nothing to do with this code,
// which is the fastest way to teach people to ignore a red test.
const liveBudget = 10 * time.Minute

func liveLoop(t *testing.T, dir string, trust core.TrustLevel) (*agent.Loop, string) {
	t.Helper()

	keyName := os.Getenv("CANOPY_LIVE_KEY")
	if keyName == "" {
		t.Skip("set CANOPY_LIVE_KEY to the name of a stored credential to run live tests")
	}
	model := os.Getenv("CANOPY_LIVE_MODEL")

	store, err := keys.Open()
	if err != nil {
		t.Fatalf("opening the key store: %v", err)
	}
	client, _, err := session.NewKeyResolver(store).Resolve(keyName, model)
	if err != nil {
		t.Fatalf("resolving the credential: %v", err)
	}

	workspace, err := tools.OpenWorkspace(dir)
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	// The full set the running application gets, deliberately. A model choosing correctly from five
	// tools tells us much less than one choosing correctly from eleven, and the whole point of a
	// live test here is whether the descriptions are good enough to disambiguate.
	registry := core.NewToolRegistry()
	for _, tool := range tools.FileTools(workspace) {
		registry.MustRegister(tool)
	}
	for _, tool := range tools.GitTools(workspace) {
		registry.MustRegister(tool)
	}
	for _, tool := range tools.WebTools() {
		registry.MustRegister(tool)
	}
	registry.MustRegister(tools.ShellTool(workspace))

	return &agent.Loop{
		Client:    client,
		Tools:     registry,
		Trust:     trust,
		Grants:    permission.NewGrants(),
		Trail:     permission.NewTrail(),
		Approver:  agent.ApproverFunc(func(context.Context, permission.Request, permission.Decision) bool { return true }),
		AgentID:   "live",
		SessionID: "live",
		MaxSteps:  12,
	}, model
}

func TestLiveAgentReadsAFileAndAnswersFromIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.txt"),
		[]byte("timeout = 47\nretries = 3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loop, model := liveLoop(t, dir, core.TrustReadOnly)

	ctx, cancel := context.WithTimeout(context.Background(), liveBudget)
	defer cancel()

	outcome, err := loop.Run(ctx, core.Request{
		Model: model,
		Messages: []core.Message{{Role: core.RoleUser,
			Text: "Read config.txt in this directory and tell me the timeout value. " +
				"Answer with just the number."}},
	}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if outcome.Steps < 2 {
		t.Errorf("the turn took %d steps, so the model answered without reading the file", outcome.Steps)
	}

	// The answer has to have come from the file, which is the whole point: a model guessing would
	// not produce 47.
	last := outcome.Messages[len(outcome.Messages)-1]
	if !strings.Contains(last.Text, "47") {
		t.Errorf("the model did not answer from the file. Final message:\n%s\nSteps: %d",
			last.Text, outcome.Steps)
	}

	// And the trail records what it actually did, which is the question the audit exists for.
	counts := loop.Trail.Count("live")
	if counts.Total == 0 {
		t.Error("nothing was recorded in the audit trail")
	}
	if counts.ByTool["read_file"] == 0 {
		t.Errorf("the model reached for %v rather than read_file, which is a property of the tool "+
			"descriptions as much as of the code", counts.ByTool)
	}
}

// A read-only agent asked to change something has to be refused by the permission layer and has to
// carry on rather than crashing, which is the behaviour the model sees.
func TestLiveReadOnlyAgentIsRefusedAndCarriesOn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("original\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	loop, model := liveLoop(t, dir, core.TrustReadOnly)

	ctx, cancel := context.WithTimeout(context.Background(), liveBudget)
	defer cancel()

	if _, err := loop.Run(ctx, core.Request{
		Model: model,
		Messages: []core.Message{{Role: core.RoleUser,
			Text: "Change notes.txt so it says 'changed' instead of 'original'."}},
	}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(content), "changed") {
		t.Fatal("a read-only agent changed a file")
	}
}

// The end to end case: an agent that writes a file and then proves it worked.
func TestLiveAgentWritesAFile(t *testing.T) {
	dir := t.TempDir()
	loop, model := liveLoop(t, dir, core.TrustConfined)

	ctx, cancel := context.WithTimeout(context.Background(), liveBudget)
	defer cancel()

	if _, err := loop.Run(ctx, core.Request{
		Model: model,
		Messages: []core.Message{{Role: core.RoleUser,
			Text: "Create a file called hello.txt in this directory containing exactly the " +
				"word: chrysanthemum"}},
	}, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("the agent did not create the file: %v. Trail: %+v",
			err, loop.Trail.Count("live").ByTool)
	}
	if !strings.Contains(strings.ToLower(string(content)), "chrysanthemum") {
		t.Errorf("the file says %q", content)
	}
}
