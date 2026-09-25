package main

import (
	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/hooks"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tools"
)

// attachToolHooks gives the engine the project's pre-tool, post-tool and turn-end hooks, run in the
// sandbox the way its other hooks are. project must already have been through the trust gate, which
// withholds the hooks of a repository nobody has trusted.
func attachToolHooks(engine *session.Engine, dir string, project config.Project, report func(hooks.Report)) *hooks.ToolRunner {
	runner := hooks.NewToolRunner(project.Runnable(), dir, hooks.ConfinedJSON(tools.Confinement), report)
	if runner != nil {
		engine.SetToolHooks(runner)
	}
	return runner
}
