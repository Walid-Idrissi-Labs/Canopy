package codex

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Who the app server says is signed in, and what their plan currently allows.
//
// Both come from asking rather than from anything Canopy stored. That distinction is the whole
// value of the answer: a credential added last month against an account that has since been signed
// out of, switched, or run out of quota says so here rather than at the next turn.

// Account is who the Codex on this machine is signed in as. It holds no token and has nowhere to
// put one.
type Account struct {
	// Kind is Codex's own word for how it is authenticated: "chatgpt" for a subscription, "apiKey"
	// for a key somebody exported into the environment.
	Kind string

	// Email identifies the account, so that somebody with two subscriptions can tell them apart.
	// Empty for an account type that does not carry one.
	Email string

	// Plan is what OpenAI calls the subscription: "plus", "pro", "team", and whatever else it
	// reports. Not interpreted beyond whether it is a subscription at all, because the set is
	// theirs and a value this build has never heard of is still the truthful answer.
	Plan string
}

// OnSubscription reports whether turns on this account draw on a ChatGPT plan.
//
// The distinction matters to a reader rather than to the code: a delegated turn on an API-key Codex
// really is billed per token to that account, which is a different arrangement from the one
// somebody signing in with a subscription expects, and they should hear it from Canopy rather than
// from an invoice.
func (a Account) OnSubscription() bool { return a.Kind == accountChatGPT }

// String is what a credential list shows.
func (a Account) String() string {
	switch {
	case a.Email == "" && a.Kind == accountAPIKey:
		return "an OpenAI API key account"
	case a.Email == "":
		return "an unnamed ChatGPT account"
	case a.Plan == "":
		return a.Email
	default:
		return a.Email + " (" + a.Plan + ")"
	}
}

// Limits is what the plan allows right now.
//
// Read from account/rateLimits/read, which is the answer S-07 built `reportsOnCredentials` for: a
// fact about the subscription rather than a fact about the file, and a better thing for
// `canopy keys test` to say than a fingerprint ever was.
type Limits struct {
	// Plan is the plan the limits belong to, which is not always the plan on the account: a
	// workspace member's limits are the workspace's.
	Plan string

	// Primary and Secondary are the two windows OpenAI meters, usually a short one and a long one.
	// Nil where the vendor reported none, which is different from a window with nothing used.
	Primary   *Window
	Secondary *Window

	// Reached is set when a limit has actually been hit, and says which sort. Empty is the ordinary
	// case.
	Reached string

	// Credits says whether the account has pay-as-you-go credit behind the plan limits.
	Credits string
}

// Window is one metered period.
type Window struct {
	UsedPercent int
	Duration    time.Duration
	ResetsAt    time.Time
}

// String renders one window for a person reading a terminal.
func (w Window) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d%% used", w.UsedPercent)
	if w.Duration > 0 {
		fmt.Fprintf(&b, " of a %s window", roughly(w.Duration))
	}
	if !w.ResetsAt.IsZero() {
		fmt.Fprintf(&b, ", resets %s", w.ResetsAt.Local().Format("2006-01-02 15:04"))
	}
	return b.String()
}

// roughly says how long a window is the way somebody would say it.
//
// Go prints 43200 minutes as "720h0m0s", which is arithmetically perfect and is not how anybody
// describes a month. This line is read rather than parsed.
func roughly(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%d day", int(d.Hours()/24)) + plural(int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%d hour", int(d.Hours())) + plural(int(d.Hours()))
	default:
		return fmt.Sprintf("%d minute", int(d.Minutes())) + plural(int(d.Minutes()))
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// Vendor is the Codex on this machine, as something Canopy can ask questions of.
//
// The zero value drives the real one. Everything it does starts an app server, asks, and stops it
// again, rather than holding a connection open: these are occasional questions from a command
// somebody typed, and a process that lives between them is a process that outlives its reason.
type Vendor struct {
	// Discovery finds the binary. The zero value looks on the real machine.
	Discovery Discovery

	// Version is Canopy's own build, sent beside the originator in the handshake.
	Version string

	// launch replaces the child process in tests.
	launch launcher
}

// vendorTimeout bounds one question to the app server.
//
// Generous next to a plain HTTP call, because this one starts a process, reads the user's config
// and may reach OpenAI, and mean next to a turn, because nothing here is a model call.
//
// Applied only where the caller supplied no deadline of its own. A sign-in legitimately takes
// minutes and passes a context that says so, and a bound imposed here regardless would cut it off
// while somebody was still typing a code.
const vendorTimeout = 45 * time.Second

func withVendorTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, vendorTimeout)
}

// open brings up an app server for one exchange.
func (v Vendor) open(ctx context.Context) (*session, error) {
	launch := v.launch
	if launch == nil {
		install, err := v.Discovery.Find()
		if err != nil {
			return nil, err
		}
		launch = spawn(install, "")
	}
	version := v.Version
	if version == "" {
		version = "dev"
	}
	return start(ctx, launch, version)
}

// Account asks who is signed in, and says plainly when nobody is.
//
// Refresh renews the app server's own grant before answering. It is off for a question somebody
// asked and on before a sign-in is recorded, and the difference is not fussiness: OpenAI rotates
// refresh tokens, so a probe that renews spends the token the user's own Codex was going to use
// next. Renewing is worth that when the answer is about to be written down as a credential and is
// not worth it to draw a line in a report.
func (v Vendor) Account(ctx context.Context, refresh bool) (Account, error) {
	ctx, cancel := withVendorTimeout(ctx)
	defer cancel()

	s, err := v.open(ctx)
	if err != nil {
		return Account{}, err
	}
	defer func() { _ = s.Close() }()

	return v.account(s, refresh)
}

func (v Vendor) account(s *session, refresh bool) (Account, error) {
	var result accountReadResult
	if err := s.call(methodAccountRead, accountReadParams{RefreshToken: refresh}, &result); err != nil {
		return Account{}, fmt.Errorf("asking the Codex app server which account it is signed in as: %w", err)
	}
	if result.Account == nil {
		return Account{}, fmt.Errorf(
			"%w. Sign in with `canopy keys signin <name> -route %s`, which hands you to OpenAI's own "+
				"flow and never asks Canopy to hold anything", ErrNotSignedIn, Route)
	}
	return Account{
		Kind:  result.Account.Type,
		Email: result.Account.Email,
		Plan:  result.Account.PlanType,
	}, nil
}

// Limits asks what the plan currently allows, alongside who it belongs to.
//
// Both in one exchange because they are one question as far as the person asking is concerned, and
// because starting two app servers to answer it would double the wait for no benefit.
func (v Vendor) Limits(ctx context.Context) (Account, Limits, error) {
	ctx, cancel := withVendorTimeout(ctx)
	defer cancel()

	s, err := v.open(ctx)
	if err != nil {
		return Account{}, Limits{}, err
	}
	defer func() { _ = s.Close() }()

	account, err := v.account(s, false)
	if err != nil {
		return Account{}, Limits{}, err
	}

	var result rateLimitsResult
	if err := s.call(methodAccountRateLimits, struct{}{}, &result); err != nil {
		// The account is a real answer on its own, so this is reported and not fatal: a build that
		// could say who somebody is and refused to because it could not also say their quota would
		// be throwing away the more useful half.
		return account, Limits{}, fmt.Errorf("asking OpenAI for this plan's limits: %w", err)
	}
	return account, readLimits(result.RateLimits), nil
}

// readLimits turns the wire snapshot into the two windows and the words around them.
func readLimits(snapshot rateLimitSnapshot) Limits {
	limits := Limits{
		Plan:      snapshot.PlanType,
		Primary:   readWindow(snapshot.Primary),
		Secondary: readWindow(snapshot.Secondary),
		Reached:   snapshot.RateLimitReachedType,
	}
	if credits := snapshot.Credits; credits != nil {
		switch {
		case credits.Unlimited:
			limits.Credits = "unlimited"
		case credits.Balance != "":
			limits.Credits = credits.Balance
		case credits.HasCredits:
			limits.Credits = "some"
		default:
			limits.Credits = "none"
		}
	}
	return limits
}

func readWindow(w *rateLimitWindow) *Window {
	if w == nil {
		return nil
	}
	window := &Window{
		UsedPercent: w.UsedPercent,
		Duration:    time.Duration(w.WindowDurationMins) * time.Minute,
	}
	if w.ResetsAt > 0 {
		window.ResetsAt = time.Unix(w.ResetsAt, 0)
	}
	return window
}

// SignOut tells the app server to forget the grant it holds.
//
// It is the app server's login, not Canopy's, so this really does sign the user's own Codex out.
// That is the honest meaning of signing out of a delegated credential and it is said out loud
// wherever it is offered, because somebody who expected only Canopy to forget something would
// otherwise find their next `codex` asking them to log in.
func (v Vendor) SignOut(ctx context.Context) error {
	ctx, cancel := withVendorTimeout(ctx)
	defer cancel()

	s, err := v.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()

	return s.call(methodLogout, struct{}{}, nil)
}
