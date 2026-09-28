package skills

import (
	"context"
	"encoding/json"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

type tool struct{ set *Set }

// Tool is the skill tool: it returns a skill's instructions, or one of its files.
func Tool(set *Set) core.Tool { return &tool{set: set} }

func (t *tool) Name() string        { return "skill" }
func (t *tool) Kind() core.ToolKind { return core.ToolRead }
func (t *tool) Description() string {
	return "Load a skill's instructions by name, from the list in your instructions. Pass file to " +
		"read another file the skill mentions."
}
func (t *tool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"file":{"type":"string"}},"required":["name"]}`)
}

func (t *tool) Run(_ context.Context, input json.RawMessage) (core.ToolResult, error) {
	var args struct {
		Name string `json:"name"`
		File string `json:"file"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return core.ToolResult{Content: "could not read the arguments: " + err.Error(), IsError: true}, nil
	}
	body, err := t.set.Body(args.Name, args.File)
	if err != nil {
		return core.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	return core.ToolResult{Content: body}, nil
}
