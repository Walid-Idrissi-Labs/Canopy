package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/copilot"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// The Copilot route, which is the one where signing in is genuinely a sign-in.
//
// Its neighbour, the Claude route, stores a record that somebody else's agent exists. This one runs
// GitHub's device flow, holds the token that comes back, and is therefore the only place in Canopy
// that obtains a subscription credential rather than finding one. D-51 permits it because GitHub
// documents exactly this arrangement: register an app, the user authorises it, the SDK is handed
// their token, and their own seat is what answers.
//
// It lives in cmd/canopy for credentials.go's reason. internal/provider/copilot knows how to talk to
// GitHub and internal/keys knows how to store a credential, and neither is allowed to name the
// screen's vocabulary. This is where the three meet, which is what a composition root is for.

// copilotRouteID is what `canopy keys signin <name> -route copilot` takes.
const copilotRouteID = copilot.Route

// copilotSignIn is the sign-in registry for the GitHub Copilot route.
type copilotSignIn struct {
	// store is where a finished sign-in is written. Nil means the real one, opened when it is
	// needed: this is reachable from a package variable, and opening a keychain to build a list of
	// route names would prompt for an unlock on every command that has nothing to do with
	// credentials.
	store *keys.Store

	// vendor is GitHub. The zero value is the real one.
	vendor copilot.Vendor
}

var _ keysui.SignIn = copilotSignIn{}

func (c copilotSignIn) keyStore() (*keys.Store, error) {
	if c.store != nil {
		return c.store, nil
	}
	return openKeyStore()
}

func (copilotSignIn) Routes() []keysui.Route {
	return []keysui.Route{{
		ID:    copilotRouteID,
		Label: "GitHub Copilot",
		// What has to already be true, which is what somebody choosing between routes is choosing on.
		Detail: "a GitHub account with a Copilot seat, plus the Copilot CLI " +
			"(`npm install -g @github/copilot`)",
		Caveat: "Signing in authorises Canopy to use your Copilot seat, and turns are metered " +
			"against that subscription rather than billed per token, so no cost is shown. Canopy runs " +
			"GitHub's agent with none of its own tools switched on: the only tools in the session are " +
			"Canopy's, and they go through Canopy's permission gate as usual. The conversation itself " +
			"lives in GitHub's runtime, so editing its history or compacting it locally does not " +
			"reach it.",
		// GitHub chooses unless the credential names a model, and Canopy's catalog holds no Copilot
		// lineup: which models a seat may use depends on the plan and on organisation policy, so a
		// list here would be a guess. Saying the vendor chooses is truer than showing an empty one,
		// and D-46 rule 1 leaves the free-text row in place for somebody who knows their model.
		VendorPicksModel: true,
	}}
}

func (c copilotSignIn) Begin(route keysui.Route, name string) (keysui.Attempt, error) {
	if route.ID != copilotRouteID {
		return nil, unknownRouteError(route.ID, c.Routes())
	}
	store, err := c.keyStore()
	if err != nil {
		return nil, err
	}

	// The device code is asked for here rather than in Wait, because Begin's whole contract is to
	// return once there is something to put in front of a person. A screen that showed "waiting" and
	// only later produced the code somebody has to type would be a screen that asked them to watch
	// it.
	ctx, cancel := context.WithTimeout(context.Background(), vendorTimeout*3)
	defer cancel()

	attempt, err := c.vendor.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &copilotAttempt{vendor: c.vendor, attempt: attempt, store: store, name: name}, nil
}

