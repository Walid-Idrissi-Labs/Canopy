package codex

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Discovery, on machines this test is not running on.
//
// Every case here is a machine without a Codex, without a login, or with one and not the other, and
// none of them can be reached by looking at the laptop the suite happens to run on. That is what the
// function fields on Discovery are for.

// machine builds a Discovery over an invented machine.
func machine(binaries map[string]string, env map[string]string, files map[string]string) Discovery {
	return Discovery{
		LookPath: func(name string) (string, error) {
			if path, ok := binaries[name]; ok {
				return path, nil
			}
			return "", errors.New("executable file not found in $PATH")
		},
		Getenv:  func(key string) string { return env[key] },
		HomeDir: func() (string, error) { return "/home/someone", nil },
		ReadFile: func(name string) ([]byte, error) {
			if body, ok := files[name]; ok {
				return []byte(body), nil
			}
			return nil, os.ErrNotExist
		},
	}
}

// idToken builds an unsigned identity token carrying the claims Codex writes.
//
// Unsigned because nothing verifies it and nothing should: see parseLogin for why reading these
// claims is not a security boundary.
func idToken(email, plan string) string {
	claims := map[string]any{
		"email":                               email,
		claimNamespace + "chatgpt_plan_type":  plan,
		claimNamespace + "chatgpt_account_id": "acct-1",
	}
	body, _ := json.Marshal(claims)
	return "header." + base64.RawURLEncoding.EncodeToString(body) + ".signature"
}

func authJSON(mode, email, plan string) string {
	return fmt.Sprintf(`{
		"auth_mode": %q,
		"OPENAI_API_KEY": null,
		"tokens": {
			"id_token": %q,
			"access_token": "an-access-token",
			"refresh_token": "a-refresh-token",
			"account_id": "acct-1"
		},
		"last_refresh": "2026-07-20T21:03:00.000000000Z"
	}`, mode, idToken(email, plan))
}

// The acceptance clause about neither being present.
func TestAMachineWithNeitherCodexNorALoginIsToldWhatToInstall(t *testing.T) {
	_, err := machine(nil, nil, nil).Find()
	if err == nil {
		t.Fatal("a machine with no Codex at all was reported as usable")
	}
	if !errors.Is(err, ErrCodexMissing) {
		t.Errorf("the failure was %v, want it to wrap ErrCodexMissing so a caller can tell it "+
			"apart from being signed out", err)
	}
	said := err.Error()
	for _, want := range []string{"@openai/codex", "brew install codex"} {
		if !strings.Contains(said, want) {
			t.Errorf("the failure does not name %q. A discovery step exists to replace "+
				"'no such file or directory' with a sentence somebody can act on. Got: %s",
				want, said)
		}
	}
	if strings.Contains(said, "no such file") || strings.Contains(said, "$PATH") {
		t.Errorf("the underlying lookup failure leaked into the message: %s", said)
	}
}

// The acceptance clause about auth.json present and the binary absent.
func TestAMachineWithALoginAndNoCodexIsToldTheProgramIsMissingRatherThanTheSignIn(t *testing.T) {
	home := "/home/someone/.codex"
	_, err := machine(nil, nil, map[string]string{
		filepath.Join(home, "auth.json"): authJSON("chatgpt", "someone@example.com", "pro"),
	}).Find()

	if err == nil {
		t.Fatal("a machine with a login and no binary was reported as usable")
	}
	said := err.Error()
	for _, want := range []string{"someone@example.com", "pro", home, "not what is missing"} {
		if !strings.Contains(said, want) {
			t.Errorf("the failure does not mention %q. Somebody with a login already does not need "+
				"to be told what Codex is, they need to be told which half is missing. Got: %s",
				want, said)
		}
	}
	if !errors.Is(err, ErrCodexMissing) {
		t.Errorf("the failure was %v, want it still to wrap ErrCodexMissing", err)
	}
}

// The degraded path reads and reports, and never becomes a second way to make requests.
func TestTheDegradedPathReadsWhoIsSignedInAndHandsBackNoToken(t *testing.T) {
	login, err := parseLogin([]byte(authJSON("chatgpt", "someone@example.com", "pro")))
	if err != nil {
		t.Fatalf("a real auth.json did not parse: %v", err)
	}
	if login.Account != "someone@example.com" || login.Plan != "pro" {
		t.Errorf("the login read as %+v, want the account and plan out of the identity token", login)
	}
	if login.LastRefresh.IsZero() {
		t.Error("last_refresh was not read, so nothing can say how old the grant is")
	}

	// The type is shaped so that a later edit cannot start carrying a token through it, which is
	// the property that keeps reading a file to learn a name different from holding a credential.
	body, marshalErr := json.Marshal(login)
	if marshalErr != nil {
		t.Fatalf("the login did not marshal: %v", marshalErr)
	}
	for _, token := range []string{"an-access-token", "a-refresh-token"} {
		if strings.Contains(string(body), token) {
			t.Errorf("a token from auth.json survived into %T, which is exactly what D-51 permits "+
				"this route on the grounds of not doing", login)
		}
	}
}

