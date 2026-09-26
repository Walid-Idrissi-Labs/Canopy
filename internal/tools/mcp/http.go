package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// rpc is what a session needs from its transport: calls with answers, notifications without, and a
// way to hang up. The stdio client and the HTTP one both provide it.
type rpc interface {
	call(ctx context.Context, method string, params any) (json.RawMessage, error)
	notify(method string, params any) error
	close()
}

// httpClient speaks the Streamable HTTP transport: each message is a POST, and the answer comes
// back as JSON or as a short event stream. A remote server has no process to start or stop.
type httpClient struct {
	url     string
	headers map[string]string
	http    *http.Client
	ids     atomic.Int64

	mu sync.Mutex
	// session is the Mcp-Session-Id the server gave at initialize, sent back with everything after.
	session string
	// version is the protocol version agreed at initialize, sent back as the spec requires.
	version string
}

func newHTTPClient(url string, headers map[string]string) *httpClient {
	return &httpClient{url: url, headers: headers, http: &http.Client{
		// A redirect is not followed: the server named in the configuration is the one trusted with
		// its headers and the model's arguments, and a redirect would send both somewhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// maxHTTPResponse bounds one answer, so a server cannot fill memory with one reply.
const maxHTTPResponse = 16 << 20

func (c *httpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.ids.Add(1)
	res, err := c.post(ctx, message{JSONRPC: "2.0", ID: encodeID(id), Method: method}, params)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return nil, fmt.Errorf("the server answered %s: %s", res.Status, oneLine(string(body)))
	}
	if method == "initialize" {
		if session := res.Header.Get("Mcp-Session-Id"); session != "" {
			c.mu.Lock()
			c.session = session
			c.mu.Unlock()
		}
	}

	mediaType, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	body := io.LimitReader(res.Body, maxHTTPResponse)
	var answer *message
	switch mediaType {
	case "text/event-stream":
		answer, err = c.fromStream(ctx, body, id)
	default:
		var m message
		if err = json.NewDecoder(body).Decode(&m); err == nil {
			answer = &m
		}
	}
	if err != nil {
		return nil, err
	}
	if answer == nil {
		return nil, errors.New("the server's reply held no answer to the call")
	}
	if answer.Error != nil {
		return nil, answer.Error
	}
	if method == "initialize" {
		var init struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(answer.Result, &init) == nil && init.ProtocolVersion != "" {
			c.mu.Lock()
			c.version = init.ProtocolVersion
			c.mu.Unlock()
		}
	}
	return answer.Result, nil
}

// fromStream reads an event stream until the answer with this id arrives. Requests the server makes
// along the way are refused, as over stdio: Canopy offers no capability a server may call back into.
func (c *httpClient) fromStream(ctx context.Context, body io.Reader, id int64) (*message, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxHTTPResponse)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			continue
		case line != "":
			continue
		}
		if data.Len() == 0 {
			continue
		}
		var m message
		err := json.Unmarshal([]byte(data.String()), &m)
		data.Reset()
		if err != nil {
			continue
		}
		if got, ok := ourID(m.ID); ok && got == id && m.Method == "" {
			return &m, nil
		}
		if m.Method != "" && len(m.ID) > 0 {
			refusal := message{JSONRPC: "2.0", ID: m.ID,
				Error: &rpcError{Code: methodNotFound, Message: "Canopy does not offer " + m.Method}}
			if res, err := c.post(ctx, refusal, nil); err == nil {
				_ = res.Body.Close()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("the server's event stream ended without an answer")
}

func (c *httpClient) notify(method string, params any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := c.post(ctx, message{JSONRPC: "2.0", Method: method}, params)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("the server answered %s", res.Status)
	}
	return nil
}

func (c *httpClient) post(ctx context.Context, m message, params any) (*http.Response, error) {
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		m.Params = raw
	}
	body, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	c.mu.Lock()
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	if c.version != "" {
		req.Header.Set("MCP-Protocol-Version", c.version)
	}
	c.mu.Unlock()
	return c.http.Do(req)
}

// close ends the session on the server, where it gave one.
func (c *httpClient) close() {
	c.mu.Lock()
	session := c.session
	c.mu.Unlock()
	if session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Mcp-Session-Id", session)
	for name, value := range c.headers {
		req.Header.Set(name, value)
	}
	if res, err := c.http.Do(req); err == nil {
		_ = res.Body.Close()
	}
}
