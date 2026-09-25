package lsp

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/childenv"
)

// server is a language server Canopy knows how to start, by the files it serves.
type server struct {
	name       string
	argv       []string
	languageID func(ext string) string
	exts       []string
}

// known are the servers looked for on PATH, first found wins for an extension.
var known = []server{
	{name: "gopls", argv: []string{"gopls"}, exts: []string{".go"}, languageID: fixed("go")},
	{name: "typescript-language-server", argv: []string{"typescript-language-server", "--stdio"},
		exts: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}, languageID: scriptID},
	{name: "basedpyright-langserver", argv: []string{"basedpyright-langserver", "--stdio"},
		exts: []string{".py"}, languageID: fixed("python")},
	{name: "pyright-langserver", argv: []string{"pyright-langserver", "--stdio"},
		exts: []string{".py"}, languageID: fixed("python")},
	{name: "rust-analyzer", argv: []string{"rust-analyzer"}, exts: []string{".rs"}, languageID: fixed("rust")},
	{name: "clangd", argv: []string{"clangd"}, exts: []string{".c", ".h", ".cc", ".cpp", ".hpp"},
		languageID: func(ext string) string {
			if ext == ".c" || ext == ".h" {
				return "c"
			}
			return "cpp"
		}},
}

func fixed(id string) func(string) string { return func(string) string { return id } }

func scriptID(ext string) string {
	switch ext {
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".jsx":
		return "javascriptreact"
	}
	return "javascript"
}

// Manager starts servers for one workspace as files of their language are edited, and keeps them.
type Manager struct {
	root    string
	wait    time.Duration
	look    func(string) (string, error)
	servers []server

	mu       sync.Mutex
	clients  map[string]*Client
	failed   map[string]bool
	timeouts map[string]int
}

// NewManager serves the workspace at root. Nothing starts until a file is checked.
func NewManager(root string) *Manager {
	return &Manager{root: root, wait: 8 * time.Second, look: exec.LookPath, servers: known,
		clients: map[string]*Client{}, failed: map[string]bool{}, timeouts: map[string]int{}}
}

// maxShown bounds what one check adds to a tool result.
const maxShown = 20

// Check reports the errors and warnings in a file just written, as lines for a tool result, or ""
// when there is nothing to say or no server for the file. A server that cannot start is not tried
// again, and its absence is never an error: diagnostics are a help, not a gate.
func (m *Manager) Check(ctx context.Context, path, content string) string {
	ext := strings.ToLower(filepath.Ext(path))
	srv, client := m.clientFor(ctx, ext)
	if client == nil {
		return ""
	}
	diags, err := client.Check(ctx, path, srv.languageID(ext), content, m.wait)
	m.mu.Lock()
	if err != nil {
		// A server still indexing a large project can take minutes to answer, and every edit would
		// wait for it; twice in a row, and it is left alone for the rest of the session.
		m.timeouts[srv.name]++
		if m.timeouts[srv.name] >= 2 {
			m.failed[srv.name] = true
		}
		m.mu.Unlock()
		return ""
	}
	m.timeouts[srv.name] = 0
	m.mu.Unlock()
	var shown []Diagnostic
	for _, d := range diags {
		if d.Severity == SeverityError || d.Severity == SeverityWarning {
			shown = append(shown, d)
		}
	}
	if len(shown) == 0 {
		return fmt.Sprintf("%s reports no problems in this file.", srv.name)
	}
	sort.SliceStable(shown, func(i, j int) bool { return shown[i].Severity < shown[j].Severity })
	rel, _ := filepath.Rel(m.root, path)
	var b strings.Builder
	fmt.Fprintf(&b, "%s reports:\n", srv.name)
	for i, d := range shown {
		if i == maxShown {
			fmt.Fprintf(&b, "... and %d more\n", len(shown)-i)
			break
		}
		kind := "error"
		if d.Severity == SeverityWarning {
			kind = "warning"
		}
		fmt.Fprintf(&b, "%s:%d:%d: %s: %s\n", filepath.ToSlash(rel), d.Line, d.Column, kind,
			strings.ReplaceAll(strings.TrimSpace(d.Message), "\n", " "))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Manager) clientFor(ctx context.Context, ext string) (server, *Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, srv := range m.servers {
		if !contains(srv.exts, ext) || m.failed[srv.name] {
			continue
		}
		if c, ok := m.clients[srv.name]; ok {
			return srv, c
		}
		if _, err := m.look(srv.argv[0]); err != nil {
			continue
		}
		// Secrets are kept from a server as from any other child: it runs the repository's toolchain.
		c, err := Start(ctx, srv.argv, m.root, childenv.Inherited())
		if err != nil {
			m.failed[srv.name] = true
			continue
		}
		m.clients[srv.name] = c
		return srv, c
	}
	return server{}, nil
}

// Close stops every server started.
func (m *Manager) Close() {
	m.mu.Lock()
	clients := m.clients
	m.clients = map[string]*Client{}
	m.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
