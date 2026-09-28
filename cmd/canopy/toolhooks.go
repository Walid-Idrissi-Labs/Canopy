package main

import (
	"fmt"
	"io"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/hooks"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tools"
)

// hookShutdownLimit is how long the way out waits for turn-end hooks still running.
const hookShutdownLimit = 10 * time.Second

// attachToolHooks gives the engine the project's pre-tool, post-tool and turn-end hooks, run in the
// sandbox the way its other hooks are, in the directory of the agent a call belongs to. project must
// already have been through the trust gate, which withholds the hooks of a repository nobody has
// trusted. A hook limited to a tool Canopy does not have is said on warn: a guard for a misspelt
// tool guards nothing.
func attachToolHooks(engine *session.Engine, dir string, project config.Project, report func(hooks.Report),
	warn io.Writer) *hooks.ToolRunner {
	runner := hooks.NewToolRunner(project.Runnable(), dir, hooks.ConfinedJSON(tools.Confinement), report)
	if runner == nil {
		return nil
	}
	runner.SetWorkspaces(func(sessionID string) (string, string) {
		for _, agent := range engine.Agents() {
			if agent.SessionID == sessionID {
				return agent.Name, agent.Dir
			}
		}
		return "", ""
	})
	if registry, ok := engine.Tools(); ok {
		for _, name := range runner.Named() {
			if _, known := registry.Get(name); !known {
				_, _ = fmt.Fprintf(warn, "warning: a hook in canopy.json is limited to %q, which is not a tool "+
					"Canopy has, so it never runs\n", terminalText(name))
			}
		}
	}
	engine.SetToolHooks(runner)
	return runner
}
