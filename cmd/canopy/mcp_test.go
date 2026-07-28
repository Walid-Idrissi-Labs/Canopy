package main

// That a configured MCP server actually reaches an agent.
//
// The package under internal/tools/mcp has its own tests and they are thorough. What none of them
// could establish is the thing A8-06 promises, because it is not true inside that package: the
// deliverable is "expose their tools to agents", and for as long as nothing imported the package,
// every agent ran with exactly the tools it would have had if no server were configured at all.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// fakeServerScript is a stdio MCP server in as little shell as speaks the protocol.
//
// The ids are fixed rather than echoed because a fresh connection numbers its requests from one in a
// fixed order: initialize is 1, tools/list is 2. Reading them back out of the request would need a
// JSON parser, which is a lot of shell to prove something this test does not depend on.
const fakeServerScript = `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"ledger","version":"1.0"}}}'
      ;;
    *'"method":"tools/list"'*)
      printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"lookup","description":"Look up a record.","inputSchema":{"type":"object","properties":{"id":{"type":"string"}}}}]}}'
      ;;
  esac
done
`

// project writes a canopy.json with one MCP server in it and returns the directory.
func projectWithServer(t *testing.T) (string, config.Project) {
	t.Helper()

	dir := t.TempDir()
	script := filepath.Join(dir, "server.sh")
	if err := os.WriteFile(script, []byte(fakeServerScript), 0o700); err != nil {
		t.Fatalf("writing the fake server: %v", err)
	}

	return dir, config.Project{
		MCP: []config.MCPServer{{
			Name:    "ledger",
			Command: script,
			Timeout: "10s",
		}},
	}
}

// The deliverable. A server in the project's configuration ends up as a tool the agent can call.
func TestAConfiguredServersToolsReachTheAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake server is a shell script")
	}

	dir, project := projectWithServer(t)

	engine := session.New(nil)
	t.Cleanup(engine.Close)

	registry := core.NewToolRegistry()
	engine.WithTools(registry, core.TrustStandard, nil)

	stop := attachMCP(engine, dir, project)
	t.Cleanup(stop)

	tool, found := registry.Get("mcp__ledger__lookup")
	if !found {
		var names []string
		for _, registered := range registry.Tools() {
			names = append(names, registered.Name())
		}
		t.Fatalf("the server's tool never reached the agent. Registered: %v", names)
	}

	// Namespaced, so two servers can both offer "lookup" and an audit entry says which one ran.
	if !strings.Contains(tool.Description(), "ledger") {
		t.Errorf("the description does not say where it came from: %q", tool.Description())
	}

	// The load bearing one. A server's own opinion of its tool decides nothing, so whatever it says
	// about itself, the tool is governed as the broadest kind there is.
	if tool.Kind() != core.ToolExecute {
		t.Errorf("kind = %q, want %q: an opaque capability from a program we did not write is "+
			"exactly what execute means", tool.Kind(), core.ToolExecute)
	}
}

// A disabled server stays in the file and contributes nothing, which is what people want when a
// server is broken, because JSON has no comments to comment it out with.
func TestADisabledServerIsNotStarted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake server is a shell script")
	}

	dir, project := projectWithServer(t)
	project.MCP[0].Disabled = true

	if specs := mcpSpecs(dir, project); len(specs) != 0 {
		t.Fatalf("specs = %+v, want none", specs)
	}

	engine := session.New(nil)
	t.Cleanup(engine.Close)

	registry := core.NewToolRegistry()
	engine.WithTools(registry, core.TrustStandard, nil)

	stop := attachMCP(engine, dir, project)
	t.Cleanup(stop)

	if _, found := registry.Get("mcp__ledger__lookup"); found {
		t.Error("a disabled server was started anyway")
	}
}

// A server that cannot start degrades that server only. Asserted here as well as in the package,
// because this is the layer where "Canopy still opens" is actually true or not.
func TestAServerThatCannotStartDoesNotStopCanopy(t *testing.T) {
	dir := t.TempDir()
	project := config.Project{
		MCP: []config.MCPServer{{
			Name:    "missing",
			Command: filepath.Join(dir, "there-is-no-such-program"),
			Timeout: "5s",
		}},
	}

	engine := session.New(nil)
	t.Cleanup(engine.Close)

	registry := core.NewToolRegistry()
	engine.WithTools(registry, core.TrustStandard, nil)

	stop := attachMCP(engine, dir, project)
	t.Cleanup(stop)

	// The point is that we got here at all, with a registry that still works.
	if registry.Get("mcp__missing__anything"); len(registry.Tools()) != 0 {
		t.Errorf("a server that never started contributed %d tools", len(registry.Tools()))
	}
}

// The working directory is the project's, not whichever directory Canopy was launched from. A server
// pointed at somebody else's repository is worse than one that did not start.
func TestServersRunInTheProjectDirectory(t *testing.T) {
	dir, project := projectWithServer(t)

	specs := mcpSpecs(dir, project)
	if len(specs) != 1 {
		t.Fatalf("%d specs, want 1", len(specs))
	}
	if specs[0].Dir != dir {
		t.Errorf("Dir = %q, want %q", specs[0].Dir, dir)
	}
	if specs[0].Timeout.String() != "10s" {
		t.Errorf("Timeout = %s, want 10s: the configured value was dropped", specs[0].Timeout)
	}
}
