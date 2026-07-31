package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
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

	// vendors builds the clients that keep something running, and holds them until this closes.
	//
	// Not a map of sessions here, which is what this used to be. Every provider before phase S was
	// stateless, so a client that outlived a turn was a new idea, and the first version of it kept
	// the clients the interface asked for and none of the ones anything else asked for. Vendors is
	// where that lives now, because the two surfaces that build clients both have to reach it and
	// only one of them has a resolver.
	vendors *Vendors
}

var (
	_ Resolver             = (*KeyResolver)(nil)
	_ conversationResolver = (*KeyResolver)(nil)
	_ resolverCloser       = (*KeyResolver)(nil)
)

// NewKeyResolver builds a resolver over a key store.
//
// The version is what Canopy calls itself to the vendors that ask, and it is a parameter rather than
// a setting for the reason NewVendors gives: it is the thing that was forgotten on this path, and a
// build that reports a version it was not built with is worse than one that reports none.
func NewKeyResolver(store *keys.Store, version string) *KeyResolver {
	return &KeyResolver{
		store:       store,
		credentials: keys.NewRefresher(store),
		vendors:     NewVendors(version),
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
		return r.vendors.Delegated(meta, in, model)
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
		return r.vendors.Copilot(conversation, meta, in, secret, model)
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

// Close ends every conversation this resolver is holding open.
//
// Called by Engine.Close after the turns have settled, through the resolverCloser assertion. What is
// actually held is Vendors, which is also what `canopy ask` holds, so both surfaces end the same
// things the same way rather than one of them ending nothing.
func (r *KeyResolver) Close() { r.vendors.Close() }

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
