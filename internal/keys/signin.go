package keys

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Kind is how Canopy came to hold a credential.
//
// It lives in the keys record rather than beside the two core.Provider constants, and not only
// because internal/core is frozen. Provider answers "which API shape does this speak" and Kind
// answers "where did this come from", and the two are independent: a subscription and a key
// somebody bought can reach the same wire protocol, and one vendor's subscription can be reached
// two ways. Folding them into one enumeration would make every combination a new constant. It is
// also D-46 rule 4 a second time, which is why record.Models is here instead of on
// core.KeyMetadata: a frozen contract does not grow a field for something the layer above it can
// carry.
type Kind string

const (
	// KindPasted is a value somebody typed in, which is what every credential was before phase S.
	//
	// Written to disk as nothing at all rather than as the word. A keys.json from a build that
	// predates any of this reads back as the document it already was, and a credential somebody
	// added last year does not acquire a claim about itself that nobody made.
	KindPasted Kind = "pasted"

	// KindSignedIn is a grant Canopy obtained by signing the user in and holds the tokens for.
	// Tokens missing from the backend is damage, and Tokens reports it as damage.
	KindSignedIn Kind = "signed-in"

	// KindDelegated is a vendor's own agent that the user signed in to themselves, which Canopy
	// drives and holds no credential for. Its half of the backend is empty and empty is correct.
	//
	// A separate kind rather than a signed-in credential that happens to have no tokens, because
	// those two would be the same record on disk and only one of them is a fault. Without the
	// distinction, either a delegated credential reads as damaged for its whole life or a sign-in
	// whose tokens were deleted from the keychain reads as fine, and both are worse than a word.
	//
	// It also turns a promise into something the code enforces. D-51 permits the delegated route on
	// the ground that Canopy never holds the user's subscription credential on it, so a delegated
	// credential handed a token at all is refused rather than stored.
	KindDelegated Kind = "delegated"
)

// IsSignIn reports whether this is a credential somebody signed in to rather than pasted.
func (k Kind) IsSignIn() bool { return k == KindSignedIn || k == KindDelegated }

func (k Kind) String() string { return string(k) }

// SignIn is everything about a signed-in credential that can be read without unlocking anything.
//
// It holds no token and has nowhere to put one, which is the same arrangement core.KeyRef has with
// core.Secret and is there for the same reason: this is the type that travels to a screen. The
// tokens are Tokens, they come from the backend, and they are fetched at the moment of use.
type SignIn struct {
	// Kind is which sort of credential this is. KindPasted here means nobody signed anything in,
	// which is a fair question to ask of any credential and a fair answer to give.
	Kind Kind

	// Account is who the vendor says the grant belongs to: a login, an email, whatever it reports.
	// Required for a sign-in, because two subscriptions on one machine are otherwise two identical
	// rows and the person choosing between them has nothing to choose on.
	Account string

	// ExpiresAt is when the access token stops being accepted.
	//
	// A pointer rather than a zero time, for the reason LastUsedAt is one: "expires at the zero
	// time" and "the vendor never said" are different facts, and something deciding whether to
	// refresh has to be able to tell them apart before it acts.
	ExpiresAt *time.Time

	// Route is which way in produced this grant. See record.Route in store.go for why it exists and
	// why it is allowed to be empty.
	Route string
}

// Tokens are the secrets behind a sign-in.
//
// core.Secret rather than string, so the protections that keep an API key out of a log keep a
// refresh token out of one too. It also settles where these can be kept: core.Secret refuses to
// unmarshal, at internal/core/secret.go:84, so tokens cannot round trip through keys.json even by
// accident, and the backend is the only place left for them.
type Tokens struct {
	// Access is the token that goes on the request.
	Access core.Secret

	// Refresh is what buys a new access token when the old one lapses. Optional: not every vendor
	// issues one, and a route that does not is a route where an expired sign-in is done again by
	// hand rather than a route that is broken.
	Refresh core.Secret
}

// IsZero reports whether there is no token here at all.
func (t Tokens) IsZero() bool { return t.Access.IsZero() && t.Refresh.IsZero() }

// grantVersion marks a backend entry as a sign-in's tokens and says which shape they are in.
const grantVersion = 1

