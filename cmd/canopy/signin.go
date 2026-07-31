package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// Signing in from the command line.
//
// The same three routes the interface offers, driven the same way, because a person who signed in at
// a terminal and a person who signed in in the wizard have to end up with the same credential. The
// shared half is keysui.SignIn: the screen defined it, the routes implement it, and this file is a
// second caller rather than a second design.
//
// What is only here is what only a terminal needs. A device code printed where it can be copied, a
// ctrl+c that stops the wait and stores nothing, and the two questions a screen answers by drawing:
// which credentials are signed in and as whom, and what is actually true of one right now.

// reportsOnCredentials is a route registry that can ask the vendor about a credential that already
// exists.
//
// Optional, and asked for by assertion rather than required, because it is genuinely optional. A
// route with no such endpoint is not a broken route, and the alternative to admitting that is a
// method that returns "not supported" and a command that has to tell the two apart anyway.
type reportsOnCredentials interface {
	Report(ctx context.Context, meta core.KeyMetadata) (signInReport, error)
}

// revokesCredentials is a route registry that can tell the vendor to forget a grant.
//
// Separate from reporting because the two are separately available. Codex publishes rate limits and
// no revocation; a GitHub token can be revoked and says nothing about a plan. A registry that had to
// implement both to offer either would offer neither.
type revokesCredentials interface {
	Revoke(ctx context.Context, meta core.KeyMetadata) error
}

// signInReport is what a vendor says about a credential right now.
//
// Facts as label and value pairs rather than a struct with a field per vendor, because what is
// available differs by route and a fixed shape would mean either empty rows or a type that grows
// every time a route lands. The vendor is named separately so the output can say who was asked,
// which is the difference between a checked fact and a stored one.
type signInReport struct {
	Vendor  string
	Account string
	Facts   []signInFact
}

type signInFact struct {
	Label string
	Value string
}

// renewsAfter says how early Canopy renews a token, in words rather than in a Go duration.
//
// keys.RefreshMargin is exported so a surface can say this instead of leaving somebody to discover
// the difference between "expires at" and "stops being used at" by watching. "5m0s" is how Go prints
// it and not how anybody says it, and this line is read by people rather than parsed.
func renewsAfter() string {
	return fmt.Sprintf("%.0f minutes", keys.RefreshMargin.Minutes())
}

// vendorTimeout bounds asking a vendor about a credential.
//
// A single small request, and one that has not answered in ten seconds is not about to. `keys test`
// is something somebody runs while looking at the terminal, so a wait long enough to look like a
// hang costs more than the answer is worth.
const vendorTimeout = 10 * time.Second

// interrupts is where a ctrl+c during a sign-in comes from.
//
// A package variable so a test can raise one without sending a real signal to the test binary, which
// is the sort of thing that takes a whole suite down with it when it goes wrong.
var interrupts = func() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	return ch, func() { signal.Stop(ch) }
}

