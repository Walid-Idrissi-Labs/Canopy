package keys

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Signing in, and why none of it is a secret.
//
// A subscription credential has nothing to paste. There is no string the user holds, so the wizard's
// secret prompt has nothing to ask for and the whole point of this file is to reach a stored
// credential without ever showing one. D-51 permits exactly three routes in and every one of them is
// "drive the vendor's own flow", so what this screen does is name the route, show whatever the vendor
// needs a person to see, and wait.
//
// The division of labour is the part worth reading. The screen never holds a token, never stores a
// credential and never learns one exists: SignIn does all three, and hands back only facts that were
// always safe to draw. That is not politeness, it is the same property Store has at model.go:24 and
// it is kept the same way, by there being no method here that could return a secret even if every
// line of this package were wrong.

// Kind is how Canopy came to hold a credential.
//
// The three words are internal/keys' three words, deliberately spelled the same. They are repeated
// here rather than imported because a screen that imports the credential store stops being swappable
// between the fake and the real one, which is the rule TestEveryInterfacePackageDependsOnTheContract
// holds. Three constants restated is a cheaper price than that, and the conversion happens once, in
// the composition root that already knows both sides.
type Kind string

const (
	// KindPasted is a value somebody typed in, which is what every credential was before phase S.
	KindPasted Kind = "pasted"
	// KindSignedIn is a grant Canopy obtained by signing the user in and holds the tokens for.
	KindSignedIn Kind = "signed-in"
	// KindDelegated is a vendor's own agent the user signed in to themselves, which Canopy drives
	// and holds no credential for.
	KindDelegated Kind = "delegated"
)

// IsSignIn reports whether somebody signed in rather than pasted.
func (k Kind) IsSignIn() bool { return k == KindSignedIn || k == KindDelegated }

// Identity is who a credential is signed in as, and how long that lasts.
//
// Everything here is already safe to draw, which is what lets the list name an account without
// stopping to unlock a keychain. A credential somebody pasted has no identity of its own and reads
// as the zero value, which is the honest answer rather than an absence to be worked around.
type Identity struct {
	Kind Kind

	// Account is who the vendor says the grant belongs to: a login, an email, whatever it reports.
	Account string

	// ExpiresAt is when the access token stops being accepted, and nil when the vendor never said.
	// A pointer for the reason internal/keys uses one: "expires at the zero time" and "nobody knows"
	// are different facts and a row that showed them alike would be inventing one of them.
	ExpiresAt *time.Time
}

// Route is one way of signing in that this build offers.
//
// Data rather than behaviour, so the screen can draw the list of ways in without knowing what any of
// them do. Which routes exist is decided where the vendors live, and this package deliberately
// cannot tell a real one from a test's.
type Route struct {
	// ID is what Begin is called with, and what `canopy keys signin -route` takes.
	ID string

	// Label is the row in the provider list: "GitHub Copilot", not "copilot".
	Label string

	// Detail is the line under it, and it says what has to already be true rather than what the
	// route is. Somebody choosing between three routes is choosing on what they already have.
	Detail string

	// Caveat is shown before anything is stored, for a route that has one worth reading first. The
	// OpenAI route's open 429 report is the case this exists for: told before signing in, not after
	// the first refusal.
	Caveat string

	// VendorPicksModel marks a route whose agent chooses the model itself and offers no say. The
	// model picker says so rather than showing an empty list, which is D-46 rule 1's spirit in a case
	// the catalog does not cover.
	VendorPicksModel bool
}

// Prompt is what a person has to do for a sign-in to complete.
//
// Text, always, and never a browser this program opened. A coding agent is routinely run over ssh on
// a machine with no browser at all, so the surface that works there is the only surface: a code to
// type and a page to visit, printed where they can be read and copied. A route that opens a browser
// as a convenience may still do so; this is what it falls back to and what the tests hold.
type Prompt struct {
	// URL is the page to open, empty for a route that needs none.
	URL string

	// Code is the code to enter there, empty for a route that needs none.
	Code string

	// Doing is what is happening while there is nothing for anybody to do: "looking for Claude
	// Code". A wait with no explanation is indistinguishable from a program that has stopped.
	Doing string
}

