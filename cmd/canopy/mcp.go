package main

// Starting the configured MCP servers and handing their tools to the agent.
//
// This is the part of A8-06 that makes the rest of it reachable. The package under
// internal/tools/mcp was complete and tested and nothing called it, which meant every acceptance
// claim about third party tools being governed like built in ones was true of code no agent could
// ever run.

import (
	"context"
	"fmt"
	"os"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tools/mcp"
)

// attachMCP starts the configured servers and adds what they offer to the agent's tools.
//
// Returns a function that stops them. It is never nil, so the caller can defer it without asking
// whether anything was started.
//
// The tools go to the conversation's own registry and not to the one an isolated agent is built
// with. That is a deliberate limit rather than an oversight: an isolated agent is confined by having
// its file tools rooted at its worktree, and a server started once at the project root is not rooted
// anywhere near it. Handing those tools to a broad isolated agent would be a way out of the
// confinement that D-33 describes, through a tool Canopy cannot see inside. See D-38 and Q-18.
func attachMCP(engine *session.Engine, dir string, project config.Project) func() {
	nothing := func() {}

	specs := mcpSpecs(dir, project)
	if len(specs) == 0 {
		return nothing
	}

	registry, ok := engine.Tools()
	if !ok {
		// No registry means tools failed to attach at all, and that has already been reported.
		return nothing
	}

	set := mcp.ConnectAll(context.Background(), specs)

	// A server that could not be started is named. Silence here is the failure this whole package
	// guards against elsewhere: a server contributing nothing looks exactly like a server nobody
	// configured, and the user has no way to tell those apart without being told.
	for _, failure := range set.Failures {
		fmt.Fprintf(os.Stderr, "warning: the MCP server %q is not available: %v\n",
			failure.Server, failure.Err)
	}

	for _, server := range set.Sessions {
		if note := server.Incomplete(); note != "" {
			fmt.Fprintf(os.Stderr, "warning: the tool list from the MCP server %q is incomplete: %s\n",
				server.Name(), note)
		}
	}

	for _, tool := range set.Tools() {
		if err := registry.Register(tool); err != nil {
			// A name that collides with something already registered. Namespacing makes this
			// unlikely, and dropping the one tool is better than failing the server for it.
			fmt.Fprintf(os.Stderr, "warning: an MCP tool was not registered: %v\n", err)
		}
	}

	return set.Close
}

// mcpSpecs turns the committed configuration into something the client can start.
//
// The working directory is the project rather than the server's own choice, because a server started
// wherever Canopy happened to be launched from is a server pointed at somebody else's repository.
func mcpSpecs(dir string, project config.Project) []mcp.Spec {
	var specs []mcp.Spec
	for _, server := range project.MCP {
		if server.Disabled {
			continue
		}
		specs = append(specs, mcp.Spec{
			Name:    server.Name,
			Command: server.Command,
			Args:    server.Args,
			Env:     server.Env,
			Dir:     dir,
			Timeout: server.MCPTimeout(),
		})
	}
	return specs
}
