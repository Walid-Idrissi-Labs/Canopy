// Package lsp speaks enough of the Language Server Protocol to ask a language server what is wrong
// with a file just edited, so an agent hears about a type error in the same step that made it
// rather than several steps later from a failing build.
//
// A server is a program from the repository's own toolchain, started outside the sandbox, so it is
// only started for a repository the person has trusted.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Diagnostic is one problem a server reported.
type Diagnostic struct {
	Line, Column int
	Severity     int
	Message      string
	Source       string
}

// SeverityError and SeverityWarning are the protocol's severities that are worth an agent's time.
const (
	SeverityError   = 1
	SeverityWarning = 2
)

// Client is one running language server.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	root   string
	writeM sync.Mutex

	mu       sync.Mutex
	nextID   int
	pending  map[int]chan json.RawMessage
	diags    map[string][]Diagnostic
	arrived  map[string]int
	versions map[string]int
	changed  chan struct{}
	done     chan struct{}
}

// Start runs a server in root and completes the handshake.
func Start(ctx context.Context, argv []string, root string, env []string) (*Client, error) {
	if len(argv) == 0 {
		return nil, errors.New("no server command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = root
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{cmd: cmd, stdin: stdin, root: root, pending: map[int]chan json.RawMessage{},
		diags: map[string][]Diagnostic{}, arrived: map[string]int{}, versions: map[string]int{},
		changed: make(chan struct{}), done: make(chan struct{})}
	go c.read(bufio.NewReader(stdout))

	params := map[string]any{
		"processId": nil,
		"rootUri":   fileURI(root),
		"capabilities": map[string]any{
			"textDocument": map[string]any{"publishDiagnostics": map[string]any{"versionSupport": true}},
		},
		"workspaceFolders": []any{map[string]any{"uri": fileURI(root), "name": filepath.Base(root)}},
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := c.call(ctx, "initialize", params); err != nil {
		c.Close()
		return nil, fmt.Errorf("initializing %s: %w", argv[0], err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// Check sends a file's new content and waits for what the server says about it: until a report
// for this version arrives and nothing more follows for a moment, or until wait runs out.
func (c *Client) Check(ctx context.Context, path, languageID, content string, wait time.Duration) ([]Diagnostic, error) {
	uri := fileURI(path)
	c.mu.Lock()
	before := c.arrived[uri]
	c.mu.Unlock()
	if err := c.Sync(path, languageID, content); err != nil {
		return nil, err
	}

	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	var settle <-chan time.Time
	for {
		c.mu.Lock()
		got, changed := c.arrived[uri], c.changed
		c.mu.Unlock()
		if got > before && settle == nil {
			// A server may report a quick parse first and the type check after it.
			settle = time.After(300 * time.Millisecond)
		}
		select {
		case <-changed:
		case <-settle:
			return c.current(uri), nil
		case <-deadline.C:
			if got > before {
				return c.current(uri), nil
			}
			return nil, errors.New("the language server did not report in time")
		case <-c.done:
			return nil, errors.New("the language server stopped")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *Client) current(uri string) []Diagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Diagnostic(nil), c.diags[uri]...)
}

// Location is a place in a file a server pointed to.
type Location struct {
	Path         string
	Line, Column int
}

// Sync makes sure the server has a file's current content, opening it the first time.
func (c *Client) Sync(path, languageID, content string) error {
	uri := fileURI(path)
	c.mu.Lock()
	version := c.versions[uri] + 1
	c.versions[uri] = version
	c.mu.Unlock()
	if version == 1 {
		return c.notify("textDocument/didOpen", map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": languageID, "version": version, "text": content}})
	}
	return c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": uri, "version": version},
		"contentChanges": []any{map[string]any{"text": content}},
	})
}

// Locations asks where a symbol at a position is defined (textDocument/definition) or used
// (textDocument/references). line and column count from one.
func (c *Client) Locations(ctx context.Context, method, path string, line, column int) ([]Location, error) {
	params := map[string]any{
		"textDocument": map[string]any{"uri": fileURI(path)},
		"position":     map[string]any{"line": line - 1, "character": column - 1},
	}
	if method == "textDocument/references" {
		params["context"] = map[string]any{"includeDeclaration": false}
	}
	raw, err := c.call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	// A single location, a list of them, or a list of links, depending on the server.
	type position struct{ Line, Character int }
	type span struct {
		Start position `json:"start"`
	}
	var many []struct {
		URI            string `json:"uri"`
		Range          span   `json:"range"`
		TargetURI      string `json:"targetUri"`
		TargetSelRange span   `json:"targetSelectionRange"`
	}
	if json.Unmarshal(raw, &many) != nil {
		var one struct {
			URI   string `json:"uri"`
			Range span   `json:"range"`
		}
		if json.Unmarshal(raw, &one) != nil || one.URI == "" {
			return nil, nil
		}
		many = append(many, struct {
			URI            string `json:"uri"`
			Range          span   `json:"range"`
			TargetURI      string `json:"targetUri"`
			TargetSelRange span   `json:"targetSelectionRange"`
		}{URI: one.URI, Range: one.Range})
	}
	var out []Location
	for _, l := range many {
		uri, start := l.URI, l.Range.Start
		if l.TargetURI != "" {
			uri, start = l.TargetURI, l.TargetSelRange.Start
		}
		u, err := url.Parse(uri)
		if err != nil || u.Scheme != "file" {
			continue
		}
		out = append(out, Location{Path: filepath.FromSlash(u.Path), Line: start.Line + 1, Column: start.Character + 1})
	}
	return out, nil
}

// Close shuts the server down, politely and then not.
func (c *Client) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = c.call(ctx, "shutdown", nil)
	_ = c.notify("exit", nil)
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
}

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	reply := make(chan json.RawMessage, 1)
	c.pending[id] = reply
	c.mu.Unlock()
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case r := <-reply:
		return r, nil
	case <-c.done:
		return nil, errors.New("the language server stopped")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Client) notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) send(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeM.Lock()
	defer c.writeM.Unlock()
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

// read takes every message the server sends: replies to calls, published diagnostics, and requests
// it makes of the client, which are answered with an empty result so it never waits on us.
func (c *Client) read(r *bufio.Reader) {
	defer close(c.done)
	for {
		body, err := readFrame(r)
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch {
		case m.Method == "textDocument/publishDiagnostics":
			c.published(m.Params)
		case m.Method != "" && len(m.ID) > 0:
			_ = c.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": nil})
		case m.Method == "" && len(m.ID) > 0:
			id, err := strconv.Atoi(string(m.ID))
			if err != nil {
				continue
			}
			c.mu.Lock()
			reply := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if reply != nil {
				reply <- m.Result
			}
		}
	}
}

func (c *Client) published(raw json.RawMessage) {
	var p struct {
		URI         string `json:"uri"`
		Version     *int   `json:"version"`
		Diagnostics []struct {
			Range struct {
				Start struct{ Line, Character int } `json:"start"`
			} `json:"range"`
			Severity int    `json:"severity"`
			Message  string `json:"message"`
			Source   string `json:"source"`
		} `json:"diagnostics"`
	}
	if json.Unmarshal(raw, &p) != nil {
		return
	}
	uri := normalizeURI(p.URI)
	c.mu.Lock()
	defer c.mu.Unlock()
	// A report about an older version than the one last sent describes text that is gone.
	if p.Version != nil && *p.Version < c.versions[uri] {
		return
	}
	var list []Diagnostic
	for _, d := range p.Diagnostics {
		severity := d.Severity
		if severity == 0 {
			severity = SeverityError
		}
		list = append(list, Diagnostic{Line: d.Range.Start.Line + 1, Column: d.Range.Start.Character + 1,
			Severity: severity, Message: d.Message, Source: d.Source})
	}
	c.diags[uri] = list
	c.arrived[uri]++
	close(c.changed)
	c.changed = make(chan struct{})
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			if length, err = strconv.Atoi(strings.TrimSpace(value)); err != nil {
				return nil, err
			}
		}
	}
	if length < 0 || length > 64<<20 {
		return nil, errors.New("a frame without a usable length")
	}
	body := make([]byte, length)
	_, err := io.ReadFull(r, body)
	return body, err
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// normalizeURI puts a server's spelling of a file URI into ours, since servers differ in what they
// escape.
func normalizeURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return uri
	}
	return fileURI(u.Path)
}
