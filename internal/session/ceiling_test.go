package session

// The configured trust level is a ceiling, not a default.
//
// SetMode's documentation has always said a mode may lower what an agent can do and may never raise
// it. The old ladder did not enforce it, and the hole was reachable without anybody choosing
// anything: a confined agent's default resolved to build, and build is standard.

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/agent"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
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

func TestEveryConfiguredLevelHasAnHonestDefaultMode(t *testing.T) {
	want := map[core.TrustLevel]string{
		core.TrustReadOnly: core.ModePlan,
		core.TrustConfined: core.ModeConfined,
		core.TrustStandard: core.ModeBuild,
		core.TrustBroad:    core.ModeCruise,
	}

	for configured, modeName := range want {
		e := New(nil)
		e.WithTools(core.NewToolRegistry(), configured, nil)
		e.WithCheckpoints(git.NewTaker(t.TempDir()))
		s := e.Create("k", "m")

		mode := e.Mode(s.ID)
		if mode.Name != modeName || mode.Trust != configured {
			t.Errorf("%s opens as %s/%s, want %s/%s: the label and enforced trust must agree",
				configured, mode.Name, mode.Trust, modeName, configured)
		}
		if err := e.SetMode(s.ID, mode); err != nil {
			t.Errorf("%s could not reselect its own visible mode: %v", configured, err)
		}
	}
}

func TestABroadDefaultCannotBypassCruiseUndoRequirement(t *testing.T) {
	e := New(nil)
	e.WithTools(core.NewToolRegistry(), core.TrustBroad, nil)
	s := e.Create("k", "m")

	mode := e.Mode(s.ID)
	if mode.Name != core.ModeBuild || e.Trust(s.ID) != core.TrustStandard {
		t.Errorf("broad with no undo opened as %s/%s, want the safe build fallback",
			mode.Name, e.Trust(s.ID))
	}
}

func TestAConfinedDefaultTellsTheProviderAboutItsActualBoundary(t *testing.T) {
	client := &scriptedClient{name: "scripted", events: reply("done")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)
	e.WithTools(core.NewToolRegistry(), core.TrustConfined, nil)
	s := e.Create("k", "m")

	turnID, err := e.Send(s.ID, "edit through the available tools")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, s.ID, turnID)

	confined, _ := core.ModeByName(core.ModeConfined)
	client.mu.Lock()
	system := client.system
	client.mu.Unlock()
	if system != confined.Prompt {
		t.Errorf("provider system prompt = %q, want the confined mode prompt", system)
	}
	if !strings.Contains(system, "cannot run shell commands") {
		t.Errorf("the confined prompt does not explain the enforced shell boundary: %q", system)
	}
}

// Workspace ownership chooses which registry is used; trust chooses which tools from that registry
// are visible and what happens when they are called. Crossing those two axes here prevents a fix
// that is safe only in the primary registry or only in an isolated agent's registry.
func TestDirectAndIsolatedToolMatricesMatchEveryTrustLevel(t *testing.T) {
	type toolCase struct {
		name    string
		kind    core.ToolKind
		command string
		offered map[core.TrustLevel]bool
		outcome map[core.TrustLevel]permission.Outcome
	}
	all := []core.TrustLevel{
		core.TrustReadOnly, core.TrustConfined, core.TrustStandard, core.TrustBroad,
	}
	cases := []toolCase{
		{"read", core.ToolRead, "", outcomes(true, true, true, true),
			decisions(permission.Allow, permission.Allow, permission.Allow, permission.Allow)},
		{"write", core.ToolWrite, "", outcomes(false, true, true, true),
			decisions(permission.Deny, permission.Allow, permission.Allow, permission.Allow)},
		{"shell", core.ToolExecute, "make test", outcomes(false, false, true, true),
			decisions(permission.Deny, permission.Deny, permission.Ask, permission.Allow)},
		{"network", core.ToolNetwork, "", outcomes(true, true, true, true),
			decisions(permission.Ask, permission.Ask, permission.Ask, permission.Ask)},
		{"git-status", core.ToolGit, "git status", outcomes(true, true, true, true),
			decisions(permission.Allow, permission.Allow, permission.Allow, permission.Allow)},
		{"git-reset", core.ToolGit, "git reset --hard HEAD", outcomes(true, true, true, true),
			decisions(permission.Deny, permission.Deny, permission.Deny, permission.Allow)},
	}

	for _, configured := range all {
		for _, ownership := range []string{"direct", "isolated"} {
			t.Run(string(configured)+"/"+ownership, func(t *testing.T) {
				registry := core.NewToolRegistry()
				for _, tc := range cases {
					registry.MustRegister(&kindTool{name: tc.name, kind: tc.kind})
				}

				e := New(nil)
				e.WithTools(registry, configured, nil)
				if configured == core.TrustBroad {
					e.WithCheckpoints(git.NewTaker(t.TempDir()))
				}
				s := e.Create("k", "m")
				if ownership == "isolated" {
					e.mu.Lock()
					e.agentTools = map[string]*core.ToolRegistry{s.ID: registry}
					e.mu.Unlock()
				}

				e.mu.Lock()
				visible, effective := e.toolsForLocked(s.ID)
				e.mu.Unlock()
				if effective != configured {
					t.Fatalf("effective trust = %s, want configured %s", effective, configured)
				}

				for _, tc := range cases {
					_, gotOffered := visible.Get(tc.name)
					if gotOffered != tc.offered[configured] {
						t.Errorf("%s offered = %t, want %t", tc.name, gotOffered,
							tc.offered[configured])
					}
					gotDecision := permission.Decide(permission.Request{
						Tool: tc.name, Kind: tc.kind, Command: tc.command,
					}, effective, permission.NewGrants())
					if gotDecision.Outcome != tc.outcome[configured] {
						t.Errorf("%s decision = %s, want %s", tc.name, gotDecision.Outcome,
							tc.outcome[configured])
					}
				}
			})
		}
	}
}

