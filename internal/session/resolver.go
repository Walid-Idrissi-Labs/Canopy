package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/openai"
)

// KeyResolver builds provider clients from the stored credentials.
//
// The credential decides which API is spoken and what the turn costs, because a named key carries
// its provider and its endpoint. Nothing above this has to be told which vendor it is talking to,
// which is the point of naming keys in the first place.
type KeyResolver struct {
	store *keys.Store
}

var _ Resolver = (*KeyResolver)(nil)

// NewKeyResolver builds a resolver over a key store.
func NewKeyResolver(store *keys.Store) *KeyResolver { return &KeyResolver{store: store} }

// Resolve returns the client for a credential name.
//
// The secret is fetched at the moment of use rather than held, so a key removed while Canopy is
// running stops working on the next turn rather than the next restart.
func (r *KeyResolver) Resolve(
	name, model string,
) (core.ProviderClient, pricing.ModelID, error) {
	meta, err := r.pick(name)
	if err != nil {
		return nil, pricing.ModelID{}, err
	}

	secret, err := r.store.Get(meta.Ref)
	if err != nil {
		return nil, pricing.ModelID{}, err
	}

	id := pricing.NewModelID(meta.Ref.Provider, meta.BaseURL, model)

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
			"no credentials stored. Add one with `canopy keys add claude`, or press k")
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

// MarkUsed records that a credential answered, so the key list can show when each was last used.
func (r *KeyResolver) MarkUsed(name string) {
	if name == "" {
		return
	}
	// A failure here is not worth surfacing: the answer is what the user asked for, and losing the
	// last-used timestamp costs them nothing they would notice.
	_ = r.store.MarkUsed(core.KeyRef{Name: name})
}

// DefaultKeyName returns the credential a new session should use when the user has not chosen one.
//
// Empty when there is no obvious answer, which the engine turns into an error naming the choice
// rather than making it.
func (r *KeyResolver) DefaultKeyName() string {
	all, err := r.store.List()
	if err != nil || len(all) != 1 {
		return ""
	}
	return all[0].Ref.Name
}