// An API-key Codex is not described as a subscription.
func TestALoginFileFromAnAPIKeyCodexIsNotDescribedAsASubscription(t *testing.T) {
	login, err := parseLogin([]byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-something"}`))
	if err != nil {
		t.Fatalf("an api-key auth.json did not parse: %v", err)
	}
	if login.Mode != "apikey" {
		t.Errorf("the mode read as %q, want the file's own word", login.Mode)
	}
	if login.Plan != "" {
		t.Errorf("a plan of %q was invented for an account that has none", login.Plan)
	}
}

// An unreadable identity token costs a name in a sentence and nothing else.
func TestALoginWhoseIdentityTokenCannotBeReadIsStillALogin(t *testing.T) {
	login, err := parseLogin([]byte(`{"auth_mode":"chatgpt","tokens":{"id_token":"not-a-jwt"}}`))
	if err != nil {
		t.Fatalf("a login with an unreadable token was rejected outright: %v", err)
	}
	if login.Mode != "chatgpt" {
		t.Errorf("the mode read as %q, want the file's own word", login.Mode)
	}
	if login.Account != "" {
		t.Errorf("an account of %q was invented from a token that does not parse", login.Account)
	}
}

// The override is checked rather than trusted.
func TestAnOverriddenBinaryIsCheckedRatherThanTrusted(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		found, err := machine(
			map[string]string{"/opt/codex-dev": "/opt/codex-dev"},
			map[string]string{BinaryEnv: "/opt/codex-dev"}, nil,
		).Find()
		if err != nil {
			t.Fatalf("a working override was refused: %v", err)
		}
		if found.Binary != "/opt/codex-dev" {
			t.Errorf("the override resolved to %q", found.Binary)
		}
	})

	t.Run("stale", func(t *testing.T) {
		_, err := machine(
			map[string]string{"codex": "/usr/bin/codex"},
			map[string]string{BinaryEnv: "/opt/gone"}, nil,
		).Find()
		if err == nil {
			t.Fatal("a stale override fell back to PATH silently, so somebody would be running a " +
				"different binary from the one they asked for and never find out")
		}
		if !strings.Contains(err.Error(), BinaryEnv) || !strings.Contains(err.Error(), "/opt/gone") {
			t.Errorf("the failure was %q, want it to name the setting and its value", err)
		}
	})
}

// Codex's own variable decides where its state lives, because somebody who set it meant it.
func TestCodexHomeIsWhereCodexSaysItIsRatherThanWhereCanopyGuesses(t *testing.T) {
	found, err := machine(
		map[string]string{"codex": "/usr/bin/codex"},
		map[string]string{HomeEnv: "/elsewhere/codex"}, nil,
	).Find()
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	if found.Home != "/elsewhere/codex" {
		t.Errorf("Canopy looked in %q while the user's own Codex uses /elsewhere/codex, so it "+
			"would report an account that is not the one their turns run on", found.Home)
	}

	byDefault, err := machine(map[string]string{"codex": "/usr/bin/codex"}, nil, nil).Find()
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	if byDefault.Home != filepath.Join("/home/someone", ".codex") {
		t.Errorf("the default home was %q, want ~/.codex", byDefault.Home)
	}
}

// The binary is discovered and not bundled, which was the choice this task had to make and record.
func TestTheBinaryIsFoundOnTheMachineRatherThanShippedInside(t *testing.T) {
	found, err := machine(map[string]string{"codex": "/usr/local/bin/codex"}, nil, nil).Find()
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	if found.Binary != "/usr/local/bin/codex" {
		t.Errorf("the binary resolved to %q, want the one on the machine", found.Binary)
	}
	// Nothing in the repository ships one, which is the other half of the claim.
	if _, err := os.Stat("codex"); err == nil {
		t.Error("a codex binary is sitting in this package, which would be a licence obligation and " +
			"a release artefact nobody decided to take on")
	}
}
