package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
)

// Route is the name this way in is stored under and asked for by.
//
// Written into the credential record so that a later build can tell a Copilot sign-in from a Codex
// one, which Provider cannot do because both are openai-compatible. See record.Route in
// internal/keys/store.go.
const Route = "copilot"

// BaseURL is the endpoint a Copilot turn ends up at.
//
// Canopy never dials it. The requests are made by the Copilot CLI runtime, and this string exists
// because core.Provider.RequiresBaseURL is true for openai-compatible credentials and core is
// frozen, so the field has to hold something. It holds the truth rather than a placeholder: this is
// where the turn goes, even though somebody else's process is what sends it. It also keeps pricing
// from filing Copilot turns under whichever other openai-compatible gateway the user has.
const BaseURL = "https://api.githubcopilot.com"

// ClientIDEnvVar names the environment variable that supplies Canopy's GitHub app client id.
//
// The id is not a secret, it is an identity, and it is deliberately not compiled in with a value
// invented here. Canopy needs its own registration, that registration is an act only the maintainer
// can perform, and a build that shipped somebody else's id would be Canopy signing users in as a
// program they did not choose. Until the registration exists this variable is how a developer points
// a build at their own app. See INSTALL.md.
const ClientIDEnvVar = "CANOPY_GITHUB_CLIENT_ID"

// ClientSecretEnvVar names the environment variable that supplies a client secret, for the one thing
// that cannot be done without one.
//
// GitHub's device flow needs no secret to obtain a token and none to use it. It needs one to renew
// an expiring token and one to revoke a grant, because both of those endpoints authenticate the app
// rather than the user. Canopy's recommended registration issues tokens that do not expire, so
// neither is reached; a maintainer who turns expiring tokens on has to supply this or accept that a
// lapsed grant is signed in to again by hand.
const ClientSecretEnvVar = "CANOPY_GITHUB_CLIENT_SECRET"

// clientID is the compiled-in client id, empty in every build until a registration exists.
//
// A var rather than a const so a release build can set it with -X and so a test can drive the flow
// without an environment variable. See INSTALL.md for what has to be registered.
var clientID = ""

// Scopes are what the device flow asks for, and this is the honest state of how they were arrived at.
//
// GitHub documents no scope for Copilot. Their published table of OAuth scopes has no entry
// containing the word, their Copilot SDK setup page tells you to create an app and names no scope at
// all, and the SDK's own Go source validates nothing about the token it is handed. So there is no
// authority to cite and the list below is evidence rather than documentation:
//
//   - "copilot" is what every third-party Copilot client sends, and GitHub's own editor flow sends
//     it. It is undocumented and it is the one that plausibly carries the entitlement.
//   - "read:user" is documented, is the smallest scope that answers GET /user, and is what makes a
//     credential able to say whose subscription it is, which S-01 requires of every sign-in.
//
// Neither has been confirmed against a live Copilot seat from this repository, because confirming it
// needs a seat and a run of the flow. Whoever first runs it should narrow this list one entry at a
// time and record what stopped working, and this comment should then say what they found instead of
// what they expected. Overriding it without a rebuild is what CANOPY_GITHUB_SCOPES is for.
var Scopes = []string{"copilot", "read:user"}

// ScopesEnvVar overrides Scopes, space separated, so the list above can be narrowed by experiment
// rather than by rebuilding.
const ScopesEnvVar = "CANOPY_GITHUB_SCOPES"

// Endpoints are the three GitHub addresses this route talks to.
//
// A struct rather than three constants because every test in this file needs to point them somewhere
// else, and a package that can only be tested against github.com is a package with no tests.
type Endpoints struct {
	// DeviceCode is where a sign-in starts.
	DeviceCode string
	// Token is where a device code becomes an access token, and where a refresh token becomes a
	// newer one. GitHub uses one endpoint for both, distinguished by grant type.
	Token string
	// API is the REST root, used only to ask who the token belongs to.
	API string
}

