package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeTool struct {
	name   string
	kind   ToolKind
	schema string
}

func (f fakeTool) Name() string            { return f.name }
func (f fakeTool) Description() string     { return "does " + f.name }
func (f fakeTool) Kind() ToolKind          { return f.kind }
func (f fakeTool) Schema() json.RawMessage { return json.RawMessage(f.schema) }

func (f fakeTool) Run(context.Context, json.RawMessage) (ToolResult, error) {
	return ToolResult{Content: "ran " + f.name}, nil
}

const pathSchema = `{
	"type": "object",
	"properties": {
		"path": {"type": "string"},
		"lines": {"type": "integer"},
		"all": {"type": "boolean"}
	},
	"required": ["path"]
}`

// A model told "path is required" fixes it on the next turn. One told "invalid input" guesses.
func TestValidationSaysWhatIsWrong(t *testing.T) {
	err := ValidateToolInput(json.RawMessage(pathSchema), json.RawMessage(`{"lines": 5}`))
	if err == nil {
		t.Fatal("a call missing a required argument should be rejected before the tool runs")
	}
	if !strings.Contains(err.Error(), "path") {
		t.Errorf("the error should name the missing argument, got %q", err)
	}

	err = ValidateToolInput(json.RawMessage(pathSchema), json.RawMessage(`{"path": 42}`))
	if err == nil {
		t.Fatal("an argument of the wrong type should be rejected")
	}
	if !strings.Contains(err.Error(), "path") || !strings.Contains(err.Error(), "string") {
		t.Errorf("the error should say what was wanted, got %q", err)
	}
}

// JSON has one number type, so a model correctly sending 3 for an integer field arrives as a
// float64. Rejecting that would refuse well formed calls for a reason nobody could fix.
func TestAnIntegerArgumentIsAcceptedAsANumber(t *testing.T) {
	if err := ValidateToolInput(
		json.RawMessage(pathSchema),
		json.RawMessage(`{"path": "main.go", "lines": 20}`),
	); err != nil {
		t.Errorf("a valid call was rejected: %v", err)
	}
}

func TestValidationAcceptsAValidCall(t *testing.T) {
	if err := ValidateToolInput(
		json.RawMessage(pathSchema),
		json.RawMessage(`{"path": "main.go", "lines": 20, "all": true}`),
	); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

// A schema we cannot read is a bug in our own tool definitions, not in the model's reply. Letting
// the call through means the tool rejects it with a better message than this could.
func TestAnUnreadableSchemaDoesNotBlockTheCall(t *testing.T) {
	if err := ValidateToolInput(json.RawMessage(`not json`), json.RawMessage(`{}`)); err != nil {
		t.Errorf("a broken schema should not refuse the call: %v", err)
	}
	if err := ValidateToolInput(nil, json.RawMessage(`{}`)); err != nil {
		t.Errorf("no schema means nothing to check: %v", err)
	}
}

func TestMalformedArgumentsAreRejected(t *testing.T) {
	err := ValidateToolInput(json.RawMessage(pathSchema), json.RawMessage(`{"path":`))
	if err == nil {
		t.Fatal("arguments that are not JSON should be rejected")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("the error should say what is wrong, got %q", err)
	}
}

// Whichever registration ran last winning is decided by import order, which is exactly the kind of
// thing that changes under you.
func TestADuplicateToolIsRefusedRatherThanReplacing(t *testing.T) {
	registry := NewToolRegistry()
	if err := registry.Register(fakeTool{name: "read", kind: ToolRead}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := registry.Register(fakeTool{name: "read", kind: ToolWrite}); err == nil {
		t.Error("registering a second tool under the same name should be refused")
	}

	tool, _ := registry.Get("read")
	if tool.Kind() != ToolRead {
		t.Error("the first registration should still be the one in place")
	}
}

func TestAToolWithNoNameOrAnUnknownKindIsRefused(t *testing.T) {
	registry := NewToolRegistry()
	if err := registry.Register(fakeTool{name: "", kind: ToolRead}); err == nil {
		t.Error("a tool with no name cannot be called")
	}
	if err := registry.Register(fakeTool{name: "x", kind: ToolKind("mystery")}); err == nil {
		t.Error("a tool with an unknown kind has no permission rule that covers it")
	}
}

// Models weight earlier tool definitions more heavily, so the order tools are registered in is a
// choice rather than an accident, and sorting it away would throw that choice out.
func TestToolsComeBackInRegistrationOrder(t *testing.T) {
	registry := NewToolRegistry()
	for _, name := range []string{"read", "edit", "bash"} {
		registry.MustRegister(fakeTool{name: name, kind: ToolRead})
	}

	var names []string
	for _, tool := range registry.Tools() {
		names = append(names, tool.Name())
	}
	want := []string{"read", "edit", "bash"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}

// One schema, from one place, for both the provider request and the local check. Two declarations
// would drift the first time somebody added a field to one of them.
func TestDefinitionsCarryTheSameSchemaValidationUses(t *testing.T) {
	registry := NewToolRegistry()
	registry.MustRegister(fakeTool{name: "read", kind: ToolRead, schema: pathSchema})

	definitions := registry.Definitions()
	if len(definitions) != 1 {
		t.Fatalf("%d definitions, want 1", len(definitions))
	}

	tool, _ := registry.Get("read")
	if string(definitions[0].InputSchema) != string(tool.Schema()) {
		t.Error("the schema sent to the provider differs from the one used to validate, so the " +
			"two can drift")
	}
	if definitions[0].Description == "" {
		t.Error("a tool with no description is one the model will not know when to use")
	}
}

// A restricted agent being told about tools it cannot use wastes context and invites it to keep
// trying them.
func TestFilteringBuildsARestrictedRegistry(t *testing.T) {
	full := NewToolRegistry()
	full.MustRegister(fakeTool{name: "read", kind: ToolRead})
	full.MustRegister(fakeTool{name: "edit", kind: ToolWrite})
	full.MustRegister(fakeTool{name: "bash", kind: ToolExecute})

	readOnly := full.Filter(func(tool Tool) bool { return tool.Kind() == ToolRead })

	if len(readOnly.Tools()) != 1 {
		t.Fatalf("%d tools in the filtered registry, want 1", len(readOnly.Tools()))
	}
	if _, ok := readOnly.Get("bash"); ok {
		t.Error("a read only agent can still see the shell tool")
	}
	// And the full registry is unchanged, so the next agent can be built from it.
	if len(full.Tools()) != 3 {
		t.Error("filtering modified the registry it was filtering")
	}
}

func TestToolKindVocabulary(t *testing.T) {
	want := []ToolKind{"read", "write", "execute", "network", "git"}
	got := AllToolKinds()
	if len(got) != len(want) {
		t.Fatalf("tool kinds changed size: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kind %d = %q, want %q", i, got[i], want[i])
		}
	}
	if ToolKind("sudo").Valid() {
		t.Error("the vocabulary is closed")
	}
}