func runKeysSignIn(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("keys signin", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	routeID := flags.String("route", "", "which way in to use")
	refused := refuseSecretFlags(flags)

	// The name comes off the front before parsing, for the reason it does in `keys add`: Go's flag
	// package stops at the first positional argument and would silently ignore every flag after it.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := refused(); err != nil {
		return err
	}

	positional := flags.Args()
	if name == "" && len(positional) > 0 {
		name, positional = positional[0], positional[1:]
	}
	if name == "" {
		return errors.New("a name is required, for example `canopy keys signin copilot`")
	}
	if len(positional) > 0 {
		return fmt.Errorf("too many arguments: `canopy keys signin <name> -route <route>`")
	}
	if err := core.ValidateKeyName(name); err != nil {
		return err
	}

	routes := signInRoutes.Routes()
	if len(routes) == 0 {
		return unknownRouteError(*routeID, nil)
	}

	route, err := chooseRoute(routes, *routeID, out)
	if err != nil {
		return err
	}

	attempt, err := signInRoutes.Begin(route, name)
	if err != nil {
		return err
	}
	if attempt == nil {
		return fmt.Errorf("the %s sign-in started and said nothing about what to do next", route.Label)
	}

	w := &errWriter{w: out}
	w.printf("Signing %q in through %s.\n", name, route.Label)
	if route.Caveat != "" {
		// Before the wait rather than after it, because it is a fact about what somebody is
		// choosing and the moment it is worth reading is the moment before they finish choosing.
		w.printf("\n%s\n", route.Caveat)
	}
	showPrompt(w, route, attempt.Prompt())
	if w.err != nil {
		return w.err
	}

	outcome, err := awaitSignIn(attempt)
	if err != nil {
		return err
	}

	w.printf("\nSigned %q in as %s.\n", outcome.Name, outcome.Identity.Account)
	if at := outcome.Identity.ExpiresAt; at != nil {
		w.printf("The grant expires %s, and Canopy renews it %s before that on its own.\n",
			at.Local().Format("2006-01-02 15:04"), renewsAfter())
	}
	w.printf("Check it any time with `canopy keys test %s`.\n", outcome.Name)
	return w.err
}

// awaitSignIn waits for the vendor, and lets ctrl+c stop the wait without leaving a credential.
//
// The wait runs on a goroutine and the signal is selected against it, rather than the obvious
// arrangement of blocking on Wait and letting the default handler kill the process. Killing it is
// the thing that must not happen: the vendor may confirm in the same moment, and a process that died
// between confirmation and cancellation leaves a working credential nobody knows they have. Cancel
// exists to undo exactly that, and it only runs if this process is still alive to call it.
func awaitSignIn(attempt keysui.Attempt) (keysui.Outcome, error) {
	type answer struct {
		outcome keysui.Outcome
		err     error
	}
	done := make(chan answer, 1)
	go func() {
		outcome, err := attempt.Wait()
		done <- answer{outcome: outcome, err: err}
	}()

	signals, stop := interrupts()
	defer stop()

	select {
	case got := <-done:
		return got.outcome, got.err
	case <-signals:
		attempt.Cancel()
		// Drained rather than abandoned, so Cancel has finished undoing whatever there was to undo
		// before this returns and the process exits.
		<-done
		return keysui.Outcome{}, errors.New("the sign-in was stopped, so nothing was stored")
	}
}

// chooseRoute settles which way in to use, and says what the choices are when it cannot.
//
// One route is chosen without being asked for, and said out loud. Requiring a flag to name the only
// possible answer is a question with one option, and the sentence printed instead is both the answer
// and the explanation of what happened.
func chooseRoute(routes []keysui.Route, id string, out io.Writer) (keysui.Route, error) {
	if id != "" {
		for _, route := range routes {
			if route.ID == id {
				return route, nil
			}
		}
		return keysui.Route{}, unknownRouteError(id, routes)
	}
	if len(routes) == 1 {
		_, err := fmt.Fprintf(out, "Using the %s route, the only one this build offers.\n", routes[0].ID)
		return routes[0], err
	}

	w := &errWriter{w: out}
	w.printf("Which way in? Pick one with -route:\n\n")
	for _, route := range routes {
		w.printf("  %-14s %s\n", route.ID, route.Label)
		if route.Detail != "" {
			w.printf("  %-14s %s\n", "", "needs "+route.Detail)
		}
	}
	w.printf("\nFor example `canopy keys signin %s -route %s`.\n", "mysub", routes[0].ID)
	if w.err != nil {
		return keysui.Route{}, w.err
	}
	return keysui.Route{}, errors.New("no route was named")
}

