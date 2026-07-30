package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/codex"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// The ChatGPT route, which is the one where Canopy asks somebody else to do the signing in.
//
// It sits between the Copilot route, where Canopy runs the flow and keeps the tokens, and the
// Claude route, where the user signed in themselves months ago and Canopy only goes looking. This
// one starts a real sign-in, and still ends with a credential that holds nothing: the Codex app
// server runs the flow, hosts the callback and keeps the grant in $CODEX_HOME afterwards. So the
// credential is keys.KindDelegated, the same as Claude's, and internal/keys refuses to put a token
// behind it.
//
// It sits in cmd/canopy rather than in internal/provider/codex for the reason credentials.go gives
// about itself: this is where the screen's vocabulary and the credential store's vocabulary meet,
// and neither of them is allowed to name the other.

const (
	// codexRouteID is the browser flow, whose callback the app server hosts on its own loopback
	// port.
	codexRouteID = "chatgpt"

	// codexDeviceRouteID is the same sign-in with a code to type instead, for a machine whose
	// browser is somewhere else.
	codexDeviceRouteID = "chatgpt-device"
)

// codexSignIn is the sign-in registry for the ChatGPT route.
type codexSignIn struct {
	// store is where a finished sign-in is written. Nil means the real one, opened when it is
	// needed rather than at process start: this is reached from a package variable, and opening a
	// keychain to build a list of route names would prompt for an unlock on every command that has
	// nothing to do with credentials.
	store *keys.Store

	// vendor is the Codex being driven. Nil drives the real one.
	vendor codexVendor
}

// codexVendor is the part of the Codex on this machine that this file uses.
//
// An interface rather than the concrete codex.Vendor for one reason, and it is a test reason said
// out loud rather than dressed up: it is what lets the tests here drive a sign-in, a cancellation
// and `canopy keys test` on a machine with no Codex installed and no ChatGPT plan to bill. The
// alternative was exporting a transport seam from internal/provider/codex so a fake app server
// could be handed in from here, which would have meant a wider public surface on that package and a
// second copy of its fake in this one. Two methods and no new exported type is the cheaper price.
type codexVendor interface {
	Begin(ctx context.Context, mode codex.LoginMode) (codexLogin, error)
	Limits(ctx context.Context) (codex.Account, codex.Limits, error)
}

// codexLogin is one sign-in in flight, which *codex.Login already is.
type codexLogin interface {
	Prompt() codex.Prompt
	Wait(ctx context.Context) (codex.Account, error)
	Cancel()
}

// realCodex is the Codex on this machine, behind the interface above.
//
// It exists only because Begin returns a concrete pointer and the interface returns an interface,
// which Go does not treat as the same signature. Nothing else about it is interesting.
type realCodex struct{ vendor codex.Vendor }

func (r realCodex) Begin(ctx context.Context, mode codex.LoginMode) (codexLogin, error) {
	return r.vendor.Begin(ctx, mode)
}

func (r realCodex) Limits(ctx context.Context) (codex.Account, codex.Limits, error) {
	return r.vendor.Limits(ctx)
}

// codexOf is the vendor this route drives, which is the real one unless a test said otherwise.
func (c codexSignIn) codexOf() codexVendor {
	if c.vendor != nil {
		return c.vendor
	}
	return realCodex{vendor: codex.Vendor{Version: version}}
}

var (
	_ keysui.SignIn        = codexSignIn{}
	_ reportsOnCredentials = codexSignIn{}
)

// quotaCaveat is what somebody is told before they sign in rather than after their first refusal.
//
// There is an open, unanswered report against this route of 429 quota errors on active Plus plans
// for third-party OAuth, which if it is real means the route is quota-segregated from what the same
// person sees in the ChatGPT client. Unanswered is not the same as untrue, and the honest place for
// a maybe is in front of the decision it would change. keysui.Route.Caveat exists for this case and
// is drawn before anything is stored, on both surfaces.
const quotaCaveat = "Canopy does not hold a ChatGPT credential of yours: OpenAI's own Codex app " +
	"server signs you in, keeps the tokens in ~/.codex, and answers the turns. Two things to know " +
	"first. There is an open, unanswered report of third-party sign-ins hitting 429 quota errors on " +
	"active Plus plans, so this may draw on a smaller allowance than the ChatGPT app does. And a " +
	"delegated turn runs Codex's own tools in Codex's own sandbox: Canopy's permission gate is not " +
	"in that path, and Canopy declines every approval Codex asks it for."

func (c codexSignIn) keyStore() (*keys.Store, error) {
	if c.store != nil {
		return c.store, nil
	}
	return openKeyStore()
}