// IsZero reports whether there is nothing to show yet.
func (p Prompt) IsZero() bool { return p.URL == "" && p.Code == "" && p.Doing == "" }

// Outcome is what a finished sign-in produced.
//
// The name of a credential that is already in the store, plus who it belongs to. No token, and
// nowhere to put one: by the time this exists the tokens have been stored by the thing that fetched
// them and this screen's only remaining job is to say whose account it was and select it.
type Outcome struct {
	Name     string
	Identity Identity
}

// SignIn is the sign-in machinery, from the screen's side of the boundary.
//
// Two methods, and neither returns a secret. Begin does not store anything and Wait does, which is
// the split that makes cancellation mean something: everything between the two is a person deciding,
// and a person who changes their mind must leave nothing behind.
type SignIn interface {
	// Routes are the ways in this build offers, in the order to show them. Empty is a legitimate
	// state and means the provider list is exactly what it was before phase S.
	Routes() []Route

	// Begin starts a sign-in and returns once the vendor has said what the person has to do.
	//
	// It talks to a vendor, so it is always called off the update loop. Bubble Tea is single
	// threaded and a blocking call here freezes every screen in the program, including the one that
	// would have said why.
	Begin(route Route, name string) (Attempt, error)
}

// Attempt is one sign-in in flight.
//
// Held by the screen only while it is waiting, and abandoned in exactly two ways: Wait returns, or
// Cancel is called. There is no third.
type Attempt interface {
	// Prompt is what to put on screen. Already known by the time Begin returned, so this does no
	// work and is safe on the update loop.
	Prompt() Prompt

	// Wait blocks until the vendor answers, and stores the credential itself.
	//
	// Storing here rather than back on the screen is what keeps a token out of this package
	// altogether. The screen learns a name and an account, which are the two things it draws.
	Wait() (Outcome, error)

	// Cancel abandons the attempt and leaves nothing stored.
	//
	// Including the case that is easy to miss: the vendor may have confirmed in the moment between
	// somebody pressing escape and this call arriving, so Cancel undoes a sign-in that has already
	// completed rather than only stopping one that has not. A cancelled sign-in that left a working
	// credential behind is a credential nobody knows they have.
	//
	// Safe to call concurrently with Wait, because that is the only way it is ever called.
	Cancel()
}

// signInStartedMsg carries the result of Begin back to the update loop.
//
// The attempt number rides along because a message cannot be recalled once its command is running. A
// person who cancels and immediately starts a second sign-in would otherwise have the first one's
// answer arrive and be taken for the second's.
type signInStartedMsg struct {
	attemptID int
	attempt   Attempt
	err       error
}

// signInDoneMsg carries the result of Wait back, and is dropped the same way.
type signInDoneMsg struct {
	attemptID int
	outcome   Outcome
	err       error
}

// beginSignIn asks the vendor what the person has to do.
func beginSignIn(service SignIn, route Route, name string, attemptID int) tea.Cmd {
	return func() tea.Msg {
		attempt, err := service.Begin(route, name)
		return signInStartedMsg{attemptID: attemptID, attempt: attempt, err: err}
	}
}

// awaitSignIn waits for the vendor, which is minutes rather than milliseconds.
func awaitSignIn(attempt Attempt, attemptID int) tea.Cmd {
	return func() tea.Msg {
		outcome, err := attempt.Wait()
		return signInDoneMsg{attemptID: attemptID, outcome: outcome, err: err}
	}
}

// cancelAttempt abandons a sign-in from a command, since revoking a device code is a network call
// like any other and the update loop is not where network calls belong.
func cancelAttempt(attempt Attempt) tea.Cmd {
	return func() tea.Msg {
		attempt.Cancel()
		return nil
	}
}

