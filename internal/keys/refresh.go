package keys

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// RefreshMargin is how long before its stated expiry a token stops being trusted.
//
// Renewing at expiry is renewing too late, for two ordinary reasons. Clocks disagree: a laptop that
// has been asleep, or one whose time was never synchronised, sits minutes away from the vendor's
// clock, and the direction that hurts is the one where Canopy still believes a token the far end has
// already retired. And a turn is not one request. An agent turn runs a tool loop, so several requests
// go out over several minutes under a single resolution, and a margin shorter than a turn is a token
// that expires in the middle of one.
//
// Five minutes covers both without being eager. Tokens on these routes last tens of minutes, so the
// cost is at worst one extra renewal every several turns, against a failure mode that costs a turn
// and a wrong diagnosis. Ten seconds, which is what x/oauth2 uses, was considered and rejected: it is
// a margin sized for a library that renews per request rather than per turn, and it is inside the
// clock drift of any machine nobody is synchronising.
//
// Exported because a screen that says when a credential expires should be able to say when Canopy
// will act on it, rather than leaving somebody to discover the difference by watching.
const RefreshMargin = 5 * time.Minute

// refreshTimeout bounds one exchange with a token endpoint.
//
// A renewal is a single small round trip, and one that has not answered in half a minute is not
// about to. Without a deadline of its own it would wait on the provider client's, which is measured
// in minutes because a reasoning turn legitimately takes that long, and a turn that hangs before it
// has sent anything is the worst version of this failure.
const refreshTimeout = 30 * time.Second

// ErrSignInLapsed means the vendor refused the renewal itself, so there is nothing left to renew.
//
// Kept apart from a failure to reach the vendor because they are different events with different
// remedies, and giving somebody the wrong one wastes their time in a specific way. "Sign in again"
// after a dropped connection sends a person through a flow that was never needed and, on a route
// that rotates refresh tokens, throws away a working grant to do it. "Try again shortly" after a
// revoked grant is advice to wait for something that will never change.
var ErrSignInLapsed = errors.New("the sign-in has lapsed")

// Renewal is what a token endpoint hands back.
//
// Named for the event rather than for the document, so it does not read as storedGrant's public
// twin: that is the shape tokens take inside the backend, and this is the answer to one request.
type Renewal struct {
	// Tokens are the new ones. A refresh token left empty means the vendor did not issue a new one,
	// which is the common case and is not the same as it having no refresh token any more.
	Tokens Tokens

	// ExpiresAt is when the new access token stops being accepted, or nil where the vendor did not
	// say. Nil is carried through rather than guessed at, for the reason SignIn.ExpiresAt is a
	// pointer: "expires at the zero time" and "the vendor never said" are different facts.
	ExpiresAt *time.Time
}

// TokenSource renews a sign-in against whatever issued it.
//
// The interface exists so this file holds the policy, meaning when to renew, how to serialise it and
// what to tell somebody when it fails, and each route holds only its own protocol. No route is
// implemented yet; S-03 and S-05 bring the first two.
type TokenSource interface {
	// Name identifies the route, so a failure says which vendor refused rather than only which
	// credential was involved.
	Name() string

	// Refresh exchanges the tokens for newer ones.
	//
	// Two failures, and the difference between them is most of why this method exists rather than
	// a bare HTTP call. A source whose grant is finished, meaning revoked, expired beyond renewal,
	// or refused by the vendor for any reason that another attempt cannot change, returns an error
	// wrapping ErrSignInLapsed. Everything else is read as transient, because a source that cannot
	// tell which it is has said the credential is fine and the moment was bad, and that is the
	// answer with the cheaper mistake.
	Refresh(ctx context.Context, in SignIn, tokens Tokens) (Renewal, error)
}

// SourceFor finds the route a credential renews against.
//
// A function rather than a map keyed by provider, because the record does not say which route a
// credential took and provider is not a stand-in for it: Copilot and Codex are both
// openai-compatible, so a map would collide the moment the second of them landed. Whatever a later
// task adds to tell them apart, it keys on it here without this file changing.
//
// Reporting false is not a failure of the credential, and the caller says so in those words.
type SourceFor func(core.KeyMetadata, SignIn) (TokenSource, bool)