// Report is what `canopy keys test` asks GitHub about an existing credential.
//
// It asks who the token belongs to, which is the strongest honest answer this route has and is a
// real network check rather than a stored fact read back. It deliberately does not claim to know
// whether the seat is still active: the SDK's account.getQuota is defined in the schema and, as of
// v1.0.8, is not implemented in the CLI, which GitHub's own end-to-end test skips over. Saying that
// out loud is better than starting a runtime to find out and calling the result a subscription check.
func (c copilotSignIn) Report(ctx context.Context, meta core.KeyMetadata) (signInReport, error) {
	store, err := c.keyStore()
	if err != nil {
		return signInReport{}, err
	}
	tokens, err := store.Tokens(meta.Ref)
	if err != nil {
		return signInReport{}, err
	}

	login, err := c.vendor.Login(ctx, tokens.Access)
	if err != nil {
		return signInReport{}, err
	}

	facts := []signInFact{{Label: "login", Value: login}}
	if in, storeErr := store.SignIn(meta.Ref); storeErr == nil && in.Account != "" && in.Account != login {
		// A fact rather than a fault, and worth saying loudly: turns on this credential bill the
		// account below, not the one the credential was named after.
		facts = append(facts, signInFact{
			Label: "note",
			Value: fmt.Sprintf(
				"this credential was added for %s and the token now belongs to %s", in.Account, login),
		})
	}
	facts = append(facts, signInFact{
		Label: "seat",
		Value: "not checked. GitHub publishes no endpoint Canopy can ask, so whether this account " +
			"still has a Copilot seat is answered by the first turn rather than here",
	})

	if _, err := copilot.FindCLI(); err != nil {
		facts = append(facts, signInFact{Label: "runtime", Value: err.Error()})
	}

	return signInReport{Vendor: "GitHub", Account: login, Facts: facts}, nil
}

// copilotAttempt is one device flow in flight.
type copilotAttempt struct {
	vendor  copilot.Vendor
	attempt *copilot.Attempt
	store   *keys.Store
	name    string

	mu        sync.Mutex
	cancelled bool
	stored    bool
}

var _ keysui.Attempt = (*copilotAttempt)(nil)

func (a *copilotAttempt) Prompt() keysui.Prompt {
	prompt := a.attempt.Prompt()
	return keysui.Prompt{URL: prompt.VerificationURI, Code: prompt.UserCode}
}

// Wait polls GitHub until the person authorises, and stores the credential itself.
//
// Storing here rather than back on the screen is S-06's arrangement and is what keeps a token out of
// internal/tui entirely: what goes back is a name and an account, which are the two things a list
// draws.
func (a *copilotAttempt) Wait() (keysui.Outcome, error) {
	// Bounded by the device code's own expiry rather than by a timeout of Canopy's. A person walking
	// to another machine to type a code is doing the thing this route asked of them, and a deadline
	// shorter than GitHub's own would fail them for being slower than a number nobody told them.
	deadline := a.attempt.Prompt().ExpiresAt.Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	grant, err := a.attempt.Wait(ctx)
	if err != nil {
		return keysui.Outcome{}, err
	}

	// Checked before the write rather than only after it, so the common case of an escape pressed
	// during the wait costs nothing at all.
	if a.abandoned() {
		return keysui.Outcome{}, errors.New("the sign-in was stopped, so nothing was stored")
	}

	// OpenAICompatible because that is the closer of the two shapes core knows and core is frozen,
	// and the route rather than the provider is what tells this credential apart from a Codex one
	// later. The base URL is where the turn genuinely ends up even though Canopy never dials it,
	// which is better than a placeholder in a field a person can see in `canopy keys test`.
	ref := core.KeyRef{Name: a.name, Provider: core.ProviderOpenAICompatible}
	meta, err := a.store.PutSignIn(
		core.KeyMetadata{Ref: ref, BaseURL: copilot.BaseURL},
		keys.SignIn{
			Kind:      keys.KindSignedIn,
			Account:   grant.Account,
			ExpiresAt: grant.ExpiresAt,
			Route:     copilotRouteID,
		},
		grant.Tokens,
	)
	if err != nil {
		return keysui.Outcome{}, err
	}

	if !a.markStored() {
		// Escape landed between GitHub confirming and the write. Undone here, because a cancelled
		// sign-in that leaves a working credential behind is a credential nobody knows they have.
		_ = a.store.Remove(ref)
		return keysui.Outcome{}, errors.New("the sign-in was stopped, so nothing was stored")
	}

	return keysui.Outcome{
		Name: meta.Ref.Name,
		Identity: keysui.Identity{
			Kind:      keysui.KindSignedIn,
			Account:   grant.Account,
			ExpiresAt: grant.ExpiresAt,
		},
	}, nil
}

// Cancel stops the wait, and undoes a sign-in that completed while the keystroke was in flight.
func (a *copilotAttempt) Cancel() {
	a.mu.Lock()
	a.cancelled = true
	alreadyStored := a.stored
	a.mu.Unlock()

	a.attempt.Cancel()

	if alreadyStored {
		_ = a.store.Remove(core.KeyRef{Name: a.name})
	}
}

func (a *copilotAttempt) abandoned() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cancelled
}

func (a *copilotAttempt) markStored() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelled {
		return false
	}
	a.stored = true
	return true
}
