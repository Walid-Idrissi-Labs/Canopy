package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
)

// Signing in, and why Canopy has almost nothing to do with it.
//
// The app server owns this end to end. It builds the authorisation URL, it hosts the loopback
// callback on its own port, it talks to OpenAI, and it keeps the tokens in $CODEX_HOME afterwards.
// What Canopy does is ask it to start, put on screen whatever the person has to go and do, and wait
// to be told it finished. No listener of Canopy's own is opened anywhere here, which is how phase
// S constraint 5 survives this route, and no token passes through this file: there is nothing on
// any type below that could hold one.

// LoginMode is which of the two flows to use.
type LoginMode string

const (
	// ModeBrowser has the app server host a loopback callback and hands the user a URL. It needs a
	// browser on the same machine, because the callback is a localhost address.
	ModeBrowser LoginMode = "browser"

	// ModeDeviceCode hands the user a short code and a URL to type it at, from any device at all.
	// The flow for a machine with no browser, which is most of the machines a coding agent runs on.
	ModeDeviceCode LoginMode = "device-code"
)

// Prompt is what a person has to go and do.
//
// Text, always, and never a browser this program opened. A coding agent is routinely run over ssh
// on a machine with no browser, so the surface that works there is the only surface.
type Prompt struct {
	URL  string
	Code string
}

// Login is one sign-in in flight.
//
// It holds the app server open for the length of the wait, because that process is the thing doing
// the signing in: closing it is how the attempt is abandoned, and it is what Cancel does after
// telling the app server so politely first.
type Login struct {
	vendor  Vendor
	session *session
	prompt  Prompt
	loginID string

	mu        sync.Mutex
	finished  bool
	cancelled bool
}

// ErrSignInStopped means somebody abandoned the sign-in rather than the sign-in failing.
//
// Separate from every other failure because it is not one: nothing went wrong, a person changed
// their mind, and a surface that reported it as an error from OpenAI would be inventing a fault.
var ErrSignInStopped = errors.New("the sign-in was stopped, so nothing was stored")

// Prompt is what to put on screen. Already known by the time Begin returned, so this does no work.
func (l *Login) Prompt() Prompt { return l.prompt }

// Begin asks the app server to sign somebody in and returns once it has said what they must do.
//
// The mode is chosen rather than asked for when the caller does not name one, and the choice is
// made by looking at the machine rather than by preference. See browserReachable.
func (v Vendor) Begin(ctx context.Context, mode LoginMode) (*Login, error) {
	s, err := v.open(ctx)
	if err != nil {
		return nil, err
	}

	if mode == "" {
		mode = defaultMode()
	}
	kind := loginChatGPT
	if mode == ModeDeviceCode {
		kind = loginDeviceCode
	}

	var result loginStartResult
	if err := s.call(methodLoginStart, loginStartParams{Type: kind}, &result); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("asking the Codex app server to start a ChatGPT sign-in: %w", err)
	}

	login := &Login{vendor: v, session: s, loginID: result.LoginID}
	switch {
	case result.UserCode != "":
		login.prompt = Prompt{URL: result.VerificationURL, Code: result.UserCode}
	default:
		login.prompt = Prompt{URL: result.AuthURL}
	}
	if login.prompt.URL == "" {
		_ = s.Close()
		return nil, errors.New(
			"the Codex app server started a sign-in without saying where to go, so there is nothing " +
				"to show you")
	}
	return login, nil
}

// defaultMode picks the flow that can actually complete on this machine.
//
// The browser flow's callback is a localhost address, so it needs a browser on the same machine as
// the app server. Over ssh the browser is somewhere else and the callback never arrives, which
// looks to the user like a sign-in that hangs forever. The device code flow works from any device,
// so it is what a session that looks remote gets. Guessing wrong in this direction costs one extra
// code to type; guessing wrong in the other costs a wait that never ends.
func defaultMode() LoginMode {
	if browserReachable() {
		return ModeBrowser
	}
	return ModeDeviceCode
}

// browserReachable is a judgement rather than a fact, and it is deliberately pessimistic.
//
// Nothing on any platform truthfully answers "can this user see a browser window", so this reads
// the two signals that are usually right: an ssh session is somebody sitting at a different
// machine, and a Unix desktop with no display server has nothing to open a window on. macOS and
// Windows have no equivalent of the second, so they are taken at face value.
func browserReachable() bool {
	if remoteSession() {
		return false
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default:
		return displaySet()
	}
}

