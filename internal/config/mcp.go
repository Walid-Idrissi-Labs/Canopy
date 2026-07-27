package config

// Which MCP servers this project uses.
//
// In the committed file rather than in a user level one, for the same reason the test commands are:
// a server is a program that gets started on your machine and handed to a model, so the list of
// them is exactly the kind of thing a reviewer should see in a diff. A server that arrives with a
// clone and starts itself without anyone reading the line that added it is the supply chain problem
// this project is supposed to be careful about.
//
// Credentials are deliberately not here. Env carries names and plain values for the ordinary case,
// and a server needing a secret should read it from the environment Canopy was started in. Putting
// a token in a committed file is a mistake the format should not make convenient.

import (
	"fmt"
	"strings"
	"time"
)

// MCPServer is one Model Context Protocol server to connect to.
type MCPServer struct {
	// Name identifies the server and prefixes every tool it offers, so two servers can both provide
	// a tool called "search" and an audit trail can say which one ran.
	Name string `json:"name"`

	// Command and Args start it. Stdio transport only in v0.1, so this is always a local program.
	Command string   `json:"command"`
	Args    []string `json:"args"`

	// Env is added to the environment the server starts with, as "KEY=value" entries.
	Env []string `json:"env"`

	// Timeout bounds the handshake and each call, as a duration string such as "30s". Empty means
	// the package default. A server that accepts a call and then goes quiet would otherwise hold a
	// turn open for as long as the model was willing to wait, which looks exactly like thinking.
	Timeout string `json:"timeout"`

	// Disabled keeps a server in the file without starting it, which is what people actually want
	// when a server is broken: commenting it out is not available, because JSON has no comments.
	Disabled bool `json:"disabled"`
}

// validateMCP checks what can be checked without starting anything.
func (p Project) validateMCP() error {
	seen := make(map[string]bool, len(p.MCP))
	for i, server := range p.MCP {
		switch {
		case server.Name == "":
			return fmt.Errorf("the MCP server at position %d has no name", i+1)
		case !validServerName(server.Name):
			// The name is not decoration. It becomes part of every tool name this server offers,
			// and a tool name reaches the provider, the transcript and the audit trail, so a name
			// with a space or a quote in it breaks all three a long way from here.
			return fmt.Errorf(
				"%q is not a usable MCP server name: use letters, digits, dashes and underscores",
				server.Name)
		case server.Command == "":
			return fmt.Errorf("the MCP server %q has no command, so there is nothing to start",
				server.Name)
		case seen[server.Name]:
			// Two servers with one name would collide on every tool they both offer, and the
			// registry refuses duplicates, so the second server would silently contribute nothing.
			return fmt.Errorf("two MCP servers are called %q", server.Name)
		}
		seen[server.Name] = true

		for _, entry := range server.Env {
			if !strings.Contains(entry, "=") {
				return fmt.Errorf("the MCP server %q has an environment entry with no value: %q",
					server.Name, entry)
			}
		}

		if _, err := parseDuration(server.Timeout); err != nil {
			return fmt.Errorf("the timeout on the MCP server %q: %w", server.Name, err)
		}
	}
	return nil
}

// validServerName is the same shape a key name has to be, and for the same reason: it travels.
func validServerName(name string) bool {
	if len(name) > 40 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// MCPTimeout returns a server's timeout as a duration, having already been validated.
func (s MCPServer) MCPTimeout() time.Duration {
	d, _ := parseDuration(s.Timeout)
	return d
}