// GitHub is the real thing.
func GitHub() Endpoints {
	return Endpoints{
		DeviceCode: "https://github.com/login/device/code",
		Token:      "https://github.com/login/oauth/access_token",
		API:        "https://api.github.com",
	}
}

// pollFloor is the shortest interval this will poll at, whatever the vendor asks for.
//
// GitHub states an interval and answers slow_down when it is not respected, and a client that
// believed a zero or a missing value would poll in a tight loop against somebody's account. Five
// seconds is GitHub's own usual answer.
const pollFloor = 5 * time.Second

// exchangeTimeout bounds one round trip in the flow.
//
// Every request here is small. The wait for a person is made of many of these rather than one long
// one, so a request that has not answered in half a minute is a request to give up on rather than
// the user being slow.
const exchangeTimeout = 30 * time.Second

// Vendor is the GitHub half of this route: signing somebody in, finding out who they are, renewing
// and revoking.
//
// Separate from the provider client because the two have nothing in common but a token. This one
// speaks HTTP to github.com; the client speaks JSON-RPC to a process on the machine.
type Vendor struct {
	// HTTP is who makes the requests. Nil means http.DefaultClient.
	HTTP *http.Client
	// Endpoints is where they go. Zero means GitHub.
	Endpoints Endpoints
	// ClientID overrides the build's own. Empty means the build's own.
	ClientID string
	// Scopes overrides the package default. Nil means the package default.
	Scopes []string
	// ClientSecret is present only where a maintainer supplied one. Empty is the ordinary case.
	ClientSecret string
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// ErrNotRegistered means this build has no GitHub app to sign anybody in as.
//
// Its own error because it is not a failure of the user's machine, their account or their network,
// and every one of those would be a wrong thing to tell them. Somebody who meets this has to go and
// register an app, which is a different kind of answer from anything else in this file.
var ErrNotRegistered = errors.New("this build has no GitHub app registered for signing in")

func (v Vendor) clientID() (string, error) {
	if v.ClientID != "" {
		return v.ClientID, nil
	}
	if fromEnv := strings.TrimSpace(os.Getenv(ClientIDEnvVar)); fromEnv != "" {
		return fromEnv, nil
	}
	if clientID != "" {
		return clientID, nil
	}
	return "", fmt.Errorf(
		"signing in to GitHub Copilot needs a GitHub app of Canopy's own, and this build was compiled "+
			"without one. Register an OAuth app with the device flow enabled and set %s to its client "+
			"id, which is not a secret. INSTALL.md says what to tick: %w", ClientIDEnvVar, ErrNotRegistered)
}

func (v Vendor) scopes() []string {
	if len(v.Scopes) > 0 {
		return v.Scopes
	}
	if fromEnv := strings.Fields(os.Getenv(ScopesEnvVar)); len(fromEnv) > 0 {
		return fromEnv
	}
	return Scopes
}

func (v Vendor) clientSecret() string {
	if v.ClientSecret != "" {
		return v.ClientSecret
	}
	return strings.TrimSpace(os.Getenv(ClientSecretEnvVar))
}

func (v Vendor) endpoints() Endpoints {
	if v.Endpoints == (Endpoints{}) {
		return GitHub()
	}
	return v.Endpoints
}

func (v Vendor) http() *http.Client {
	if v.HTTP != nil {
		return v.HTTP
	}
	return http.DefaultClient
}

func (v Vendor) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

// Prompt is what a person has to do for a sign-in to complete.
type Prompt struct {
	// UserCode is typed into the page.
	UserCode string
	// VerificationURI is the page.
	VerificationURI string
	// ExpiresAt is when the code stops being accepted.
	ExpiresAt time.Time
	// Interval is how often GitHub is willing to be asked whether it has happened yet.
	Interval time.Duration
}

// Grant is a finished sign-in.
type Grant struct {
	// Account is the GitHub login the grant belongs to.
	Account string
	// Tokens are the secrets. The refresh token is empty on the recommended registration, where
	// tokens do not expire and there is nothing to renew.
	Tokens keys.Tokens
	// ExpiresAt is nil where GitHub did not say, which is the recommended registration's answer and
	// is what keeps keys.Refresher from renewing on a guess.
	ExpiresAt *time.Time
}

// Attempt is one sign-in in flight.
//
// Begin returns as soon as there is something to put on a screen, and Wait blocks for as long as it
// takes a person to walk to a browser. Splitting them is what lets a screen draw the code while the
// waiting happens somewhere that is not the update loop.
type Attempt struct {
	vendor     Vendor
	deviceCode string
	prompt     Prompt

	// tick is how the loop waits between polls. A field rather than a call to time.After so that a
	// test can hold the interval GitHub was actually obeyed with, instead of inferring it from how
	// long the test took, which is the kind of assertion that passes on one machine and fails on
	// another.
	tick func(time.Duration) <-chan time.Time

	done chan struct{}

	mu      sync.Mutex
	stopped bool
}

// Prompt is what to show. Known before Wait is called, so this does no work.
func (a *Attempt) Prompt() Prompt { return a.prompt }

// Begin asks GitHub for a device code.
func (v Vendor) Begin(ctx context.Context) (*Attempt, error) {
	id, err := v.clientID()
	if err != nil {
		return nil, err
	}

	form := url.Values{
		"client_id": {id},
		"scope":     {strings.Join(v.scopes(), " ")},
	}

	var answer struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
		Description     string `json:"error_description"`
	}
	if err := v.post(ctx, v.endpoints().DeviceCode, form, &answer); err != nil {
		return nil, fmt.Errorf("asking GitHub for a device code: %w", err)
	}
	if answer.Error != "" {
		return nil, fmt.Errorf("GitHub refused to start the sign-in: %s", said(answer.Error, answer.Description))
	}
	if answer.DeviceCode == "" || answer.UserCode == "" {
		return nil, errors.New(
			"GitHub started the sign-in without returning a code, so there is nothing to show anybody")
	}

	interval := time.Duration(answer.Interval) * time.Second
	if interval < pollFloor {
		interval = pollFloor
	}
	expires := v.now().Add(time.Duration(answer.ExpiresIn) * time.Second)
	if answer.ExpiresIn == 0 {
		// GitHub always states this. A default rather than an error, because a sign-in that works and
		// has no deadline printed beside it is better than one refused over a missing field.
		expires = v.now().Add(15 * time.Minute)
	}
	where := answer.VerificationURI
	if where == "" {
		where = "https://github.com/login/device"
	}

	return &Attempt{
		vendor:     v,
		deviceCode: answer.DeviceCode,
		prompt: Prompt{
			UserCode:        answer.UserCode,
			VerificationURI: where,
			ExpiresAt:       expires,
			Interval:        interval,
		},
		tick: time.After,
		done: make(chan struct{}),
	}, nil
}

