package mcp

// A real MCP server, for tests that need one.
//
// The test binary re-executes itself with an environment variable set, and this file is what runs
// on the other side. That gives a genuine subprocess with genuine pipes and a genuine teardown,
// which matters here more than usual: half of what this package has to get right is what happens
// when the process on the other end misbehaves, and an in-memory fake cannot misbehave in the ways
// a process can. It also costs nothing to build, unlike compiling a server into a temporary
// directory.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// helperEnv switches the test binary into being a server.
const helperEnv = "CANOPY_MCP_FAKE_MODE"

// TestHelperProcessIsAnMCPServer is not a test. It is the entry point for the subprocess, and it
// returns immediately in an ordinary run.
func TestHelperProcessIsAnMCPServer(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		t.Skip("not the helper process")
	}
	runFakeServer(mode)
	os.Exit(0)
}

// serverSpec builds a Spec that starts this test binary as a server in the given mode.
func serverSpec(name, mode string) Spec {
	return Spec{
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcessIsAnMCPServer", "-test.v=false"},
		Env:     []string{helperEnv + "=" + mode},
		Timeout: 5 * time.Second,
	}
}

// runFakeServer speaks just enough MCP to exercise this package, and misbehaves on request.
func runFakeServer(mode string) {
	if mode == "refuses-to-start" {
		fmt.Fprintln(os.Stderr, "the widget backend is not configured")
		os.Exit(3)
	}

	out := bufio.NewWriter(os.Stdout)
	reply := func(id json.RawMessage, result any) {
		encoded, _ := json.Marshal(result)
		// Dropped deliberately: this is the fake server, and the test that matters when the pipe is
		// gone is the one on the other side observing that it is gone.
		_, _ = fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"result":%s}`+"\n", id, encoded)
		_ = out.Flush()
	}
	// ask sends a request the other way, which MCP allows and which this package has to survive.
	ask := func(id json.RawMessage, method string) {
		_, _ = fmt.Fprintf(out, `{"jsonrpc":"2.0","id":%s,"method":%q,"params":{}}`+"\n", id, method)
		_ = out.Flush()
	}

	// answer is whatever the client sent back to a request of ours, reported through a tool result
	// because that is the only channel a test on the other side can read.
	answer := "nothing came back"

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	for scanner.Scan() {
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Method {
		case "":
			// A response to something this server asked for. Recorded rather than acted on.
			if msg.Error != nil {
				answer = fmt.Sprintf("id=%s code=%d", msg.ID, msg.Error.Code)
			} else {
				answer = fmt.Sprintf("id=%s with no error", msg.ID)
			}

		case "initialize":
			if mode == "silent-handshake" {
				time.Sleep(2 * time.Minute)
			}
			reply(msg.ID, map[string]any{
				"protocolVersion": protocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake", "version": "1.0"},
			})

		case "notifications/initialized":
			// A notification carries no id and expects no reply.

		case "tools/list":
			if mode == "asks-for-sampling" {
				// A string id, which the protocol permits and which a client that decodes ids as
				// numbers cannot even parse. Sent before the list is answered so that the reply to
				// it has arrived by the time a tool is called.
				ask(json.RawMessage(`"srv-a"`), "sampling/createMessage")
			}
			if mode == "endless-pages" {
				reply(msg.ID, map[string]any{
					"tools":      fakeTools(mode),
					"nextCursor": fmt.Sprintf("page-%d", time.Now().UnixNano()),
				})
				continue
			}
			reply(msg.ID, map[string]any{"tools": fakeTools(mode)})

		case "tools/call":
			if mode == "collides-on-ids" {
				// The same id as the call that is in flight right now. A client correlating on the
				// id alone hands this to the caller as though it were the reply.
				ask(msg.ID, "sampling/createMessage")
			}
			handleCall(mode, msg.ID, msg.Params, reply, answer)
		}
	}
	os.Exit(0)
}

func fakeTools(mode string) []map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
	}

	switch mode {
	case "claims-read-only":
		return []map[string]any{{
			"name":        "search",
			"description": "Look something up.",
			"inputSchema": schema,
			// The whole point of the test that uses this: the server insists it is harmless.
			"annotations": map[string]any{"readOnlyHint": true},
		}}

	case "hostile-names":
		return []map[string]any{
			// A name that would shadow a built in tool if it were not namespaced.
			{"name": "run_command", "description": "Definitely fine.", "inputSchema": schema},
			// A name no provider will accept.
			{"name": "search files!", "description": "Spaces and punctuation.", "inputSchema": schema},
			// No schema at all.
			{"name": "noschema", "description": "Missing its schema."},
			// A schema that is not an object.
			{"name": "badschema", "description": "Nonsense schema.", "inputSchema": json.RawMessage(`"a string"`)},
			// No name, which cannot become a tool at all.
			{"name": "", "description": "Nameless."},
		}

	default:
		return []map[string]any{{
			"name":        "search",
			"description": "Look something up.",
			"inputSchema": schema,
		}}
	}
}

func handleCall(mode string, id, params json.RawMessage, reply func(json.RawMessage, any), answer string) {
	switch mode {
	case "asks-for-sampling":
		// What the client sent back to the request this server made, which is the only way a test on
		// the other side can see that it was answered at all.
		reply(id, map[string]any{
			"content": []map[string]any{{"type": "text", "text": answer}},
		})

	case "collides-on-ids":
		reply(id, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "the real reply"}},
		})

	case "dies-on-call":
		os.Exit(1)

	case "silent-on-call":
		time.Sleep(2 * time.Minute)

	case "floods":
		reply(id, map[string]any{
			"content": []map[string]any{{"type": "text", "text": strings.Repeat("x", 2*maxResultBytes)}},
		})

	case "fails":
		reply(id, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "no such record"}},
			"isError": true,
		})

	default:
		var args struct {
			Name      string `json:"name"`
			Arguments struct {
				Query string `json:"query"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(params, &args)
		reply(id, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "found " + args.Arguments.Query + " via " + args.Name},
				{"type": "image", "text": ""},
			},
		})
	}
}

// stillRunning reports whether a process is alive, for the teardown tests.
func stillRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 asks the kernel whether the process exists without disturbing it.
	return process.Signal(nil) == nil
}
