package core

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Provider identifies a model API vendor.
type Provider string

const (
	// ProviderAnthropic is the Anthropic Messages API.
	ProviderAnthropic Provider = "anthropic"
	// ProviderOpenAICompatible is the OpenAI chat completions API, reachable at any base URL.
	// This covers OpenAI itself plus Kimi, MiniMax, DeepSeek, Groq, OpenRouter and local runtimes
	// such as Ollama and LM Studio.
	ProviderOpenAICompatible Provider = "openai-compatible"
)

// AllProviders returns every supported provider.
func AllProviders() []Provider {
	return []Provider{ProviderAnthropic, ProviderOpenAICompatible}
}

// Valid reports whether p is a supported provider.
func (p Provider) Valid() bool {
	for _, known := range AllProviders() {
		if p == known {
			return true
		}
	}
	return false
}

// RequiresBaseURL reports whether this provider needs an endpoint to be configured.
func (p Provider) RequiresBaseURL() bool {
	return p == ProviderOpenAICompatible
}

func (p Provider) String() string { return string(p) }

// keyNamePattern constrains what a credential may be called.
//
// The constraint is a safety feature, not tidiness. A name is displayed, logged, put in events and
// written into transcripts, so if a user could name a key after its own value, the value would
// travel everywhere the name does. Real credentials are long and contain characters this rejects,
// so a pasted key fails to validate rather than becoming a permanent leak.
var keyNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,30}$`)

// credentialPrefixes are how the common providers' keys start.
//
// Used only to give a better error, never to decide whether something is secret. A value that
// looks like a credential is rejected as a name either way, by length or by character set. This
// just replaces "too long" with an explanation of what the user probably did.
var credentialPrefixes = []string{"sk-", "sk_", "ghp_", "gho_", "github_pat_", "gsk_", "xai-", "AIza", "hf_", "r8_"}

// LooksLikeCredential reports whether a string resembles an API key.
//
// Deliberately a hint rather than a security control. Treating it as a control would mean a
// credential that does not match becomes acceptable input somewhere, which is the wrong shape of
// defence. The real protection is that names are constrained and secrets are never arguments.
func LooksLikeCredential(s string) bool {
	for _, prefix := range credentialPrefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return len(s) >= 40
}

// ValidateKeyName reports whether a string is acceptable as a credential name.
func ValidateKeyName(name string) error {
	if LooksLikeCredential(name) {
		return fmt.Errorf(
			"%q looks like a credential rather than a name. The value is not passed on the command "+
				"line, since arguments reach shell history and the process list. Give the key a short "+
				"name such as \"claude\", and paste the value when prompted",
			truncateForError(name))
	}
	switch {
	case name == "":
		return fmt.Errorf("a key name is required")
	case len(name) > 31:
		return fmt.Errorf("key name %q is too long, use 31 characters or fewer", truncateForError(name))
	case !keyNamePattern.MatchString(name):
		return fmt.Errorf(
			"key name %q is not allowed, use lowercase letters, digits, dashes and underscores, "+
				"starting with a letter or digit (for example \"claude\" or \"kimi-work\")",
			truncateForError(name))
	}
	return nil
}

// truncateForError shortens a rejected value before it goes into an error message.
//
// If somebody pastes a credential where a name belongs, the rejection must not print the thing it
// just rejected. Errors get logged.
func truncateForError(s string) string {
	const limit = 8
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// KeyRef names a stored credential.
//
// It carries no secret and has nowhere to put one. This is the type that travels: it goes into
// profiles, agents, events, snapshots and transcripts, and all of that is safe precisely because
// the value it refers to is somewhere else. Resolving a ref to an actual credential happens in the
// key store, at the moment of use.
type KeyRef struct {
	// Name is how a user refers to this credential. "claude", "kimi", "minimax".
	Name string
	// Provider is which API the credential is for.
	Provider Provider
}

// Validate checks that a reference is usable.
func (k KeyRef) Validate() error {
	if err := ValidateKeyName(k.Name); err != nil {
		return err
	}
	if !k.Provider.Valid() {
		return fmt.Errorf("key %q has unknown provider %q", k.Name, k.Provider)
	}
	return nil
}

// IsZero reports whether this reference points at nothing.
func (k KeyRef) IsZero() bool { return k.Name == "" }

func (k KeyRef) String() string {
	if k.IsZero() {
		return "(no key)"
	}
	return fmt.Sprintf("%s (%s)", k.Name, k.Provider)
}

// KeyRate is what the user says this credential charges, per million tokens.
//
// Exists because Canopy will never hold rates for every gateway in the OpenAI compatible family and
// should not pretend to: the gateway sets the price and there are many gateways. The person who
// signed up for one knows what they pay, so this turns "we cannot price this" into "tell us once".
//
// Kept separate from the built in table rather than merged into it, and labelled as the user's
// figure wherever it is shown. The point of the dated table is that Canopy is honest about where a
// number came from, and quietly absorbing somebody's guess into it would throw that away.
type KeyRate struct {
	// InputPerMTok and OutputPerMTok are dollars per million tokens.
	InputPerMTok  float64
	OutputPerMTok float64

	// CacheReadPerMTok is the rate for tokens served from a cache. Zero means "same as input",
	// which is the honest default: most gateways in this family either do not cache or do not say
	// what they charge for it, and assuming a discount nobody promised would understate the bill.
	CacheReadPerMTok float64
}

// IsZero reports whether no rate has been set.
func (r KeyRate) IsZero() bool { return r == KeyRate{} }

// Validate checks that a rate is usable.
func (r KeyRate) Validate() error {
	switch {
	case r.InputPerMTok < 0 || r.OutputPerMTok < 0 || r.CacheReadPerMTok < 0:
		return fmt.Errorf("a rate cannot be negative")
	case r.InputPerMTok == 0 && r.OutputPerMTok == 0:
		// A rate of zero on both would report every turn as free, which is a claim, not an absence.
		// Somebody who genuinely runs something free should leave the rate unset and let it read as
		// unpriced, or use a local endpoint, which Canopy already knows is free.
		return fmt.Errorf("a rate of zero would report every turn as free. Leave it unset instead, " +
			"which reads as unpriced rather than as costing nothing")
	}
	return nil
}

// KeyMetadata is everything about a stored credential that is safe to display.
//
// Note what is absent. There is no field for the value, and adding one would be a contract change
// that a reviewer would see, which is the point.
type KeyMetadata struct {
	Ref KeyRef

	// BaseURL is the endpoint, for providers that need one.
	BaseURL string

	// Model is the model this credential talks to by default.
	//
	// Stored on the credential rather than chosen per conversation, because for every provider that
	// is not Anthropic there is no default anybody could guess. A credential pointed at somebody
	// else's gateway with no model recorded produces a request with an empty model field, which
	// fails at the far end with a message about the request rather than about the missing setting.
	//
	// Empty is allowed and means "use the provider's default", which only Anthropic has.
	Model string

	// Rate is what the user told us this credential charges. Zero means they have not said, which
	// is different from free.
	Rate KeyRate

	// Fingerprint is a short non reversible identifier, so two credentials can be told apart in a
	// list without either being shown.
	Fingerprint string

	CreatedAt time.Time

	// LastUsedAt is nil if the credential has never been used.
	LastUsedAt *time.Time
}

// Validate checks that stored metadata is coherent.
func (m KeyMetadata) Validate() error {
	if err := m.Ref.Validate(); err != nil {
		return err
	}
	if m.Ref.Provider.RequiresBaseURL() && m.BaseURL == "" {
		return fmt.Errorf("key %q uses provider %q, which needs a base URL", m.Ref.Name, m.Ref.Provider)
	}
	return nil
}
