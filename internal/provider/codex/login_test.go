package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// Signing in, on a machine with no browser and no ChatGPT plan.
//
// Every test here drives the fake app server, so what is being held is the exchange Canopy has with
// it: which flow it asked for, what it put on screen, and what it does when somebody gives up. The
// half that belongs to OpenAI is the half Canopy deliberately does not implement.

func deviceLogin() *loginStartResult {
	return &loginStartResult{
		Type:            loginDeviceCode,
		LoginID:         "login-1",
		VerificationURL: "https://auth.openai.com/codex/device",
		UserCode:        "ABCD-1234",
	}
}

func browserLogin() *loginStartResult {
	return &loginStartResult{
		Type:    loginChatGPT,
		LoginID: "login-1",
		AuthURL: "https://auth.openai.com/oauth/authorize?originator=canopy",
	}
}

// The acceptance clause about signing in: it drives account/login/start and completes.
func TestSigningInDrivesTheAppServersOwnFlowAndEndsWithTheAccountItSignedIn(t *testing.T) {
	server := &appServer{
		login:        browserLogin(),
		loginOutcome: &loginCompletedParams{LoginID: "login-1", Success: true},
		account:      chatgpt("someone@example.com", "pro"),
	}

	login, err := server.vendor().Begin(context.Background(), ModeBrowser)
	if err != nil {
		t.Fatalf("starting the sign-in failed: %v", err)
	}
	account, err := login.Wait(context.Background())
	if err != nil {
		t.Fatalf("the sign-in did not complete: %v", err)
	}

	if account.Email != "someone@example.com" || account.Plan != "pro" {
		t.Errorf("the sign-in produced %+v, want the account asked for after the fact rather than "+
			"guessed at: the completion notification says a sign-in worked and not whose it was",
			account)
	}
	frame, ok := server.sentMethod(methodLoginStart)
	if !ok {
		t.Fatal("no sign-in was started, so nothing happened at OpenAI")
	}
	var params loginStartParams
	if err := json.Unmarshal(frame.Params, &params); err != nil {
		t.Fatalf("the sign-in frame did not decode: %v", err)
	}
	if params.Type != loginChatGPT {
		t.Errorf("the sign-in asked for %q, want %q", params.Type, loginChatGPT)
	}
}

// The acceptance clause about a machine with no browser.
func TestAMachineWithNoBrowserIsGivenACodeToTypeSomewhereElse(t *testing.T) {
	server := &appServer{
		login:        deviceLogin(),
		loginOutcome: &loginCompletedParams{LoginID: "login-1", Success: true},
		account:      chatgpt("someone@example.com", "plus"),
	}

	login, err := server.vendor().Begin(context.Background(), ModeDeviceCode)
	if err != nil {
		t.Fatalf("starting the device-code sign-in failed: %v", err)
	}

	prompt := login.Prompt()
	if prompt.Code != "ABCD-1234" {
		t.Errorf("the code shown was %q, want the one OpenAI issued", prompt.Code)
	}
	if !strings.Contains(prompt.URL, "auth.openai.com") {
		t.Errorf("the address shown was %q, want the verification page", prompt.URL)
	}

	account, err := login.Wait(context.Background())
	if err != nil {
		t.Fatalf("the device-code sign-in did not complete: %v", err)
	}
	if account.Email != "someone@example.com" {
		t.Errorf("the sign-in produced %+v", account)
	}

	frame, _ := server.sentMethod(methodLoginStart)
	var params loginStartParams
	_ = json.Unmarshal(frame.Params, &params)
	if params.Type != loginDeviceCode {
		t.Errorf("the sign-in asked for %q, want %q on a machine with no browser",
			params.Type, loginDeviceCode)
	}
}

