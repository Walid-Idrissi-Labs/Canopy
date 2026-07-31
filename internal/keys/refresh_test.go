package keys

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// noon is where every clock in this file is stopped. Nothing here waits for real time to pass: a
// test that proves a token is renewed early by sleeping until it nearly expires is a test nobody
// runs twice.
var noon = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

const (
	renewedAccess  = "gho_RENEWED-ACCESS-TOKEN"
	renewedRefresh = "ghr_RENEWED-REFRESH-TOKEN"
)

// expiringIn is a token expiry a given distance from noon.
func expiringIn(d time.Duration) *time.Time {
	at := noon.Add(d)
	return &at
}

// fakeSource is the token endpoint, answering from the test rather than from a vendor.
type fakeSource struct {
	calls atomic.Int64
	// linger holds a renewal open, so a test about two turns arriving at once has something for the
	// second one to arrive during.
	linger time.Duration
	answer func(SignIn, Tokens) (Renewal, error)
}

func (f *fakeSource) Name() string { return "the fake vendor" }

func (f *fakeSource) Refresh(_ context.Context, in SignIn, tokens Tokens) (Renewal, error) {
	f.calls.Add(1)
	time.Sleep(f.linger)
	return f.answer(in, tokens)
}

// renewsWith answers every renewal with the same new pair of tokens.
func renewsWith(access, refresh string, expiresAt *time.Time) *fakeSource {
	return &fakeSource{answer: func(SignIn, Tokens) (Renewal, error) {
		return Renewal{
			Tokens:    Tokens{Access: core.NewSecret(access), Refresh: core.NewSecret(refresh)},
			ExpiresAt: expiresAt,
		}, nil
	}}
}

func refuses(err error) *fakeSource {
	return &fakeSource{answer: func(SignIn, Tokens) (Renewal, error) { return Renewal{}, err }}
}

