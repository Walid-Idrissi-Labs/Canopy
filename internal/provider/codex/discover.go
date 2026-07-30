package codex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// What has to be true before this route works, and what to say when it is not.
//
// One thing has to be on the machine: the `codex` binary. Everything else this route needs, the
// sign-in, the tokens, the renewal and the conversation, lives behind it. That is the whole reason
// the app server was chosen over running an OAuth flow of Canopy's own, and it is also the reason
// the failure when it is absent has to be a sentence somebody can act on rather than an exec error.
// "fork/exec codex: no such file or directory" is true and tells nobody anything.

// Route is what this way in is called, in `canopy keys signin -route` and in the keys record.
//
// A route rather than a provider, because provider cannot tell these apart: a Codex credential and
// a Copilot one are both openai-compatible, so a switch on provider alone sends one of them to the
// other's client. This is the value keys.SourceFor and the resolver key on.
const Route = "codex"

// BinaryEnv names the environment variable that overrides where `codex` is found.
//
// An escape hatch rather than the normal path, for somebody running a build out of a checkout or on
// a machine whose PATH is assembled by something other than a shell profile. The alternative is a
// config field for a value almost nobody sets.
const BinaryEnv = "CANOPY_CODEX"

// HomeEnv is Codex's own variable for where it keeps its state, including the login.
//
// Read rather than set. Somebody who has pointed their Codex at a different directory has done so
// deliberately, and a Canopy that ignored it would sign them in somewhere they will never look and
// report an account that is not the one their `codex` uses.
const HomeEnv = "CODEX_HOME"

// binaryName is what the Codex CLI installs as.
const binaryName = "codex"

// Sentinel errors, so a caller can tell the failures apart without reading the message.
//
// Separate rather than one ErrNotAvailable, because the remedies are not interchangeable and
// offering the wrong one wastes a specific amount of somebody's time: installing Codex does not
// help a user who has it and is signed out, and signing in again does not help a user who has no
// binary.
var (
	// ErrCodexMissing means there is no `codex` on this machine to drive.
	ErrCodexMissing = errors.New("the codex cli is not installed")

	// ErrNotSignedIn means Codex is here and nobody has signed in to it, or the grant has ended.
	ErrNotSignedIn = errors.New("codex is not signed in")
)

// Installation is a Codex this machine can run.
//
// It holds no token and has nowhere to put one. What Canopy knows about the sign-in it learns by
// asking the app server, the same way a person would, rather than by reading a credential file and
// deciding for itself.
type Installation struct {
	// Binary is the absolute path to `codex`. Kept so an error can name the binary that answered
	// rather than the name that was looked up, which on a machine with several installs is the
	// difference between a message that helps and one that starts an argument.
	Binary string

	// Home is $CODEX_HOME, which is where the app server keeps the login it owns. Canopy never
	// writes here and reads only in the degraded path; it is carried so a message can name the
	// directory the user's own Codex is actually using.
	Home string
}

