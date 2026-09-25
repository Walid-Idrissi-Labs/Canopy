package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as a fake language server when LSP_FAKE_SERVER is set: it answers initialize,
// asks the client a question of its own, and reports an error on every line containing BROKEN.
func TestMain(m *testing.M) {
	if os.Getenv("LSP_FAKE_SERVER") == "1" {
		fakeServer()
		return
	}
	os.Exit(m.Run())
}

func fakeServer() {
	r := bufio.NewReader(os.Stdin)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Printf("Content-Length: %d\r\n\r\n%s", len(b), b)
	}
	for {
		body, err := readFrame(r)
		if err != nil {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
					Text    string `json:"text"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text string `json:"text"`
				} `json:"contentChanges"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &m)
		switch m.Method {
		case "initialize":
			// A question of the server's own first, which a client must answer or the server waits.
			send(map[string]any{"jsonrpc": "2.0", "id": 900, "method": "workspace/configuration",
				"params": map[string]any{"items": []any{}}})
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"capabilities": map[string]any{}}})
		case "shutdown":
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": nil})
		case "exit":
			return
		case "textDocument/didOpen", "textDocument/didChange":
			if os.Getenv("LSP_FAKE_SILENT") == "1" {
				continue
			}
			text := m.Params.TextDocument.Text
			if len(m.Params.ContentChanges) > 0 {
				text = m.Params.ContentChanges[0].Text
			}
			var diags []any
			for i, line := range strings.Split(text, "\n") {
				if col := strings.Index(line, "BROKEN"); col >= 0 {
					diags = append(diags, map[string]any{
						"range":    map[string]any{"start": map[string]any{"line": i, "character": col}},
						"severity": 1, "message": "undefined: BROKEN", "source": "fake"})
				}
			}
			send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
				"params": map[string]any{"uri": m.Params.TextDocument.URI, "version": m.Params.TextDocument.Version,
					"diagnostics": diags}})
		}
	}
}

func fakeManager(t *testing.T) (*Manager, string) {
	t.Helper()
	t.Setenv("LSP_FAKE_SERVER", "1")
	root, _ := filepath.EvalSymlinks(t.TempDir())
	m := NewManager(root)
	m.servers = []server{{name: "fakels", argv: []string{os.Args[0]}, exts: []string{".fake"}, languageID: fixed("fake")}}
	m.wait = 5 * time.Second
	t.Cleanup(m.Close)
	return m, root
}

// An edit that breaks a file is reported with where and what, and fixing it clears the report.
func TestAnEditIsCheckedByTheLanguageServer(t *testing.T) {
	m, root := fakeManager(t)
	path := filepath.Join(root, "dir with space", "a.fake")
	got := m.Check(context.Background(), path, "fine\nx := BROKEN\n")
	if !strings.Contains(got, "dir with space/a.fake:2:6: error: undefined: BROKEN") {
		t.Fatalf("the report is %q", got)
	}
	if got := m.Check(context.Background(), path, "fine\nx := 1\n"); !strings.Contains(got, "no problems") {
		t.Fatalf("the fixed file still reports %q", got)
	}
}

// A file no server handles, or a server that is not installed, adds nothing and fails nothing.
func TestNoServerMeansNothingToSay(t *testing.T) {
	m, root := fakeManager(t)
	if got := m.Check(context.Background(), filepath.Join(root, "notes.txt"), "BROKEN"); got != "" {
		t.Fatalf("a file with no server got %q", got)
	}
	m.servers[0].argv = []string{"no-such-language-server"}
	m.servers[0].exts = []string{".other"}
	if got := m.Check(context.Background(), filepath.Join(root, "a.other"), "BROKEN"); got != "" {
		t.Fatalf("a missing server got %q", got)
	}
}

// A server that never answers is waited for twice and then left alone, so a large project still
// indexing does not slow every edit for the rest of the session.
func TestASilentServerIsDroppedAfterTwoTimeouts(t *testing.T) {
	t.Setenv("LSP_FAKE_SILENT", "1")
	m, root := fakeManager(t)
	m.wait = 200 * time.Millisecond
	path := filepath.Join(root, "a.fake")
	for i := 0; i < 2; i++ {
		if got := m.Check(context.Background(), path, "BROKEN"); got != "" {
			t.Fatalf("a silent server produced %q", got)
		}
	}
	start := time.Now()
	_ = m.Check(context.Background(), path, "BROKEN")
	if waited := time.Since(start); waited > 100*time.Millisecond {
		t.Fatalf("the third edit waited %v on a server that never answers", waited)
	}
}