// Refresher hands out the value a request authenticates with, renewing it first if it is close
// enough to expiry to be a risk.
//
// The renewal happens here, ahead of the request being built, and never in response to a rejection.
// That ordering is the whole point. core.ErrAuthentication is documented as never retry and never
// fall back, at internal/core/provider.go:351, because a wrong credential is a thing to fix and
// routing around it hides the problem while billing elsewhere. An expired token arrives as the same
// 401 as a wrong one, so a design that renewed in response to a rejection would have to either
// misclassify it or teach that distinction to a frozen package. Renewing first removes the question:
// by the time a request exists its token is valid, so a 401 means what it has always meant.
//
// Separate from Store rather than a method on it, because Store's whole subject is two halves of
// local storage and this one talks to a vendor over a network. It is also the seam a route plugs
// into, and a seam is easier to see on a type of its own.
type Refresher struct {
	store *Store

	// mu guards sources and renewing. Not the store's mutex: a renewal is a network exchange, and
	// holding the lock that every read of keys.json takes for the length of one would stop an
	// unrelated credential's turn from starting.
	mu       sync.Mutex
	sources  SourceFor
	renewing map[string]*sync.Mutex

	clock func() time.Time
}

// NewRefresher builds a refresher over a key store.
//
// It knows no routes until Renews is called, and a build with none can still resolve every
// credential it has: nothing can be signed in to before S-03, so nothing can need renewing.
func NewRefresher(store *Store) *Refresher {
	return &Refresher{
		store:    store,
		renewing: map[string]*sync.Mutex{},
		clock:    time.Now,
	}
}

// Renews says where a signed-in credential buys a new token.
//
// Called once at wiring time, before any turn starts. A setter rather than a constructor argument
// because the routes arrive one task at a time and each of them would otherwise change this
// constructor and everything that calls it.
func (r *Refresher) Renews(sources SourceFor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources = sources
}

// SetClock replaces the clock. For tests.
func (r *Refresher) SetClock(now func() time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clock = now
}

// Credential returns the value a request to this credential's provider should authenticate with.
//
// One call for both kinds of credential on purpose. The rule about when a token is too old to send
// is one rule, and the two places that build a client, the resolver at
// internal/session/resolver.go and newClient in cmd/canopy/ask.go, have to be unable to disagree
// about it. A pasted secret goes straight through, since there is nothing to renew and never was.
func (r *Refresher) Credential(meta core.KeyMetadata) (core.Secret, error) {
	in, err := r.store.SignIn(meta.Ref)
	if err != nil {
		return core.Secret{}, err
	}
	if !in.Kind.IsSignIn() {
		return r.store.Get(meta.Ref)
	}

	// Delegated credentials fall in here and are refused by Tokens, in the terms that say Canopy
	// holds none of the user's rather than that some went missing. A route Canopy holds no token for
	// has no token to renew.
	tokens, err := r.store.Tokens(meta.Ref)
	if err != nil {
		return core.Secret{}, err
	}
	if !r.due(in.ExpiresAt) {
		return tokens.Access, nil
	}
	return r.renew(meta)
}

// due reports whether a token is close enough to its expiry to be renewed now.
//
// An expiry nobody stated is never due. The vendor not saying is not evidence that the token is
// old, and renewing on that guess would spend a refresh token, and on a rotating route the current
// one, on every turn for the life of the credential.
func (r *Refresher) due(expiresAt *time.Time) bool {
	if expiresAt == nil {
		return false
	}
	r.mu.Lock()
	now := r.clock()
	r.mu.Unlock()
	return !now.Add(RefreshMargin).Before(*expiresAt)
}