// storedGrant is the shape the tokens take inside the backend.
//
// Both tokens go into one backend entry, under the credential's own name, rather than into two
// entries under names derived from it. The alternative was real and not unsafe: a derived name
// could not collide with anything a user is allowed to type, since keyNamePattern at
// internal/core/key.go:50 admits no character a derivation would use. It was rejected on what
// happens when a write stops halfway. Two entries are two Set calls with no transaction around
// them, and a failure between them leaves an access token with no refresh token beside it. There is
// no ordering that makes that half state harmless the way an orphaned secret is harmless, because
// the credential still looks usable and is quietly no longer renewable. Two entries also mean
// Remove has to delete both, and the one a later edit forgets is a refresh token sitting in
// somebody's keychain after they believe they deleted the credential.
//
// One entry does not handle either problem, it does not have them: one write, one delete, and
// Remove needed no change at all to take the tokens with it. It also makes it impossible for a
// credential to be a pasted secret and a sign-in at once, since both write the same slot, so
// converting either into the other leaves nothing behind to be found later.
//
// The cost is that the backend now holds a document where it used to hold a bare string, and the
// price of that is paid in the field name below.
//
// Its own type with plain string fields rather than Tokens, because Backend deals in strings and
// the conversion belongs at that boundary, so the window in which a bare token exists as an
// ordinary string stays as narrow as it is for a pasted secret. Its own type for storedModel's
// reason as well: this is a stored format, and a struct meant for a Go API is not one.
type storedGrant struct {
	// CanopyGrant is the marker and the version at once, and it is named for a person rather than
	// for the parser. Somebody who opens Keychain Access, finds this entry and wonders what it is
	// should be able to answer that from its first line.
	CanopyGrant int `json:"canopyGrant"`

	Access  string `json:"access"`
	Refresh string `json:"refresh,omitempty"`
}

// parseGrant reads a backend entry as a sign-in's tokens, reporting whether it is one.
//
// The marker field decides, not whether the value happens to parse as JSON. A pasted credential is
// not a JSON object, but "starts with a brace" is a guess and this is an answer.
func parseGrant(value string) (storedGrant, bool) {
	var grant storedGrant
	if err := json.Unmarshal([]byte(value), &grant); err != nil {
		return storedGrant{}, false
	}
	return grant, grant.CanopyGrant != 0
}

// signIn reads a record's sign-in facts, reading an absent kind as a credential somebody pasted.
func (r record) signIn() SignIn {
	kind := Kind(r.Kind)
	if kind == "" {
		kind = KindPasted
	}
	return SignIn{Kind: kind, Account: r.Account, ExpiresAt: r.ExpiresAt, Route: r.Route}
}

// validate checks a sign-in and its tokens agree with each other before anything is written.
func (in SignIn) validate(name string, tokens Tokens) error {
	switch in.Kind {
	case KindSignedIn:
		if tokens.Access.IsZero() {
			return fmt.Errorf(
				"signing key %q in produced no access token, so there is no sign-in to store", name)
		}
	case KindDelegated:
		// Refused rather than quietly dropped. D-51 permits the delegated route because Canopy
		// never holds the user's subscription credential on it, so a caller arriving here with one
		// has either misread the route or built the thing that decision rules out. Both are worth
		// stopping in front of somebody.
		if !tokens.IsZero() {
			return fmt.Errorf(
				"key %q is a delegated sign-in, which exists because Canopy holds no credential of "+
					"the user's on that route, so it cannot be given tokens", name)
		}
		if in.ExpiresAt != nil {
			return fmt.Errorf(
				"key %q is a delegated sign-in and has no token of its own to expire", name)
		}
	case KindPasted:
		return fmt.Errorf("key %q was not signed in to, so it is stored by Put and not here", name)
	default:
		return fmt.Errorf("key %q has unknown credential kind %q", name, in.Kind)
	}

	if in.Account == "" {
		return fmt.Errorf(
			"signing key %q in needs the account it belongs to, since two subscriptions on one "+
				"machine are otherwise two rows nobody can tell apart", name)
	}
	return nil
}

