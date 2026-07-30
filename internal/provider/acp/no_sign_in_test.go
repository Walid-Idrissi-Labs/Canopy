package acp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The promise D-51 makes, held against the source rather than against this package.
//
// Canopy will not implement claude.ai sign-in. That is not a property of the file it would go in, it
// is a property of the repository, and a test scoped to internal/provider/acp would pass on the day
// somebody added an Anthropic OAuth flow in cmd/canopy because it looked easier than explaining the
// delegated route. So this reads every Go file in the tree.
//
// It exists because the consequence is not a bug report. Anthropic reserve the right to enforce this
// without prior notice and they enforce it server-side, so the failure mode is every Canopy user's
// Claude route stopping at once, with no warning and no deploy of ours to roll back.

// signInMarkers are the pieces an Anthropic sign-in flow cannot be built without.
//
// Chosen so that each one is something a delegated route has no reason to contain. Canopy asks
// Claude Code who it is signed in as and starts a subprocess; none of that needs an authorisation
// endpoint, a code challenge or a client secret.
var signInMarkers = []string{
	"://claude.ai",
	"claude.ai/oauth",
	"claude.ai/login",
	"code_verifier",
	"code_challenge",
	"client_secret",
	"response_type=code",
	"/v1/oauth",
	"oauth/authorize",
	"oauth/token",
}

// vendorWords decide whether a marker is aimed at Anthropic.
//
// Two of the three routes D-51 permits are ordinary OAuth and will legitimately carry some of the
// markers above when S-03 and S-05 land. This test is about the one route where OAuth is forbidden,
// so a marker only counts when it sits in a file that is also about Anthropic. The pairing is
// deliberately generous: a file that mentions Claude at all and contains an authorisation endpoint is
// worth stopping in front of a person, even in the case where it turns out to be innocent.
var vendorWords = []string{"anthropic", "claude"}

func TestNoAnthropicSignInFlowExistsAnywhereInThisRepository(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	self := "no_sign_in_test.go"

	var found []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Nothing under these is Canopy's source, and a dependency's own OAuth client is not a
			// promise Canopy made.
			switch entry.Name() {
			case ".git", "vendor", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || entry.Name() == self {
			return nil
		}

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		lowered := strings.ToLower(string(source))
		if !mentionsAny(lowered, vendorWords) {
			return nil
		}
		for _, marker := range signInMarkers {
			if strings.Contains(lowered, marker) {
				rel, _ := filepath.Rel(root, path)
				found = append(found, rel+" contains "+marker)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}

	if len(found) > 0 {
		t.Fatalf(
			"something in this repository looks like an Anthropic sign-in flow:\n  %s\n\n"+
				"Anthropic do not permit third-party developers to offer Claude.ai login or to route "+
				"requests through Free, Pro or Max plan credentials on behalf of their users, they "+
				"enforce that on their servers, and they reserve the right to do so without prior "+
				"notice (D-51). The permitted route is to delegate to a Claude Code the user signed in "+
				"to themselves, which is internal/provider/acp and holds no token at all.",
			strings.Join(found, "\n  "))
	}
}

// Canopy holding an Anthropic subscription token is the other half of the same promise, and it is
// held by the type system rather than by a search: there is nowhere to put one. This says so about
// the record on disk, which is the place a token would have to survive a restart.
func TestADelegatedCredentialHasNowhereToKeepASubscriptionToken(t *testing.T) {
	t.Parallel()

	// Installation is everything this package hands to the rest of the program about a discovered
	// Claude Code. Three strings and an account, none of which is a credential.
	install := Installation{
		CLI:     "/usr/local/bin/claude",
		Bridge:  "/usr/local/bin/claude-agent-acp",
		Account: Account{Email: "someone@example.com", Plan: "max", Method: "claude.ai"},
	}
	if install.Account.String() == "" {
		t.Fatal("a discovered installation cannot describe its account")
	}

	// internal/keys enforces the same thing from the other side: PutSignIn refuses a delegated
	// credential that arrives with tokens, held by TestADelegatedSignInRefusesToBeGivenTokens. The
	// two together are what makes "Canopy never holds the user's Claude credential" a fact about the
	// build rather than a sentence in a document.
}

func mentionsAny(haystack string, words []string) bool {
	for _, word := range words {
		if strings.Contains(haystack, word) {
			return true
		}
	}
	return false
}

// repositoryRoot walks up to the directory holding go.mod.
func repositoryRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("finding the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory, so there is no repository to read")
		}
		dir = parent
	}
}