// The flow is chosen by looking at the machine, and the guess errs towards the one that works
// anywhere.
func TestASessionThatLooksRemoteIsGivenTheCodeRatherThanTheBrowser(t *testing.T) {
	original := lookupEnv
	t.Cleanup(func() { lookupEnv = original })

	lookupEnv = func(key string) string {
		if key == "SSH_CONNECTION" {
			return "10.0.0.1 4242 10.0.0.2 22"
		}
		return ""
	}
	if got := defaultMode(); got != ModeDeviceCode {
		t.Errorf("an ssh session was offered the %q flow, want %q. The browser flow's callback is a "+
			"localhost address, so over ssh it never arrives and the wait never ends",
			got, ModeDeviceCode)
	}

	lookupEnv = func(key string) string {
		if key == "DISPLAY" {
			return ":0"
		}
		return ""
	}
	if got := defaultMode(); got != ModeBrowser {
		t.Errorf("a local session with a display was offered the %q flow, want %q", got, ModeBrowser)
	}
}

// Nothing is stored and nothing keeps polling when somebody gives up.
func TestCancellingASignInStopsThePollingAndSaysItWasStoppedRatherThanThatItFailed(t *testing.T) {
	server := &appServer{
		login:   deviceLogin(),
		account: chatgpt("someone@example.com", "plus"),
		// No outcome, so the sign-in sits there the way a real one does while somebody reads a code.
	}

	login, err := server.vendor().Begin(context.Background(), ModeDeviceCode)
	if err != nil {
		t.Fatalf("starting the sign-in failed: %v", err)
	}

	waited := make(chan error, 1)
	go func() {
		_, err := login.Wait(context.Background())
		waited <- err
	}()

	// Give the wait a moment to be genuinely waiting, so this is the race the wizard produces.
	time.Sleep(20 * time.Millisecond)
	login.Cancel()

	select {
	case err := <-waited:
		if !errors.Is(err, ErrSignInStopped) {
			t.Errorf("a cancelled sign-in reported %v, want ErrSignInStopped. Nothing went wrong: "+
				"somebody changed their mind, and reporting that as a failure at OpenAI invents a "+
				"fault", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait never returned after Cancel, so a wizard would sit on the sign-in step forever")
	}

	if _, ok := server.sentMethod(methodLoginCancel); !ok {
		t.Error("OpenAI was never told to stop. An abandoned device code otherwise goes on being " +
			"polled every few seconds for as long as the program runs, on behalf of somebody who " +
			"pressed escape")
	}
	if !server.wasStopped() {
		t.Error("the app server was left running after a cancelled sign-in")
	}
}

// A refusal from OpenAI is reported as a refusal, in its own words.
func TestASignInOpenAIRefusedSaysWhatOpenAISaid(t *testing.T) {
	server := &appServer{
		login: deviceLogin(),
		loginOutcome: &loginCompletedParams{
			LoginID: "login-1", Success: false, Error: "the code expired",
		},
	}

	login, err := server.vendor().Begin(context.Background(), ModeDeviceCode)
	if err != nil {
		t.Fatalf("starting the sign-in failed: %v", err)
	}
	if _, err := login.Wait(context.Background()); err == nil {
		t.Fatal("a refused sign-in was reported as a success")
	} else if !strings.Contains(err.Error(), "the code expired") {
		t.Errorf("the refusal was %q, want it to quote what the vendor said", err)
	}
}

// A sign-in that completed and then found nobody signed in is a failure rather than a blank
// credential.
func TestASignInThatCompletesWithNoAccountBehindItIsRefused(t *testing.T) {
	server := &appServer{
		login:        deviceLogin(),
		loginOutcome: &loginCompletedParams{LoginID: "login-1", Success: true},
		// account nil: the app server says it signed somebody in and then reports nobody.
	}

	login, err := server.vendor().Begin(context.Background(), ModeDeviceCode)
	if err != nil {
		t.Fatalf("starting the sign-in failed: %v", err)
	}
	if _, err := login.Wait(context.Background()); !errors.Is(err, ErrNotSignedIn) {
		t.Errorf("the sign-in reported %v, want ErrNotSignedIn. A credential with no account behind "+
			"it is a row nobody can tell from another row", err)
	}
}

// The experimental token-handling mode is not reachable from this package.
func TestCanopyNeverAsksToOwnTheTokenLifecycleItself(t *testing.T) {
	for _, mode := range []LoginMode{ModeBrowser, ModeDeviceCode, ""} {
		server := &appServer{
			login:        browserLogin(),
			loginOutcome: &loginCompletedParams{LoginID: "login-1", Success: true},
			account:      chatgpt("someone@example.com", "plus"),
		}
		login, err := server.vendor().Begin(context.Background(), mode)
		if err != nil {
			t.Fatalf("starting the sign-in failed: %v", err)
		}
		_, _ = login.Wait(context.Background())

		for _, frame := range server.sent() {
			body, _ := json.Marshal(frame)
			if strings.Contains(string(body), "chatgptAuthTokens") {
				t.Fatalf("mode %q reached for chatgptAuthTokens, whose own schema says it is "+
					"unstable, for OpenAI internal use, and not to be used. It exists for hosts "+
					"that own the token lifecycle, which is the liability this route avoids", mode)
			}
		}
	}
}

// Nothing in this package asks the app server to renew the grant, on any path.
func TestNothingHereSpendsTheRefreshTokenTheUsersOwnCodexIsGoingToNeed(t *testing.T) {
	server := &appServer{
		login:        deviceLogin(),
		loginOutcome: &loginCompletedParams{LoginID: "login-1", Success: true},
		account:      chatgpt("someone@example.com", "plus"),
	}

	login, err := server.vendor().Begin(context.Background(), ModeDeviceCode)
	if err != nil {
		t.Fatalf("starting the sign-in failed: %v", err)
	}
	if _, err := login.Wait(context.Background()); err != nil {
		t.Fatalf("the sign-in failed: %v", err)
	}

	// A second fake rather than the same one again, because each of these really is a second
	// process: a fake reused across two connections is one struct with two sets of pipes written
	// over it.
	reporting := &appServer{account: chatgpt("someone@example.com", "plus")}
	if _, _, err := reporting.vendor().Limits(context.Background()); err != nil {
		t.Fatalf("reading the limits failed: %v", err)
	}

	var asked int
	for _, from := range []*appServer{server, reporting} {
		for _, frame := range from.sent() {
			if frame.Method != methodAccountRead {
				continue
			}
			asked++
			var params accountReadParams
			_ = json.Unmarshal(frame.Params, &params)
			if params.RefreshToken {
				t.Error("something asked the app server to renew the grant before answering. " +
					"OpenAI rotate refresh tokens, so whichever process redeems one last wins, and " +
					"Canopy spending it would sign somebody out of their own Codex")
			}
		}
	}
	if asked < 2 {
		t.Fatalf("the account was asked for %d times across a sign-in and a report, want both, so "+
			"this proves less than it claims", asked)
	}
}

// Canopy does not sign somebody out of their own Codex, and there is no way for it to try.
func TestNothingHereCanSignTheUsersOwnCodexOut(t *testing.T) {
	server := &appServer{
		login:        deviceLogin(),
		loginOutcome: &loginCompletedParams{LoginID: "login-1", Success: true},
		account:      chatgpt("someone@example.com", "plus"),
	}

	login, err := server.vendor().Begin(context.Background(), ModeDeviceCode)
	if err != nil {
		t.Fatalf("starting the sign-in failed: %v", err)
	}
	if _, err := login.Wait(context.Background()); err != nil {
		t.Fatalf("the sign-in failed: %v", err)
	}
	login.Cancel()

	if _, ok := server.sentMethod(methodLogout); ok {
		t.Error("account/logout reached the app server. The grant belongs to the app server and " +
			"the user's own `codex` uses the same one, so signing it out because somebody asked " +
			"Canopy to forget a credential is a surprise they cannot undo without signing in again")
	}
}
