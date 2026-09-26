package tools

import (
	"context"
	"encoding/json"
	"os"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Navigator answers where a symbol is defined or used, a language server behind it.
type Navigator interface {
	Find(ctx context.Context, kind, path, content string, line int, symbol string) (string, error)
}

// NavigationTools are find_definition and find_references for a workspace.
func NavigationTools(w *Workspace, n Navigator) []core.Tool {
	return []core.Tool{
		&navigateTool{w: w, n: n, kind: "definition", name: "find_definition",
			description: "Where a symbol is defined, from the language server: give the file, a line it " +
				"appears on and its name. Exact where grep guesses, across packages and imports."},
		&navigateTool{w: w, n: n, kind: "references", name: "find_references",
			description: "Every place a symbol is used, from the language server: give the file, a line " +
				"it appears on and its name. Finds real uses, not every string that happens to match."},
	}
}

type navigateTool struct {
	w           *Workspace
	n           Navigator
	kind, name  string
	description string
}

func (t *navigateTool) Name() string        { return t.name }
func (t *navigateTool) Kind() core.ToolKind { return core.ToolRead }
func (t *navigateTool) Description() string { return t.description }

func (t *navigateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {"type": "string", "description": "A file the symbol appears in."},
			"line": {"type": "integer", "description": "A line, counting from 1, the symbol appears on."},
			"symbol": {"type": "string", "description": "The symbol's name as written on that line."}
		},
		"required": ["path", "line", "symbol"]
	}`)
}

func (t *navigateTool) Run(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Path   string `json:"path"`
		Line   int    `json:"line"`
		Symbol string `json:"symbol"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return failure("could not read the arguments: %v", err), nil
	}
	path, err := t.w.Resolve(args.Path)
	if err != nil {
		return pathFailure(err), nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return failure("%s: %v", args.Path, err), nil
	}
	answer, err := t.n.Find(ctx, t.kind, path, string(content), args.Line, args.Symbol)
	if err != nil {
		return failure("%v", err), nil
	}
	return core.ToolResult{Content: answer}, nil
}