// Wait polls until the person authorises, refuses, or the code expires.
//
// The context bounds the whole wait, which is how a cancelled sign-in stops polling on behalf of
// somebody who has already left.
func (a *Attempt) Wait(ctx context.Context) (Grant, error) {
	id, err := a.vendor.clientID()
	if err != nil {
		return Grant{}, err
	}

	// The first pass waits for nothing. GitHub's own guidance is to poll at the stated interval, and
	// a person who authorised the code before the client got round to asking should not sit through
	// one anyway.
	first := true
	for {
		wait := a.prompt.Interval
		if first {
			wait, first = 0, false
		}
		select {
		case <-ctx.Done():
			return Grant{}, fmt.Errorf("the sign-in was stopped before GitHub answered: %w", ctx.Err())
		case <-a.done:
			return Grant{}, errors.New("the sign-in was cancelled, so nothing was stored")
		case <-a.tick(wait):
		}

		form := url.Values{
			"client_id":   {id},
			"device_code": {a.deviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		var answer tokenAnswer
		if err := a.vendor.post(ctx, a.vendor.endpoints().Token, form, &answer); err != nil {
			return Grant{}, fmt.Errorf("asking GitHub whether the sign-in has completed: %w", err)
		}

		switch answer.Error {
		case "":
		case "authorization_pending":
			continue
		case "slow_down":
			// GitHub says how much slower, and ignoring it is how a client gets its device code
			// rejected outright a few polls later. Kept on the prompt rather than in a local, so a
			// slower interval survives the next pass round the loop.
			if answer.Interval > 0 {
				a.prompt.Interval = time.Duration(answer.Interval) * time.Second
			} else {
				a.prompt.Interval += pollFloor
			}
			continue
		case "expired_token":
			return Grant{}, errors.New(
				"the code expired before it was entered. Start the sign-in again to get a new one")
		case "access_denied":
			return Grant{}, errors.New(
				"the sign-in was refused on GitHub, so nothing was stored")
		default:
			return Grant{}, fmt.Errorf("GitHub refused the sign-in: %s",
				said(answer.Error, answer.Description))
		}

		if answer.AccessToken == "" {
			return Grant{}, errors.New(
				"GitHub completed the sign-in without returning a token, so there is nothing to store")
		}

		account, err := a.vendor.Login(ctx, core.NewSecret(answer.AccessToken))
		if err != nil {
			// Refused rather than stored anonymously. S-01 requires the account a grant belongs to,
			// because two Copilot seats on one machine are otherwise two rows nobody can tell apart,
			// and a credential that cannot say whose it is cannot be chosen between.
			return Grant{}, fmt.Errorf(
				"GitHub authorised the sign-in but would not say whose account it is, so there is "+
					"nothing to name the credential after: %w", err)
		}

		return Grant{
			Account:   account,
			Tokens:    keys.Tokens{Access: core.NewSecret(answer.AccessToken), Refresh: core.NewSecret(answer.RefreshToken)},
			ExpiresAt: answer.expiry(a.vendor.now()),
		}, nil
	}
}

// Cancel stops a wait that is still going.
//
// It does not revoke, because there is nothing to revoke: a device code that is never exchanged
// expires on GitHub's side within minutes and no token was ever issued. Whoever calls this is
// responsible for the case where the exchange had already succeeded, which is the caller's to undo
// because only the caller knows what it stored.
func (a *Attempt) Cancel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return
	}
	a.stopped = true
	close(a.done)
}

