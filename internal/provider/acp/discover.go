package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	canopyexec "github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// What has to be true before a delegated turn can happen, and what to say when it is not.
//
// Two separate things have to be on the machine and they fail for different reasons, so they are
// found separately and reported separately. Claude Code itself is what the user signed in to and is
// the only thing that knows which account that was. The ACP bridge is a second program that turns
// the Claude Agent SDK into an ACP agent; it is published by the protocol's own maintainers, it is
// not part of Claude Code, and a machine can perfectly well have one and not the other.
//
// Every failure below names the thing to install or the command to run. The rule this exists to
// enforce is that a missing installation must never surface as an exec error: "fork/exec
// claude-agent-acp: no such file or directory" is a true sentence that tells somebody nothing, and
// the whole point of a discovery step is to replace it with the sentence they can act on.

// Sentinel errors, so a caller can tell the three failures apart without reading the message.
//
// Separate rather than one ErrNotAvailable, because the remedies are not interchangeable and a
// screen that offers the wrong one wastes a specific amount of somebody's time: installing the
// bridge does not help a user who is not signed in, and signing in again does not help a user who
// has no bridge.
var (
	// ErrClaudeCodeMissing means there is no `claude` on this machine to delegate to.
	ErrClaudeCodeMissing = errors.New("claude code is not installed")

	// ErrNotSignedIn means Claude Code is installed and nobody has signed in to it.
	ErrNotSignedIn = errors.New("claude code is not signed in")

	// ErrBridgeMissing means Claude Code is here and signed in, and nothing on this machine can
	// speak ACP to it.
	ErrBridgeMissing = errors.New("the claude code acp bridge is not installed")
)

// BridgeEnv names the environment variable that overrides where the bridge is found.
//
// An escape hatch rather than the normal path. Somebody running the bridge out of a checkout, or on
// a machine whose PATH is assembled by something other than a shell profile, needs a way to say
// where it is, and the alternative is a config field for a value almost nobody sets.
const BridgeEnv = "CANOPY_CLAUDE_ACP"

// bridgeNames are what the ACP bridge is called, newest name first.
//
// Two names because it was renamed. The current package is @agentclientprotocol/claude-agent-acp and
// installs `claude-agent-acp`; it was @zed-industries/claude-code-acp and installed
// `claude-code-acp` before that, and a machine set up a few months ago has the old one. Accepting
// both costs one LookPath and saves a user being told to install something they already have under
// its previous name.
var bridgeNames = []string{"claude-agent-acp", "claude-code-acp"}

// Account is who the user's Claude Code says they are. It holds no token and has nowhere to put one.
//
// Read from `claude auth status --json`, which reports the account and the plan and never the
// credential. That is the whole reason this route is permitted, and it is worth saying where the
// fact comes from: Canopy asks the CLI who it is signed in as, the same way a person would, rather
// than reading a credential file and deciding for itself.
type Account struct {
	// Email identifies the account. This is what a credential list shows so that somebody with two
	// subscriptions can tell them apart.
	Email string

	// Plan is what the vendor calls the subscription: "max", "pro", and whatever else it reports.
	// Not interpreted, only shown, because the set is theirs and a value this build has never heard
	// of is still the truthful answer.
	Plan string

	// Method is how Claude Code is authenticated: "claude.ai" for a subscription, something else for
	// an API account. It matters because a delegated turn on an API account is billed per token to
	// that account, which is a different arrangement from the one somebody signing in with a
	// subscription is expecting, and they should hear it from Canopy rather than from an invoice.
	Method string
}

// OnSubscription reports whether delegated turns will draw on a Claude subscription.
func (a Account) OnSubscription() bool { return a.Method == "claude.ai" }

// String is what a credential list shows.
func (a Account) String() string {
	switch {
	case a.Email == "":
		return "an unnamed Claude Code account"
	case a.Plan == "":
		return a.Email
	default:
		return a.Email + " (" + a.Plan + ")"
	}
}

// Installation is a Claude Code somebody has already signed in to, and the way to talk to it.
type Installation struct {
	// CLI is the absolute path to `claude`. Kept so that an error can name the binary that answered
	// rather than the name that was looked up, which on a machine with several installs is the
	// difference between a message that helps and one that starts an argument.
	CLI string

	// Bridge is the absolute path to the program that speaks ACP.
	Bridge string

	// Account is who Claude Code says is signed in.
	Account Account
}

// Discovery finds a Claude Code installation. The zero value looks on the real machine.
//
// The two function fields are how this is tested without a Claude Code and without a subscription. A
// test that needed either would be a test that runs on one laptop, and the acceptance this has to
// hold is exactly about the machines where those things are absent.
type Discovery struct {
	// LookPath resolves a program name against PATH. Defaults to exec.LookPath.
	LookPath func(name string) (string, error)

	// Status runs `claude auth status --json` and returns what it printed. Defaults to running it.
	Status func(ctx context.Context, cli string) (string, error)

	// Getenv reads the environment. Defaults to os.Getenv.
	Getenv func(key string) string
}

func (d Discovery) lookPath(name string) (string, error) {
	if d.LookPath != nil {
		return d.LookPath(name)
	}
	return exec.LookPath(name)
}

func (d Discovery) getenv(key string) string {
	if d.Getenv != nil {
		return d.Getenv(key)
	}
	return os.Getenv(key)
}

// statusTimeout bounds the account probe.
//
// Twenty seconds rather than exec's two minute default, which is sized for a test suite. This is one
// process printing one line, and a wait long enough to look like a hang is worse than a failure that
// says what it was waiting for.
const statusTimeout = 20 * time.Second