// PutSignIn stores a credential somebody signed in to, replacing any existing one of that name.
//
// The counterpart of Put. The tokens reach the backend before the record reaches disk, so metadata
// can never claim a sign-in whose grant was not stored. The old backend value is preserved first and
// restored if saving metadata fails, because the safer ordering still has to be a compensated
// operation: an orphaned grant or an old account label backed by a new account's token is not an
// acceptable failed sign-in.
//
// Signing in again is rotation, so it carries over what rotation carries over. That is not a
// promise made twice but the same upsert Put uses, which is what stops the two answers drifting
// apart the next time one of them is edited.
//
// No fingerprint is recorded. Put's fingerprint exists so two pasted keys can be told apart in a
// list without either being shown, and here the account does that job better and is already on
// screen. A fingerprint of the access token would also change on every refresh, which is a column
// that looks like an identity and moves like a clock.
func (s *Store) PutSignIn(meta core.KeyMetadata, in SignIn, tokens Tokens) (core.KeyMetadata, error) {
	if err := meta.Ref.Validate(); err != nil {
		return core.KeyMetadata{}, err
	}
	if meta.Ref.Provider.RequiresBaseURL() && meta.BaseURL == "" {
		return core.KeyMetadata{}, fmt.Errorf(
			"key %q uses provider %q, which needs a base URL", meta.Ref.Name, meta.Ref.Provider)
	}
	in.Account = strings.TrimSpace(in.Account)
	if err := in.validate(meta.Ref.Name, tokens); err != nil {
		return core.KeyMetadata{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return core.KeyMetadata{}, err
	}

	previous, previousErr := s.backend.Get(meta.Ref.Name)
	previousExists := previousErr == nil
	if previousErr != nil && !errors.Is(previousErr, ErrNotFound) {
		return core.KeyMetadata{}, fmt.Errorf(
			"preserving the previous value for key %q before signing in: %w",
			meta.Ref.Name, previousErr)
	}

	if err := s.writeTokens(meta.Ref.Name, tokens); err != nil {
		return core.KeyMetadata{}, err
	}

	stored := record{
		Name:             meta.Ref.Name,
		Provider:         string(meta.Ref.Provider),
		BaseURL:          meta.BaseURL,
		Model:            meta.Model,
		CreatedAt:        s.clock(),
		Kind:             string(in.Kind),
		Account:          in.Account,
		ExpiresAt:        in.ExpiresAt,
		Route:            in.Route,
		InputPerMTok:     meta.Rate.InputPerMTok,
		OutputPerMTok:    meta.Rate.OutputPerMTok,
		CacheReadPerMTok: meta.Rate.CacheReadPerMTok,
	}
	records, stored = upsert(records, stored, meta.Rate)

	if err := s.save(records); err != nil {
		var restoreErr error
		if previousExists {
			restoreErr = s.backend.Set(meta.Ref.Name, previous)
		} else {
			restoreErr = s.backend.Delete(meta.Ref.Name)
		}
		if restoreErr != nil {
			return core.KeyMetadata{}, fmt.Errorf(
				"saving the sign-in for key %q failed: %w; restoring its previous %s value also "+
					"failed: %v. The credential backend and keys.json may now disagree; do not "+
					"retry blindly",
				meta.Ref.Name, err, s.backend.Name(), restoreErr)
		}
		return core.KeyMetadata{}, err
	}
	return stored.toMetadata(), nil
}

// writeTokens puts a sign-in's tokens where secrets go, or empties the slot when there are none.
//
// Emptying it rather than leaving it alone. A delegated sign-in holds nothing, so a credential that
// was a pasted key or a token grant a moment ago would otherwise keep that old value in the
// keychain under the same name: a live credential nothing lists and nobody will think to revoke.
func (s *Store) writeTokens(name string, tokens Tokens) error {
	if tokens.IsZero() {
		return s.backend.Delete(name)
	}
	encoded, err := json.Marshal(storedGrant{
		CanopyGrant: grantVersion,
		Access:      tokens.Access.Reveal(),
		Refresh:     tokens.Refresh.Reveal(),
	})
	if err != nil {
		return fmt.Errorf("encoding the tokens for key %q: %w", name, err)
	}
	return s.backend.Set(name, string(encoded))
}

// Renew replaces a sign-in's tokens and the moment they expire, and touches nothing else.
//
// Separate from PutSignIn rather than a call to it, because renewing is not authenticating again and
// the difference shows up under concurrency. PutSignIn is handed a whole record and writes back
// whatever the caller was holding, so a renewal that arrived beside a rate change or a model
// selection would put back the values it read before that change and lose the other one silently.
// This owns two fields and edits two fields, which is the same rule SetModel and SetRate follow.
//
// The tokens reach the backend before the record reaches disk, as in Put, but the reason is this
// task's rather than Put's. Failing between the two leaves a record whose expiry is older than the
// tokens behind it, which costs one unnecessary renewal on the next turn and nothing else. The other
// order leaves a record promising another hour with the old, now dead token still in the keychain:
// a request rejected as unauthenticated, and a user told to replace a credential that only needed
// renewing. That is the exact failure the refresher exists to remove.
func (s *Store) Renew(ref core.KeyRef, renewal Renewal) error {
	if renewal.Tokens.Access.IsZero() {
		return fmt.Errorf("renewing key %q needs an access token to renew it with", ref.Name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return err
	}

	for i, existing := range records {
		if existing.Name != ref.Name {
			continue
		}
		// Refused rather than converted. A credential that was signed in when the renewal started
		// and is a pasted secret now had its value replaced while this was in flight, and writing a
		// grant over that would undo a change somebody made deliberately, in favour of one nobody
		// asked for.
		if in := existing.signIn(); in.Kind != KindSignedIn {
			return fmt.Errorf(
				"key %q is no longer a sign-in Canopy holds tokens for, so there is nothing to renew",
				ref.Name)
		}

		if err := s.writeTokens(ref.Name, renewal.Tokens); err != nil {
			return err
		}
		records[i].ExpiresAt = renewal.ExpiresAt
		return s.save(records)
	}

	// Removed while the renewal was in flight. Reported rather than appended: a credential somebody
	// deleted must not come back because a request that outlived it succeeded.
	return fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
}

// SignIn returns what is known about a credential's sign-in without touching the backend.
//
// Separate from Tokens on purpose. Everything here is safe to display, so a list can say who a
// credential belongs to and when it stops working without prompting for a keychain unlock, which is
// the difference between a list that draws and a list that stops to ask.
func (s *Store) SignIn(ref core.KeyRef) (SignIn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return SignIn{}, err
	}
	found, ok := findRecord(records, ref.Name)
	if !ok {
		return SignIn{}, fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
	}
	return found.signIn(), nil
}

// Tokens returns the tokens behind a signed-in credential.
//
// The counterpart of Get, and it separates the three ways this can fail rather than reporting one
// of them for all three. A credential that was pasted has no tokens and never did. A delegated one
// has none by design. A signed-in one with nothing behind it has lost them, and only that last case
// is damage.
func (s *Store) Tokens(ref core.KeyRef) (Tokens, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	records, err := s.load()
	if err != nil {
		return Tokens{}, err
	}
	found, ok := findRecord(records, ref.Name)
	if !ok {
		return Tokens{}, fmt.Errorf("no key named %q: %w", ref.Name, ErrNotFound)
	}

	in := found.signIn()
	switch in.Kind {
	case KindSignedIn:
	case KindDelegated:
		return Tokens{}, fmt.Errorf(
			"key %q is a delegated sign-in as %s, which Canopy drives and holds no tokens for",
			ref.Name, in.Account)
	default:
		return Tokens{}, fmt.Errorf(
			"key %q holds a value somebody pasted rather than a sign-in, so it has no tokens",
			ref.Name)
	}

	value, err := s.backend.Get(ref.Name)
	if errors.Is(err, ErrNotFound) {
		// The remedy names no command, unlike the pasted one, because the commands that sign
		// somebody in arrive with S-03 and S-04. An error telling a user to run something that
		// does not exist yet is worse than one that stops at what they have to do.
		return Tokens{}, s.halvesDisagree(ref.Name,
			fmt.Sprintf("Sign in again as %s to restore it", in.Account))
	}
	if err != nil {
		return Tokens{}, err
	}

	grant, ok := parseGrant(value)
	if !ok {
		// The record says signed in and the backend holds something that is not a sign-in's
		// tokens, which is the other half of the case Get refuses. Named for what it is rather
		// than reported as a decoding failure, since nothing the user did is malformed.
		return Tokens{}, fmt.Errorf(
			"key %q is recorded as signed in as %s but the %s backend holds something else under "+
				"its name, which is what a change that stopped halfway leaves behind. Sign in "+
				"again to replace it", ref.Name, in.Account, s.backend.Name())
	}
	return Tokens{
		Access:  core.NewSecret(grant.Access),
		Refresh: core.NewSecret(grant.Refresh),
	}, nil
}
