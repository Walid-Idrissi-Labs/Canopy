package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
			// A question of the server's own first, which a client must answer: like a real server,
			// this one does not finish initializing until it has.
			send(map[string]any{"jsonrpc": "2.0", "id": 900, "method": "workspace/configuration",
				"params": map[string]any{"items": []any{}}})
			for {
				answer, err := readFrame(r)
				if err != nil {
					return
				}
				var reply struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				_ = json.Unmarshal(answer, &reply)
				if string(reply.ID) == "900" && reply.Method == "" {
					break
				}
			}
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"capabilities": map[string]any{}}})
			if os.Getenv("LSP_FAKE_STALL") == "1" {
				// Stops reading altogether, as a hung server does.
				select {}
			}
		case "textDocument/definition":
			// The definition of anything is line 1 of the same file; a list of links, as some servers send.
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": []any{map[string]any{
				"targetUri":            m.Params.TextDocument.URI,
				"targetSelectionRange": map[string]any{"start": map[string]any{"line": 0, "character": 5}}}}})
		case "textDocument/references":
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": []any{
				map[string]any{"uri": m.Params.TextDocument.URI, "range": map[string]any{"start": map[string]any{"line": 2, "character": 1}}},
				map[string]any{"uri": "file:///usr/lib/elsewhere.fake", "range": map[string]any{"start": map[string]any{"line": 9, "character": 0}}},
			}})
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
			if os.Getenv("LSP_FAKE_STALE") == "1" && m.Params.TextDocument.Version > 1 {
				// A late report about the previous version, which the client must not take for this one.
				send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
					"params": map[string]any{"uri": m.Params.TextDocument.URI, "version": m.Params.TextDocument.Version - 1,
						"diagnostics": []any{map[string]any{"range": map[string]any{"start": map[string]any{"line": 0, "character": 0}},
							"severity": 1, "message": "stale: from the previous version"}}}})
			}
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

// Definitions and references come back as places: inside the workspace with their line of code,
// outside it by path only. A symbol not on the line given is said so rather than guessed.
func TestFindingDefinitionsAndReferences(t *testing.T) {
	m, root := fakeManager(t)
	path := filepath.Join(root, "a.fake")
	content := "func HandleThing()\n\n HandleThing()\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	def, err := m.Find(context.Background(), "definition", path, content, 3, "HandleThing")
	if err != nil || def != "a.fake:1:6  func HandleThing()" {
		t.Fatalf("definition = %q, %v", def, err)
	}
	refs, err := m.Find(context.Background(), "references", path, content, 1, "HandleThing")
	outside := strings.Contains(refs, "/usr/lib/elsewhere.fake:10:1") && !strings.Contains(refs, "elsewhere.fake:10:1  ")
	if err != nil || !strings.Contains(refs, "a.fake:3:2  HandleThing()") || !outside {
		t.Fatalf("references = %q, %v", refs, err)
	}
	if _, err := m.Find(context.Background(), "definition", path, content, 2, "HandleThing"); err == nil {
		t.Fatal("a symbol not on the line was looked up anyway")
	}
	if got := wordIndex("xHandleThing HandleThing", "HandleThing"); got != 13 {
		t.Fatalf("wordIndex matched inside a longer word: %d", got)
	}
}

// A server that stops reading cannot hang an edit, or Canopy's exit: sends give up at their
// deadline, and closing does not wait on it.
func TestAStalledServerDoesNotHangTheEdit(t *testing.T) {
	t.Setenv("LSP_FAKE_STALL", "1")
	m, root := fakeManager(t)
	m.wait = 300 * time.Millisecond
	big := strings.Repeat("x := 1\n", 200000)
	start := time.Now()
	for i := 0; i < 3; i++ {
		_ = m.Check(context.Background(), filepath.Join(root, "a.fake"), big)
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Fatalf("edits waited %v on a server that stopped reading", waited)
	}
	start = time.Now()
	m.Close()
	if waited := time.Since(start); waited > 6*time.Second {
		t.Fatalf("closing waited %v", waited)
	}
}

// A late report about an earlier version of the file does not replace the report for the current one.
func TestAReportForAnOlderVersionIsIgnored(t *testing.T) {
	t.Setenv("LSP_FAKE_STALE", "1")
	m, root := fakeManager(t)
	path := filepath.Join(root, "a.fake")
	_ = m.Check(context.Background(), path, "BROKEN")
	if got := m.Check(context.Background(), path, "fine"); strings.Contains(got, "stale") || !strings.Contains(got, "no problems") {
		t.Fatalf("a report for the previous version was taken for the current one: %q", got)
	}
}

// Servers nobody has used for a while are stopped, and started again when a file needs them.
func TestIdleServersAreStoppedAndRestarted(t *testing.T) {
	m, root := fakeManager(t)
	path := filepath.Join(root, "a.fake")
	_ = m.Check(context.Background(), path, "fine")
	m.reapIdle(time.Now().Add(m.idle + time.Minute))
	m.mu.Lock()
	running := len(m.clients)
	m.mu.Unlock()
	if running != 0 {
		t.Fatalf("%d servers still running after the idle time", running)
	}
	if got := m.Check(context.Background(), path, "BROKEN"); !strings.Contains(got, "undefined: BROKEN") {
		t.Fatalf("the server was not started again: %q", got)
	}
}

// A server the sandbox cannot confine is not started at all.
func TestAServerThatCannotBeConfinedIsNotStarted(t *testing.T) {
	m, root := fakeManager(t)
	m.SetWrap(func([]string) ([]string, error) { return nil, errors.New("no sandbox here") })
	if got := m.Check(context.Background(), filepath.Join(root, "a.fake"), "BROKEN"); got != "" {
		t.Fatalf("an unconfined server ran: %q", got)
	}
}
