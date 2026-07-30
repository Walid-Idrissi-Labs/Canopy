package acp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// Discovery, tested on machines that have none of it.
//
// Every case below is a machine that is missing something, which is the point: the acceptance this
// has to hold is about what somebody without Claude Code is told, and a test that needed Claude Code
// installed could not check a single one of them.

// machine describes what a test machine has on it.
type machine struct {
	// present are the program names that resolve, mapped to where they resolve to.
	present map[string]string

	// says is what `claude auth status --json` prints.
	says string

	// fails is what running it returns instead.
	fails error

	env map[string]string
}

func (m machine) discovery() Discovery {
	return Discovery{
		LookPath: func(name string) (string, error) {
			if path, ok := m.present[name]; ok {
				return path, nil
			}
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		},
		Status: func(ctx context.Context, cli string) (string, error) {
			if m.fails != nil {
				return "", m.fails
			}
			return m.says, nil
		},
		Getenv: func(key string) string { return m.env[key] },
	}
}

// signedIn is the machine the happy path runs on.
func signedIn() machine {
	return machine{
		present: map[string]string{
			"claude":            "/usr/local/bin/claude",
			"claude-agent-acp":  "/usr/local/bin/claude-agent-acp",
			"claude-code-acp":   "/usr/local/bin/claude-code-acp",
			"/opt/mine/bridge":  "/opt/mine/bridge",
			"not-a-real-bridge": "",
		},
		says: `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty",` +
			`"email":"someone@example.com","orgId":"x","orgName":"y","subscriptionType":"max"}`,
	}
}

func TestFindingASignedInClaudeCodeReportsTheAccountAndNoSecret(t *testing.T) {
	t.Parallel()

	found, err := signedIn().discovery().Find(context.Background())
	if err != nil {
		t.Fatalf("finding a signed-in Claude Code: %v", err)
	}

	if found.Account.Email != "someone@example.com" {
		t.Errorf("the account is %q", found.Account.Email)
	}
	if found.Account.Plan != "max" {
		t.Errorf("the plan is %q", found.Account.Plan)
	}
	if !found.Account.OnSubscription() {
		t.Error("a claude.ai sign-in did not read as a subscription")
	}
	if found.Bridge != "/usr/local/bin/claude-agent-acp" {
		t.Errorf("the bridge is %q", found.Bridge)
	}
	if got := found.Account.String(); got != "someone@example.com (max)" {
		t.Errorf("the account reads as %q", got)
	}
}

// The whole reason this route is permitted, held as a property of the type rather than of a code
// path: there is nowhere in what discovery returns to put a token.
func TestNothingDiscoveryReturnsHasRoomForACredential(t *testing.T) {
	t.Parallel()

	found, err := signedIn().discovery().Find(context.Background())
	if err != nil {
		t.Fatalf("finding a signed-in Claude Code: %v", err)
	}

	rendered := fmt.Sprintf("%#v", found)
	for _, forbidden := range []string{"token", "Token", "secret", "Secret", "sk-ant"} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("a discovered installation carries %q:\n%s", forbidden, rendered)
		}
	}
}

func TestAMachineWithoutClaudeCodeIsToldWhatToInstallRatherThanShownAnExecError(t *testing.T) {
	t.Parallel()

	bare := machine{present: map[string]string{}}
	_, err := bare.discovery().Find(context.Background())

	if !errors.Is(err, ErrClaudeCodeMissing) {
		t.Fatalf("a machine with no Claude Code reported %v", err)
	}
	if !strings.Contains(err.Error(), "claude.com/claude-code") {
		t.Errorf("the failure does not say where to get it: %v", err)
	}
	if !strings.Contains(err.Error(), "signed in to yourself") {
		t.Errorf("the failure does not say Canopy will not sign anybody in: %v", err)
	}
	// The clause that matters most: no sign-in is offered anywhere on this path.
	for _, forbidden := range []string{"canopy keys signin", "authorize", "oauth"} {
		if strings.Contains(strings.ToLower(err.Error()), forbidden) {
			t.Errorf("the failure offers to sign somebody in with %q: %v", forbidden, err)
		}
	}
}

func TestAClaudeCodeNobodyIsSignedInToNamesTheCommandThatFixesIt(t *testing.T) {
	t.Parallel()

	loggedOut := signedIn()
	loggedOut.says = `{"loggedIn":false}`

	_, err := loggedOut.discovery().Find(context.Background())
	if !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("a logged-out Claude Code reported %v", err)
	}
	if !strings.Contains(err.Error(), "Run `claude` and sign in") {
		t.Errorf("the remedy is not the one that works: %v", err)
	}
	if !strings.Contains(err.Error(), "does not permit third-party tools to sign you in") {
		t.Errorf("the failure does not say why Canopy has no sign-in of its own: %v", err)
	}
}

