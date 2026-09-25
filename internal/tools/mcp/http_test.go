package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// httpServer is a remote MCP server: JSON answers, an event stream for the tool list with a
// question of its own in the middle, a session id from initialize, and a record of what it saw.
type httpServer struct {
	mu       sync.Mutex
	sessions []string
	versions []string
	auth     []string
	refused  bool
	deleted  bool
}

func (h *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.sessions = append(h.sessions, r.Header.Get("Mcp-Session-Id"))
	h.versions = append(h.versions, r.Header.Get("MCP-Protocol-Version"))
	h.auth = append(h.auth, r.Header.Get("Authorization"))
	h.mu.Unlock()
	if r.Method == http.MethodDelete {
		h.mu.Lock()
		h.deleted = true
		h.mu.Unlock()
		return
	}
	body, _ := io.ReadAll(r.Body)
	var m message
	_ = json.Unmarshal(body, &m)
	answer := func(result any) {
		raw, _ := json.Marshal(result)
		out, _ := json.Marshal(message{JSONRPC: "2.0", ID: m.ID, Result: raw})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}
	switch {
	case m.Method == "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-42")
		answer(map[string]any{"protocolVersion": "2025-06-18", "serverInfo": map[string]any{"name": "remote"},
			"capabilities": map[string]any{"tools": map[string]any{}}})
	case m.Method == "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case m.Method == "tools/list":
		w.Header().Set("Content-Type", "text/event-stream")
		ask, _ := json.Marshal(message{JSONRPC: "2.0", ID: json.RawMessage(`"srv-1"`), Method: "roots/list"})
		raw, _ := json.Marshal(map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echoes",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}}}}})
		result, _ := json.Marshal(message{JSONRPC: "2.0", ID: m.ID, Result: raw})
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\nevent: message\ndata: %s\n\n", ask, result)
	case m.Method == "tools/call":
		var params struct {
			Arguments struct{ Text string } `json:"arguments"`
		}
		_ = json.Unmarshal(m.Params, &params)
		answer(map[string]any{"content": []any{map[string]any{"type": "text", "text": "echo: " + params.Arguments.Text}}})
	case m.Method == "" && m.Error != nil:
		h.mu.Lock()
		h.refused = true
		h.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

// A remote server over Streamable HTTP: the handshake, a tool list that arrives as an event stream
// with a server request refused on the way, a call, the session id carried after initialize, the
// headers on every request, and the session ended when Canopy is done.
func TestARemoteServerOverHTTP(t *testing.T) {
	h := &httpServer{}
	server := httptest.NewServer(h)
	defer server.Close()
	session, err := Connect(context.Background(), Spec{Name: "remote", URL: server.URL,
		Headers: map[string]string{"Authorization": "Bearer t0ken"}})
	if err != nil {
		t.Fatal(err)
	}
	tools := session.Tools()
	if len(tools) != 1 || !strings.Contains(tools[0].Name(), "echo") {
		t.Fatalf("tools = %v", tools)
	}
	result, err := tools[0].Run(context.Background(), json.RawMessage(`{"text":"hi"}`))
	if err != nil || !strings.Contains(result.Content, "echo: hi") {
		t.Fatalf("call = %+v, %v", result, err)
	}
	session.Close()

	h.mu.Lock()
	defer h.mu.Unlock()
	// Nothing before initialize has them; everything after it, each POST as well as the DELETE, does.
	if h.sessions[0] != "" || h.versions[0] != "" {
		t.Errorf("initialize carried a session or version: %v %v", h.sessions, h.versions)
	}
	for i := 1; i < len(h.sessions); i++ {
		if h.sessions[i] != "sess-42" {
			t.Errorf("request %d went without the session id: %v", i, h.sessions)
		}
	}
	for i := 1; i < len(h.versions)-1; i++ {
		if h.versions[i] != "2025-06-18" {
			t.Errorf("request %d went without the protocol version: %v", i, h.versions)
		}
	}
	for _, a := range h.auth {
		if a != "Bearer t0ken" {
			t.Errorf("a request went without the header: %v", h.auth)
			break
		}
	}
	if !h.refused {
		t.Error("the server's own request was never answered")
	}
	if !h.deleted {
		t.Error("the session was not ended")
	}
}

// A server that redirects is not followed: its headers and the model's arguments go only to the url
// the configuration names.
func TestARedirectIsNotFollowed(t *testing.T) {
	var reached bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()
	if _, err := Connect(context.Background(), Spec{Name: "r", URL: redirecting.URL,
		Headers: map[string]string{"Authorization": "Bearer t0ken"}}); err == nil {
		t.Fatal("a redirecting server was connected")
	}
	if reached {
		t.Fatal("the redirect was followed")
	}
}
