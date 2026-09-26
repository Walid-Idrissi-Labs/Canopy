package main

// Starting the configured MCP servers and handing their tools to the agent.
//
// This is the part of A8-06 that makes the rest of it reachable. The package under
// internal/tools/mcp was complete and tested and nothing called it, which meant every acceptance
// claim about third party tools being governed like built in ones was true of code no agent could
// ever run.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tools"
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
		headers, missing, refused := server.ExpandedHeaders()
		if len(refused) > 0 {
			fmt.Fprintf(os.Stderr, "warning: the MCP server %q is not connected: %s is a general "+
				"credential, which Canopy never sends to a server a repository names\n",
				server.Name, strings.Join(refused, ", "))
			continue
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "warning: the MCP server %q is not connected: %s is not set\n",
				server.Name, strings.Join(missing, ", "))
			continue
		}
		spec := mcp.Spec{
			Name:    server.Name,
			Command: server.Command,
			Args:    server.Args,
			Env:     server.Env,
			Dir:     dir,
			Timeout: server.MCPTimeout(),
			URL:     server.URL,
			Headers: headers,
		}
		// A local server runs the project's configuration, and often its code, so it runs in the
		// sandbox like everything else the project starts, unless the project says otherwise.
		if server.URL == "" && !server.Unconfined {
			spec.Sandbox, spec.SandboxEnv = tools.Confinement(dir)
			if spec.Sandbox != nil {
				// npx and uvx install the server before running it, into directories of Canopy's
				// own rather than the person's: theirs are what their own npx and uv run from later,
				// outside the sandbox.
				writable, env := mcpInstallDirs(dir, server.Name)
				spec.Sandbox.Writable = append(spec.Sandbox.Writable, writable...)
				spec.Env = append(env, spec.Env...)
			}
			// Said rather than done quietly: where there is no sandbox to run it in, it runs as it
			// did before, like every other command here, and the person should know.
			if spec.Sandbox == nil && !sandbox.Disabled() {
				fmt.Fprintf(os.Stderr, "warning: the MCP server %q runs outside the sandbox, which is "+
					"not available here\n", server.Name)
			}
		}
		specs = append(specs, spec)
	}
	return specs
}

// mcpInstallDirs are where a confined server's npx and uv install and cache things, under Canopy's
// cache directory, with the environment that points them there. Nothing outside a confined server
// runs from them. One set per project and server: shared, an untrusted repository's server could
// plant a package that a trusted project's server of the same name then runs with its credentials.
func mcpInstallDirs(project, server string) (writable, env []string) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, nil
	}
	if abs, err := filepath.Abs(project); err == nil {
		project = abs
	}
	sum := sha256.Sum256([]byte(project + "\x00" + server))
	root := filepath.Join(cache, "canopy", "mcp-servers", hex.EncodeToString(sum[:8]))
	dirs := map[string]string{
		"npm_config_cache":      "npm",
		"UV_CACHE_DIR":          "uv-cache",
		"UV_TOOL_DIR":           "uv-tools",
		"UV_TOOL_BIN_DIR":       "uv-bin",
		"UV_PYTHON_INSTALL_DIR": "uv-python",
	}
	for _, name := range []string{"npm_config_cache", "UV_CACHE_DIR", "UV_TOOL_DIR", "UV_TOOL_BIN_DIR", "UV_PYTHON_INSTALL_DIR"} {
		dir := filepath.Join(root, dirs[name])
		if err := os.MkdirAll(dir, 0o700); err != nil {
			continue
		}
		writable = append(writable, dir)
		env = append(env, name+"="+dir)
	}
	return writable, env
}
