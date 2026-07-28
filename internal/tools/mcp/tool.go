package mcp

// Presenting somebody else's tool as one of ours.
//
// This is where the acceptance criterion for A8-06 is actually met, and it is met by construction
// rather than by a check somebody could forget to write. The permission model in internal/permission
// decides entirely on core.ToolKind and never on a tool's name, so a tool that reports a kind is
// governed by that kind with no route around it. What this file has to get right is the kind.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// maxResultBytes bounds what one call can return into the model's context.
//
// A tool result goes into the transcript and back to the provider on every subsequent turn, so an
// unbounded one is not a single large message, it is a large message charged again on every step
// until the conversation is compacted.
const maxResultBytes = 256 * 1024

// namePrefix marks a tool as coming from a server rather than from Canopy.
//
// Every tool name is namespaced with it and with the server's configured name, for three reasons
// that all bite. Two servers may both offer `search`, and the registry refuses duplicate names, so
// without a namespace the second server silently contributes nothing. A server may offer a tool
// called `run_command`, and shadowing a built in would be a way to replace a governed tool with an
// unaudited one. And an audit trail entry saying `search` cannot be traced back to whoever ran it.
const namePrefix = "mcp"

// remoteTool is one tool on one server.
type remoteTool struct {
	session *Session
	// remote is the name the server knows it by, which is what goes back over the wire. Kept
	// separately from the namespaced name the model sees.
	remote      string
	name        string
	description string
	schema      json.RawMessage
}

// adapt turns what a server reported into registrable tools.
//
// A descriptor that cannot be made into a usable tool is dropped rather than failing the server,
// because one malformed entry in a list of twenty is not a reason to lose the other nineteen.
func adapt(session *Session, descriptors []descriptor) []core.Tool {
	out := make([]core.Tool, 0, len(descriptors))
	seen := map[string]bool{}

	for _, d := range descriptors {
		if strings.TrimSpace(d.Name) == "" {
			continue
		}
		name := toolName(session.spec.Name, d.Name)
		if len(name) > 128 || seen[name] {
			// Over the length every provider enforces, or a duplicate the server sent twice.
			continue
		}
		seen[name] = true

		out = append(out, &remoteTool{
			session:     session,
			remote:      d.Name,
			name:        name,
			description: describe(session.spec.Name, d),
			schema:      usableSchema(d.InputSchema),
		})
	}
	return out
}

// toolName namespaces a server's tool and strips anything a provider will not accept.
//
// Providers constrain tool names to letters, digits, underscores and dashes. A server is free to
// send something outside that, and the request would then be rejected as a whole, which fails every
// tool in the turn rather than the one with the bad name.
func toolName(server, remote string) string {
	var b strings.Builder
	b.WriteString(namePrefix)
	b.WriteString("__")
	b.WriteString(sanitize(server))
	b.WriteString("__")
	b.WriteString(sanitize(remote))
	return b.String()
}

func sanitize(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// describe is the prompt text the model sees.
//
// It names the server, because a model choosing between two tools that both search should know one
// is the project's own index and the other is somebody's web service. The server's own annotations
// are reported here and nowhere else: they are useful context for a model and they are not evidence,
// so this is the only place they are allowed to have any effect at all.
func describe(server string, d descriptor) string {
	text := strings.TrimSpace(d.Description)
	if text == "" {
		text = strings.TrimSpace(d.Title)
	}
	if text == "" {
		text = "A tool provided by the " + server + " server."
	}

	suffix := " (provided by the MCP server " + server
	switch {
	case d.Annotations.DestructiveHint:
		suffix += ", which describes this tool as destructive"
	case d.Annotations.ReadOnlyHint:
		suffix += ", which describes this tool as read only"
	}
	return text + suffix + ")"
}

// usableSchema returns something the provider will accept.
//
// A server that sends no schema, or one that is not a JSON object, would otherwise produce a
// request the provider rejects outright, and a rejected request fails the whole turn rather than
// this one tool. Substituting an open object is the degradation that keeps the rest working: the
// model can still call it, and the server is still the thing validating its own arguments.
func usableSchema(raw json.RawMessage) json.RawMessage {
	open := json.RawMessage(`{"type":"object","properties":{},"additionalProperties":true}`)
	if len(raw) == 0 {
		return open
	}

	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return open
	}
	if probe["type"] != "object" {
		return open
	}
	return raw
}