// signedInAt builds a store holding one signed-in credential, and a refresher over it whose clock
// is stopped at noon. A nil source is a build that knows no routes, which is every build until S-03.
func signedInAt(
	t *testing.T, expiresAt *time.Time, source TokenSource,
) (*Store, *Refresher, core.KeyMetadata) {
	t.Helper()
	store, _ := newTestStore(t)
	meta := anthropic("copilot")
	if _, err := store.PutSignIn(
		meta, signedIn("walid@example.invalid", expiresAt), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	return store, refresherOver(store, source), meta
}

func refresherOver(store *Store, source TokenSource) *Refresher {
	r := NewRefresher(store)
	r.SetClock(func() time.Time { return noon })
	if source != nil {
		r.Renews(func(core.KeyMetadata, SignIn) (TokenSource, bool) { return source, true })
	}
	return r
}

// holdsTokens fails unless the store's backend holds exactly these two.
func holdsTokens(t *testing.T, store *Store, ref core.KeyRef, access, refresh string) {
	t.Helper()
	tokens, err := store.Tokens(ref)
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if tokens.Access.Reveal() != access {
		t.Errorf("the stored access token is %q, want %q", tokens.Access.Reveal(), access)
	}
	if tokens.Refresh.Reveal() != refresh {
		t.Errorf("the stored refresh token is %q, want %q", tokens.Refresh.Reveal(), refresh)
	}
}

// The token has not expired at the moment this runs, and it is renewed anyway. That is the point of
// the margin: a machine whose clock is a few minutes ahead of the vendor's, or a tool loop that
// takes a few minutes to finish, would otherwise send a token the far end had already retired.
func TestATokenInsideTheRefreshWindowIsRenewedBeforeItIsHandedOut(t *testing.T) {
	later := expiringIn(45 * time.Minute)
	source := renewsWith(renewedAccess, renewedRefresh, later)
	store, refresher, meta := signedInAt(t, expiringIn(2*time.Minute), source)

	got, err := refresher.Credential(meta)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if got.Reveal() != renewedAccess {
		t.Errorf("the request would carry %q, want the token that was just bought", got.Reveal())
	}
	if source.calls.Load() != 1 {
		t.Errorf("the vendor was asked %d times, want once", source.calls.Load())
	}

	holdsTokens(t, store, meta.Ref, renewedAccess, renewedRefresh)
	in, err := store.SignIn(meta.Ref)
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if in.ExpiresAt == nil || !in.ExpiresAt.Equal(*later) {
		t.Errorf("the record still expires at %v, want %v", in.ExpiresAt, later)
	}
}

// The other half of the same rule. A session on a healthy credential must make no call it did not
// need, or every turn pays for a round trip to a token endpoint to learn nothing.
func TestATokenValidPastTheRefreshWindowIsNotRenewed(t *testing.T) {
	for _, left := range []time.Duration{45 * time.Minute, RefreshMargin + time.Second} {
		source := renewsWith(renewedAccess, renewedRefresh, expiringIn(time.Hour))
		_, refresher, meta := signedInAt(t, expiringIn(left), source)

		got, err := refresher.Credential(meta)
		if err != nil {
			t.Fatalf("Credential with %s left: %v", left, err)
		}
		if got.Reveal() != plantedAccess {
			t.Errorf("with %s left the token was replaced, and it did not need to be", left)
		}
		if source.calls.Load() != 0 {
			t.Errorf("with %s left the vendor was called %d times", left, source.calls.Load())
		}
	}
}

// A renewal held only in memory is a renewal paid for again by the next process that starts, and on
// a route that rotates refresh tokens the one it would try with is already dead.
func TestARenewedTokenIsWrittenDownSoTheNextRunDoesNotBuyAnother(t *testing.T) {
	backend := NewMemoryBackend()
	path := filepath.Join(t.TempDir(), "keys.json")
	meta := anthropic("copilot")

	store := NewStore(backend, path)
	if _, err := store.PutSignIn(
		meta, signedIn("walid@example.invalid", expiringIn(time.Minute)), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	source := renewsWith(renewedAccess, renewedRefresh, expiringIn(45*time.Minute))
	if _, err := refresherOver(store, source).Credential(meta); err != nil {
		t.Fatalf("Credential: %v", err)
	}

	// What the next `canopy` sees: the same two files, opened again from nothing.
	restarted := NewStore(backend, path)
	got, err := refresherOver(restarted, source).Credential(meta)
	if err != nil {
		t.Fatalf("Credential after a restart: %v", err)
	}
	if got.Reveal() != renewedAccess {
		t.Errorf("a new process got %q, want the token the last one bought", got.Reveal())
	}
	if source.calls.Load() != 1 {
		t.Errorf("the vendor was asked %d times across two processes, want once", source.calls.Load())
	}
}

// A grant the vendor will not renew is finished, and the only thing that fixes it is signing in
// again. It is not a wrong credential, so it must not arrive as one: ErrAuthentication is documented
// as never retry and never fall back, and no request was even made here.
func TestARefusedRenewalIsALapsedSignInThatNamesItsRemedy(t *testing.T) {
	source := refuses(fmt.Errorf("the refresh token was revoked: %w", ErrSignInLapsed))
	store, refresher, meta := signedInAt(t, expiringIn(time.Minute), source)

	_, err := refresher.Credential(meta)
	if err == nil {
		t.Fatal("a refused renewal produced a credential")
	}
	if !errors.Is(err, ErrSignInLapsed) {
		t.Errorf("a revoked grant did not read as a lapsed sign-in: %v", err)
	}
	for _, want := range []string{"copilot", "walid@example.invalid", "Sign in again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
	var provErr *core.ProviderError
	if errors.As(err, &provErr) {
		t.Errorf("a failure before any request was built came back as a provider error: %v", provErr)
	}

	// Nothing was written. The old tokens are dead, but overwriting them buys nothing and loses the
	// only record of what the credential was.
	holdsTokens(t, store, meta.Ref, plantedAccess, plantedRefresh)
}

// The other failure, and the reason the two are told apart at all. Sending somebody through a
// sign-in because a connection dropped costs them a flow they did not need, and on a rotating route
// it throws away a grant that was working.
func TestARenewalThatCouldNotReachTheVendorIsNotALapsedSignIn(t *testing.T) {
	source := refuses(errors.New("dial tcp 140.82.121.6:443: connect: connection refused"))
	store, refresher, meta := signedInAt(t, expiringIn(time.Minute), source)

	_, err := refresher.Credential(meta)
	if err == nil {
		t.Fatal("an unreachable vendor produced a credential")
	}
	if errors.Is(err, ErrSignInLapsed) {
		t.Errorf("a dropped connection was reported as a finished sign-in: %v", err)
	}
	if strings.Contains(err.Error(), "Sign in again") {
		t.Errorf("the remedy for a network failure is not a sign-in: %v", err)
	}
	for _, want := range []string{"copilot", "walid@example.invalid", "trying again", "intact"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
	var provErr *core.ProviderError
	if errors.As(err, &provErr) {
		t.Errorf("a failure before any request was built came back as a provider error: %v", provErr)
	}

	holdsTokens(t, store, meta.Ref, plantedAccess, plantedRefresh)
	in, err := store.SignIn(meta.Ref)
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if in.ExpiresAt == nil || !in.ExpiresAt.Equal(*expiringIn(time.Minute)) {
		t.Errorf("a failed renewal moved the expiry to %v", in.ExpiresAt)
	}
}

// Two conversations on one credential is the ordinary case, not the exotic one, and two renewals
// between them is not merely wasteful: on a route that rotates refresh tokens the second exchange
// spends the token the first one just bought.
func TestTwoTurnsStartingAtOnceRenewOneCredentialOnce(t *testing.T) {
	source := renewsWith(renewedAccess, renewedRefresh, expiringIn(45*time.Minute))
	source.linger = 20 * time.Millisecond
	store, refresher, meta := signedInAt(t, expiringIn(time.Minute), source)

	const turns = 8
	start := make(chan struct{})
	got := make([]string, turns)
	errs := make([]error, turns)

	var wg sync.WaitGroup
	for i := range turns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			secret, err := refresher.Credential(meta)
			got[i], errs[i] = secret.Reveal(), err
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
		if got[i] != renewedAccess {
			t.Errorf("turn %d ran on %q, want the renewed token", i, got[i])
		}
	}
	if source.calls.Load() != 1 {
		t.Errorf("%d turns produced %d renewals, want one", turns, source.calls.Load())
	}
	holdsTokens(t, store, meta.Ref, renewedAccess, renewedRefresh)
}

// Most vendors answer a renewal with an access token and nothing else, meaning keep the refresh
// token you have. Reading that silence as "there is no refresh token now" stores a credential that
// renews exactly once and then tells its owner to sign in again for no reason.
func TestARenewalThatIssuesNoNewRefreshTokenKeepsTheOneItHas(t *testing.T) {
	source := &fakeSource{answer: func(SignIn, Tokens) (Renewal, error) {
		return Renewal{
			Tokens:    Tokens{Access: core.NewSecret(renewedAccess)},
			ExpiresAt: expiringIn(45 * time.Minute),
		}, nil
	}}
	store, refresher, meta := signedInAt(t, expiringIn(time.Minute), source)

	if _, err := refresher.Credential(meta); err != nil {
		t.Fatalf("Credential: %v", err)
	}
	holdsTokens(t, store, meta.Ref, renewedAccess, plantedRefresh)
}

// A vendor that never said when the token expires has not said it is old. Renewing on that guess
// spends a refresh token every turn for the life of the credential, and on a rotating route it
// spends the current one.
func TestASignInWhoseExpiryTheVendorNeverGaveIsNotRenewedOnAGuess(t *testing.T) {
	source := renewsWith(renewedAccess, renewedRefresh, nil)
	_, refresher, meta := signedInAt(t, nil, source)

	got, err := refresher.Credential(meta)
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if got.Reveal() != plantedAccess {
		t.Errorf("the token was replaced on a guess, got %q", got.Reveal())
	}
	if source.calls.Load() != 0 {
		t.Errorf("an expiry nobody stated caused %d renewals", source.calls.Load())
	}
}

// Both call sites ask one thing for a credential, whichever kind it is, so the rule about when a
// token is too old to send cannot be implemented twice and drift. A pasted secret goes through
// untouched, and a delegated credential is refused in the terms it was refused in before, since a
// route Canopy holds no token for has no token to renew.
func TestOnlyASignInIsEverRenewed(t *testing.T) {
	store, _ := newTestStore(t)
	source := renewsWith(renewedAccess, renewedRefresh, expiringIn(time.Hour))
	refresher := refresherOver(store, source)

	pasted := anthropic("claude")
	if _, err := store.Put(pasted, core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := refresher.Credential(pasted)
	if err != nil {
		t.Fatalf("Credential on a pasted key: %v", err)
	}
	if got.Reveal() != planted {
		t.Errorf("the pasted value came back as %q", got.Reveal())
	}

	delegated := anthropic("claude-code")
	if _, err := store.PutSignIn(
		delegated, SignIn{Kind: KindDelegated, Account: "walid@example.invalid"}, Tokens{},
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	if _, err := refresher.Credential(delegated); err == nil {
		t.Error("a delegated credential produced something to put in a header")
	} else if !strings.Contains(err.Error(), "delegated") {
		t.Errorf("the refusal should say what this credential is, got: %v", err)
	}

	if source.calls.Load() != 0 {
		t.Errorf("a credential with no tokens to renew caused %d renewals", source.calls.Load())
	}
}

// failOnSecondSet is a backend that accepts the sign-in and then loses the keychain, which is what a
// locked or removed one looks like from here.
type failOnSecondSet struct {
	Backend
	sets atomic.Int64
}

func (f *failOnSecondSet) Set(account, secret string) error {
	if f.sets.Add(1) > 1 {
		return errors.New("the keychain is locked")
	}
	return f.Backend.Set(account, secret)
}

// A renewal that cannot be recorded is the last one that will work on a rotating route, because the
// refresh token it replaced is already dead at the vendor. Using the new token anyway would get this
// turn answered and leave the credential to stop for no visible reason on a later one.
func TestARenewalThatCannotBeRecordedIsReportedRatherThanUsed(t *testing.T) {
	backend := &failOnSecondSet{Backend: NewMemoryBackend()}
	store := NewStore(backend, filepath.Join(t.TempDir(), "keys.json"))
	meta := anthropic("copilot")
	if _, err := store.PutSignIn(
		meta, signedIn("walid@example.invalid", expiringIn(time.Minute)), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}

	source := renewsWith(renewedAccess, renewedRefresh, expiringIn(45*time.Minute))
	_, err := refresherOver(store, source).Credential(meta)
	if err == nil {
		t.Fatal("a renewal nobody could write down was handed out as if it were safe")
	}
	for _, want := range []string{"could not record", "copilot", "sign in again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should mention %q, got: %v", want, err)
		}
	}
}

// A renewal owns the tokens and the expiry. Everything else about a credential belongs to the
// credential rather than to the value behind it, exactly as it does when a pasted key is rotated.
func TestRenewingTouchesTheTokensAndTheExpiryAndNothingElse(t *testing.T) {
	store, _ := newTestStore(t)
	meta := anthropic("copilot")
	first, err := store.PutSignIn(meta, signedIn("walid@example.invalid", expiringIn(time.Minute)), bothTokens())
	if err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	if err := store.MarkUsed(meta.Ref); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	if err := store.AddModel(meta.Ref, "gpt-5.2-codex", "GPT-5.2 Codex"); err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	if err := store.SetRate(meta.Ref, core.KeyRate{InputPerMTok: 1.25, OutputPerMTok: 10}); err != nil {
		t.Fatalf("SetRate: %v", err)
	}

	later := expiringIn(45 * time.Minute)
	if err := store.Renew(meta.Ref, Renewal{
		Tokens:    Tokens{Access: core.NewSecret(renewedAccess), Refresh: core.NewSecret(renewedRefresh)},
		ExpiresAt: later,
	}); err != nil {
		t.Fatalf("Renew: %v", err)
	}

	after, err := store.Metadata(meta.Ref)
	if err != nil {
		t.Fatalf("Metadata: %v", err)
	}
	if !after.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("renewing changed the creation date from %v to %v", first.CreatedAt, after.CreatedAt)
	}
	if after.LastUsedAt == nil {
		t.Error("renewing forgot when the credential was last used")
	}
	if after.Rate.InputPerMTok != 1.25 || after.Rate.OutputPerMTok != 10 {
		t.Errorf("renewing dropped the rate: %+v", after.Rate)
	}
	models, err := store.Models(meta.Ref)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 1 || models[0].ID != "gpt-5.2-codex" {
		t.Errorf("renewing lost the models its owner added: %+v", models)
	}

	holdsTokens(t, store, meta.Ref, renewedAccess, renewedRefresh)
	in, err := store.SignIn(meta.Ref)
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if in.ExpiresAt == nil || !in.ExpiresAt.Equal(*later) {
		t.Errorf("the expiry is %v, want %v", in.ExpiresAt, later)
	}
}

// A renewal is a network exchange, so the credential it belongs to can change underneath it. Neither
// case may be papered over: a credential somebody deleted must not come back because a request that
// outlived it succeeded, and one somebody replaced with a pasted key must not be converted back.
func TestACredentialThatStoppedBeingASignInIsNotRenewedIntoOneAgain(t *testing.T) {
	renewal := Renewal{
		Tokens:    Tokens{Access: core.NewSecret(renewedAccess), Refresh: core.NewSecret(renewedRefresh)},
		ExpiresAt: expiringIn(45 * time.Minute),
	}

	store, _ := newTestStore(t)
	meta := anthropic("copilot")
	if _, err := store.PutSignIn(
		meta, signedIn("walid@example.invalid", expiringIn(time.Minute)), bothTokens(),
	); err != nil {
		t.Fatalf("PutSignIn: %v", err)
	}
	if err := store.Remove(meta.Ref); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := store.Renew(meta.Ref, renewal); !errors.Is(err, ErrNotFound) {
		t.Errorf("renewing a removed credential gave %v, want it to report the credential is gone", err)
	}
	all, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("a removed credential came back: %+v", all)
	}

	// And the same credential name pasted over while a renewal was in flight.
	if _, err := store.Put(meta, core.NewSecret(planted)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := store.Renew(meta.Ref, renewal); err == nil {
		t.Error("a renewal was written over a value somebody pasted")
	}
	got, err := store.Get(meta.Ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Reveal() != planted {
		t.Errorf("the pasted value became %q", got.Reveal())
	}
}
