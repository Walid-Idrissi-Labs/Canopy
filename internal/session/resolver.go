package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/acp"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/codex"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/copilot"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/openai"
)

// KeyResolver builds provider clients from the stored credentials.
//
// The credential decides which API is spoken and what the turn costs, because a named key carries
// its provider and its endpoint. Nothing above this has to be told which vendor it is talking to,
// which is the point of naming keys in the first place.
type KeyResolver struct {
	store       *keys.Store
	credentials *keys.Refresher

	// sessions holds the clients that are conversations rather than callers.
	//
	// Every provider before phase S was stateless, and a map like this would have been an oddity: a
	// second turn on one credential is served perfectly well by a second client. GitHub's Copilot
	// agent is not like that. It owns the conversation, it has no API for being handed a history,
	// and so a client that was rebuilt every turn would open a session that had never heard of the
	// previous message. Asking twice has to give back the same one.
	//
	// Keyed on the conversation and the credential together. Changing which subscription a
	// conversation runs on starts a new conversation with the vendor, which is the honest outcome:
	// the alternative is a turn billed to one account arriving in a session opened by another.
	mu       sync.Mutex
	sessions map[string]conversationClient
	closed   bool
}

// conversationClient is a provider client that holds a conversation and has to be shut down.
type conversationClient interface {
	core.ProviderClient
	Close() error
}

var (
	_ Resolver             = (*KeyResolver)(nil)
	_ conversationResolver = (*KeyResolver)(nil)
	_ resolverCloser       = (*KeyResolver)(nil)
)

// NewKeyResolver builds a resolver over a key store.
func NewKeyResolver(store *keys.Store) *KeyResolver {
	return &KeyResolver{
		store:       store,
		credentials: keys.NewRefresher(store),
		sessions:    map[string]conversationClient{},
	}
}

// Renews says where a signed-in credential buys a new token when its own is nearly out.
//
// Called at wiring time, by the composition root that knows which routes this build has.
func (r *KeyResolver) Renews(sources keys.SourceFor) { r.credentials.Renews(sources) }

// Resolve returns the client for a credential name, for a caller with no conversation of its own.
//
// An aside and a compaction both come through here, and on a route that holds its own history that
// is the right answer rather than a limitation: an aside is a separate conversation by definition,
// and a compaction is one question asked once about a transcript. Giving either the conversation's
// own session would put a summarisation request into the middle of somebody's session.
func (r *KeyResolver) Resolve(name, model string) (core.ProviderClient, pricing.ModelID, error) {
	return r.ResolveFor("", name, model)
}

// ResolveFor returns the client a named conversation's next turn runs on.
//
// The conversation is empty for anything that is not one, and a route that does not care ignores it,
// which is every route but Copilot's. See conversationResolver in engine.go for why it exists.
func (r *KeyResolver) ResolveFor(
	conversation, name, model string,
) (core.ProviderClient, pricing.ModelID, error) {
	// The secret is fetched at the moment of use rather than held, so a key removed while Canopy is
	// running stops working on the next turn rather than the next restart.
	//
	// A signed-in credential is renewed here too, before the request exists rather than after one
	// comes back rejected. That order is what keeps a 401 meaning what core says it means: an expired
	// token and a wrong one arrive as the same status, and the only way to tell them apart without
	// teaching a frozen package a new distinction is to make sure the token was valid when it went
	// out.
	meta, err := r.pick(name)
	if err != nil {
		return nil, pricing.ModelID{}, err
	}

	// Which kind of credential this is comes before which provider it speaks, and it has to. A
	// delegated credential is an Anthropic credential by provider and is not a credential at all by
	// substance: there is no secret behind it, so asking the refresher for one below would refuse it
	// in internal/keys' own words and the route would never be reached.
	in, err := r.store.SignIn(meta.Ref)
	if err != nil {
		return nil, pricing.ModelID{}, err
	}
	if in.Kind == keys.KindDelegated {
		return r.delegate(meta, in, model)
	}

	secret, err := r.credentials.Credential(meta)
	if err != nil {
		return nil, pricing.ModelID{}, err
	}

	// Before the provider switch, and it has to be, for the reason internal/keys wrote SourceFor as
	// a function rather than a map: a Copilot credential and an OpenAI one are both
	// openai-compatible, so a switch on provider alone would send a Copilot turn to a chat
	// completions client pointed at a host that does not serve one.
	if in.Route == copilot.Route {
		return r.copilot(conversation, meta, in, secret, model)
	}

	id := pricing.NewModelID(meta.Ref.Provider, meta.BaseURL, model).WithUserRate(meta.Rate)

	switch meta.Ref.Provider {
	case core.ProviderAnthropic:
		return anthropic.New(secret), id, nil

	case core.ProviderOpenAICompatible:
		// No default model, deliberately. This provider is whatever endpoint it was pointed at, and
		// guessing a model name for someone else's gateway fails with a confusing 404 rather than a
		// clear message.
		if model == "" {
			return nil, pricing.ModelID{}, fmt.Errorf(
				"key %q needs a model named, since its endpoint has no default", meta.Ref.Name)
		}
		return openai.New(meta.BaseURL, secret, openai.WithName(meta.Ref.Name)), id, nil

	default:
		return nil, pricing.ModelID{}, fmt.Errorf(
			"key %q has provider %q, which this build does not know how to reach",
			meta.Ref.Name, meta.Ref.Provider)
	}
}