// showPrompt puts what the vendor needs done where somebody can read it and type it somewhere else.
//
// Text and never a browser this command opened. A coding agent is routinely run over ssh on a
// machine with no browser at all, and a flow that only works where one exists does not work on the
// machines this program is for.
func showPrompt(w *errWriter, route keysui.Route, prompt keysui.Prompt) {
	switch {
	case prompt.URL != "" || prompt.Code != "":
		w.printf("\n")
		if prompt.URL != "" {
			w.printf("  open this page   %s\n", prompt.URL)
		}
		if prompt.Code != "" {
			w.printf("  and enter        %s\n", prompt.Code)
		}
		w.printf("\nWaiting for %s to confirm. Press ctrl+c to stop, and nothing is stored.\n",
			route.Label)
	case prompt.Doing != "":
		w.printf("\n%s. Press ctrl+c to stop, and nothing is stored.\n", prompt.Doing)
	default:
		w.printf("\nWaiting for %s. Press ctrl+c to stop, and nothing is stored.\n", route.Label)
	}
}

// runKeysSignOut ends a sign-in, at the vendor where it can and locally either way.
//
// Not `keys remove` under another name. Removing forgets a record; signing out is a statement about
// a grant that exists somewhere else, and doing only the local half while calling it signing out is
// how somebody ends up believing they revoked access they still have.
func runKeysSignOut(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("a name is required, for example `canopy keys signout copilot`")
	}
	name := args[0]

	store, err := openStore(out)
	if err != nil {
		return err
	}
	meta, err := store.Metadata(core.KeyRef{Name: name})
	if err != nil {
		return err
	}
	in, err := store.SignIn(meta.Ref)
	if err != nil {
		return err
	}
	if !in.Kind.IsSignIn() {
		return fmt.Errorf(
			"key %q holds a value somebody pasted rather than a sign-in, so there is nothing to sign "+
				"out of. Remove it with `canopy keys remove %s`, and revoke it wherever it was issued",
			name, name)
	}

	w := &errWriter{w: out}
	revoked := false
	if revoker, ok := signInRoutes.(revokesCredentials); ok {
		ctx, cancel := context.WithTimeout(context.Background(), vendorTimeout)
		defer cancel()
		if err := revoker.Revoke(ctx, meta); err != nil {
			// Reported and not fatal. The local half still has to happen: a signout that refuses to
			// delete anything because a vendor was unreachable leaves the tokens in the keychain of
			// somebody who has just said they want them gone.
			w.printf("The grant could not be revoked at the vendor: %v\n", err)
		} else {
			revoked = true
		}
	}

	// Remove takes the tokens with it, which is S-01's arrangement: one backend entry under the
	// credential's own name, so one delete leaves nothing behind.
	if err := store.Remove(meta.Ref); err != nil {
		return err
	}

	w.printf("Signed %q out. Its tokens are gone from the %s and its record is gone from keys.json.\n",
		name, store.BackendName())
	if in.Kind == keys.KindDelegated {
		w.printf("Nothing was revoked anywhere: a delegated sign-in is a vendor's own agent that you "+
			"signed in to, and Canopy never held a credential of yours on it. %s is still signed in "+
			"as far as the vendor is concerned.\n", in.Account)
		return w.err
	}
	if revoked {
		w.printf("The grant was revoked at the vendor, so the tokens are dead there too.\n")
		return w.err
	}
	w.printf("The grant was not revoked at the vendor, because this build has no route that can. "+
		"Revoke Canopy's access for %s where you granted it if you want it gone there too.\n",
		in.Account)
	return w.err
}

