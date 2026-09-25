package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// headlessHome points every piece of state Canopy keeps at a temporary directory and stores one
// key named fake that answers from server.
func headlessHome(t *testing.T, server *httptest.Server) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv(keys.BackendEnvVar, "file")
	t.Setenv(session.PathEnvVar, filepath.Join(home, "history.db"))
	t.Setenv("CANOPY_TRUST_FILE", filepath.Join(home, "trust.json"))
	store, err := openKeyStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(core.KeyMetadata{
		Ref:     core.KeyRef{Name: "fake", Provider: core.ProviderOpenAICompatible},
		BaseURL: server.URL, Model: "fake-model",
	}, core.NewSecret("sk-fake")); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	return work
}

func fakeChat(reply string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"content": reply}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 12, "completion_tokens": 3},
		})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
}

// A headless run takes a prompt, runs the agent, prints the result in the format asked for, and
// exits zero when the turn completed.
func TestAHeadlessRunPrintsTheResult(t *testing.T) {
	srv := fakeChat("the answer is 42")
	defer srv.Close()
	headlessHome(t, srv)

	var out, errOut bytes.Buffer
	code := runHeadless([]string{"-p", "what is it", "-key", "fake", "-output", "json"},
		strings.NewReader(""), &out, &errOut)
	if code != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if result["text"] != "the answer is 42" || result["state"] != string(core.TurnComplete) {
		t.Fatalf("result = %v", result)
	}
}

func TestAHeadlessRunReadsThePromptFromStdin(t *testing.T) {
	srv := fakeChat("ok")
	defer srv.Close()
	headlessHome(t, srv)
	var out, errOut bytes.Buffer
	if code := runHeadless([]string{"-key", "fake"}, strings.NewReader("from stdin\n"), &out, &errOut); code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("text output = %q", out.String())
	}
}

func TestAHeadlessRunRefusesBadArguments(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runHeadless([]string{"-p", "x", "-output", "yaml"}, strings.NewReader(""), &out, &errOut); code != exitUsage {
		t.Fatalf("an unknown format gave exit %d", code)
	}
	if code := runHeadless([]string{"-p", "x", "-mode", "yolo"}, strings.NewReader(""), &out, &errOut); code != exitUsage {
		t.Fatalf("an unknown mode gave exit %d", code)
	}
}
