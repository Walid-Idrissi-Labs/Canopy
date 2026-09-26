package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A key is checked against the provider's free model list, with the header that provider reads,
// and a refusal, an endpoint with no list and an unreachable one are each told apart.
func TestAKeyIsCheckedAgainstTheFreeModelList(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/models" && r.Header.Get("x-api-key") == "sk-ant-good" &&
			r.Header.Get("anthropic-version") != "":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/compat/models" && r.Header.Get("Authorization") == "Bearer sk-good":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/nolist"):
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()
	t.Setenv("ANTHROPIC_BASE_URL", server.URL)

	store := memoryStore(t)
	put := func(name string, provider core.Provider, base, secret string) {
		if _, err := store.Put(core.KeyMetadata{Ref: core.KeyRef{Name: name, Provider: provider}, BaseURL: base},
			core.NewSecret(secret)); err != nil {
			t.Fatal(err)
		}
	}
	put("claude", core.ProviderAnthropic, "", "sk-ant-good")
	put("typo", core.ProviderAnthropic, "", "sk-ant-typo")
	put("kimi", core.ProviderOpenAICompatible, server.URL+"/compat/", "sk-good")
	put("local", core.ProviderOpenAICompatible, server.URL+"/nolist", "sk-x")
	put("gone", core.ProviderOpenAICompatible, "http://127.0.0.1:1", "sk-x")

	check := checkKey(store, server.Client())
	if r := check("claude"); !r.Accepted {
		t.Errorf("claude: %+v", r)
	}
	if r := check("typo"); !r.Refused || r.Note != "HTTP 401" {
		t.Errorf("typo: %+v", r)
	}
	if r := check("kimi"); !r.Accepted {
		t.Errorf("kimi: %+v (%v)", r, seen)
	}
	if r := check("local"); r.Accepted || r.Refused || !strings.Contains(r.Note, "no models") {
		t.Errorf("local: %+v", r)
	}
	if r := check("gone"); r.Accepted || r.Refused || strings.Contains(r.Note, "127.0.0.1") {
		t.Errorf("gone: %+v", r)
	}
	for _, request := range seen {
		if !strings.HasPrefix(request, "GET ") {
			t.Errorf("a check made %s, not a free GET", request)
		}
	}
}
