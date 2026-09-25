package config

import (
	"strings"
	"testing"
)

// A server is a command or a url, never both, and a url keeps what is sent to it private in transit.
func TestAnMCPServerURLIsCheckedBeforeAnythingIsSent(t *testing.T) {
	for _, c := range []struct {
		server MCPServer
		ok     bool
	}{
		{MCPServer{Name: "a", URL: "https://mcp.example.com/mcp"}, true},
		{MCPServer{Name: "a", URL: "http://localhost:8123/mcp"}, true},
		{MCPServer{Name: "a", URL: "http://127.0.0.1:8123/mcp"}, true},
		{MCPServer{Name: "a", URL: "http://mcp.example.com/mcp"}, false},
		{MCPServer{Name: "a", URL: "ftp://mcp.example.com"}, false},
		{MCPServer{Name: "a", URL: "https://"}, false},
		{MCPServer{Name: "a", Command: "npx", URL: "https://x.test"}, false},
		{MCPServer{Name: "a"}, false},
		{MCPServer{Name: "a", Command: "npx"}, true},
	} {
		server, ok := c.server, c.ok
		err := Project{MCP: []MCPServer{server}}.validateMCP()
		if (err == nil) != ok {
			t.Errorf("%+v: %v", server, err)
		}
	}
}

// A header names its token in the environment; a missing one is reported rather than sent empty.
func TestHeadersComeFromTheEnvironment(t *testing.T) {
	t.Setenv("CANOPY_TEST_TOKEN", "s3cret")
	server := MCPServer{Headers: map[string]string{"Authorization": "Bearer ${CANOPY_TEST_TOKEN}",
		"X-Other": "${CANOPY_TEST_UNSET_VALUE}"}}
	headers, missing := server.ExpandedHeaders()
	if headers["Authorization"] != "Bearer s3cret" {
		t.Fatalf("headers = %v", headers)
	}
	if len(missing) != 1 || !strings.Contains(missing[0], "UNSET") {
		t.Fatalf("missing = %v", missing)
	}
}
