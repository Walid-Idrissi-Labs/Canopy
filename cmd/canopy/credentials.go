package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/copilot"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// Where the credential store meets the credential screen.
//
// The two describe the same credentials and are not allowed to share a type. internal/tui may depend
// on internal/core and on other internal/tui packages and on nothing else, held by
// TestEveryInterfacePackageDependsOnTheContractAndNotOnTheEngine, so the screen cannot name
// keys.SignIn and the store cannot name keysui.Identity. The translation has to happen somewhere and
// this is the only place that already knows both: the command line is where the program is
// assembled.

// signInAware is the key store with the one method the credential screen asks for that internal/keys
// spells differently.
//
// Embedded rather than reimplemented, so every other method the screen needs stays the store's own
// and this file cannot drift into being a second, weaker key store.
type signInAware struct{ *keys.Store }

// Identity is keys.SignIn in the words the interface uses.
//
// A plain conversion of the kind, which is safe because both sides spell the three words
// identically, and TestTheStoreAndTheScreenAgreeAboutWhatAKindIsCalled fails if either side ever
// renames one. A switch here would be the same claim written twice, and the copy that stops being
// updated is the one that reports a delegated credential as a pasted one.
func (s signInAware) Identity(ref core.KeyRef) (keysui.Identity, error) {
	in, err := s.SignIn(ref)
	if err != nil {
		return keysui.Identity{}, err
	}
	return keysui.Identity{
		Kind:      keysui.Kind(in.Kind),
		Account:   in.Account,
		ExpiresAt: in.ExpiresAt,
	}, nil
}

// signInRoutes is every way of signing in that this build offers.
//
// A package variable for the reason openKeyStore is one: it is what a test swaps to drive the flow
// without a vendor on the other end of it. Nothing else about it is dynamic.
//
// Two of the three D-51 permits are here: GitHub Copilot from S-03, where signing in is genuinely a
// sign-in, and Claude Code from S-04, where it is a search because the user already did it. ChatGPT
// arrives with S-05 and joins the list below.
//
// The composition S-04 left for whoever landed second is routeSet, and the second case turned out to
// need one thing a single registry never did: a way to know which vendor a credential that already
// exists belongs to. That is what keys.SignIn.Route records and what routeSet dispatches on.
var signInRoutes keysui.SignIn = routeSet{copilotSignIn{}, claudeCode{}}

// routeSet is every way in this build offers, as one registry.
//
// Ordered, and Copilot is first because `canopy keys signin` with no -route on a build with several
// makes somebody choose, and the order they are offered in is the order they appear. Nothing depends
// on it beyond that.
type routeSet []keysui.SignIn

var (
	_ keysui.SignIn        = routeSet(nil)
	_ reportsOnCredentials = routeSet(nil)
)

func (s routeSet) Routes() []keysui.Route {
	all := make([]keysui.Route, 0, len(s))
	for _, member := range s {
		all = append(all, member.Routes()...)
	}
	return all
}

// Begin hands the sign-in to whichever member owns the route named.
func (s routeSet) Begin(route keysui.Route, name string) (keysui.Attempt, error) {
	for _, member := range s {
		for _, offered := range member.Routes() {
			if offered.ID == route.ID {
				return member.Begin(offered, name)
			}
		}
	}
	return nil, unknownRouteError(route.ID, s.Routes())
}

// Report asks the vendor a credential actually belongs to.
//
// Dispatched on the route recorded with the credential wherever there is one, because asking every
// member is how `canopy keys test` on a Copilot credential ends up reporting what it found on the
// machine about Claude Code: both members answer questions about a vendor, and only one of them is
// the vendor being asked about.
//
// A credential with no route recorded falls back to asking each vendor in turn and taking the first
// that answers. That is weaker than dispatching and it exists for one reason: keys.SignIn.Route
// arrived with the second route, so a credential stored before it has nothing to dispatch on, and an
// error there would mean a working credential suddenly reporting that Canopy cannot ask about it.
// The fallback is safe rather than lucky, because a registry handed a credential that is not its own
// fails on the credential rather than answering about it: the Copilot route reads tokens, and a
// delegated credential has none by design and says so.
func (s routeSet) Report(ctx context.Context, meta core.KeyMetadata) (signInReport, error) {
	store, err := openKeyStore()
	if err != nil {
		return signInReport{}, err
	}
	in, err := store.SignIn(meta.Ref)
	if err != nil {
		return signInReport{}, err
	}

	var last error
	for _, member := range s {
		reporter, ok := member.(reportsOnCredentials)
		if !ok {
			continue
		}
		if in.Route != "" && !offers(member, in.Route) {
			continue
		}
		report, err := reporter.Report(ctx, meta)
		if err == nil {
			return report, nil
		}
		last = err
	}

	if last != nil {
		return signInReport{}, last
	}
	if in.Route != "" {
		return signInReport{}, fmt.Errorf(
			"key %q was signed in through %q, which this build no longer offers",
			meta.Ref.Name, in.Route)
	}
	return signInReport{}, fmt.Errorf("no route in this build can say anything about key %q",
		meta.Ref.Name)
}

// signInSources is where a signed-in credential buys a new token, for every route this build has.
//
// One function for the whole program, called by both places that build a client, because S-02's
// point was that "how old is too old" must be one rule rather than two: the resolver and
// `canopy ask` disagreeing about it is a credential that works in one surface and not the other.
//
// Composed by asking each route rather than switching on provider, which is the collision
// internal/keys/refresh.go was written to avoid. Copilot and a future Codex are both
// openai-compatible, and a map keyed on that would hand one of them the other's token endpoint.
func signInSources() keys.SourceFor {
	sources := []keys.SourceFor{copilot.Vendor{}.Sources()}
	return func(meta core.KeyMetadata, in keys.SignIn) (keys.TokenSource, bool) {
		for _, source := range sources {
			if found, ok := source(meta, in); ok {
				return found, true
			}
		}
		return nil, false
	}
}

// offers reports whether a registry owns a route id.
func offers(member keysui.SignIn, id string) bool {
	for _, route := range member.Routes() {
		if route.ID == id {
			return true
		}
	}
	return false
}

// noRoutes is what a build with no vendor behind it offers, which is nothing, said plainly.
type noRoutes struct{}

func (noRoutes) Routes() []keysui.Route { return nil }

func (noRoutes) Begin(route keysui.Route, _ string) (keysui.Attempt, error) {
	return nil, unknownRouteError(route.ID, nil)
}

// unknownRouteError says what can be signed in to when somebody names something that cannot.
//
// It names the three routes even when none of them is built, because the question behind "how do I
// sign in" is usually "am I allowed to", and an error that only says no leaves that unanswered.
func unknownRouteError(id string, routes []keysui.Route) error {
	if len(routes) == 0 {
		return errors.New(
			"this build has no way to sign in yet. Signing in with a GitHub Copilot seat, with a " +
				"Claude Code installation you have already signed in to, or with a ChatGPT " +
				"subscription is being built. Until then a credential is a value you paste: " +
				"`canopy keys add <name>`")
	}

	named := make([]string, 0, len(routes))
	for _, route := range routes {
		named = append(named, route.ID)
	}
	if id == "" {
		return fmt.Errorf("which route? One of: %s", strings.Join(named, ", "))
	}
	return fmt.Errorf("no sign-in route called %q. This build offers: %s", id, strings.Join(named, ", "))
}
