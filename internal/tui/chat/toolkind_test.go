package chat_test

// Labelling a call by what kind of thing it is.
//
// A wall of tool names all in one colour is a wall. The same wall with "run" against the one that
// shells out is something an eye can skim, and the skim is what somebody watching an agent work
// actually does.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// labelled is a tool that exists to have a name and a kind.
type labelled struct {
	name string
	kind core.ToolKind
}

func (t *labelled) Name() string            { return t.name }
func (t *labelled) Description() string     { return "a tool" }
func (t *labelled) Kind() core.ToolKind     { return t.kind }
func (t *labelled) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *labelled) Run(context.Context, json.RawMessage) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}

func registryOf(tools ...core.Tool) *core.ToolRegistry {
	registry := core.NewToolRegistry()
	for _, tool := range tools {
		registry.MustRegister(tool)
	}
	return registry
}

// The one that matters most, and the reason the kind is asked of the registry rather than guessed
// from the name. Every MCP tool is an execute tool whatever its server calls it, and a name nobody
// has ever seen is exactly where "this runs commands" is worth saying.
func TestARemoteToolIsLabelledAsSomethingThatRunsCommands(t *testing.T) {
	engine := &fakeEngine{
		session: withCall("mcp__widgets__do_a_thing", `{"query":"x"}`,
			core.ToolResult{Content: "done"}),
		tools: registryOf(&labelled{name: "mcp__widgets__do_a_thing", kind: core.ToolExecute}),
	}

	body := plain(model(engine).Body())
	if !strings.Contains(body, "run") {
		t.Errorf("a tool from somebody else's server is not labelled as one that runs commands, "+
			"which is the one thing about it worth knowing:\n%s", body)
	}
	if !strings.Contains(body, "mcp__widgets__do_a_thing") {
		t.Errorf("the tool is not named:\n%s", body)
	}
}

// A tool the registry does not know about gets space rather than a wrong label, and the column still
// lines up. A guess here would be worse than a blank: "read" against something that writes is a
// statement, and a blank is an absence.
func TestAnUnknownToolIsNotGuessedAt(t *testing.T) {
	engine := &fakeEngine{
		session: withCall("something_else", `{}`, core.ToolResult{Content: "done"}),
		tools:   registryOf(&labelled{name: "read_file", kind: core.ToolRead}),
	}

	body := plain(model(engine).Body())
	for _, label := range []string{"read", "edit", "run", "net", "git"} {
		if strings.Contains(body, label+" something_else") {
			t.Errorf("an unidentified tool was labelled %q:\n%s", label, body)
		}
	}
}

// Each kind gets its own word, so the difference survives a terminal with no colour in it.
func TestEachKindGetsItsOwnWord(t *testing.T) {
	for _, tc := range []struct {
		kind core.ToolKind
		want string
	}{
		{core.ToolRead, "read"},
		{core.ToolWrite, "edit"},
		{core.ToolExecute, "run"},
		{core.ToolNetwork, "net"},
		{core.ToolGit, "git"},
	} {
		engine := &fakeEngine{
			session: withCall("subject", `{}`, core.ToolResult{Content: "done"}),
			tools:   registryOf(&labelled{name: "subject", kind: tc.kind}),
		}

		// Padded to four so the tool names line up in a column whichever kinds are on screen, which
		// is why this asserts the padded form rather than the bare word.
		body := plain(model(engine).Body())
		want := fmt.Sprintf("%-4s subject", tc.want)
		if !strings.Contains(body, want) {
			t.Errorf("a %s tool is not labelled %q:\n%s", tc.kind, want, body)
		}
	}
}
