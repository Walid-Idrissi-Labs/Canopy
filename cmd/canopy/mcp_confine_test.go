package main

import (
	"os"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
)

// A local server is started in the sandbox unless the project says it must not be; a remote one
// starts nothing, so there is nothing to confine.
func TestLocalServersAreConfinedUnlessTheProjectSaysOtherwise(t *testing.T) {
	if err := sandbox.Available(); err != nil || sandbox.Disabled() {
		if os.Getenv("CANOPY_REQUIRE_SANDBOX") != "" {
			t.Fatal("no sandbox")
		}
		t.Skip("no sandbox here")
	}
	specs := mcpSpecs(t.TempDir(), config.Project{MCP: []config.MCPServer{
		{Name: "files", Command: "npx", Args: []string{"server"}},
		{Name: "docker", Command: "docker", Unconfined: true},
		{Name: "remote", URL: "https://mcp.example.com/mcp"},
	}})
	if len(specs) != 3 || specs[0].Sandbox == nil || specs[1].Sandbox != nil || specs[2].Sandbox != nil {
		t.Fatalf("confined: files %v, docker %v, remote %v", specs[0].Sandbox != nil, specs[1].Sandbox != nil,
			specs[2].Sandbox != nil)
	}
}
