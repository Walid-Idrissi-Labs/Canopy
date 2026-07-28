package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The tool contract.
//
// A tool declares its schema once. That same schema is sent to the provider and used to validate
// what comes back, so the two cannot drift. Two declarations would drift the first time somebody
// added a field to one of them, and the failure would be a model confidently passing an argument
// that is silently ignored.

// ToolKind classifies what a tool does, for the permission model.
//
// Coarse on purpose. The permission model asks "may this agent write files" rather than "may this
// agent call `edit`", because a per tool allow list has to be updated every time a tool is added,
// and the update that gets forgotten is the one that grants more than intended.
type ToolKind string

const (
	// ToolRead observes without changing anything. Reading a file, listing a directory, searching.
	ToolRead ToolKind = "read"
	// ToolWrite changes files in the agent's workspace.
	ToolWrite ToolKind = "write"
	// ToolExecute runs a command. The broadest kind by a distance, because a command can do
	// anything the user can, including things no other kind covers.
	ToolExecute ToolKind = "execute"
	// ToolNetwork reaches outside the machine.
	//
	// Its own kind rather than a flavour of read, because the risk is different in both directions:
	// what comes back is untrusted, and what goes out has left.
	ToolNetwork ToolKind = "network"
	// ToolGit operates on version control.
	//
	// Separate from write because the destructive ones are destructive in a way file edits are not:
	// a bad edit is recoverable from git, and a bad `git checkout` is what you would have recovered
	// from.
	ToolGit ToolKind = "git"
)

// AllToolKinds returns every tool kind.
func AllToolKinds() []ToolKind {
	return []ToolKind{ToolRead, ToolWrite, ToolExecute, ToolNetwork, ToolGit}
}

// Valid reports whether k is a known kind.
func (k ToolKind) Valid() bool {
	for _, known := range AllToolKinds() {
		if k == known {
			return true
		}
	}
	return false
}

func (k ToolKind) String() string { return string(k) }

// Tool is something an agent can do.
type Tool interface {
	// Name is what the model calls. Stable, because it appears in transcripts and audit trails that
	// outlive any one version.
	Name() string

	// Description tells the model when to use this. It is prompt text and is worth writing as
	// carefully as any other prompt.
	Description() string

	// Kind classifies it for the permission model.
	Kind() ToolKind

	// Schema is the JSON Schema for the arguments, used both to tell the provider what this accepts
	// and to check what comes back.
	Schema() json.RawMessage

	// Run performs the call.
	//
	// It returns a result rather than an error for anything the model could act on. A tool that
	// failed because the file was not there has told the model something useful, and turning that
	// into a Go error would end the turn instead of letting the model try a different path. The
	// error return is for failures the model cannot do anything about, such as the context being
	// cancelled.
	Run(ctx context.Context, input json.RawMessage) (ToolResult, error)
}

// ExternalTool is a tool whose arguments follow a vocabulary Canopy did not define.
//
// Anything reached over MCP, and anything added later that is described by somebody else's schema.
// Optional rather than a method on Tool, so that adding it did not touch every tool in the codebase
// to say "no" and so that the answer for a tool that has not thought about it is the safe one.
//
// It exists because the permission layer reads argument names to decide how narrowly an approval
// should be scoped, and looking for "path" or "command" is a sound reading of Canopy's own tools and
// a guess about everybody else's. A remote tool naming something "path" does not make it a path, and
// treating it as one lets a single approval cover calls that differ everywhere else.
type ExternalTool interface {
	Tool

	// External reports that this tool's arguments are somebody else's vocabulary.
	External() bool
}

// ToolResult is what a tool call produced.
//
// Deliberately the same shape the provider contract already uses, so a result travels from a tool
// to a session to a request without being converted. Every conversion between two types that mean
// the same thing is a place for a field to go missing, and the field that goes missing here is
// IsError, which turns a refused call into one the model reads as having succeeded.