// tokenAnswer is GitHub's reply at the token endpoint, for both grant types.
type tokenAnswer struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int    `json:"expires_in"`
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
	Scope                 string `json:"scope"`
	TokenType             string `json:"token_type"`
	Interval              int    `json:"interval"`
	Error                 string `json:"error"`
	Description           string `json:"error_description"`
}

// expiry reads when the new token stops working, nil where GitHub did not say.
//
// Nil is the answer on Canopy's recommended registration, and carrying it through rather than
// inventing one is what stops keys.Refresher spending a refresh token every turn for the life of a
// credential that never needed one.
func (t tokenAnswer) expiry(now time.Time) *time.Time {
	if t.ExpiresIn <= 0 {
		return nil
	}
	at := now.Add(time.Duration(t.ExpiresIn) * time.Second)
	return &at
}

// Login asks GitHub who a token belongs to.
//
// The documented endpoint, with a documented scope, rather than the internal one editors use to
// fetch a Copilot token. This is the only request Canopy makes to github.com on a user's behalf
// outside the flow itself, and it exists because a credential has to be able to say whose it is.
func (v Vendor) Login(ctx context.Context, token core.Secret) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.endpoints().API+"/user", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token.Reveal())
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)

	resp, err := v.http().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s when asked whose token this is", resp.Status)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", fmt.Errorf("reading GitHub's answer: %w", err)
	}
	if user.Login == "" {
		return "", errors.New("GitHub returned an account with no login")
	}
	return user.Login, nil
}

// userAgent is how Canopy names itself to GitHub.
//
// Honestly, and that is a decision rather than a default. D-51 permits this route partly because
// nothing about it lies about who is calling, and the established projects on this path all identify
// themselves. Sending another editor's version string here would be the one behaviour that turns a
// defensible integration into an indefensible one.
const userAgent = "canopy"

