package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// shellTool runs a command in the agent's workspace.
//
// The broadest tool there is, by a distance. Everything else here is confined by construction: a
// file tool cannot touch what `Workspace.Resolve` will not resolve. A shell command is an opaque
// string that can do anything the user can, and no amount of inspecting it changes that. **The
// confinement for this one is the permission model, not this file**, which is why its kind is
// `execute` and why A4-04 treats that kind differently from every other.
//
// Canopy does not sandbox and must never imply that it does.
type shellTool struct {
	w *Workspace
}

// ShellTool builds the shell tool for a workspace.
func ShellTool(w *Workspace) core.Tool { return &shellTool{w: w} }

func (t *shellTool) Name() string        { return "run_command" }
func (t *shellTool) Kind() core.ToolKind { return core.ToolExecute }

func (t *shellTool) Description() string {
	return "Run a shell command in the workspace. Use this for building, testing and anything " +
		"there is no dedicated tool for. Output is truncated in the middle if it is very long."
}

func (t *shellTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {"type": "string", "description": "The command to run, as you would type it in a shell."},
			"timeout_seconds": {"type": "integer", "description": "How long to allow, defaulting to 120."}
		},
		"required": ["command"]
	}`)
}

// maxTimeoutSeconds caps what a model may ask for.
//
// A model that has decided a command needs an hour is a model that has misunderstood something, and
// letting it wait an hour turns that misunderstanding into an hour of somebody's time.
const maxTimeoutSeconds = 600

func (t *shellTool) Run(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return failure("could not read the arguments: %v", err), nil
	}
	if strings.TrimSpace(args.Command) == "" {
		return failure("a command is required"), nil
	}

	timeout := time.Duration(args.Timeout) * time.Second
	if args.Timeout > maxTimeoutSeconds {
		timeout = maxTimeoutSeconds * time.Second
	}

	// Through a shell rather than split into arguments here, because the model wrote it as a shell
	// command and it will contain pipes, redirections and globs. Splitting it ourselves would run
	// something subtly different from what was asked for and approved, which is worse than running
	// what was asked for.
	result, err := exec.Run(ctx, "/bin/sh", []string{"-c", args.Command}, exec.Options{
		Dir:     t.w.Root(),
		Timeout: timeout,
	})
	if err != nil {
		return failure("%v", err), nil
	}

	return core.ToolResult{
		Content: describe(args.Command, result),
		IsError: !result.Succeeded(),
	}, nil
}

// describe turns a result into something a model can act on.
//
// The exit status is stated in words as well as a number. A model reading "exit status 1" alongside
// a wall of test output has to work out which of those is the answer, and models get that wrong in
// the expensive direction: they report success because the output looked like output.
func describe(command string, result exec.Result) string {
	var b strings.Builder

	if result.Output != "" {
		b.WriteString(strings.TrimRight(result.Output, "\n"))
		b.WriteString("\n")
	}

	switch {
	case result.TimedOut:
		fmt.Fprintf(&b, "\nThe command was still running after %s and was stopped. "+
			"Any output above is partial.", result.Duration.Round(time.Second))
	case result.Cancelled:
		b.WriteString("\nThe command was cancelled. Any output above is partial.")
	case !result.Ran:
		fmt.Fprintf(&b, "\n%q could not be run.", command)
	case result.ExitCode != 0:
		fmt.Fprintf(&b, "\nExited %d, which means it failed.", result.ExitCode)
	default:
		if result.Output == "" {
			b.WriteString("Exited 0 with no output.")
		}
	}
	return strings.TrimSpace(b.String())
}