// renew buys a new token for one credential, once, however many turns asked for it.
//
// The lock is per credential rather than one for the whole refresher, so two conversations on two
// subscriptions do not queue behind each other for something neither of them shares. Everything is
// read again after it is taken, because the turn that held it may have already done the work, and a
// second exchange on a rotating route would spend a refresh token to buy a token Canopy already has.
//
// This serialises the turns inside one Canopy. Two Canopy processes on one machine can still renew
// the same credential at once, and that is a known limit rather than an oversight: the fix is a lock
// file, and a lock file held across a network call is a way for a crashed process to leave a
// credential unusable until somebody deletes a file they have never heard of. The cost of the limit
// is one wasted renewal, and on a rotating route the loser of the race renews again on its next turn.
func (r *Refresher) renew(meta core.KeyMetadata) (core.Secret, error) {
	lock := r.lockFor(meta.Ref.Name)
	lock.Lock()
	defer lock.Unlock()

	in, err := r.store.SignIn(meta.Ref)
	if err != nil {
		return core.Secret{}, err
	}
	tokens, err := r.store.Tokens(meta.Ref)
	if err != nil {
		return core.Secret{}, err
	}
	if !r.due(in.ExpiresAt) {
		return tokens.Access, nil
	}

	if tokens.Refresh.IsZero() {
		// Not every vendor issues a refresh token, and one that does not is not broken. It is a
		// route where a lapsed sign-in is done again by hand, which is exactly what this says.
		return core.Secret{}, r.lapsed(meta.Ref.Name, in,
			"and it came with nothing to renew it with")
	}

	source, ok := r.source(meta, in)
	if !ok {
		return core.Secret{}, fmt.Errorf(
			"key %q is signed in as %s and its token is due to be renewed, but this build has no "+
				"route that knows how to renew it. That is a gap in Canopy rather than anything "+
				"wrong with the credential", meta.Ref.Name, in.Account)
	}

	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()

	// A context of the refresher's own rather than the turn's. Threading one through would change
	// session.Resolver, which four callers and every fake implement, to bound an exchange that is
	// already bounded above. It also loses nothing worth keeping: a renewal that outlives a
	// cancelled turn finishes, is written down, and is waiting for the next one.
	renewal, err := source.Refresh(ctx, in, tokens)
	switch {
	case errors.Is(err, ErrSignInLapsed):
		return core.Secret{}, r.lapsed(meta.Ref.Name, in, fmt.Sprintf("and %s refused to renew it: %v",
			source.Name(), err))
	case err != nil:
		// Deliberately not a core.ProviderError of any kind. A ProviderError describes what a
		// provider said about a request, and no request was made. Every kind available would also be
		// a lie about what to do next: ErrAuthentication sends somebody to replace a credential that
		// is fine, and the two kinds the chain falls back on would bill a different key because this
		// one needed a token, which is the dishonesty at internal/provider/chain.go:32 in a new place.
		return core.Secret{}, fmt.Errorf(
			"could not reach %s to renew the sign-in for key %q as %s: %w. The sign-in itself is "+
				"intact, so this is worth trying again shortly",
			source.Name(), meta.Ref.Name, in.Account, err)
	}

	if renewal.Tokens.Access.IsZero() {
		return core.Secret{}, fmt.Errorf(
			"%s renewed the sign-in for key %q as %s without returning an access token, so there is "+
				"nothing to send", source.Name(), meta.Ref.Name, in.Account)
	}
	if renewal.Tokens.Refresh.IsZero() {
		// Most vendors answer a renewal with an access token and nothing else, meaning keep the
		// refresh token you have. Taking that silence as "there is no refresh token now" would store
		// a credential that renews exactly once and then tells its owner to sign in again.
		renewal.Tokens.Refresh = tokens.Refresh
	}

	if err := r.store.Renew(meta.Ref, renewal); err != nil {
		// Returned rather than shrugged off with the new token used anyway. A renewal Canopy cannot
		// record is one it has to make again on every turn, and on a route that rotates refresh
		// tokens the one it just replaced is already dead, so this is the last renewal that will
		// work. Somebody has to hear about that at the moment it happens rather than a turn later
		// when the credential stops for no visible reason.
		return core.Secret{}, fmt.Errorf(
			"renewed the sign-in for key %q as %s but could not record it: %w. If %s stops working, "+
				"sign in again", meta.Ref.Name, in.Account, err, meta.Ref.Name)
	}
	return renewal.Tokens.Access, nil
}

// lapsed says a grant is finished, in the terms that describe the remedy rather than the mechanism.
//
// No command is named. The commands that sign somebody in arrive with S-03 and S-05, and an error
// telling a user to run something that does not exist is worse than one that stops at what they have
// to do. Tokens makes the same choice for the same reason, in signin.go.
func (r *Refresher) lapsed(name string, in SignIn, why string) error {
	return fmt.Errorf("the sign-in for key %q as %s has expired %s. Sign in again as %s to use it: %w",
		name, in.Account, why, in.Account, ErrSignInLapsed)
}

func (r *Refresher) source(meta core.KeyMetadata, in SignIn) (TokenSource, bool) {
	r.mu.Lock()
	sources := r.sources
	r.mu.Unlock()
	if sources == nil {
		return nil, false
	}
	return sources(meta, in)
}

func (r *Refresher) lockFor(name string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	lock, ok := r.renewing[name]
	if !ok {
		lock = &sync.Mutex{}
		r.renewing[name] = lock
	}
	return lock
}
