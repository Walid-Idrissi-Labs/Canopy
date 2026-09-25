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
	"sync/atomic"
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

// escalatingChat answers the first request with nothing useful, and a request saying the tests
// fail by writing the file the test wants.
func escalatingChat(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		last := body.Messages[len(body.Messages)-1]
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(v any) {
			b, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		}
		switch {
		case last.Role == "user" && strings.Contains(last.Content, "tests fail"):
			args, _ := json.Marshal(map[string]string{"path": "ok.txt", "content": "ok"})
			send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{
				map[string]any{"index": 0, "id": "c1", "type": "function",
					"function": map[string]any{"name": "write_file", "arguments": string(args)}}}}}}})
			send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}}})
		default:
			send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "done"}, "finish_reason": "stop"}}})
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

// With -verify the project's tests decide the exit code, and -escalate retries a red result at a
// higher effort until it passes or the attempts run out.
func TestARedRunIsEscalatedUntilItPasses(t *testing.T) {
	srv := escalatingChat(t)
	defer srv.Close()
	work := headlessHome(t, srv)
	config := `{"tests":[{"name":"has-ok","command":{"argv":["test","-f","ok.txt"]},"required":true}]}`
	if err := os.WriteFile(filepath.Join(work, "canopy.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	trustForTest(t, work)

	var out, errOut bytes.Buffer
	if code := runHeadless([]string{"-p", "make it pass", "-key", "fake", "-verify"},
		strings.NewReader(""), &out, &errOut); code != exitRed {
		t.Fatalf("a red result without escalation exited %d:\n%s", code, errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runHeadless([]string{"-p", "make it pass", "-key", "fake", "-verify", "-escalate", "1", "-effort", "low"},
		strings.NewReader(""), &out, &errOut); code != exitOK {
		t.Fatalf("escalation did not reach a passing result, exit %d:\n%s", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "trying again at medium effort") {
		t.Fatalf("the retry did not raise the effort:\n%s", errOut.String())
	}
}

// -verify with nothing to verify is refused before any money is spent: exiting 0 on a run nobody
// checked would read as green to the script that asked. So is -escalate without -verify.
func TestVerifyingWithNoTestsIsRefused(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer srv.Close()
	work := headlessHome(t, srv)
	// Configured but not trusted, so headless withholds it.
	config := `{"tests":[{"name":"never","command":{"argv":["false"]},"required":true}]}`
	if err := os.WriteFile(filepath.Join(work, "canopy.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-p", "x", "-key", "fake", "-verify"},
		{"-p", "x", "-key", "fake", "-escalate", "2"},
	} {
		var out, errOut bytes.Buffer
		if code := runHeadless(args, strings.NewReader(""), &out, &errOut); code != exitUsage {
			t.Errorf("%v exited %d:\n%s", args, code, errOut.String())
		}
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d requests were made before refusing", n)
	}
}

func TestEscalationRaisesTheDefaultEffort(t *testing.T) {
	if got := nextEffort(core.EffortDefault); got != core.EffortXHigh {
		t.Fatalf("the default steps to %s, which is where current models already sit", got)
	}
}
