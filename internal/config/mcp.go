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
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/childenv"
)

// MCPServer is one Model Context Protocol server to connect to.
type MCPServer struct {
	// Name identifies the server and prefixes every tool it offers, so two servers can both provide
	// a tool called "search" and an audit trail can say which one ran.
	Name string `json:"name"`

	// Command and Args start it as a local program over stdio.
	Command string   `json:"command"`
	Args    []string `json:"args"`

	// URL instead reaches a remote server over the Streamable HTTP transport: https, or http to the
	// loopback address. Headers go with every request, and ${NAME} in a value is replaced from the
	// environment Canopy started in, so a token is named here and never written here.
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`

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
		case server.Command == "" && server.URL == "":
			return fmt.Errorf("the MCP server %q has no command or url, so there is nothing to reach",
				server.Name)
		case server.Command != "" && server.URL != "":
			return fmt.Errorf("the MCP server %q has both a command and a url; it is one or the other",
				server.Name)
		case server.URL != "" && !usableURL(server.URL):
			return fmt.Errorf("the MCP server %q's url must be https, or http to localhost: %q",
				server.Name, server.URL)
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

// usableURL reports whether a server URL keeps what is sent to it private in transit: https, or
// plain http only to this machine.
func usableURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	}
	return false
}

// ExpandedHeaders is Headers with ${NAME} replaced from the environment. It also returns the names
// that were not set, so a missing token is said rather than sent empty, and the names it refused:
// a general credential (a code host's, a cloud's, a model provider's) is never sent to a server a
// repository names, whatever the file says.
func (s MCPServer) ExpandedHeaders() (headers map[string]string, missing, refused []string) {
	headers = make(map[string]string, len(s.Headers))
	for name, value := range s.Headers {
		headers[name] = os.Expand(value, func(key string) string {
			if childenv.WellKnown(key) {
				refused = append(refused, key)
				return ""
			}
			v, ok := os.LookupEnv(key)
			if !ok {
				missing = append(missing, key)
			}
			return v
		})
	}
	return headers, missing, refused
}

// HeaderVariables names the environment variables the headers read, for the trust prompt.
func (s MCPServer) HeaderVariables() []string {
	var names []string
	for _, value := range s.Headers {
		os.Expand(value, func(key string) string {
			names = append(names, key)
			return ""
		})
	}
	sort.Strings(names)
	return names
}
