package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The promise D-51 makes about this route, held against the source rather than against this package.
//
// Canopy identifies itself to OpenAI as canopy and never as another client. That is not a property
// of the file it happens to be written in, it is a property of the repository, and a test scoped to
// this package would pass on the day somebody set the originator somewhere else because a model
// resolved differently under a first-party name. So this reads every Go file in the tree.
//
// It exists because the consequence is not a bug report. Impersonating another client is the single
// behaviour OpenAI's terms plausibly reach, none of the established projects on this path do it, and
// a route chosen because it is defensible stops being defensible the moment it lies about who is
// calling. The failure mode is not a broken build; it is the argument for the route being gone.

// otherClients are names that belong to somebody else.
//
// codex_cli_rs is the Codex CLI's own default originator. The rest are OpenAI's own first-party
// clients, which their code recognises by name, and any originator beginning "Codex " is treated the
// same way. Each one is listed rather than matched by a rule, because the thing being forbidden is
// the specific act of passing for a named product.
var otherClients = []string{
	"codex_cli_rs",
	"codex_vscode",
	"codex-tui",
	"codex_atlas",
	"codex_chatgpt_desktop",
	"codex_app_server_daemon",
	"codex-backend",
}

func TestNoOriginatorBelongingToAnotherClientAppearsAnywhereInThisRepository(t *testing.T) {
	t.Parallel()

	root := repositoryRootFrom(t)
	self := "no_impersonation_test.go"

	var found []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Nothing under these is Canopy's source, and a dependency's own identity is not a
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
		rel, _ := filepath.Rel(root, path)

		for _, line := range strings.Split(string(source), "\n") {
			trimmed := strings.TrimSpace(line)
			// Comments and test assertions are allowed to name these, and have to be: the tests that
			// hold this property work by naming exactly what must not be sent. What is forbidden is a
			// name in a value Canopy would put on the wire, so a line that only mentions one inside a
			// failure message or an explanation is not the thing being looked for.
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			for _, theirs := range otherClients {
				if !strings.Contains(trimmed, `"`+theirs+`"`) {
					continue
				}
				if strings.Contains(trimmed, "t.Errorf") || strings.Contains(trimmed, "t.Fatal") ||
					strings.HasSuffix(rel, "_test.go") {
					continue
				}
				found = append(found, rel+" contains "+theirs)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("reading the repository: %v", err)
	}

	if len(found) > 0 {
		t.Fatalf(
			"something in this repository names another client where Canopy's own name belongs:\n"+
				"  %s\n\nThe value at clientInfo.name becomes the originator the Codex app server "+
				"sends upstream. Impersonating another client is the one behaviour OpenAI's terms "+
				"plausibly reach, and a route chosen because it is defensible stops being defensible "+
				"the moment it lies about who is calling (D-51). The name Canopy sends is "+
				"codex.Originator.",
			strings.Join(found, "\n  "))
	}
}

// The other half: the name actually sent is Canopy's own.
func TestTheOriginatorCanopySendsIsItsOwnName(t *testing.T) {
	t.Parallel()

	if Originator != "canopy" {
		t.Fatalf("Canopy identifies itself as %q", Originator)
	}
	for _, theirs := range otherClients {
		if Originator == theirs {
			t.Fatalf("Canopy identifies itself as %q, which belongs to another client", theirs)
		}
	}
	if strings.HasPrefix(Originator, "Codex ") {
		t.Fatalf("Canopy identifies itself as %q, and the app server treats any name starting "+
			"'Codex ' as one of its own", Originator)
	}
	// It also has to survive being an HTTP header value, which the app server checks and refuses
	// the handshake over.
	if strings.ContainsAny(Originator, "\r\n\t ") {
		t.Fatalf("the originator %q is not a valid HTTP header value, so the handshake would be "+
			"refused before anything else happened", Originator)
	}
}

// Canopy holding a ChatGPT subscription token is the other promise this route rests on, and it is
// held by the shape of the types rather than by a search: there is nowhere to put one.
func TestADelegatedChatGPTCredentialHasNowhereToKeepAToken(t *testing.T) {
	t.Parallel()

	// Installation is everything this package hands to the rest of the program about a discovered
	// Codex. Two paths, and neither is a credential.
	install := Installation{Binary: "/usr/local/bin/codex", Home: "/home/someone/.codex"}
	if install.Binary == "" || install.Home == "" {
		t.Fatal("a discovered installation cannot describe itself")
	}

	// Account is what a sign-in produces. An address and a plan, both of which are already on a
	// credential list, and no token.
	account := Account{Kind: accountChatGPT, Email: "someone@example.com", Plan: "pro"}
	if !account.OnSubscription() {
		t.Fatal("a ChatGPT account did not read as a subscription")
	}

	// internal/keys enforces the same thing from the other side: PutSignIn refuses a delegated
	// credential that arrives with tokens, held by TestADelegatedSignInRefusesToBeGivenTokens. The
	// two together are what makes "Canopy never holds the user's ChatGPT credential" a fact about
	// the build rather than a sentence in a document.
}

// repositoryRootFrom walks up to the directory holding go.mod.
func repositoryRootFrom(t *testing.T) string {
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