// delegate builds the client for a credential that drives somebody else's agent.
//
// Discovery happens here, on every turn, rather than being cached on the credential. What was on the
// machine when the credential was added is not what is on it now: the vendor's agent can be
// uninstalled, updated, or signed out of between two messages, and each of those has its own remedy
// that only a fresh look can name. The cost is one short subprocess per turn against a turn that is
// about to start a subprocess anyway.
//
// The route decides which agent, and it has to be the route rather than the provider. Both delegated
// routes reach a different vendor and one of them is openai-compatible, so a switch on provider
// would send a ChatGPT credential to Claude Code on the day somebody stored one under the other
// provider. That is the collision internal/keys/refresh.go wrote SourceFor as a function to avoid,
// and this is the same key.
//
// The model identity is marked delegated, which is what stops a dollar figure appearing beside a turn
// nobody is billed per token for. See pricing.ModelID.Delegated.
func (r *KeyResolver) delegate(
	meta core.KeyMetadata, in keys.SignIn, model string,
) (core.ProviderClient, pricing.ModelID, error) {
	// The provider is carried through so a failure can still say which credential this was, and the
	// model is carried through so the client can ask for it if the delegated agent offers a choice.
	// Neither of them prices anything, because Delegated is checked first.
	id := pricing.ModelID{Provider: meta.Ref.Provider, Model: model, Delegated: true}

	if in.Route == codex.Route {
		found, err := delegatedCodex.Find()
		if err != nil {
			return nil, pricing.ModelID{}, fmt.Errorf("key %q delegates to Codex: %w",
				meta.Ref.Name, err)
		}
		return codex.New(found), id, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), delegateTimeout)
	defer cancel()

	found, err := delegatedAgent.Find(ctx)
	if err != nil {
		return nil, pricing.ModelID{}, fmt.Errorf("key %q delegates to Claude Code: %w",
			meta.Ref.Name, err)
	}
	return acp.New(found), id, nil
}

// copilot builds, or finds again, the client that holds this conversation's Copilot session.
//
// This is the one place in the tree where resolving twice has to answer twice with the same object,
// and it is worth being explicit about why rather than leaving it to be inferred from the map.
// GitHub's SDK is session-shaped: a session is the conversation, it accumulates the history, and
// there is no call that seeds one. Canopy's contract is request-shaped and hands over the whole
// message list every turn. A resolver that built a fresh client per turn would therefore open a
// fresh session per turn, and every message after the first would be answered by an agent with
// amnesia while the transcript on screen said otherwise. Holding the client is what makes the two
// shapes meet.
//
// The consequence is written down in LIMITATIONS.md rather than hidden here: on this route the
// history lives in GitHub's runtime, so Canopy's history editing, re-rolls and compaction do not
// reach it.
//
// A conversation with no name gets a client of its own that nothing keeps. That is an aside or a
// compaction, both of which want a fresh agent and neither of which wants to be remembered, and
// caching them under one key would have every aside in the program share a session.
func (r *KeyResolver) copilot(
	conversation string, meta core.KeyMetadata, in keys.SignIn, secret core.Secret, model string,
) (core.ProviderClient, pricing.ModelID, error) {
	if model == "" {
		model = meta.Model
	}

	// Delegated for the reason S-04's route is: the tokens are real and are counted, and their list
	// price is a number about an invoice nobody receives, because a Copilot seat is charged monthly
	// and this usage is metered against it. See pricing.ModelID.Delegated.
	id := pricing.ModelID{Provider: meta.Ref.Provider, Model: model, Delegated: true}

	build := func() conversationClient {
		return copilot.New(meta.Ref.Name, copilot.Conversation{
			Token: secret,
			Model: model,
		})
	}

	if conversation == "" {
		return build(), id, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil, pricing.ModelID{}, errors.New(
			"this Canopy is shutting down, so no new conversation is being started")
	}

	// The account is in the key, not only the credential name, so that a credential which was signed
	// in again as somebody else does not inherit the previous person's session.
	at := conversation + "\x00" + meta.Ref.Name + "\x00" + in.Account
	if existing, ok := r.sessions[at]; ok {
		return existing, id, nil
	}
	client := build()
	r.sessions[at] = client
	return client, id, nil
}

