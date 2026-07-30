package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/acp"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// The Claude route, which is the one where signing in is somebody else's job.
//
// Every other route this file's neighbours will hold is a sign-in: a code, a browser, a token that
// comes back and gets stored. This one has none of that and must not grow any, because D-51 permits
// it precisely on the grounds that Canopy never holds an Anthropic subscription credential. So
// "signing in" here means looking: finding the Claude Code the user already signed in to, asking it
// which account that is, and writing down that a delegated agent exists.
//
// It sits in cmd/canopy rather than in internal/provider/acp for the reason credentials.go gives
// about itself: this is where the screen's vocabulary and the credential store's vocabulary meet, and
// neither of them is allowed to name the other.

// claudeCodeRouteID is what `canopy keys signin <name> -route claude-code` takes.
const claudeCodeRouteID = "claude-code"

// claudeCode is the sign-in registry for the delegated Claude route.
//
// One route today. S-03 and S-05 each add another and will need these three composed rather than
// replaced; that composition is deliberately not written yet, because a registry-of-registries with
// one member in it is a shape guessed at before the second case exists.
type claudeCode struct {
	// store is where a finished sign-in is written. Nil means the real one, opened when it is needed
	// rather than at process start: this is a package variable, and opening a keychain to build a
	// list of route names would prompt for an unlock on every command that has nothing to do with
	// credentials. A test sets it to a temporary store.
	store *keys.Store

	// discovery is how the machine is inspected. The zero value looks at the real one.
	discovery acp.Discovery
}

// keyStore is the store to write to, opened on demand.
func (c claudeCode) keyStore() (*keys.Store, error) {
	if c.store != nil {
		return c.store, nil
	}
	return openKeyStore()
}

var _ keysui.SignIn = claudeCode{}

func (claudeCode) Routes() []keysui.Route {
	return []keysui.Route{{
		ID:    claudeCodeRouteID,
		Label: "Claude Code",
		// What has to already be true, which is the thing somebody choosing between routes is
		// choosing on. Not what the route is.
		Detail: "Claude Code installed and signed in, plus the ACP bridge " +
			"(`npm install -g @agentclientprotocol/claude-agent-acp`)",
		Caveat: "Canopy does not sign you in to Claude and never holds a Claude credential: " +
			"Anthropic do not permit third-party tools to do either. It drives the Claude Code you " +
			"signed in to yourself, so turns draw on that plan's usage limits. Claude Code runs its " +
			"own tools under its own permissions, and Canopy's permission gate is not in that path.",
		// The delegated agent picks its own model. Saying so is better than a model picker whose
		// selection changes nothing.
		VendorPicksModel: true,
	}}
}

func (c claudeCode) Begin(route keysui.Route, name string) (keysui.Attempt, error) {
	if route.ID != claudeCodeRouteID {
		return nil, unknownRouteError(route.ID, c.Routes())
	}
	store, err := c.keyStore()
	if err != nil {
		return nil, err
	}
	return &claudeCodeAttempt{route: c, store: store, name: name}, nil
}

// Report is what `canopy keys test` asks the vendor about an existing credential.
//
// The strongest honest answer available on this route, and a better one than most: it is not a stored
// fact read back, it is the machine being looked at again. A credential that was added last month
// against a Claude Code that has since been signed out of, uninstalled, or signed in as somebody else
// says so here rather than at the next turn.
func (c claudeCode) Report(ctx context.Context, meta core.KeyMetadata) (signInReport, error) {
	found, err := c.discovery.Find(ctx)
	if err != nil {
		return signInReport{}, err
	}

	facts := []signInFact{
		{Label: "plan", Value: planOrNothing(found.Account)},
		{Label: "signed in", Value: found.CLI},
		{Label: "bridge", Value: found.Bridge},
	}
	store, storeErr := c.keyStore()
	if in, err := storeSignIn(store, storeErr, meta.Ref); err == nil && in.Account != "" &&
		in.Account != found.Account.Email {
		// Two subscriptions on one machine is an ordinary arrangement, so this is a fact rather than
		// a fault. It is worth saying loudly: turns on this credential bill the account below, not
		// the one the credential is named after.
		facts = append(facts, signInFact{
			Label: "note",
			Value: fmt.Sprintf(
				"this credential was added for %s, and Claude Code is now signed in as %s. "+
					"Turns run as %s", in.Account, found.Account.Email, found.Account.Email),
		})
	}
	if !found.Account.OnSubscription() {
		facts = append(facts, signInFact{
			Label: "billing",
			Value: "an API account rather than a subscription, so turns are billed per token there",
		})
	}

	return signInReport{
		Vendor:  "Claude Code on this machine",
		Account: found.Account.Email,
		Facts:   facts,
	}, nil
}