func TestAMachineWithNoBridgeIsToldToInstallTheBridgeAndNothingElse(t *testing.T) {
	t.Parallel()

	noBridge := signedIn()
	delete(noBridge.present, "claude-agent-acp")
	delete(noBridge.present, "claude-code-acp")

	_, err := noBridge.discovery().Find(context.Background())
	if !errors.Is(err, ErrBridgeMissing) {
		t.Fatalf("a machine with no bridge reported %v", err)
	}
	if !strings.Contains(err.Error(), "@agentclientprotocol/claude-agent-acp") {
		t.Errorf("the failure does not name the package: %v", err)
	}
	if !strings.Contains(err.Error(), "Nothing about your Claude Code sign-in changes") {
		t.Errorf("the failure does not say the existing sign-in is kept: %v", err)
	}
	if errors.Is(err, ErrNotSignedIn) || errors.Is(err, ErrClaudeCodeMissing) {
		t.Error("a missing bridge was also reported as a missing or logged-out Claude Code")
	}
}

func TestTheBridgesPreviousNameIsStillFound(t *testing.T) {
	t.Parallel()

	old := signedIn()
	delete(old.present, "claude-agent-acp")

	found, err := old.discovery().Find(context.Background())
	if err != nil {
		t.Fatalf("a machine with the previously named bridge reported %v", err)
	}
	if found.Bridge != "/usr/local/bin/claude-code-acp" {
		t.Errorf("the older bridge was not used: %q", found.Bridge)
	}
}

func TestAnOverriddenBridgeIsCheckedRatherThanTrusted(t *testing.T) {
	t.Parallel()

	pointed := signedIn()
	pointed.env = map[string]string{BridgeEnv: "/opt/mine/bridge"}

	found, err := pointed.discovery().Find(context.Background())
	if err != nil {
		t.Fatalf("an overridden bridge reported %v", err)
	}
	if found.Bridge != "/opt/mine/bridge" {
		t.Errorf("the override was ignored: %q", found.Bridge)
	}

	stale := signedIn()
	stale.env = map[string]string{BridgeEnv: "/opt/gone/bridge"}
	_, err = stale.discovery().Find(context.Background())
	if !errors.Is(err, ErrBridgeMissing) {
		t.Fatalf("a stale override reported %v", err)
	}
	if !strings.Contains(err.Error(), BridgeEnv) {
		t.Errorf("the failure does not name the variable that caused it: %v", err)
	}
	if !strings.Contains(err.Error(), "Unset it") {
		t.Errorf("the failure does not say how to go back to looking on PATH: %v", err)
	}
}

// "Too old" is detected by asking for something and not getting it, rather than by comparing a
// version number against one somebody has to keep correct.
func TestAClaudeCodeTooOldToReportItsAccountIsToldToUpdate(t *testing.T) {
	t.Parallel()

	ancient := signedIn()
	ancient.says = "Logged in as someone@example.com"

	_, err := ancient.discovery().Find(context.Background())
	if err == nil {
		t.Fatal("prose where JSON was expected was accepted")
	}
	if !strings.Contains(err.Error(), "claude update") {
		t.Errorf("the failure does not name the upgrade: %v", err)
	}
	if !strings.Contains(err.Error(), "predates") {
		t.Errorf("the failure does not say what it thinks is wrong: %v", err)
	}
}

func TestAClaudeCodeThatCannotBeRunIsReportedAsSuchRatherThanAsLoggedOut(t *testing.T) {
	t.Parallel()

	broken := signedIn()
	broken.fails = errors.New("permission denied")

	_, err := broken.discovery().Find(context.Background())
	if err == nil {
		t.Fatal("a Claude Code that would not run was accepted")
	}
	if errors.Is(err, ErrNotSignedIn) {
		t.Error("a Claude Code that would not run was reported as one nobody is signed in to, " +
			"which sends somebody through a sign-in that cannot help")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("the failure does not carry what actually happened: %v", err)
	}
}

func TestASignInThroughAnApiAccountIsNotDescribedAsASubscription(t *testing.T) {
	t.Parallel()

	console := signedIn()
	console.says = `{"loggedIn":true,"authMethod":"console","email":"team@example.com"}`

	found, err := console.discovery().Find(context.Background())
	if err != nil {
		t.Fatalf("an API-account Claude Code reported %v", err)
	}
	if found.Account.OnSubscription() {
		t.Error("an API account read as a subscription, which is the difference between usage " +
			"included in a plan and usage that arrives on an invoice")
	}
	if got := found.Account.String(); got != "team@example.com" {
		t.Errorf("an account with no plan reads as %q", got)
	}
}