// testSignedIn is `canopy keys test` for a credential nobody pasted.
//
// The fingerprint comparison the pasted path makes has nothing to compare here, and skipping it would
// leave the command with nothing to say. What replaces it is a better answer than a fingerprint ever
// was: a grant exists, it is unexpired or renewable, and where the vendor publishes it, this is the
// account and these are its limits. That is a fact about the subscription rather than a fact about
// the file.
func testSignedIn(
	out io.Writer, store *keys.Store, meta core.KeyMetadata, in keys.SignIn,
) error {
	w := &errWriter{w: out}
	w.printf("%s is signed in as %s.\n", meta.Ref.Name, in.Account)
	w.printf("  provider     %s\n", meta.Ref.Provider)
	if meta.BaseURL != "" {
		w.printf("  base url     %s\n", meta.BaseURL)
	}
	w.printf("  kind         %s\n", in.Kind)
	w.printf("  account      %s\n", in.Account)

	if in.Kind == keys.KindDelegated {
		// A delegated credential's keychain half is empty and empty is correct, so this must not
		// read as the missing-secret damage the pasted path reports. D-51 is the reason the half is
		// empty and this says so rather than leaving it to be inferred.
		w.printf("  tokens       none, and none is correct: Canopy drives an agent you signed in to " +
			"yourself\n")
		w.printf("\nThis says that Canopy holds a record of a delegated sign-in, not that the agent\n")
		w.printf("behind it is installed and still signed in.\n")
		return withVendorReport(w, meta)
	}

	tokens, err := store.Tokens(meta.Ref)
	if err != nil {
		// The damaged case, reported in internal/keys' own words, which already name the remedy.
		return err
	}
	switch {
	case tokens.Refresh.IsZero():
		w.printf("  tokens       an access token, with nothing to renew it with\n")
	default:
		w.printf("  tokens       an access token and a refresh token, in the %s\n", store.BackendName())
	}

	lapsed := false
	switch {
	case in.ExpiresAt == nil:
		w.printf("  expires      the vendor did not say, so Canopy renews it only when it fails\n")
	case time.Now().After(*in.ExpiresAt):
		lapsed = true
		w.printf("  expires      %s, which has passed\n", in.ExpiresAt.Local().Format("2006-01-02 15:04"))
	case time.Now().Add(keys.RefreshMargin).After(*in.ExpiresAt):
		w.printf("  expires      %s, so Canopy renews it on the next turn\n",
			in.ExpiresAt.Local().Format("2006-01-02 15:04"))
	default:
		w.printf("  expires      %s, and Canopy renews it %s before that\n",
			in.ExpiresAt.Local().Format("2006-01-02 15:04"), renewsAfter())
	}

	if err := withVendorReport(w, meta); err != nil {
		return err
	}
	if !lapsed {
		return w.err
	}

	if tokens.Refresh.IsZero() {
		// An error rather than a note, because this credential cannot answer a message and the exit
		// code is what a script checking it reads.
		if w.err != nil {
			return w.err
		}
		return fmt.Errorf(
			"the sign-in for key %q as %s has lapsed and has nothing to renew it with. "+
				"Sign in again with `canopy keys signin %s`",
			meta.Ref.Name, in.Account, meta.Ref.Name)
	}
	w.printf("\nThe grant has lapsed. Canopy renews it from the refresh token on the next turn; if\n")
	w.printf("that is refused, sign in again with `canopy keys signin %s`.\n", meta.Ref.Name)
	return w.err
}

// withVendorReport appends what the vendor says, or says that nobody was asked.
//
// Saying so is the point. A command that printed a stored account and let it read as a checked one
// would be claiming a network check it never made, which is the specific dishonesty the old closing
// line about A2 had settled into.
func withVendorReport(w *errWriter, meta core.KeyMetadata) error {
	reporter, ok := signInRoutes.(reportsOnCredentials)
	if !ok {
		w.printf("\nNo vendor was contacted, so every line above is what Canopy holds rather than what\n")
		w.printf("the vendor would accept right now.\n")
		return w.err
	}

	ctx, cancel := context.WithTimeout(context.Background(), vendorTimeout)
	defer cancel()

	report, err := reporter.Report(ctx, meta)
	if err != nil {
		w.printf("\nThe vendor could not be asked: %v\n", err)
		w.printf("Every line above is what Canopy holds rather than what the vendor would accept.\n")
		return w.err
	}

	w.printf("\n%s says:\n", report.Vendor)
	if report.Account != "" {
		w.printf("  account      %s\n", report.Account)
	}
	for _, fact := range report.Facts {
		w.printf("  %-12s %s\n", fact.Label, fact.Value)
	}
	return w.err
}
