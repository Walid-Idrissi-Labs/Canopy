package childenv

import (
	"strings"
	"testing"
)

func TestSecretsAreRemovedAndTheShellIsKept(t *testing.T) {
	env := []string{
		"PATH=/usr/bin", "HOME=/h", "GOPATH=/g", "HTTPS_PROXY=http://p", "NVM_DIR=/n", "TERM=xterm",
		"ANTHROPIC_API_KEY=sk-ant", "OPENAI_API_KEY=sk", "GITHUB_TOKEN=ghp", "GH_TOKEN=gho",
		"AWS_SECRET_ACCESS_KEY=a", "STRIPE_SECRET_KEY=s", "MY_SERVICE_API_KEY=k", "DB_PASSWORD=p",
		"OTHER_APP_SECRET=c", "ANTHROPIC_BASE_URL=u",
	}
	got := strings.Join(Scrub(env), "\n")
	for _, kept := range []string{"PATH=", "HOME=", "GOPATH=", "HTTPS_PROXY=", "NVM_DIR=", "TERM="} {
		if !strings.Contains(got, kept) {
			t.Errorf("%s was removed; build tooling needs an ordinary shell environment", kept)
		}
	}
	for _, gone := range []string{"sk-ant", "OPENAI_API_KEY", "ghp", "gho", "AWS_SECRET", "STRIPE",
		"MY_SERVICE_API_KEY", "DB_PASSWORD", "OTHER_APP_SECRET", "ANTHROPIC_BASE_URL"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s reached the child environment", gone)
		}
	}
}

func TestAProjectCanKeepANamedVariable(t *testing.T) {
	got := Scrub([]string{"NPM_TOKEN=t", "GITHUB_TOKEN=g"}, "npm_token")
	if len(got) != 1 || got[0] != "NPM_TOKEN=t" {
		t.Fatalf("keep did not keep exactly the named variable: %v", got)
	}
}

// The general credentials are recognised across clouds, CI, registries and services, and a
// project's own token is not.
func TestWellKnownCredentials(t *testing.T) {
	for _, name := range []string{"ANTHROPIC_API_KEY", "GITHUB_TOKEN", "AWS_ACCESS_KEY_ID", "AZURE_STORAGE_KEY", "ARM_ACCESS_KEY",
		"CLOUDFLARE_API_TOKEN", "VAULT_TOKEN", "DATABASE_URL", "CI_JOB_TOKEN", "GOOGLE_APPLICATION_CREDENTIALS"} {
		if !WellKnown(name) {
			t.Errorf("%s is not recognised as a general credential", name)
		}
	}
	for _, name := range []string{"ISSUES_TOKEN", "MYPROJECT_MCP_KEY"} {
		if WellKnown(name) {
			t.Errorf("%s, a project's own token, was refused", name)
		}
	}
}
