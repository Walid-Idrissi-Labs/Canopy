package core

import "fmt"

// TrustLevel is how much an agent may do without asking.
//
// Trust belongs to the profile rather than to Canopy as a whole, because the entire point of this
// product is running agents with different levels of risk at the same time. A scratch agent in a
// throwaway worktree and an agent working next to code you care about should not share a posture.
// One global setting would force the strictest agent's friction onto every agent, and people
// respond to that by loosening everything, which is how a permission model stops meaning anything.
type TrustLevel string

const (
	// TrustReadOnly may read files and run read-only inspections. It cannot write, run shell
	// commands, or change git state. Useful for exploration and for the cheap half of a handoff.
	TrustReadOnly TrustLevel = "read-only"

	// TrustConfined may read and write inside its own worktree. Shell and anything destructive
	// still ask.
	TrustConfined TrustLevel = "confined"

	// TrustStandard may read, write and run shell commands inside its own worktree. Destructive
	// git operations and anything outside the worktree still ask. This is the sensible default.
	TrustStandard TrustLevel = "standard"

	// TrustBroad may also perform destructive git operations on its own branch without asking.
	// It still cannot touch another agent's worktree or the primary checkout, because those are
	// refused rather than merely gated.
	TrustBroad TrustLevel = "broad"
)

// AllTrustLevels returns every level, least trusted first.
func AllTrustLevels() []TrustLevel {
	return []TrustLevel{TrustReadOnly, TrustConfined, TrustStandard, TrustBroad}
}

// Valid reports whether t is a known trust level.
func (t TrustLevel) Valid() bool {
	for _, known := range AllTrustLevels() {
		if t == known {
			return true
		}
	}
	return false
}

// rank orders trust levels for comparison. Unknown levels rank below everything, so a typo is
// treated as the least trusted rather than the most.
func (t TrustLevel) rank() int {
	for i, known := range AllTrustLevels() {
		if t == known {
			return i
		}
	}
	return -1
}

// AtLeast reports whether this level is at least as permissive as other.
//
// Used by the permission model so comparisons happen in one place. An unknown level is never at
// least anything, which means a corrupted or misspelled value fails closed.
func (t TrustLevel) AtLeast(other TrustLevel) bool {
	if !t.Valid() || !other.Valid() {
		return false
	}
	return t.rank() >= other.rank()
}

// AllowsWrites reports whether agents at this level may modify files in their own worktree.
func (t TrustLevel) AllowsWrites() bool { return t.AtLeast(TrustConfined) }

// AllowsShell reports whether agents at this level may run shell commands without asking.
func (t TrustLevel) AllowsShell() bool { return t.AtLeast(TrustStandard) }

// AllowsDestructiveGit reports whether agents at this level may run destructive git operations on
// their own branch without asking.
//
// No level permits touching another agent's worktree or the primary checkout. Those are refused,
// which is a different thing from gated, and the distinction is deliberate: some actions should
// not have an approval path at all.
func (t TrustLevel) AllowsDestructiveGit() bool { return t.AtLeast(TrustBroad) }

func (t TrustLevel) String() string { return string(t) }

// AgentProfile is a named recipe for starting an agent.
//
// This is what makes "use 2 claude agents for this" mean something. A profile name resolves to a
// credential, a model, a system prompt and a posture, so a user can talk about agents by name
// instead of restating their configuration every time.
type AgentProfile struct {
	// Name is how the profile is referred to in conversation and on the command line.
	Name string

	// Key is the credential agents on this profile use.
	Key KeyRef

	// Fallbacks are tried in order when the primary is overloaded or rate limited.
	//
	// Authentication failures deliberately do not fall through. A wrong key is a thing to fix,
	// not a thing to route around, and quietly billing a different key would be dishonest.
	Fallbacks []KeyRef

	// Model is the provider's model identifier.
	Model string

	// SystemPrompt is prepended to every session on this profile.
	SystemPrompt string

	// Trust is the permission posture for agents started from this profile.
	Trust TrustLevel

	// Tools names the tools available. Empty means the default set.
	Tools []string

	// MaxTokens caps a single response. Zero means the provider default.
	MaxTokens int

	// Temperature is nil when the provider default should be used.
	Temperature *float64

	// PlanFirst makes agents propose a plan and wait for approval before acting, after which they
	// execute what was approved without asking per tool.
	PlanFirst bool

	// BudgetUSD caps total spend for one agent. Zero means no cap.
	//
	// Enforced before the next request rather than reported after the spend, which is the
	// difference between a guardrail and a receipt.
	BudgetUSD float64
}

// DefaultTrust is the posture used when a profile does not name one.
const DefaultTrust = TrustStandard

// Validate checks that a profile can actually start an agent.
func (p AgentProfile) Validate() error {
	if err := ValidateKeyName(p.Name); err != nil {
		return fmt.Errorf("profile name: %w", err)
	}
	if err := p.Key.Validate(); err != nil {
		return fmt.Errorf("profile %q: %w", p.Name, err)
	}
	if p.Model == "" {
		return fmt.Errorf("profile %q needs a model", p.Name)
	}
	if p.Trust != "" && !p.Trust.Valid() {
		return fmt.Errorf("profile %q has unknown trust level %q", p.Name, p.Trust)
	}
	for i, fallback := range p.Fallbacks {
		if err := fallback.Validate(); err != nil {
			return fmt.Errorf("profile %q fallback %d: %w", p.Name, i, err)
		}
		if fallback == p.Key {
			return fmt.Errorf("profile %q lists its own key %q as a fallback", p.Name, p.Key.Name)
		}
	}
	if p.BudgetUSD < 0 {
		return fmt.Errorf("profile %q has a negative budget", p.Name)
	}
	if p.MaxTokens < 0 {
		return fmt.Errorf("profile %q has negative max tokens", p.Name)
	}
	return nil
}

// EffectiveTrust returns the profile's trust level, falling back to the default.
//
// An unset level becomes the default. An invalid one becomes read-only rather than the default,
// because a value nobody recognises should reduce what an agent can do, never quietly grant it
// the usual amount.
func (p AgentProfile) EffectiveTrust() TrustLevel {
	switch {
	case p.Trust == "":
		return DefaultTrust
	case !p.Trust.Valid():
		return TrustReadOnly
	default:
		return p.Trust
	}
}

// KeyChain returns the primary key followed by its fallbacks, in the order they should be tried.
func (p AgentProfile) KeyChain() []KeyRef {
	chain := make([]KeyRef, 0, len(p.Fallbacks)+1)
	chain = append(chain, p.Key)
	return append(chain, p.Fallbacks...)
}