// Routes offers the same sign-in twice, differing only in what the person has to do.
//
// Two entries rather than one with a guess, because the guess is occasionally wrong in a way that
// costs somebody a wait that never ends: the browser flow's callback is a localhost address, so it
// only completes if the browser is on this machine. Canopy picks between them by looking at the
// session when the plain route is chosen, and the second entry is there for the person the guess
// got wrong.
//
// Both store the same route on the credential. How somebody signed in is not a property of the
// credential; where its turns go is, and that is the app server either way.
func (codexSignIn) Routes() []keysui.Route {
	return []keysui.Route{
		{
			ID:    codexRouteID,
			Label: "ChatGPT (Codex)",
			// What has to already be true, which is what somebody choosing between routes is
			// choosing on. Not what the route is.
			Detail: "a ChatGPT plan and the Codex CLI installed " +
				"(`npm install -g @openai/codex`)",
			Caveat: quotaCaveat,
			// Codex picks its own default and Canopy stores no model for a delegated credential.
			// Saying so beats a model picker whose selection changes nothing.
			VendorPicksModel: true,
		},
		{
			ID:               codexDeviceRouteID,
			Label:            "ChatGPT (Codex), with a code to type elsewhere",
			Detail:           "the same, on a machine whose browser is somewhere else",
			Caveat:           quotaCaveat,
			VendorPicksModel: true,
		},
	}
}

func (c codexSignIn) Begin(route keysui.Route, name string) (keysui.Attempt, error) {
	var mode codex.LoginMode
	switch route.ID {
	case codexRouteID:
		// Left empty so the vendor looks at the machine. See codex.defaultMode.
	case codexDeviceRouteID:
		mode = codex.ModeDeviceCode
	default:
		return nil, unknownRouteError(route.ID, c.Routes())
	}

	store, err := c.keyStore()
	if err != nil {
		return nil, err
	}

	// Begun here rather than inside Wait, because Begin's contract is that it returns once the
	// vendor has said what the person has to do, and on this route that is a real round trip to
	// OpenAI. Everything after it is waiting.
	ctx, cancel := context.WithTimeout(context.Background(), codexStartTimeout)
	defer cancel()

	login, err := c.codexOf().Begin(ctx, mode)
	if err != nil {
		return nil, err
	}
	return &codexAttempt{login: login, store: store, name: name}, nil
}

// codexStartTimeout bounds getting as far as a code on the screen.
//
// Short, because nothing in it waits for a person: it starts a process, does a handshake and asks
// OpenAI for a device code or an authorisation URL. The long wait is afterwards and is bounded
// separately.
const codexStartTimeout = 60 * time.Second

// codexWaitTimeout bounds waiting for somebody to finish signing in.
//
// Fifteen minutes, which is a person finding their phone, opening a page and typing a code, with
// room for them to be interrupted. Shorter would fail people who walked away for a moment; longer
// would leave an abandoned attempt polling OpenAI for most of an hour.
const codexWaitTimeout = 15 * time.Minute

// Report is what `canopy keys test` asks the vendor about an existing credential.
//
// The strongest honest answer available on this route, and the one S-07 built `reportsOnCredentials`
// for: not a stored fact read back, but the account and its live plan limits asked for again.
// `account/rateLimits/read` is a fact about the subscription rather than a fact about the file,
// which is what stops `keys test` on this route from having to say that no vendor was contacted.
func (c codexSignIn) Report(ctx context.Context, meta core.KeyMetadata) (signInReport, error) {
	account, limits, err := c.codexOf().Limits(ctx)
	if err != nil {
		return signInReport{}, err
	}

	facts := []signInFact{{Label: "plan", Value: planOrUnstated(account, limits)}}
	if limits.Primary != nil {
		facts = append(facts, signInFact{Label: "usage", Value: limits.Primary.String()})
	}
	if limits.Secondary != nil {
		facts = append(facts, signInFact{Label: "also", Value: limits.Secondary.String()})
	}
	if limits.Reached != "" {
		facts = append(facts, signInFact{
			Label: "limit hit",
			Value: strings.ReplaceAll(limits.Reached, "_", " ") +
				", so turns on this credential are being refused right now",
		})
	}
	if limits.Credits != "" && limits.Credits != "none" {
		facts = append(facts, signInFact{Label: "credits", Value: limits.Credits})
	}

	store, storeErr := c.keyStore()
	if in, err := storeSignIn(store, storeErr, meta.Ref); err == nil && in.Account != "" &&
		in.Account != account.Email && account.Email != "" {
		// Two ChatGPT accounts on one machine is an ordinary arrangement, and Codex holds one login
		// at a time, so this is a fact rather than a fault. It is worth saying loudly: turns on this
		// credential draw on the account below, not the one the credential is named after.
		facts = append(facts, signInFact{
			Label: "note",
			Value: fmt.Sprintf(
				"this credential was added for %s, and Codex is now signed in as %s. Turns run as %s",
				in.Account, account.Email, account.Email),
		})
	}
	if !account.OnSubscription() {
		facts = append(facts, signInFact{
			Label: "billing",
			Value: "an API account rather than a ChatGPT plan, so turns are billed per token there",
		})
	}

	return signInReport{
		Vendor:  "the Codex app server on this machine",
		Account: account.Email,
		Facts:   facts,
	}, nil
}

