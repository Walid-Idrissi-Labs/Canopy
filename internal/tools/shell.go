package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/gitsafe"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
	osexec "os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// shellTool runs a command in the agent's workspace.
//
// The broadest tool there is, by a distance. Structured path tools can refuse what
// `Workspace.Resolve` will not resolve. A shell command is an opaque string that can do anything the
// user can, and no amount of inspecting it changes that. The permission model controls whether this
// process may start; it does not contain the process after that. This distinction is why its kind is
// `execute` and why A4-04 treats that kind differently from every other.
//
// Canopy does not sandbox and must never imply that it does.
type shellTool struct {
	w       *Workspace
	outputs *OutputStore

	policyOnce sync.Once
	policy     sandbox.Policy
}

// ShellTool builds the shell tool for a workspace.
func ShellTool(w *Workspace) core.Tool { return &shellTool{w: w} }

// ShellToolWithOutputs is ShellTool with long output kept in store and summarised in the
// conversation; see offload.
func ShellToolWithOutputs(w *Workspace, store *OutputStore) core.Tool {
	return &shellTool{w: w, outputs: store}
}

func (t *shellTool) Name() string        { return "run_command" }
func (t *shellTool) Kind() core.ToolKind { return core.ToolExecute }

func (t *shellTool) Description() string {
	return "Run a shell command starting in the workspace. It is not sandboxed and can reach " +
		"anything the user's account can reach. Use this for building, testing and anything there " +
		"is no dedicated tool for. Output is truncated in the middle if it is very long."
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
	options := exec.Options{Dir: t.w.Root(), Timeout: timeout}
	var unconfined string
	switch err := sandbox.Available(); {
	case sandbox.Disabled():
		unconfined = "sandboxing is switched off (" + sandbox.DisableEnvVar + "=off)"
	case err != nil:
		unconfined = err.Error()
	default:
		policy := t.sandboxPolicy()
		options.Sandbox = &policy
	}
	if t.outputs != nil {
		options.MaxOutput = capturedOutputBytes
	}
	result, err := exec.Run(ctx, "/bin/sh", []string{"-c", args.Command}, options)
	if err != nil {
		return failure("%v", err), nil
	}

	content := describe(args.Command, result)
	if t.outputs != nil {
		content = strings.TrimSpace(offload(t.outputs, strings.TrimRight(result.Output, "\n")) +
			"\n" + outcome(args.Command, result))
	}
	if unconfined != "" {
		// Said on every result, because neither the model nor a person reading the transcript
		// should assume a boundary that was not there for this command.
		content += "\n(This ran without a sandbox: " + unconfined + ".)"
	}
	return core.ToolResult{Content: content, IsError: !result.Succeeded()}, nil
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
	return b.String() + outcome(command, result)
}

// outcome is the sentence that says how the command ended.
func outcome(command string, result exec.Result) string {
	var b strings.Builder

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

// sandboxPolicy is the confinement for this workspace, worked out once: the workspace, the git
// directory it shares with its repository when it is a worktree, temporary directories and
// toolchain caches.
func (t *shellTool) sandboxPolicy() sandbox.Policy {
	t.policyOnce.Do(func() {
		var extra []string
		cmd := osexec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		cmd.Dir = t.w.Root()
		cmd.Env = gitsafe.InheritedFor(t.w.Root())
		if out, err := cmd.Output(); err == nil {
			extra = append(extra, strings.TrimSpace(string(out)))
		}
		t.policy = sandbox.ForWorkspace(t.w.Root(), extra...)
	})
	return t.policy
}
