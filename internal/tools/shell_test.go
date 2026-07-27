package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// A command that floods is not rare. `npm ci` on a cold cache, a verbose build, a test suite with
// logging left on: all of them print megabytes, and every byte of a tool result travels twice, into
// the model's context and onto the screen. The bound belongs to exec, and this is the assertion that
// the tool passes the result through rather than reassembling it from somewhere unbounded.
func TestAFloodOfOutputReachesTheModelBoundedRatherThanWhole(t *testing.T) {
	w := testWorkspace(t)
	tool := ShellTool(w)

	input, err := json.Marshal(map[string]any{
		"command": `dd if=/dev/zero bs=1048576 count=8 2>/dev/null | tr '\0' x`,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(result.Content, "could not be run") {
		t.Skipf("dd or tr is not available here: %s", result.Content)
	}

	// The bound plus room for the sentence about what was dropped and the one about the exit status.
	if limit := exec.MaxOutputBytes + 400; len(result.Content) > limit {
		t.Errorf("the tool returned %d bytes for a command that printed eight megabytes, against a "+
			"%d byte limit", len(result.Content), exec.MaxOutputBytes)
	}
	// The gap has to be visible in the text, or a model reads the two surviving halves as adjacent
	// and answers about a file that never had those lines next to each other.
	if !strings.Contains(result.Content, "dropped") {
		t.Error("output was dropped and the result does not say so")
	}
}