// post sends a form to GitHub and reads a JSON answer.
//
// Accept: application/json is what turns GitHub's oauth endpoints from form-encoded replies into
// JSON ones. Without it the body comes back as a query string and every decode here fails on a
// response that was actually fine.
func (v Vendor) post(ctx context.Context, endpoint string, form url.Values, into any) error {
	ctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := v.http().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("reading GitHub's answer: %w", err)
	}
	return nil
}

// said joins GitHub's error code with its description, when there is one.
func said(code, description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return code
	}
	return code + ": " + description
}

// Name identifies this route in a renewal failure, so the message says which vendor refused.
func (v Vendor) Name() string { return "GitHub" }

// Refresh renews a grant, which on Canopy's recommended registration never happens.
//
// GitHub's refresh grant authenticates the app, not the user, so it needs the client secret a
// public program cannot ship. That is not a gap to work around: the way out is the registration
// itself. An OAuth app, or a GitHub app with expiring user tokens switched off, issues a token with
// no expiry, keys.Refresher never marks it due, and this method is never called. A maintainer who
// turns expiring tokens on has to supply the secret, and if they have not, this says so in the terms
// of the thing they can actually change.
//
// Reported as lapsed rather than transient in that case, deliberately. A missing secret does not
// become present by waiting, and "try again shortly" would be advice to wait for something that
// will never happen. Signing in again genuinely works.
func (v Vendor) Refresh(ctx context.Context, _ keys.SignIn, tokens keys.Tokens) (keys.Renewal, error) {
	if tokens.Refresh.IsZero() {
		return keys.Renewal{}, fmt.Errorf(
			"this GitHub grant came with nothing to renew it with: %w", keys.ErrSignInLapsed)
	}
	id, err := v.clientID()
	if err != nil {
		return keys.Renewal{}, err
	}
	secret := v.clientSecret()
	if secret == "" {
		return keys.Renewal{}, fmt.Errorf(
			"this grant expires and GitHub will only renew it for an app that can prove who it is, "+
				"which needs the client secret in %s. Either set it, or register the app so that its "+
				"user tokens do not expire, which is what Canopy recommends and needs no secret at "+
				"all: %w", ClientSecretEnvVar, keys.ErrSignInLapsed)
	}

	form := url.Values{
		"client_id":     {id},
		"client_secret": {secret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {tokens.Refresh.Reveal()},
	}
	var answer tokenAnswer
	if err := v.post(ctx, v.endpoints().Token, form, &answer); err != nil {
		// Transient. Nothing was learned about the grant, and telling somebody to sign in again on a
		// dropped connection throws away a working refresh token to do it.
		return keys.Renewal{}, err
	}
	switch answer.Error {
	case "":
	case "bad_refresh_token", "expired_token", "unauthorized_client", "access_denied", "incorrect_client_credentials":
		return keys.Renewal{}, fmt.Errorf("GitHub refused the renewal: %s: %w",
			said(answer.Error, answer.Description), keys.ErrSignInLapsed)
	default:
		// An error nobody has classified is read as transient, because that is the mistake with the
		// smaller cost: a needless retry against a needless sign-in.
		return keys.Renewal{}, fmt.Errorf("GitHub refused the renewal: %s",
			said(answer.Error, answer.Description))
	}
	if answer.AccessToken == "" {
		return keys.Renewal{}, errors.New("GitHub renewed the grant without returning a token")
	}

	return keys.Renewal{
		Tokens: keys.Tokens{
			Access:  core.NewSecret(answer.AccessToken),
			Refresh: core.NewSecret(answer.RefreshToken),
		},
		ExpiresAt: answer.expiry(v.now()),
	}, nil
}

// Sources is the keys.SourceFor entry for this route.
//
// It answers only for credentials that say they came this way. A build with several routes composes
// these rather than switching on provider, which is the collision refresh.go warned about: Copilot
// and Codex are both openai-compatible and a provider switch would send one of them to the other's
// token endpoint.
func (v Vendor) Sources() keys.SourceFor {
	return func(_ core.KeyMetadata, in keys.SignIn) (keys.TokenSource, bool) {
		if in.Route != Route {
			return nil, false
		}
		return v, true
	}
}
