package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
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
// One of the three D-51 permits is here: Claude Code, which S-04 built, and which is the route where
// there is nothing to sign in to because the user already did. Copilot arrives with S-03 and ChatGPT
// with S-05, and whichever of those lands next has to compose the registries rather than replace this
// one. That composition is not written yet on purpose: a registry of registries with a single member
// is a shape guessed at before the second case exists.
var signInRoutes keysui.SignIn = claudeCode{}

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