func outcomes(readOnly, confined, standard, broad bool) map[core.TrustLevel]bool {
	return map[core.TrustLevel]bool{
		core.TrustReadOnly: readOnly, core.TrustConfined: confined,
		core.TrustStandard: standard, core.TrustBroad: broad,
	}
}

func decisions(
	readOnly, confined, standard, broad permission.Outcome,
) map[core.TrustLevel]permission.Outcome {
	return map[core.TrustLevel]permission.Outcome{
		core.TrustReadOnly: readOnly, core.TrustConfined: confined,
		core.TrustStandard: standard, core.TrustBroad: broad,
	}
}

func TestCommandAuditOutcomesMatchTrustInBothRegistryPaths(t *testing.T) {
	for _, configured := range []core.TrustLevel{
		core.TrustReadOnly, core.TrustConfined, core.TrustStandard, core.TrustBroad,
	} {
		for _, ownership := range []string{"direct", "isolated"} {
			t.Run(string(configured)+"/"+ownership, func(t *testing.T) {
				client := &askingClient{name: "scripted"}
				e := New(fixedResolver{client: client, id: anthropicID()})
				t.Cleanup(e.Close)

				command := &kindTool{name: "run_command", kind: core.ToolExecute}
				registry := core.NewToolRegistry()
				registry.MustRegister(command)
				var approvals atomic.Int32
				e.WithTools(registry, configured, agent.ApproverFunc(func(
					context.Context, permission.Request, permission.Decision,
				) bool {
					approvals.Add(1)
					return true
				}))
				if configured == core.TrustBroad {
					e.WithCheckpoints(git.NewTaker(t.TempDir()))
				}
				s := e.Create("k", "m")
				if ownership == "isolated" {
					e.mu.Lock()
					e.agentTools = map[string]*core.ToolRegistry{s.ID: registry}
					e.mu.Unlock()
				}
				trail := e.Trail()

				turnID, err := e.Send(s.ID, "try a command")
				if err != nil {
					t.Fatalf("Send: %v", err)
				}
				waitForTurn(t, e, s.ID, turnID)

				entries := trail.Entries()
				if len(entries) != 1 {
					t.Fatalf("audit entries = %d, want one attempted command: %+v", len(entries), entries)
				}
				entry := entries[0]
				if entry.Tool != "run_command" {
					t.Fatalf("audit entry = %+v, want run_command", entry)
				}

				if configured.AtLeast(core.TrustStandard) {
					if command.runs.Load() != 1 || entry.Outcome != permission.Allow || !entry.Ran {
						t.Errorf("audit = %+v, runs = %d; want one allowed execution",
							entry, command.runs.Load())
					}
					wantApprovals := int32(0)
					if configured == core.TrustStandard {
						wantApprovals = 1
					}
					if approvals.Load() != wantApprovals {
						t.Errorf("approval prompts = %d, want %d", approvals.Load(), wantApprovals)
					}
					return
				}

				if command.runs.Load() != 0 || entry.Outcome != permission.Deny || entry.Ran {
					t.Errorf("audit = %+v, runs = %d; want a denied, unrun command",
						entry, command.runs.Load())
				}
				if !strings.Contains(entry.Reason, "no such tool") {
					t.Errorf("audit reason = %q, want the structural tool-filter refusal", entry.Reason)
				}
			})
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
	runs atomic.Int32
}

func (t *kindTool) Name() string            { return t.name }
func (t *kindTool) Description() string     { return "a tool" }
func (t *kindTool) Kind() core.ToolKind     { return t.kind }
func (t *kindTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *kindTool) Run(context.Context, json.RawMessage) (core.ToolResult, error) {
	t.runs.Add(1)
	return core.ToolResult{}, nil
}