// startSignIn moves the wizard onto the sign-in step and asks the vendor to begin.
func (m *Model) startSignIn(route Route) tea.Cmd {
	m.abandonAttempt()
	m.mode = modeSignIn
	m.draftRoute = route
	m.prompt = Prompt{}
	m.signingIn = true
	m.status, m.err = "", nil
	return beginSignIn(m.signIn, route, m.draftName, m.attemptID)
}

// abandonAttempt makes every message still in flight stale, and returns the command that stops the
// attempt they belong to.
//
// The counter is bumped before anything else, so there is no window in which a late answer is still
// taken for the current one.
func (m *Model) abandonAttempt() tea.Cmd {
	m.attemptID++
	m.signingIn = false
	m.prompt = Prompt{}

	attempt := m.attempt
	m.attempt = nil
	if attempt == nil {
		return nil
	}
	return cancelAttempt(attempt)
}

// handleSignInKey is the sign-in step's keyboard, which is one key.
//
// Nothing is typed here, which is the entire point of the step, so every key except the one that
// leaves would be a key that does nothing. Escape is the one that leaves.
func (m *Model) handleSignInKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type != tea.KeyEsc {
		return nil
	}
	cancel := m.abandonAttempt()
	m.cancelDraft()
	return cancel
}

// signInStarted takes the vendor's instructions, or its refusal to give any.
func (m *Model) signInStarted(msg signInStartedMsg) tea.Cmd {
	if msg.attemptID != m.attemptID {
		// Somebody cancelled while this was in flight. The attempt still has to be stopped: a device
		// code left polling is a request every few seconds for as long as the program runs, on behalf
		// of a person who pressed escape.
		if msg.attempt != nil {
			return cancelAttempt(msg.attempt)
		}
		return nil
	}

	if msg.err != nil {
		// Back to the route list rather than out to the credential list. The name survives, the other
		// two routes are still there, and a route that is missing something on this machine is a
		// thing somebody can act on without starting again.
		m.signingIn = false
		m.err = msg.err
		m.mode = modeProvider
		return nil
	}
	if msg.attempt == nil {
		m.signingIn = false
		m.err = fmt.Errorf("the %s sign-in started and said nothing about what to do next", m.draftRoute.Label)
		m.mode = modeProvider
		return nil
	}

	m.attempt = msg.attempt
	m.prompt = msg.attempt.Prompt()
	return awaitSignIn(msg.attempt, msg.attemptID)
}

// signInDone takes the finished sign-in and ends the wizard on it.
func (m *Model) signInDone(msg signInDoneMsg) tea.Cmd {
	if msg.attemptID != m.attemptID {
		// Cancelled. Nothing is claimed and nothing is undone here, because Cancel already owns
		// undoing it and two things undoing one sign-in is how one of them removes a credential
		// somebody else had just legitimately created.
		return nil
	}

	m.attempt = nil
	m.signingIn = false
	m.prompt = Prompt{}

	if msg.err != nil {
		m.err = msg.err
		m.mode = modeProvider
		return nil
	}

	// Stored is not chosen, and the wizard ends where the person walking it thinks it ended. This is
	// afterSecret's argument with no secret in it: somebody who has just signed in has said which
	// credential they want, and leaving the list with nothing selected means the next message runs on
	// whichever credential the resolver happens to prefer.
	m.chosen = msg.outcome.Name
	m.storedChoice = true

	m.status = signedInStatus(msg.outcome)
	m.err = nil
	m.mode = modeList
	m.reload()

	for i, key := range m.keys {
		if key.Ref.Name == m.chosen {
			m.cursor = i
			break
		}
	}
	return nil
}

// signedInStatus is what the list says after a sign-in.
//
// The account rather than the credential's name, because the name is something the person invented a
// moment ago and the account is the thing they can check against the vendor. Somebody with two
// subscriptions has just found out which one this is.
func signedInStatus(outcome Outcome) string {
	if outcome.Identity.Account == "" {
		return fmt.Sprintf("Signed %q in.", outcome.Name)
	}
	return fmt.Sprintf("Signed %q in as %s.", outcome.Name, outcome.Identity.Account)
}
