package main

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// anthropicAPI is where an Anthropic key is checked, unless ANTHROPIC_BASE_URL points elsewhere,
// as it does for the client that will use the key.
const anthropicAPI = "https://api.anthropic.com"

// checkKey asks a credential's provider for its model list: free, and answered only for a key the
// provider takes, so a typo is found before the first message rather than by it. A signed-in
// credential was checked by signing in, and is not asked about again here.
func checkKey(store *keys.Store, client *http.Client) keysui.Check {
	// Redirects are not followed: Go drops Authorization when one crosses hosts, but not x-api-key,
	// so following one could hand an Anthropic key to whatever host the answer named.
	noRedirects := *client
	noRedirects.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client = &noRedirects
	return func(name string) keysui.CheckResult {
		meta, err := store.Metadata(core.KeyRef{Name: name})
		if err != nil {
			return keysui.CheckResult{Note: err.Error()}
		}
		if in, err := store.SignIn(meta.Ref); err == nil && in.Kind.IsSignIn() {
			return keysui.CheckResult{Note: "it is a sign-in, checked when it was made"}
		}
		secret, err := store.Get(meta.Ref)
		if err != nil {
			return keysui.CheckResult{Note: err.Error()}
		}

		var url string
		header := http.Header{}
		switch meta.Ref.Provider {
		case core.ProviderAnthropic:
			base := strings.TrimRight(os.Getenv("ANTHROPIC_BASE_URL"), "/")
			if base == "" {
				base = anthropicAPI
			}
			url = base + "/v1/models?limit=1"
			header.Set("x-api-key", secret.Reveal())
			header.Set("anthropic-version", "2023-06-01")
		case core.ProviderOpenAICompatible:
			if meta.BaseURL == "" {
				return keysui.CheckResult{Note: "it has no endpoint"}
			}
			url = strings.TrimRight(meta.BaseURL, "/") + "/models"
			header.Set("Authorization", "Bearer "+secret.Reveal())
		default:
			return keysui.CheckResult{Note: "Canopy cannot check a " + string(meta.Ref.Provider) + " key"}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return keysui.CheckResult{Note: "the endpoint is not a URL"}
		}
		req.Header = header
		resp, err := client.Do(req)
		if err != nil {
			// Not the error's text: it can quote the URL, and nothing here should echo what it sent.
			return keysui.CheckResult{Note: "the provider could not be reached"}
		}
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return keysui.CheckResult{Accepted: true}
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return keysui.CheckResult{Refused: true, Note: "HTTP " + strconv.Itoa(resp.StatusCode)}
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			return keysui.CheckResult{Note: "the provider answered with a redirect, which is not followed with a key"}
		case resp.StatusCode == http.StatusNotFound:
			return keysui.CheckResult{Note: "the endpoint lists no models, so there was nothing free to ask"}
		}
		return keysui.CheckResult{Note: "the provider answered HTTP " + strconv.Itoa(resp.StatusCode)}
	}
}
