// Package childenv decides which of Canopy's environment variables a child process may inherit.
//
// Every process Canopy starts, a shell command, a test run, a hook, an MCP server, a vendor agent,
// used to receive Canopy's whole environment. Canopy never puts its own stored keys there, but a
// developer's shell usually has ANTHROPIC_API_KEY, OPENAI_API_KEY or GITHUB_TOKEN exported, and
// handing those to a command a model wrote, or to a vendor agent Canopy does not gate, gives them
// credentials they have no need for. Build tooling still needs PATH, HOME, proxies, version-manager
// shims and the rest of an ordinary shell, so this removes secrets by name rather than keeping an
// allow list that would break installs.
package childenv

import (
	"os"
	"strings"
)

// exact are variables removed whatever they look like.
var exact = map[string]bool{
	"GITHUB_TOKEN": true, "GH_TOKEN": true, "GH_ENTERPRISE_TOKEN": true, "GITHUB_ENTERPRISE_TOKEN": true,
	"COPILOT_GITHUB_TOKEN": true, "GITLAB_TOKEN": true, "NPM_TOKEN": true, "NODE_AUTH_TOKEN": true,
	"AWS_SECRET_ACCESS_KEY": true, "AWS_SESSION_TOKEN": true,
	"GOOGLE_API_KEY": true, "GEMINI_API_KEY": true, "HF_TOKEN": true, "HUGGING_FACE_HUB_TOKEN": true,
}

// prefixes are families of provider credentials.
var prefixes = []string{"ANTHROPIC_", "OPENAI_", "CLAUDE_CODE_OAUTH", "OPENROUTER_", "MOONSHOT_",
	"DEEPSEEK_", "MISTRAL_", "GROQ_", "TOGETHER_", "XAI_", "NVIDIA_API", "COHERE_", "FIREWORKS_",
	"MINIMAX_", "ZHIPU", "DASHSCOPE_"}

// suffixes catch the conventional spellings of a secret on any other service.
var suffixes = []string{"_API_KEY", "_APIKEY", "_SECRET", "_SECRET_KEY", "_ACCESS_TOKEN",
	"_AUTH_TOKEN", "_TOKEN", "_PASSWORD", "_PASSWD", "_PRIVATE_KEY"}

// Secret reports whether a variable name is one a child should not inherit.
func Secret(name string) bool {
	upper := strings.ToUpper(name)
	if exact[upper] {
		return true
	}
	for _, p := range prefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	for _, s := range suffixes {
		if strings.HasSuffix(upper, s) {
			return true
		}
	}
	return false
}

// general are credentials that open far more than any one server needs: clouds, CI, registries,
// chat and payment services, and the URLs that carry passwords.
var general = map[string]bool{
	"AWS_ACCESS_KEY_ID":    true,
	"CLOUDFLARE_API_TOKEN": true, "CLOUDFLARE_API_KEY": true, "DIGITALOCEAN_ACCESS_TOKEN": true,
	"HEROKU_API_KEY": true, "VAULT_TOKEN": true, "DOCKER_PASSWORD": true, "DOCKERHUB_TOKEN": true,
	"SLACK_BOT_TOKEN": true, "SLACK_TOKEN": true, "STRIPE_SECRET_KEY": true, "STRIPE_API_KEY": true,
	"SENTRY_AUTH_TOKEN": true, "CARGO_REGISTRY_TOKEN": true, "TWINE_PASSWORD": true,
	"CI_JOB_TOKEN": true, "DATABASE_URL": true, "REDIS_URL": true, "MONGODB_URI": true,
	"GOOGLE_APPLICATION_CREDENTIALS": true, "FIREBASE_TOKEN": true, "VERCEL_TOKEN": true,
	"NETLIFY_AUTH_TOKEN": true, "PULUMI_ACCESS_TOKEN": true, "TF_TOKEN_APP_TERRAFORM_IO": true,
}

// generalPrefixes are families of cloud and platform credentials.
var generalPrefixes = []string{"AWS_", "AZURE_", "ARM_", "GOOGLE_", "GCLOUD_", "CLOUDSDK_", "DIGITALOCEAN_"}

// WellKnown reports whether a variable is one of the general credentials: a code host's, a cloud's,
// a CI system's, a registry's or a model provider's. Unlike a project's own token, such a key opens
// far more than any one server needs, and a repository must never be able to direct it anywhere.
func WellKnown(name string) bool {
	upper := strings.ToUpper(name)
	if exact[upper] || general[upper] {
		return true
	}
	for _, p := range append(append([]string(nil), prefixes...), generalPrefixes...) {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return false
}

// Scrub returns env without secret variables, keeping any name listed in keep. keep is how a project
// that genuinely needs a token in its tests says so, visibly, in its own configuration.
func Scrub(env []string, keep ...string) []string {
	allowed := map[string]bool{}
	for _, k := range keep {
		allowed[strings.ToUpper(k)] = true
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if Secret(name) && !allowed[strings.ToUpper(name)] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Inherited is the current process environment with secrets removed.
func Inherited(keep ...string) []string { return Scrub(os.Environ(), keep...) }
