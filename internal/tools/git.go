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

// Git as structured tools rather than as a shell wrapper.
//
// **This is not a convenience layer over `run_command`.** A shell tool hands the permission model an
// opaque string, and no amount of inspecting that string reliably tells `git status` apart from
// `git push --force`. Here the operation is a named tool and its arguments are fields, so
// confinement is enforceable per argument and the destructive ones can be gated separately from the
// ordinary ones. In a shell string neither is possible.
//
// The second reason is that structured output is far more reliable for a model to act on than
// parsed porcelain. A model reading `git status --porcelain` gets it right most of the time, and the
// times it does not are the times it decides a file is untracked when it is staged.

// GitTools builds the git tools for a workspace.
func GitTools(w *Workspace) []core.Tool {
	return []core.Tool{
		&gitTool{w: w, name: "git_status", kind: core.ToolRead,
			description: "Show what has changed in the working tree: staged, unstaged and " +
				"untracked files, and which branch you are on.",
			schema: `{"type":"object","properties":{},"required":[]}`,
			build:  func(json.RawMessage) ([]string, error) { return []string{"status", "--porcelain=v1", "--branch"}, nil },
		},
		&gitTool{w: w, name: "git_diff", kind: core.ToolRead,
			description: "Show the changes in the working tree as a diff. Pass staged: true for " +
				"what is staged, or a path to limit it.",
			schema: `{
				"type": "object",
				"properties": {
					"staged": {"type": "boolean", "description": "Show staged changes rather than unstaged ones."},
					"path": {"type": "string", "description": "Limit the diff to one path."}
				},
				"required": []
			}`,
			build: buildDiff,
		},
		&gitTool{w: w, name: "git_log", kind: core.ToolRead,
			description: "Show recent commits on the current branch.",
			schema: `{
				"type": "object",
				"properties": {
					"count": {"type": "integer", "description": "How many commits, defaulting to 20."}
				},
				"required": []
			}`,
			build: buildLog,
		},
		&gitTool{w: w, name: "git_add", kind: core.ToolWrite,
			description: "Stage changes for the next commit.",
			schema: `{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to stage. Use \".\" for everything changed."}
				},
				"required": ["path"]
			}`,
			build: buildAdd,
		},
		&gitTool{w: w, name: "git_commit", kind: core.ToolWrite,
			description: "Commit what is staged. Stage first with git_add.",
			schema: `{
				"type": "object",
				"properties": {
					"message": {"type": "string", "description": "The commit message."}
				},
				"required": ["message"]
			}`,
			build: buildCommit,
		},
		&gitTool{w: w, name: "git_branch", kind: core.ToolGit,
			description: "List branches, or create and switch to one.",
			schema: `{
				"type": "object",
				"properties": {
					"create": {"type": "string", "description": "Name of a branch to create and switch to. Omit to list."}
				},
				"required": []
			}`,
			build: buildBranch,
		},
	}
}

// gitTool is one git operation.
//
// One type with a builder per operation rather than a type per operation, because the parts that
// differ are the arguments and the command, and the parts that are the same, running git in the
// workspace and reporting the result, are the parts worth having in one place.
type gitTool struct {
	w           *Workspace
	name        string
	kind        core.ToolKind
	description string
	schema      string
	build       func(json.RawMessage) ([]string, error)
}

func (t *gitTool) Name() string            { return t.name }
func (t *gitTool) Description() string     { return t.description }
func (t *gitTool) Kind() core.ToolKind     { return t.kind }
func (t *gitTool) Schema() json.RawMessage { return json.RawMessage(t.schema) }

