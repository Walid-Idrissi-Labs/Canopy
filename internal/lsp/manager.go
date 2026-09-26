package lsp

import (
	"context"
	"fmt"
	"os"
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
	// options are sent at initialize: settings that stop a server running the project's own code.
	options any
}

// ServerNames are the language servers Canopy looks for on PATH.
func ServerNames() []string {
	names := make([]string, 0, len(known))
	for _, s := range known {
		names = append(names, s.name)
	}
	return names
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
	// Build scripts and procedural macros are the project's own code, run by the server; off, so an
	// agent cannot have the server run something by writing it.
	{name: "rust-analyzer", argv: []string{"rust-analyzer"}, exts: []string{".rs"}, languageID: fixed("rust"),
		options: map[string]any{"cargo": map[string]any{"buildScripts": map[string]any{"enable": false}},
			"procMacro": map[string]any{"enable": false}}},
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
	// wrap turns a server's command into one confined by the sandbox, or refuses; nil runs it as is.
	wrap func(argv []string) ([]string, error)
	// idle is how long a server may go unused before it is stopped; started again when needed.
	idle time.Duration

	mu       sync.Mutex
	clients  map[string]*Client
	lastUsed map[string]time.Time
	failed   map[string]bool
	timeouts map[string]int
	stop     chan struct{}
	stopOnce sync.Once
}

// NewManager serves the workspace at root. Nothing starts until a file is checked.
func NewManager(root string) *Manager {
	m := &Manager{root: root, wait: 8 * time.Second, look: exec.LookPath, servers: known,
		idle: 10 * time.Minute, clients: map[string]*Client{}, lastUsed: map[string]time.Time{},
		failed: map[string]bool{}, timeouts: map[string]int{}, stop: make(chan struct{})}
	go m.reap()
	return m
}

// SetWrap confines every server this manager starts; see Manager.wrap.
func (m *Manager) SetWrap(wrap func(argv []string) ([]string, error)) { m.wrap = wrap }

// reap stops servers nobody has used for a while: a worktree that was removed, or a language
// touched once, would otherwise keep a server running until Canopy exits.
func (m *Manager) reap() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.reapIdle(time.Now())
		}
	}
}

func (m *Manager) reapIdle(now time.Time) {
	m.mu.Lock()
	var stale []*Client
	for name, c := range m.clients {
		if now.Sub(m.lastUsed[name]) >= m.idle {
			stale = append(stale, c)
			delete(m.clients, name)
		}
	}
	m.mu.Unlock()
	for _, c := range stale {
		c.Close()
	}
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
			m.lastUsed[srv.name] = time.Now()
			return srv, c
		}
		path, err := m.look(srv.argv[0])
		if err != nil {
			continue
		}
		argv := append([]string{path}, srv.argv[1:]...)
		if m.wrap != nil {
			if argv, err = m.wrap(argv); err != nil {
				m.failed[srv.name] = true
				continue
			}
		}
		// Secrets are kept from a server as from any other child: it runs the repository's toolchain.
		c, err := Start(ctx, argv, m.root, childenv.Inherited(), srv.options)
		if err != nil {
			m.failed[srv.name] = true
			continue
		}
		m.clients[srv.name] = c
		m.lastUsed[srv.name] = time.Now()
		return srv, c
	}
	return server{}, nil
}

// Find answers where a symbol on a line of a file is defined, or where it is used, as lines for a
// tool result. kind is "definition" or "references". Places inside the workspace are shown with
// their line of code; places outside it, a standard library for instance, by path only.
func (m *Manager) Find(ctx context.Context, kind, path, content string, line int, symbol string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	srv, client := m.clientFor(ctx, ext)
	if client == nil {
		return "", fmt.Errorf("no language server for %s files is running or installed", ext)
	}
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return "", fmt.Errorf("line %d is outside the file, which has %d lines", line, len(lines))
	}
	column := wordIndex(lines[line-1], symbol)
	if column < 0 {
		return "", fmt.Errorf("%q is not on line %d", symbol, line)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := client.Sync(ctx, path, srv.languageID(ext), content); err != nil {
		return "", err
	}
	method := "textDocument/definition"
	if kind == "references" {
		method = "textDocument/references"
	}
	found, err := client.Locations(ctx, method, path, line, column+1)
	if err != nil {
		return "", fmt.Errorf("%s did not answer: %w", srv.name, err)
	}
	if len(found) == 0 {
		return fmt.Sprintf("%s found no %s for %s.", srv.name, kind, symbol), nil
	}
	var b strings.Builder
	for i, l := range found {
		if i == 40 {
			fmt.Fprintf(&b, "... and %d more\n", len(found)-i)
			break
		}
		rel, err := filepath.Rel(m.root, l.Path)
		if err != nil || strings.HasPrefix(rel, "..") {
			fmt.Fprintf(&b, "%s:%d:%d\n", l.Path, l.Line, l.Column)
			continue
		}
		fmt.Fprintf(&b, "%s:%d:%d  %s\n", filepath.ToSlash(rel), l.Line, l.Column, lineOf(m.root, l.Path, l.Line))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// wordIndex is where symbol first stands as a whole word in line, or -1.
func wordIndex(line, symbol string) int {
	for from := 0; symbol != ""; {
		i := strings.Index(line[from:], symbol)
		if i < 0 {
			return -1
		}
		i += from
		end := i + len(symbol)
		if (i == 0 || !wordByte(line[i-1])) && (end == len(line) || !wordByte(line[end])) {
			return i
		}
		from = i + 1
	}
	return -1
}

func wordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// lineOf is one line of a file inside root, trimmed, for showing a place in context. A path that
// resolves outside root, through a symlink for instance, shows nothing.
func lineOf(root, path string, n int) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	if rel, err := filepath.Rel(root, resolved); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if n < 1 || n > len(lines) {
		return ""
	}
	text := strings.TrimSpace(lines[n-1])
	if len(text) > 160 {
		text = text[:157] + "..."
	}
	return text
}

// Close stops every server started.
func (m *Manager) Close() {
	m.stopOnce.Do(func() { close(m.stop) })
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