// storeSignIn reads a credential's sign-in facts, carrying an earlier failure through rather than
// panicking on a nil store. The caller only wants to add a line when there is one to add.
func storeSignIn(store *keys.Store, err error, ref core.KeyRef) (keys.SignIn, error) {
	if err != nil {
		return keys.SignIn{}, err
	}
	return store.SignIn(ref)
}

func planOrNothing(account acp.Account) string {
	if account.Plan == "" {
		return "not stated"
	}
	return account.Plan
}

// claudeCodeAttempt is a sign-in with nothing to wait for.
//
// The Attempt shape exists for routes where a person has to go and do something in a browser, and on
// this one there is nothing for them to do: the doing already happened, in Claude Code, possibly
// months ago. So Prompt says what Canopy is doing rather than what they are, and Wait is the search.
//
// Held to the same contract as a route that does wait, and the part that matters is Cancel. A person
// who presses escape while this is running must be left with nothing, and "nothing" here has to
// include the credential a search that finished a millisecond later would have written.
type claudeCodeAttempt struct {
	route claudeCode
	store *keys.Store
	name  string

	mu        sync.Mutex
	cancelled bool
	stored    bool
}

func (a *claudeCodeAttempt) Prompt() keysui.Prompt {
	return keysui.Prompt{Doing: "Looking for the Claude Code you signed in to"}
}

func (a *claudeCodeAttempt) Wait() (keysui.Outcome, error) {
	ctx, cancel := context.WithTimeout(context.Background(), vendorTimeout*3)
	defer cancel()

	found, err := a.route.discovery.Find(ctx)
	if err != nil {
		return keysui.Outcome{}, err
	}

	// Checked before the write rather than only after it, so the common case of an escape pressed
	// during the search costs nothing at all.
	if a.abandoned() {
		return keysui.Outcome{}, errors.New("the sign-in was stopped, so nothing was stored")
	}

	ref := core.KeyRef{Name: a.name, Provider: core.ProviderAnthropic}
	// No tokens, no expiry, and internal/keys refuses this record if it ever arrives with either.
	// That refusal is D-51's reason for permitting this route turned into something the build
	// enforces, and this call is the only place in Canopy that exercises it.
	meta, err := a.store.PutSignIn(
		core.KeyMetadata{Ref: ref},
		keys.SignIn{Kind: keys.KindDelegated, Account: found.Account.Email},
		keys.Tokens{},
	)
	if err != nil {
		return keysui.Outcome{}, err
	}

	if !a.markStored() {
		// Escape landed between the search and the write. Undone here rather than left, because a
		// cancelled sign-in that leaves a working credential behind is a credential nobody knows
		// they have.
		_ = a.store.Remove(ref)
		return keysui.Outcome{}, errors.New("the sign-in was stopped, so nothing was stored")
	}

	return keysui.Outcome{
		Name: meta.Ref.Name,
		Identity: keysui.Identity{
			Kind:    keysui.KindDelegated,
			Account: found.Account.Email,
		},
	}, nil
}

func (a *claudeCodeAttempt) Cancel() {
	a.mu.Lock()
	a.cancelled = true
	alreadyStored := a.stored
	a.mu.Unlock()

	if alreadyStored {
		// The other half of the race. Wait finished and wrote the credential before this arrived, so
		// undoing it is this call's job rather than Wait's.
		_ = a.store.Remove(core.KeyRef{Name: a.name})
	}
}

func (a *claudeCodeAttempt) abandoned() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancelled
}

// markStored records that the credential exists, reporting whether it is allowed to survive.
func (a *claudeCodeAttempt) markStored() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelled {
		return false
	}
	a.stored = true
	return true
}
