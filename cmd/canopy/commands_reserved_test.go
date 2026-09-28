package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
)

// A global file with one reserved name still gives the others to the conversation.
func TestAReservedGlobalCommandLeavesTheOthersUsable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.json")
	if err := os.WriteFile(path, []byte(`{"commands": [
		{"name": "mouse", "description": "old", "prompt": "do it"},
		{"name": "deploy", "description": "ship it", "prompt": "deploy now"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.GlobalCommandsEnv, path)
	prompt, invoked, err := loadCommands(nil).Expand("/deploy")
	if err != nil || !invoked || prompt != "deploy now" {
		t.Fatalf("/deploy expanded to %q, %v, %v", prompt, invoked, err)
	}
}