func (t *remoteTool) Name() string        { return t.name }
func (t *remoteTool) Description() string { return t.description }

func (t *remoteTool) Schema() json.RawMessage { return t.schema }

// Kind is the load bearing decision in this package, so the reasoning is here rather than in a
// commit message.
//
// Every MCP tool is core.ToolExecute, whatever the server says about it. That kind is defined as
// the broadest one there is, because a command can do anything the user can, and an opaque
// capability from a program we did not write is exactly that: Canopy cannot see what it does, only
// what it is called.
//
// MCP lets a server annotate its own tools with readOnlyHint and destructiveHint, and the tempting
// thing is to map those onto kinds so a documentation lookup does not need approval. That is a
// trap. It lets the party being governed choose how strictly it is governed, which means the only
// server that gets the strict treatment is an honest one. A tool that declares itself read only and
// then writes is not a hypothetical; it is the supply chain version of the false green this whole
// project is built to refuse. The hint is shown to the user and to the model, and it decides
// nothing.
//
// What this costs: read-only and confined agents get no MCP tools at all, because ToolExecute is
// structurally denied below standard trust. Standard sees every call before it runs. Broad runs
// them without asking, which is what broad means. That is a real cost and it is the right side to
// be wrong on.
func (t *remoteTool) Kind() core.ToolKind { return core.ToolExecute }

// Run calls the tool on its server.
//
// A failure to reach the server is a Go error rather than a result, which is the distinction the
// audit trail depends on: the tool did not run, and recording it as one that ran and failed would
// be the same class of mistake as the confinement refusal that A4-04 fixed. A tool that ran and
// reported a failure comes back as a result, because that is something the model can act on.
func (t *remoteTool) Run(ctx context.Context, input json.RawMessage) (core.ToolResult, error) {
	timeout := t.session.spec.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	arguments := json.RawMessage(`{}`)
	if len(input) > 0 {
		arguments = input
	}

	raw, err := t.session.rpc.call(ctx, "tools/call", map[string]any{
		"name":      t.remote,
		"arguments": arguments,
	})
	if err != nil {
		if ctx.Err() != nil {
			return core.ToolResult{}, fmt.Errorf(
				"the MCP server %q did not answer within %s", t.session.spec.Name, timeout)
		}
		return core.ToolResult{}, fmt.Errorf(
			"the MCP server %q: %w", t.session.spec.Name, err)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return core.ToolResult{}, fmt.Errorf(
			"the MCP server %q answered with something unreadable: %w", t.session.spec.Name, err)
	}

	return core.ToolResult{
		Content: renderContent(result.Content),
		IsError: result.IsError,
	}, nil
}

// renderContent flattens the content array into text.
//
// Only text parts are rendered. Images and embedded resources are named rather than dropped
// silently, because a model told nothing came back will retry, and a model told an image came back
// that Canopy cannot pass on will do something else.
func renderContent(parts []struct {
	Type string `json:"type"`
	Text string `json:"text"`
},
) string {
	if len(parts) == 0 {
		return "the tool returned no content"
	}

	var b strings.Builder
	for _, part := range parts {
		if b.Len() >= maxResultBytes {
			fmt.Fprintf(&b, "\n\n[the rest was dropped at %d bytes]", maxResultBytes)
			break
		}
		switch part.Type {
		case "text":
			b.WriteString(part.Text)
			b.WriteString("\n")
		default:
			fmt.Fprintf(&b, "[a %s part, which Canopy cannot pass on]\n", part.Type)
		}
	}

	text := strings.TrimRight(b.String(), "\n")
	if len(text) > maxResultBytes {
		return text[:maxResultBytes] + fmt.Sprintf("\n\n[truncated at %d bytes]", maxResultBytes)
	}
	return text
}