// planOrUnstated prefers the plan the limits belong to, since a workspace member's limits are the
// workspace's and that is the number that will actually stop them.
func planOrUnstated(account codex.Account, limits codex.Limits) string {
	switch {
	case limits.Plan != "":
		return limits.Plan
	case account.Plan != "":
		return account.Plan
	default:
		return "not stated"
	}
}

// codexAttempt is a sign-in that is genuinely waiting for a person.
//
// The first route in this build where that is true. Copilot's is the same shape, Claude's has
// nothing to wait for, and this one hands somebody a code and then sits there. Everything about it
// is arranged around the two ways that ends: the vendor answers, or somebody gives up. Cancel owns
// the second, including the case where both happen at once.
type codexAttempt struct {
	login codexLogin
	store *keys.Store
	name  string

	mu        sync.Mutex
	cancelled bool
	stored    bool
}

func (a *codexAttempt) Prompt() keysui.Prompt {
	prompt := a.login.Prompt()
	return keysui.Prompt{URL: prompt.URL, Code: prompt.Code}
}

func (a *codexAttempt) Wait() (keysui.Outcome, error) {
	ctx, cancel := context.WithTimeout(context.Background(), codexWaitTimeout)
	defer cancel()

	account, err := a.login.Wait(ctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return keysui.Outcome{}, fmt.Errorf(
				"nobody finished the ChatGPT sign-in within %s, so nothing was stored. Run the "+
					"command again for a fresh code", codexWaitTimeout)
		}
		return keysui.Outcome{}, err
	}

	// Checked before the write rather than only after it, so the common case of an escape pressed
	// during the wait costs nothing at all.
	if a.abandoned() {
		return keysui.Outcome{}, codex.ErrSignInStopped
	}

	ref := core.KeyRef{Name: a.name, Provider: core.ProviderOpenAICompatible}
	// No tokens and no expiry, and internal/keys refuses this record if it ever arrives with
	// either. That refusal is D-51's reason for permitting this route turned into something the
	// build enforces: the app server holds the grant, renews it, and Canopy never sees it.
	meta, err := a.store.PutSignIn(
		core.KeyMetadata{Ref: ref, BaseURL: codex.BaseURL},
		keys.SignIn{Kind: keys.KindDelegated, Account: accountName(account), Route: codex.Route},
		keys.Tokens{},
	)
	if err != nil {
		return keysui.Outcome{}, err
	}

	if !a.markStored() {
		// Escape landed between OpenAI confirming and the write. Undone here rather than left,
		// because a cancelled sign-in that leaves a working credential behind is a credential
		// nobody knows they have.
		_ = a.store.Remove(ref)
		return keysui.Outcome{}, codex.ErrSignInStopped
	}

	return keysui.Outcome{
		Name: meta.Ref.Name,
		Identity: keysui.Identity{
			Kind:    keysui.KindDelegated,
			Account: accountName(account),
			// No expiry, deliberately. The grant does expire, and it is not Canopy's grant: the app
			// server renews it without being asked, so a date here would be Canopy promising to act
			// on something it does not hold. See the notes on S-05.
		},
	}, nil
}

// accountName is what the credential records as the account it belongs to.
//
// Required by internal/keys, which refuses a sign-in without one, and required for a better reason
// than that: two ChatGPT accounts on one machine are otherwise two identical rows.
func accountName(account codex.Account) string {
	if name := strings.TrimSpace(account.String()); name != "" {
		return name
	}
	return "a ChatGPT account"
}

func (a *codexAttempt) Cancel() {
	a.mu.Lock()
	a.cancelled = true
	alreadyStored := a.stored
	a.mu.Unlock()

	// The vendor first, because a device code nobody cancelled goes on polling OpenAI every few
	// seconds for as long as the process runs, on behalf of somebody who pressed escape.
	a.login.Cancel()

	if alreadyStored {
		// The other half of the race. Wait finished and wrote the credential before this arrived, so
		// undoing it is this call's job rather than Wait's.
		_ = a.store.Remove(core.KeyRef{Name: a.name})
	}
}

func (a *codexAttempt) abandoned() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancelled
}

// markStored records that the credential exists, reporting whether it is allowed to survive.
func (a *codexAttempt) markStored() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelled {
		return false
	}
	a.stored = true
	return true
}