func (d Discovery) status(ctx context.Context, cli string) (string, error) {
	if d.Status != nil {
		return d.Status(ctx, cli)
	}

	// Through internal/exec rather than os/exec directly, for the reason that package exists: the
	// probe gets its own process group, a bounded wait and bounded output, so a Claude Code that
	// hangs on a prompt is a timeout with a message rather than a Canopy that never returns.
	//
	// Run from the temporary directory rather than from the user's project. `claude` reads settings
	// out of the directory it starts in, and a status probe that picks up a repository's
	// configuration is a probe whose answer depends on where Canopy happened to be launched.
	result, err := canopyexec.Run(ctx, cli, []string{"auth", "status", "--json"}, canopyexec.Options{
		Dir:     os.TempDir(),
		Timeout: statusTimeout,
	})
	if err != nil {
		return "", err
	}
	if !result.Ran {
		return "", fmt.Errorf("running `%s auth status`: %s", cli, strings.TrimSpace(result.Output))
	}
	if result.TimedOut {
		return "", fmt.Errorf("`%s auth status` did not answer within %s", cli, statusTimeout)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("`%s auth status` exited %d: %s",
			cli, result.ExitCode, strings.TrimSpace(result.Output))
	}
	return result.Output, nil
}

// authStatus is the shape `claude auth status --json` prints. No field here is a secret and there is
// nowhere in it to put one.
type authStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	AuthMethod       string `json:"authMethod"`
	Email            string `json:"email"`
	SubscriptionType string `json:"subscriptionType"`
}

// Find locates a Claude Code the user has signed in to, or says what is missing.
//
// The order is deliberate and is the order somebody would fix things in. Claude Code first, because
// without it there is nothing to delegate to and no account to report. Then whether anybody is
// signed in, because a bridge is no use in front of a logged-out CLI. The bridge last, because it is
// the only one of the three that is Canopy's business to explain and the least likely to already be
// there.
func (d Discovery) Find(ctx context.Context) (Installation, error) {
	cli, err := d.lookPath("claude")
	if err != nil {
		// The underlying lookup failure is deliberately not carried through. "executable file not
		// found in $PATH" adds nothing to the sentence above and turns advice somebody can act on
		// into advice with an implementation detail stapled to it. That is the opposite trade from
		// core.WithDetail's, and it is the right one here because there is no provider message to
		// preserve: the whole fact is that the file is not there.
		return Installation{}, fmt.Errorf(
			"%w. Canopy does not sign anybody in to Claude: it drives a Claude Code you have already "+
				"signed in to yourself, so there has to be one. Install it from "+
				"https://claude.com/claude-code, then run `claude` once and sign in",
			ErrClaudeCodeMissing)
	}

	account, err := d.account(ctx, cli)
	if err != nil {
		return Installation{}, err
	}

	bridge, err := d.bridge()
	if err != nil {
		return Installation{}, err
	}

	return Installation{CLI: cli, Bridge: bridge, Account: account}, nil
}

// account asks Claude Code who is signed in.
//
// Output that cannot be parsed is reported as a version problem rather than as corruption, and that
// is a judgement rather than a guess: `auth status --json` is the newer shape of a command that used
// to print prose, so a Claude Code old enough to predate it prints something this cannot read. Naming
// the upgrade is a better first suggestion than naming the JSON, and `claude update` is harmless if
// the diagnosis is wrong.
//
// Deliberately not a version-number comparison. A minimum version is a number somebody has to keep
// correct, it goes stale silently, and it fails on the day the vendor changes their numbering. Asking
// for the thing and seeing whether the answer arrives tests what actually matters.
func (d Discovery) account(ctx context.Context, cli string) (Account, error) {
	output, err := d.status(ctx, cli)
	if err != nil {
		return Account{}, fmt.Errorf(
			"could not ask %s which account it is signed in as: %w", cli, err)
	}

	var status authStatus
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &status); err != nil {
		return Account{}, fmt.Errorf(
			"%s did not report its account in a shape this build understands, which usually means it "+
				"predates `claude auth status --json`. Update it with `claude update` and try again",
			cli)
	}

	if !status.LoggedIn {
		return Account{}, fmt.Errorf(
			"%w. Run `claude` and sign in, then add this credential again. Canopy has no sign-in of "+
				"its own to offer here: Anthropic does not permit third-party tools to sign you in to "+
				"Claude, so the only sign-in that counts is the one in Claude Code", ErrNotSignedIn)
	}

	return Account{
		Email:  status.Email,
		Plan:   status.SubscriptionType,
		Method: status.AuthMethod,
	}, nil
}

// bridge finds the program that speaks ACP to Claude Code.
func (d Discovery) bridge() (string, error) {
	if override := strings.TrimSpace(d.getenv(BridgeEnv)); override != "" {
		// Checked rather than trusted. A stale override is a path that used to exist, and finding out
		// at the first turn gives a message about a turn rather than about a setting.
		if path, err := d.lookPath(override); err == nil {
			return path, nil
		}
		return "", fmt.Errorf(
			"%w: %s is set to %q and there is nothing runnable there. Unset it to look on PATH instead",
			ErrBridgeMissing, BridgeEnv, override)
	}

	for _, name := range bridgeNames {
		if path, err := d.lookPath(name); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf(
		"%w. Claude Code does not speak the Agent Client Protocol itself, so one more program is "+
			"needed between them. Install it with `npm install -g @agentclientprotocol/claude-agent-acp` "+
			"and make sure %s is on your PATH. Nothing about your Claude Code sign-in changes: the "+
			"bridge uses the account you are already signed in as",
		ErrBridgeMissing, bridgeNames[0])
}