// ValidateToolInput checks arguments against a schema before a tool runs.
//
// Deliberately shallow. This is not a JSON Schema implementation and should not become one: it
// catches the mistakes models actually make, which are a missing required field and a value of
// entirely the wrong type. Anything subtler is the tool's own business, because the tool is the
// only thing that knows what its arguments mean.
//
// The point is to fail with a message the model can act on, rather than to be complete. A model
// told "path is required" fixes it on the next turn; one told "invalid input" guesses.
func ValidateToolInput(schema json.RawMessage, input json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}

	var spec struct {
		Type       string `json:"type"`
		Required   []string
		Properties map[string]struct {
			Type string `json:"type"`
		}
	}
	if err := json.Unmarshal(schema, &spec); err != nil {
		// A schema we cannot read is a bug in our own tool definitions, not in the model's reply.
		// Letting the call through means the tool rejects it with a better message than this could.
		return nil
	}

	var args map[string]any
	if len(input) == 0 {
		args = map[string]any{}
	} else if err := json.Unmarshal(input, &args); err != nil {
		return fmt.Errorf("the arguments are not valid JSON: %w", err)
	}

	var missing []string
	for _, name := range spec.Required {
		value, present := args[name]
		if !present || value == nil {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required argument%s: %s",
			plural(len(missing)), strings.Join(missing, ", "))
	}

	for name, property := range spec.Properties {
		value, present := args[name]
		if !present || value == nil || property.Type == "" {
			continue
		}
		if got := jsonTypeOf(value); got != property.Type && !numericMatch(property.Type, got) {
			return fmt.Errorf("argument %q should be %s, got %s", name, property.Type, got)
		}
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func jsonTypeOf(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "null"
	}
}

// numericMatch allows a number where an integer is expected.
//
// JSON has one number type, so a model that correctly sends 3 for an integer field arrives here as
// a float64. Rejecting that would refuse well formed calls for a reason nobody could fix.
func numericMatch(want, got string) bool {
	return want == "integer" && got == "number"
}

// ToolRegistry holds the tools available to an agent.
//
// An agent's tools are a property of the agent, not a global list, because the whole point of trust
// levels is that a read only agent and a broad one see different tools. Handing every agent the
// same registry and filtering at call time would mean a restricted agent still being told about
// tools it cannot use, which wastes context and invites it to keep trying.
type ToolRegistry struct {
	tools map[string]Tool
	order []string
}

// NewToolRegistry builds an empty registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]Tool{}}
}

// Register adds a tool. A duplicate name is an error rather than a silent replacement.
func (r *ToolRegistry) Register(tool Tool) error {
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("a tool needs a name")
	}
	if !tool.Kind().Valid() {
		return fmt.Errorf("tool %q has unknown kind %q", name, tool.Kind())
	}
	if _, exists := r.tools[name]; exists {
		// Silently replacing would mean whichever registration ran last wins, which is decided by
		// import order and is exactly the kind of thing that changes under you.
		return fmt.Errorf("tool %q is already registered", name)
	}
	r.tools[name] = tool
	r.order = append(r.order, name)
	return nil
}

// MustRegister is Register for package level definitions, where a failure is a programming error.
func (r *ToolRegistry) MustRegister(tool Tool) {
	if err := r.Register(tool); err != nil {
		panic(err)
	}
}

// Get returns a tool by name.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// Tools returns every registered tool, in registration order.
//
// Registration order rather than alphabetical, because that order is chosen: the tools an agent
// should reach for first are registered first, and models weight earlier definitions more heavily.
func (r *ToolRegistry) Tools() []Tool {
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

// Definitions renders the registry for a provider request.
//
// One schema, from one place, for both the request and the local validation. That is the whole
// reason this function exists rather than each tool being described twice.
func (r *ToolRegistry) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(r.order))
	for _, name := range r.order {
		tool := r.tools[name]
		out = append(out, ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			InputSchema: tool.Schema(),
		})
	}
	return out
}

// Filter returns a registry containing only the tools a predicate accepts.
//
// Used to build an agent's own registry from the full set according to its trust level. Returns a
// new registry rather than mutating, so the full set stays available to build the next agent from.
func (r *ToolRegistry) Filter(keep func(Tool) bool) *ToolRegistry {
	out := NewToolRegistry()
	for _, name := range r.order {
		if tool := r.tools[name]; keep(tool) {
			out.MustRegister(tool)
		}
	}
	return out
}
