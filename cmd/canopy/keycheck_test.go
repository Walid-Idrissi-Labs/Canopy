package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
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

	if _, err := store.PutSignIn(core.KeyMetadata{Ref: core.KeyRef{Name: "signed", Provider: core.ProviderOpenAICompatible},
		BaseURL: server.URL + "/compat/"}, keys.SignIn{Kind: keys.KindSignedIn, Account: "someone"},
		keys.Tokens{Access: core.NewSecret("gho_x")}); err != nil {
		t.Fatal(err)
	}

	check := checkKey(store, server.Client())
	before := len(seen)
	if r := check("signed"); r.Accepted || r.Refused || !strings.Contains(r.Note, "sign-in") || len(seen) != before {
		t.Errorf("a sign-in was checked again: %+v", r)
	}
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
	// A redirect is not followed, so the key never reaches the host it names.
	var leaked []string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = append(leaked, r.Header.Get("x-api-key"))
		w.WriteHeader(http.StatusOK)
	}))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()
	t.Setenv("ANTHROPIC_BASE_URL", redirecting.URL)
	if r := check("claude"); r.Accepted || r.Refused || !strings.Contains(r.Note, "redirect") || len(leaked) != 0 {
		t.Errorf("redirected: %+v, and the other host saw %v", r, leaked)
	}

	for _, request := range seen {
		if !strings.HasPrefix(request, "GET ") {
			t.Errorf("a check made %s, not a free GET", request)
		}
	}
}
