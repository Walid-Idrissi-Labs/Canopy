package session

import (
	"context"
	"fmt"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/acp"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/codex"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/copilot"
)

// Vendors builds every client that keeps something running, for every surface this build has.
//
// It exists because the same two things kept drifting between the interface and `canopy ask`. Both
// of them decide which vendor a credential reaches, and each used to build the client itself: one
// passed Canopy's version to the vendors that ask for it and the other did not, so a turn started
// from the interface told GitHub and OpenAI it was Canopy "dev" whatever the binary said. And one
// closed the Copilot sessions it opened while the other dropped them, so `canopy ask` left a CLI
// process resident and a session the vendor believed was open.
//
// Neither is a thing a caller can now get wrong, because neither is a thing a caller does. What Canopy
// calls itself is given to this once, and what has to be closed is held here rather than by whoever
// happened to ask for it. This is the same answer S-02 gave for "how old is too old", which is one
// keys.Refresher shared by both surfaces, applied to the two questions that turned out to have the
// same shape.
//
// The pasted-key providers are deliberately not here. They hold no process, no session and no
// identity, so building one in two places costs nothing, and the test that the two surfaces know the
// same set of providers already covers the case that would.
type Vendors struct {
	// version is what Canopy calls itself to a vendor that asks. Empty means the provider's own
	// default, which is "dev", and a build that says so is a build nobody should file a version
	// number against.
	version string

	// copilots is every Copilot conversation open in this program. See copilot.Clients: it is the
	// only way to make one, so nothing can build a session that this cannot close.
	copilots *copilot.Clients
}

// NewVendors builds the client factory for one program.
//
// The version is a parameter rather than a setting because it is the thing that was forgotten. A
// setter would be one more call to leave out, and a default would be the "dev" that shipped.
func NewVendors(version string) *Vendors {
	return &Vendors{version: version, copilots: copilot.NewClients()}
}

// Close ends every conversation these vendors are holding open.
//
// Called by KeyResolver.Close, which Engine.Close calls after the turns have settled. Each of these
// is a child process on the machine and a session a vendor believes is open, and a program that
// exits without saying so leaves one of each behind, one of them on somebody else's server.
//
// Failures are dropped rather than returned. There is nothing a caller could do with them at the
// moment the program is ending, and refusing to close the rest because one would not close is the
// worst available answer.
func (v *Vendors) Close() {
	_ = v.copilots.Close()
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

// Delegated builds the client for a credential that drives somebody else's agent.
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
func (v *Vendors) Delegated(
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
		return codex.New(found, codex.WithVersion(v.version)), id, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), delegateTimeout)
	defer cancel()

	found, err := delegatedAgent.Find(ctx)
	if err != nil {
		return nil, pricing.ModelID{}, fmt.Errorf("key %q delegates to Claude Code: %w",
			meta.Ref.Name, err)
	}
	return acp.New(found, acp.WithVersion(v.version)), id, nil
}

// Copilot builds, or finds again, the client that holds a conversation's Copilot session.
//
// This is the one place in the tree where resolving twice has to answer twice with the same object,
// and it is worth being explicit about why rather than leaving it to be inferred. GitHub's SDK is
// session-shaped: a session is the conversation, it accumulates the history, and there is no call
// that seeds one. Canopy's contract is request-shaped and hands over the whole message list every
// turn. A resolver that built a fresh client per turn would therefore open a fresh session per turn,
// and every message after the first would be answered by an agent with amnesia while the transcript
// on screen said otherwise. Holding the client is what makes the two shapes meet.
//
// The consequence is written down in LIMITATIONS.md rather than hidden here: on this route the
// history lives in GitHub's runtime, so Canopy's history editing, re-rolls and compaction do not
// reach it.
//
// A conversation with no name gets a client that ends with its turn. That is an aside, a compaction
// or `canopy ask`, all of which want a fresh agent and none of which has a second turn coming.
// Caching them under one key would have every aside in the program share a session; handing them one
// that nothing closes is what left a process per side question.
func (v *Vendors) Copilot(
	conversation string, meta core.KeyMetadata, in keys.SignIn, secret core.Secret, model string,
) (core.ProviderClient, pricing.ModelID, error) {
	if model == "" {
		model = meta.Model
	}

	// Delegated for the reason S-04's route is: the tokens are real and are counted, and their list
	// price is a number about an invoice nobody receives, because a Copilot seat is charged monthly
	// and this usage is metered against it. See pricing.ModelID.Delegated.
	id := pricing.ModelID{Provider: meta.Ref.Provider, Model: model, Delegated: true}

	conv := copilot.Conversation{Token: secret, Model: model}

	if conversation == "" {
		client, err := v.copilots.Once(meta.Ref.Name, conv)
		if err != nil {
			return nil, pricing.ModelID{}, err
		}
		return client, id, nil
	}

	// The account is in the key, not only the credential name, so that a credential which was signed
	// in again as somebody else does not inherit the previous person's session.
	client, err := v.copilots.For(
		conversation+"\x00"+meta.Ref.Name+"\x00"+in.Account, meta.Ref.Name, conv)
	if err != nil {
		return nil, pricing.ModelID{}, err
	}
	return client, id, nil
}
