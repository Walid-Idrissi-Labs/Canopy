package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// A confined server's npx and uv install into Canopy's own directories, which are writable for it;
// the person's own, which their next npx or uv tool run uses outside the sandbox, stay closed.
func TestConfinedServersInstallIntoDirectoriesOfTheirOwn(t *testing.T) {
	if err := sandbox.Available(); err != nil || sandbox.Disabled() {
		if os.Getenv("CANOPY_REQUIRE_SANDBOX") != "" {
			t.Fatal("no sandbox")
		}
		t.Skip("no sandbox here")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	specs := mcpSpecs(t.TempDir(), config.Project{MCP: []config.MCPServer{{Name: "files", Command: "npx"}}})
	if len(specs) != 1 || specs[0].Sandbox == nil {
		t.Fatal("the server was not confined")
	}
	cache, _ := os.UserCacheDir()
	env := map[string]string{}
	for _, kv := range specs[0].Env {
		name, value, _ := strings.Cut(kv, "=")
		env[name] = value
	}
	for _, name := range []string{"npm_config_cache", "UV_CACHE_DIR", "UV_TOOL_DIR", "UV_TOOL_BIN_DIR", "UV_PYTHON_INSTALL_DIR"} {
		dir := env[name]
		if !strings.HasPrefix(dir, filepath.Join(cache, "canopy", "mcp-servers")) {
			t.Errorf("%s is %q", name, dir)
			continue
		}
		if !slices.Contains(specs[0].Sandbox.Writable, dir) {
			t.Errorf("%s (%s) is not writable", name, dir)
		}
	}
	// Another project's server of the same name, and another server in this project, install apart.
	other := mcpSpecs(t.TempDir(), config.Project{MCP: []config.MCPServer{{Name: "files", Command: "npx"},
		{Name: "time", Command: "uvx"}}})
	for _, spec := range other {
		for _, kv := range spec.Env {
			if strings.HasPrefix(kv, "npm_config_cache=") && strings.TrimPrefix(kv, "npm_config_cache=") == env["npm_config_cache"] {
				t.Errorf("%s shares its npm directory with another project's server", spec.Name)
			}
		}
	}
	if other[0].Env[0] == other[1].Env[0] {
		t.Error("two servers of one project share an install directory")
	}
	for _, mine := range []string{".npm/_npx", ".local/share/uv", ".cache/uv", "Library/Caches/uv"} {
		if slices.Contains(specs[0].Sandbox.Writable, filepath.Join(home, mine)) {
			t.Errorf("the person's %s is writable", mine)
		}
	}
}