func (t *gitTool) Run(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	args, err := t.build(input)
	if err != nil {
		return failure("%v", err), nil
	}

	result, err := exec.Run(ctx, "git", args, exec.Options{
		Dir: t.w.Root(),
		// Short. Every operation here is local and finishes in milliseconds; one that does not has
		// hit an interactive prompt, which is the case a timeout exists for.
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return failure("%v", err), nil
	}

	if !result.Ran {
		return failure("git could not be run: %s", strings.TrimSpace(result.Output)), nil
	}
	if result.ExitCode != 0 {
		return core.ToolResult{
			Content: strings.TrimSpace(result.Output),
			IsError: true,
		}, nil
	}

	output := strings.TrimSpace(result.Output)
	if output == "" {
		// Git is famously silent on success, and a model handed an empty string cannot tell that
		// apart from a failure it did not notice. Saying so explicitly is the difference between
		// the model moving on and the model running the command again to check.
		output = fmt.Sprintf("%s finished with nothing to report.", t.name)
	}
	return core.ToolResult{Content: output}, nil
}

func buildDiff(input json.RawMessage) ([]string, error) {
	var args struct {
		Staged bool   `json:"staged"`
		Path   string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("could not read the arguments: %w", err)
	}

	out := []string{"diff"}
	if args.Staged {
		out = append(out, "--staged")
	}
	if args.Path != "" {
		if err := safePathArgument(args.Path); err != nil {
			return nil, err
		}
		// The separator matters: without it a path that looks like a flag is read as one, and a
		// path that matches a branch name is read as a revision.
		out = append(out, "--", args.Path)
	}
	return out, nil
}

func buildLog(input json.RawMessage) ([]string, error) {
	var args struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("could not read the arguments: %w", err)
	}

	count := args.Count
	if count <= 0 {
		count = 20
	}
	if count > 200 {
		count = 200
	}
	return []string{"log", fmt.Sprintf("-%d", count), "--pretty=format:%h %ad %an: %s",
		"--date=short"}, nil
}

func buildAdd(input json.RawMessage) ([]string, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("could not read the arguments: %w", err)
	}
	if args.Path == "" {
		return nil, fmt.Errorf("a path is required. Use \".\" to stage everything changed")
	}
	if err := safePathArgument(args.Path); err != nil {
		return nil, err
	}
	return []string{"add", "--", args.Path}, nil
}

func buildCommit(input json.RawMessage) ([]string, error) {
	var args struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("could not read the arguments: %w", err)
	}
	if strings.TrimSpace(args.Message) == "" {
		return nil, fmt.Errorf("a commit message is required")
	}
	// No --author, no --amend, no -a. Amending rewrites a commit that may already have been pushed,
	// and -a stages files the model never looked at. Both are reachable through the shell tool,
	// where they are visible and approved as what they are.
	return []string{"commit", "-m", args.Message}, nil
}

func buildBranch(input json.RawMessage) ([]string, error) {
	var args struct {
		Create string `json:"create"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil, fmt.Errorf("could not read the arguments: %w", err)
	}
	if args.Create == "" {
		return []string{"branch", "--list"}, nil
	}
	if err := safeBranchName(args.Create); err != nil {
		return nil, err
	}
	// checkout -b rather than checkout, so this tool can only ever create. Switching to an existing
	// branch can discard uncommitted work, which is a destructive operation and belongs behind the
	// permission model's destructive gate rather than inside a tool called "branch".
	return []string{"checkout", "-b", args.Create}, nil
}

// safePathArgument refuses a path that would be read as a flag.
//
// Git reads a leading dash as an option wherever it appears, so a path called `-f` becomes a flag.
// The `--` separator handles most of this and is used everywhere here; this catches the rest and
// makes the intent explicit rather than relying on every builder remembering the separator.
func safePathArgument(path string) error {
	if strings.HasPrefix(path, "-") {
		return fmt.Errorf("%q starts with a dash, which git reads as an option rather than a path",
			path)
	}
	return nil
}

// safeBranchName refuses names git would reject or misread.
//
// Checked here rather than left to git, because git's own error for a bad ref name is written for
// somebody who knows the ref format documentation, and a model reading it tries something adjacent
// rather than something correct.
func safeBranchName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("a branch name is required")
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("a branch name cannot start with a dash")
	case strings.ContainsAny(name, " ~^:?*[\\"):
		return fmt.Errorf("%q contains a character git does not allow in a branch name", name)
	case strings.Contains(name, ".."):
		return fmt.Errorf("a branch name cannot contain two dots")
	case strings.HasSuffix(name, ".lock"), strings.HasSuffix(name, "/"):
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	return nil
}