// Discovery finds a Codex installation. The zero value looks on the real machine.
//
// The two function fields are how every test in this package runs on a machine with no Codex and no
// ChatGPT subscription. A test that needed either would be a test that runs on one laptop, and the
// acceptance this has to hold is exactly about the machines where those things are absent.
type Discovery struct {
	// LookPath resolves a program name against PATH. Defaults to exec.LookPath.
	LookPath func(name string) (string, error)

	// Getenv reads the environment. Defaults to os.Getenv.
	Getenv func(key string) string

	// HomeDir is the user's home directory, for the default $CODEX_HOME. Defaults to os.UserHomeDir.
	HomeDir func() (string, error)

	// ReadFile reads the degraded path's evidence. Defaults to os.ReadFile.
	ReadFile func(name string) ([]byte, error)
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

func (d Discovery) homeDir() (string, error) {
	if d.HomeDir != nil {
		return d.HomeDir()
	}
	return os.UserHomeDir()
}

func (d Discovery) readFile(name string) ([]byte, error) {
	if d.ReadFile != nil {
		return d.ReadFile(name)
	}
	return os.ReadFile(name)
}

// Find locates a Codex this machine can run, or says what is missing and what would fix it.
//
// The binary is discovered rather than bundled, and that was the choice this task was asked to make
// and record. Bundling would mean shipping a per-platform Rust binary inside a Go release: an
// Apache-2.0 notice obligation, a release artefact several times the size of Canopy itself for
// every platform, and a version pinned on the day of the release that goes stale while the vendor's
// own protocol moves. Discovering costs the opposite thing, which is that the version is not
// Canopy's to control, so the handshake checks what it found rather than assuming a shape. That
// trade is recorded in LIMITATIONS.md rather than only here, because it is the user who lives with
// it.
func (d Discovery) Find() (Installation, error) {
	home, err := d.codexHome()
	if err != nil {
		return Installation{}, err
	}

	binary, err := d.binary()
	if err != nil {
		// The degraded path, and it is a better message rather than a second way in. If there is a
		// login sitting in $CODEX_HOME then the user is not somebody who needs to be told what Codex
		// is, they are somebody whose PATH is wrong or who removed the binary, and the sentence that
		// helps them is a different sentence.
		if login, ok := d.existingLogin(home); ok {
			return Installation{}, fmt.Errorf("%w. %s", err, login.remedy(home))
		}
		return Installation{}, err
	}
	return Installation{Binary: binary, Home: home}, nil
}

// binary resolves `codex`, honouring the override.
func (d Discovery) binary() (string, error) {
	if override := strings.TrimSpace(d.getenv(BinaryEnv)); override != "" {
		// Checked rather than trusted. A stale override is a path that used to exist, and finding
		// that out at the first turn gives a message about a turn rather than about a setting.
		if path, err := d.lookPath(override); err == nil {
			return path, nil
		}
		return "", fmt.Errorf(
			"%w: %s is set to %q and there is nothing runnable there. Unset it to look on PATH instead",
			ErrCodexMissing, BinaryEnv, override)
	}

	path, err := d.lookPath(binaryName)
	if err != nil {
		// The underlying lookup failure is deliberately not carried through. "executable file not
		// found in $PATH" adds nothing to the sentence below and turns advice somebody can act on
		// into advice with an implementation detail stapled to it.
		return "", fmt.Errorf(
			"%w. Canopy reaches ChatGPT by driving OpenAI's own Codex app server, which is part of "+
				"the Codex CLI, so there has to be one. Install it with "+
				"`npm install -g @openai/codex` or `brew install codex`, then try again. Canopy signs "+
				"you in through it, so there is nothing to do first",
			ErrCodexMissing)
	}
	return path, nil
}

// codexHome is where Codex keeps its state, which is where the login it owns lives.
func (d Discovery) codexHome() (string, error) {
	if set := strings.TrimSpace(d.getenv(HomeEnv)); set != "" {
		return set, nil
	}
	home, err := d.homeDir()
	if err != nil {
		return "", fmt.Errorf(
			"could not work out where Codex keeps its state, because this machine has no home "+
				"directory Canopy can read: %w. Set %s to say where it is", err, HomeEnv)
	}
	return filepath.Join(home, ".codex"), nil
}

// existingLogin is the degraded path: what can be said about a machine with a Codex login on it and
// no Codex to drive.
//
// This reads and reports, and it deliberately does not authenticate anything onward. That is a
// decision rather than an omission and it is argued for in three parts, because the task block asks
// for a fallback that runs turns and this does not.
//
// The first is D-51, which permits this route "through the Codex app server" and permits the Claude
// route on the explicit ground that Canopy holds none of the user's subscription credential. Lifting
// the tokens out of auth.json and calling chatgpt.com with them is Canopy holding exactly that, on a
// route the decision did not sanction. TASKS.md may expand a decision into executable criteria and
// may not contradict one.
//
// The second is that it would break the thing it is trying to rescue. The refresh token in auth.json
// is the user's own Codex's refresh token, and OpenAI rotates it: whichever process redeems it last
// wins and the other one is signed out. A Canopy that renewed a login it does not own would sign
// somebody out of their own Codex to keep a copy working.
//
// The third is that the fallback's own premise is narrow. auth.json is written by `codex login`, so
// a machine that has one and no binary is a machine where the binary was removed or is not on PATH.
// The sentence that fixes that is the one below, and it is a better answer than an undocumented
// second inference path would have been.
func (d Discovery) existingLogin(home string) (storedLogin, bool) {
	raw, err := d.readFile(filepath.Join(home, "auth.json"))
	if err != nil {
		return storedLogin{}, false
	}
	login, err := parseLogin(raw)
	if err != nil {
		return storedLogin{}, false
	}
	return login, true
}

// storedLogin is what $CODEX_HOME/auth.json says, minus the tokens.
//
// The tokens are read, because the account and the plan are inside one of them, and they are not
// kept: nothing on this type can hold one and nothing returns one. Reading a file to learn a name
// is a different act from holding a credential, and the type is shaped so that the difference
// cannot be eroded by a later edit.
type storedLogin struct {
	// Mode is Codex's own `auth_mode`: "chatgpt" for a subscription, "apikey" for a key.
	Mode string

	// Account and Plan are read out of the id_token's claims, under OpenAI's own namespace.
	Account string
	Plan    string

	// LastRefresh is when Codex last renewed the grant, or the zero time when it never said.
	LastRefresh time.Time
}

// remedy is what to tell somebody who has this login and no binary to use it with.
func (l storedLogin) remedy(home string) string {
	who := l.Account
	if who == "" {
		who = "an account"
	}
	plan := ""
	if l.Plan != "" {
		plan = " on the " + l.Plan + " plan"
	}
	return fmt.Sprintf(
		"There is already a Codex login in %s, as %s%s, so the sign-in is not what is missing: the "+
			"program that uses it is. Reinstall the Codex CLI, or put the one you have back on PATH, "+
			"and this login is the one Canopy will use", home, who, plan)
}

// authFile is the shape of $CODEX_HOME/auth.json. Written by Codex, never by Canopy.
type authFile struct {
	AuthMode     string `json:"auth_mode"`
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	LastRefresh  string `json:"last_refresh"`
	Tokens       *struct {
		IDToken   string `json:"id_token"`
		AccountID string `json:"account_id"`
	} `json:"tokens"`
}

// claimNamespace is where OpenAI put the facts about somebody's ChatGPT plan inside the id token.
const claimNamespace = "https://api.openai.com/auth/"

// parseLogin reads auth.json into the two facts worth having.
//
// The id token is decoded and not verified, and that is correct rather than lazy: this is not a
// security boundary. Nothing is authorised on the strength of what it says, the file it came from
// is mode 0600 in the user's own home directory, and anybody who could forge the claims could have
// changed the answer some other way. What it is used for is a display string.
func parseLogin(raw []byte) (storedLogin, error) {
	var file authFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return storedLogin{}, fmt.Errorf("reading the Codex login on this machine: %w", err)
	}
	if file.Tokens == nil && file.OpenAIAPIKey == "" {
		return storedLogin{}, errors.New("the Codex login on this machine holds nothing")
	}

	login := storedLogin{Mode: file.AuthMode}
	if at, err := time.Parse(time.RFC3339, file.LastRefresh); err == nil {
		login.LastRefresh = at
	}
	if file.Tokens == nil {
		return login, nil
	}

	claims, err := idTokenClaims(file.Tokens.IDToken)
	if err != nil {
		// Not a failure. The file exists and says which mode it is in, which is most of what the
		// remedy needs; a claim set this build cannot read costs a name in a sentence.
		return login, nil
	}
	login.Account, _ = claims["email"].(string)
	login.Plan, _ = claims[claimNamespace+"chatgpt_plan_type"].(string)
	return login, nil
}

// idTokenClaims decodes the payload of a JWT without verifying it. See parseLogin for why that is
// the right amount of work here.
func idTokenClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("the identity token is not in three parts")
	}
	// Raw URL encoding, since JWT payloads are unpadded base64url and Go's padded decoder refuses
	// them.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decoding the identity token: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("reading the identity token: %w", err)
	}
	return claims, nil
}

// BaseURL records which service ultimately answers a turn on this route.
//
// Not an endpoint Canopy calls, and nothing in this package ever will: the app server makes the
// request and Canopy speaks only to the app server, over a pipe. It exists because the keys store
// requires a base URL of an openai-compatible credential, and the honest thing to put there is the
// service that actually answers rather than a placeholder. A credential holding this can never be
// resolved as a plain HTTP one, because both places that build a client branch on the delegated
// kind before they reach the provider switch.
const BaseURL = "https://chatgpt.com/backend-api/codex"
