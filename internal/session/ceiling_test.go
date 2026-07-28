package session

// The configured trust level is a ceiling, not a default.
//
// SetMode's documentation has always said a mode may lower what an agent can do and may never raise
// it. The mode ladder did not enforce it, and the hole was reachable without anybody choosing
// anything: there is no mode at confined, so a confined agent's default resolved to build, and build
// is standard.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// offered reports whether a session would be shown a tool of the given kind.
//
// Asked through toolsForLocked rather than through permission.Decide directly, because the filter
// there is what decides whether the model is even told a tool exists, and a tool the model is told
// about at a level that cannot run it is the escalation this is about.
func offered(t *testing.T, configured core.TrustLevel, kind core.ToolKind) (bool, core.TrustLevel) {
	t.Helper()

	registry := core.NewToolRegistry()
	registry.MustRegister(&kindTool{name: "subject", kind: kind})

	e := New(nil)
	e.WithTools(registry, configured, nil)
	s := e.Create("k", "m")

	e.mu.Lock()
	tools, resolved := e.toolsForLocked(s.ID)
	e.mu.Unlock()

	for _, definition := range tools.Definitions() {
		if definition.Name == "subject" {
			return true, resolved
		}
	}
	return false, resolved
}

// The one that was actually broken. A confined agent may read and write inside its own worktree and
// may not run commands, and it was being handed every execute tool there is.
func TestAConfinedAgentIsNotSilentlyPromotedToStandard(t *testing.T) {
	got, resolved := offered(t, core.TrustConfined, core.ToolExecute)

	if resolved.AtLeast(core.TrustStandard) {
		t.Errorf("a confined agent resolved to %s, which is above what it is configured for. "+
			"Confinement is what makes an isolated agent's worktree a boundary rather than a "+
			"suggestion", resolved)
	}
	if got {
		t.Error("a confined agent was offered a tool that runs commands, which its profile says it " +
			"can never do")
	}
}

// The general rule, stated once rather than per level. No configuration may end up with more than it
// asked for, whichever mode the ladder happens to resolve to.
func TestNoAgentEndsUpAboveItsConfiguredLevel(t *testing.T) {
	for _, configured := range core.AllTrustLevels() {
		_, resolved := offered(t, configured, core.ToolRead)
		if !configured.AtLeast(resolved) {
			t.Errorf("configured %s resolved to %s, which is more permissive", configured, resolved)
		}
	}
}

// The other half, and the one it would be easy to break while fixing the first. A mode is worth
// having because it lowers what an agent may do, so a standard agent in plan mode has to actually be
// read only rather than falling back to its configuration.
func TestAModeStillLowersWhatAnAgentMayDo(t *testing.T) {
	registry := core.NewToolRegistry()
	registry.MustRegister(&kindTool{name: "subject", kind: core.ToolWrite})

	e := New(nil)
	e.WithTools(registry, core.TrustStandard, nil)
	s := e.Create("k", "m")

	plan, _ := core.ModeByName(core.ModePlan)
	if err := e.SetMode(s.ID, plan); err != nil {
		t.Fatalf("SetMode: %v", err)
	}

	e.mu.Lock()
	tools, resolved := e.toolsForLocked(s.ID)
	e.mu.Unlock()

	if resolved != core.TrustReadOnly {
		t.Errorf("a standard agent in plan mode resolved to %s, want read-only: a mode that cannot "+
			"lower anything is decoration", resolved)
	}
	for _, definition := range tools.Definitions() {
		if definition.Name == "subject" {
			t.Error("plan mode offered a tool that writes files")
		}
	}
}

// kindTool is a tool that exists only to have a kind.
type kindTool struct {
	name string
	kind core.ToolKind
}

func (t *kindTool) Name() string            { return t.name }
func (t *kindTool) Description() string     { return "a tool" }
func (t *kindTool) Kind() core.ToolKind     { return t.kind }
func (t *kindTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *kindTool) Run(context.Context, json.RawMessage) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