// Close ends every conversation this resolver is holding open.
//
// Called by Engine.Close after the turns have settled. Each of these is a child process on the
// machine and a session GitHub believes is open, and a program that exits without saying so leaves
// one of each behind, one of them on somebody else's server.
//
// Failures are counted rather than returned. There is nothing a caller could do with them at the
// moment the program is ending, and refusing to close the rest because one would not close is the
// worst available answer.
func (r *KeyResolver) Close() {
	r.mu.Lock()
	holding := r.sessions
	r.sessions = map[string]conversationClient{}
	r.closed = true
	r.mu.Unlock()

	for _, client := range holding {
		_ = client.Close()
	}
}

// delegateTimeout bounds looking for the delegated agent before a turn.
const delegateTimeout = 30 * time.Second

// delegatedAgent and delegatedCodex are how the machine is inspected for a delegated route.
//
// Package variables for the reason cmd/canopy's openKeyStore is one: they are what a test swaps to
// drive a route without the vendor's agent installed. Nothing else about them is dynamic. Two of
// them rather than one interface, because the two discoveries answer different questions and share
// no field: one looks for a CLI, an account and a bridge, the other for one binary.
var (
	delegatedAgent = acp.Discovery{}
	delegatedCodex = codex.Discovery{}
)

// pick chooses a credential.
//
// With one stored the choice is obvious. With several it refuses and lists them rather than
// picking: silently choosing which key gets billed is not a decision to make on someone's behalf.
func (r *KeyResolver) pick(name string) (core.KeyMetadata, error) {
	if name != "" {
		return r.store.Metadata(core.KeyRef{Name: name})
	}

	all, err := r.store.List()
	if err != nil {
		return core.KeyMetadata{}, err
	}

	switch len(all) {
	case 0:
		return core.KeyMetadata{}, errors.New(
			"no credentials stored. Add one with `canopy keys add claude`, or press ctrl+k")
	case 1:
		return all[0], nil
	default:
		names := make([]string, len(all))
		for i, meta := range all {
			names[i] = meta.Ref.Name
		}
		return core.KeyMetadata{}, fmt.Errorf(
			"several credentials could be used (%s), so pick one. Choosing which key gets billed "+
				"is not a decision to make silently", strings.Join(names, ", "))
	}
}

// MarkUsed records that a credential answered.
//
// Two things read it: the key list, which shows when each credential was last used, and
// DefaultKeyName, which is why a second launch opens on a conversation rather than on the credential
// list.
func (r *KeyResolver) MarkUsed(name string) {
	if name == "" {
		// An unnamed credential is the one pick resolved for the turn, and it only resolves when
		// there is exactly one. Asked again here rather than assumed, so the record names the
		// credential that actually answered instead of nothing at all.
		meta, err := r.pick("")
		if err != nil {
			return
		}
		name = meta.Ref.Name
	}
	// A failure here is not worth surfacing: the answer is what the user asked for, and losing the
	// last-used timestamp costs them nothing they would notice.
	_ = r.store.MarkUsed(core.KeyRef{Name: name})
}

// DefaultKeyName returns the credential a new session should use when the user has not chosen one.
//
// With one stored the choice is obvious. With several the one last used is the answer, because the
// credential somebody has been running on is a choice they already made, and asking them to make it
// again on every launch is not caution, it is amnesia. This used to return empty for any count other
// than one, which meant that adding a second credential turned every subsequent `canopy` into the
// credential list rather than a conversation, with no way to say "this one, from now on".
//
// It is not a silent choice. The screen a conversation opens on says "using <name>" along its
// bottom left, and ctrl+k changes it, so the credential about to be billed is on screen before the
// first message is typed.
//
// Empty is still a legitimate answer, and it means one of two things: nothing is stored, or several
// are and none of them has ever answered. The second is the only case where there is genuinely
// nothing to go on, and it is the one the credential screen exists for.
func (r *KeyResolver) DefaultKeyName() string {
	all, err := r.store.List()
	if err != nil || len(all) == 0 {
		return ""
	}
	if len(all) == 1 {
		return all[0].Ref.Name
	}

	name := ""
	var used time.Time
	for _, meta := range all {
		if meta.LastUsedAt == nil {
			continue
		}
		if name == "" || meta.LastUsedAt.After(used) {
			name, used = meta.Ref.Name, *meta.LastUsedAt
		}
	}
	// List is ordered by name, and the comparison above is strictly later, so two credentials sharing
	// a timestamp resolve the same way on every run. A default that changed between launches would be
	// worse than having none.
	return name
}