// The environment is read through a package variable so a test can answer for a machine it is not
// running on. The acceptance for this route is about machines with no browser, and a test that
// needed one would be a test that runs on one laptop.
var lookupEnv = os.Getenv

func remoteSession() bool {
	return lookupEnv("SSH_CONNECTION") != "" || lookupEnv("SSH_TTY") != "" ||
		lookupEnv("SSH_CLIENT") != ""
}

func displaySet() bool {
	return lookupEnv("DISPLAY") != "" || lookupEnv("WAYLAND_DISPLAY") != ""
}

// Wait blocks until OpenAI confirms, or until the app server says it will not.
//
// Reads the connection rather than polling, because the protocol says so: the sign-in ends with one
// notification and there is no method to ask "is it done yet". Cancelling produces the same
// notification with success false, which was confirmed against a real app server rather than
// assumed, so there is exactly one event that ends this wait and no path that leaves it hanging.
func (l *Login) Wait(ctx context.Context) (Account, error) {
	defer l.close()

	completed := make(chan loginCompletedParams, 1)
	failed := make(chan error, 1)

	go func() {
		for {
			m, err := l.session.conn.read()
			if err != nil {
				failed <- err
				return
			}
			if m.Method == notifyLoginCompleted {
				var params loginCompletedParams
				if err := json.Unmarshal(m.Params, &params); err != nil {
					failed <- fmt.Errorf("the Codex app server ended the sign-in unreadably: %w", err)
					return
				}
				completed <- params
				return
			}
			l.session.decline(m)
		}
	}()

	select {
	case <-ctx.Done():
		return Account{}, ctx.Err()

	case err := <-failed:
		// Cancellation is checked before the read error, for the reason every stream in this
		// repository checks it first: Cancel stops the app server, so looking at the error first
		// would report a sign-in somebody abandoned as a sign-in that broke.
		if l.wasCancelled() {
			return Account{}, ErrSignInStopped
		}
		if said := lastLine(l.session.child.stderr.String()); said != "" {
			return Account{}, fmt.Errorf("the Codex app server stopped during the sign-in: %s", said)
		}
		return Account{}, fmt.Errorf("waiting for the ChatGPT sign-in to finish: %w", err)

	case params := <-completed:
		if !params.Success {
			if l.wasCancelled() {
				return Account{}, ErrSignInStopped
			}
			return Account{}, fmt.Errorf("the ChatGPT sign-in did not complete: %s",
				orNoReason(params.Error))
		}
	}

	// Asked rather than assumed. The notification says a sign-in succeeded and says nothing about
	// whose it was, and the account is the one fact a credential cannot do without: two
	// subscriptions on one machine are otherwise two identical rows.
	account, err := l.vendor.account(l.session, false)
	if err != nil {
		return Account{}, err
	}
	if !account.OnSubscription() {
		// Not a failure. Somebody whose Codex authenticates with an API key has a working route and
		// a different bill, and they should hear that from Canopy rather than from an invoice.
		return account, nil
	}
	return account, nil
}

func orNoReason(said string) string {
	if trimmed := strings.TrimSpace(said); trimmed != "" {
		return trimmed
	}
	return "OpenAI did not say why"
}

// Cancel abandons the sign-in and leaves nothing behind.
//
// The app server is told first and killed second. Telling it is what stops a device code polling
// OpenAI every few seconds on behalf of somebody who pressed escape, and it is also what makes the
// waiter return, since a cancelled login still produces its completion notification. Closing the
// process is the belt to that brace: an app server that ignored the cancel is stopped anyway.
//
// Safe to call concurrently with Wait, which is the only way it is ever called.
func (l *Login) Cancel() {
	l.mu.Lock()
	l.cancelled = true
	l.mu.Unlock()

	if l.loginID != "" {
		// Sent on the connection directly rather than through call, because Wait owns the reads and
		// two readers on one pipe is a frame going to whichever of them asked first. The answer is
		// never collected, which is correct: the process is about to be stopped and the only thing
		// that mattered was that OpenAI stopped being polled.
		_, _ = l.session.conn.send(methodLoginCancel, loginCancelParams{LoginID: l.loginID})
	}
	l.close()
}

func (l *Login) wasCancelled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cancelled
}

func (l *Login) close() {
	l.mu.Lock()
	already := l.finished
	l.finished = true
	l.mu.Unlock()

	if !already {
		_ = l.session.Close()
	}
}
