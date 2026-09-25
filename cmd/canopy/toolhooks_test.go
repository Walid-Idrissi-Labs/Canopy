package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/hooks"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

type namedTool struct{ name string }

func (t namedTool) Name() string            { return t.name }
func (t namedTool) Description() string     { return t.name }
func (t namedTool) Kind() core.ToolKind     { return core.ToolRead }
func (t namedTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t namedTool) Run(context.Context, json.RawMessage) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}

// A hook limited to a tool Canopy does not have is said at the start: a guard for a misspelt tool
// guards nothing.
func TestAHookForAToolThatDoesNotExistIsSaid(t *testing.T) {
	engine := session.New(nil)
	t.Cleanup(engine.Close)
	registry := core.NewToolRegistry()
	registry.MustRegister(namedTool{"run_command"})
	engine.WithTools(registry, core.TrustStandard, nil)
	project := config.Project{Hooks: []config.Hook{
		{On: "pre-tool", Run: "guard", Tools: []string{"run_command", "run_comand"}}}}
	var warned bytes.Buffer
	if runner := attachToolHooks(engine, t.TempDir(), project, func(hooks.Report) {}, &warned); runner == nil {
		t.Fatal("no runner for a project with a hook")
	}
	if got := warned.String(); !strings.Contains(got, `"run_comand"`) || strings.Contains(got, `"run_command"`) {
		t.Fatalf("warned %q", got)
	}
}
