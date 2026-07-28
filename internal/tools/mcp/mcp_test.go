package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

func connect(t *testing.T, name, mode string) *Session {
	t.Helper()

	session, err := Connect(context.Background(), serverSpec(name, mode))
	if err != nil {
		t.Fatalf("Connect(%s): %v", mode, err)
	}
	t.Cleanup(session.Close)
	return session
}

func only(t *testing.T, session *Session) core.Tool {
	t.Helper()

	tools := session.Tools()
	if len(tools) != 1 {
		t.Fatalf("%d tools, want 1", len(tools))
	}
	return tools[0]
}

// MCP is bidirectional and both sides number their requests from one, so a server's request and a
// client's request collide on the id constantly. Correlating on the id alone delivers the server's
// request to whoever is waiting on that number, which corrupts two calls at once: the caller gets a
// frame with no result in it, and the real reply arrives later to find nobody waiting.
//
// The server here sends a request carrying the exact id of the call that is in flight.
func TestARequestFromTheServerIsNotMistakenForTheReply(t *testing.T) {
	session := connect(t, "docs", "collides-on-ids")
	tool := only(t, session)

	result, err := tool.Run(context.Background(), json.RawMessage(`{"query":"widgets"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Content, "the real reply") {
		t.Errorf("result = %q, want the server's actual reply: a request that arrived on the same "+
			"id was handed over as though it were the answer", result.Content)
	}
}

// The other half. A server that asks something and is never answered waits, and a server waiting on
// its client has stopped serving tools. Canopy advertises no capabilities, so the honest answer is
// method not found rather than silence.
//
// The id here is a string, which the protocol permits. Decoding ids as numbers meant the whole frame
// failed to parse and was dropped, so the answer and the id are checked together.
func TestARequestFromTheServerIsAnsweredRatherThanIgnored(t *testing.T) {
	session := connect(t, "docs", "asks-for-sampling")
	tool := only(t, session)

	result, err := tool.Run(context.Background(), json.RawMessage(`{"query":"widgets"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if strings.Contains(result.Content, "nothing came back") {
		t.Fatalf("the server's request was never answered, so it would wait forever: %q", result.Content)
	}
	if !strings.Contains(result.Content, `id="srv-a"`) {
		t.Errorf("result = %q, want the string id echoed back exactly: re-encoding a correlation "+
			"identifier is how it stops matching", result.Content)
	}
	if !strings.Contains(result.Content, "code=-32601") {
		t.Errorf("result = %q, want method not found", result.Content)
	}
}

// A server that keeps handing back a fresh cursor has to be stopped, and stopping it is not the part
// that was wrong. Returning the tools gathered so far with no indication that there were more is: a
// tool the model needed is then missing, and nothing anywhere says why. A capability that is absent
// because of a bound Canopy imposed should be a configuration problem rather than a mystery.
func TestAToolListThatNeverEndsIsBoundedAndSaysWhatItLeftOut(t *testing.T) {
	session := connect(t, "docs", "endless-pages")

	note := session.Incomplete()
	if note == "" {
		t.Fatal("the page bound was hit and the session reports a complete list, so a missing tool " +
			"would look exactly like a tool the server never offered")
	}
	if !strings.Contains(note, "50") {
		t.Errorf("note = %q, want it to say what the bound was", note)
	}

	// Said where somebody will see it, not only where a test can reach it.
	if !strings.Contains(session.Describe(), "incomplete") {
		t.Errorf("Describe = %q, want it to carry the same warning", session.Describe())
	}

	// And what did arrive is still usable. Degrading to nothing would be a worse answer than
	// degrading to most of it.
	if len(session.Tools()) == 0 {
		t.Error("the tools that were read were thrown away with the ones that were not")
	}
}

func TestExactlyTheToolLimitIsStillACompleteList(t *testing.T) {
	session := connect(t, "docs", "exactly-500")
	if got := len(session.Tools()); got != maxTools {
		t.Fatalf("tools = %d, want %d", got, maxTools)
	}
	if note := session.Incomplete(); note != "" {
		t.Errorf("an exactly complete list was reported as incomplete: %q", note)
	}
}

func TestMoreThanTheToolLimitIsReportedAsIncomplete(t *testing.T) {
	session := connect(t, "docs", "over-500")
	if got := len(session.Tools()); got != maxTools {
		t.Fatalf("tools = %d, want the bounded %d", got, maxTools)
	}
	if note := session.Incomplete(); !strings.Contains(note, strconv.Itoa(maxTools)) {
		t.Errorf("incomplete note = %q, want the tool bound", note)
	}
}

func TestARepeatedCursorIsNotReportedAsACompleteList(t *testing.T) {
	session := connect(t, "docs", "repeated-cursor")
	if note := session.Incomplete(); !strings.Contains(note, "repeated cursor") {
		t.Errorf("incomplete note = %q, want the malformed cursor named", note)
	}
}

// The ordinary path, end to end through a real subprocess.
func TestAServersToolsBecomeCallableTools(t *testing.T) {
	session := connect(t, "docs", "normal")
	tool := only(t, session)

	// Namespaced, because two servers may offer the same name and because an audit entry saying
	// "search" cannot be traced back to whoever ran it.
	if tool.Name() != "mcp__docs__search" {
		t.Errorf("name = %q, want mcp__docs__search", tool.Name())
	}
	if !strings.Contains(tool.Description(), "docs") {
		t.Errorf("the description does not say where it came from: %q", tool.Description())
	}

	result, err := tool.Run(context.Background(), json.RawMessage(`{"query":"widgets"}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Content, "found widgets via search") {
		t.Errorf("result = %q", result.Content)
	}
	// The remote name goes over the wire, not the namespaced one. A server asked for a tool it has
	// never heard of returns an error, so this is load bearing rather than cosmetic.
	if strings.Contains(result.Content, "mcp__") {
		t.Errorf("the namespaced name was sent to the server: %q", result.Content)
	}
	// A part Canopy cannot pass on is named rather than dropped, so the model knows something was
	// there and does not retry expecting different content.
	if !strings.Contains(result.Content, "image") {
		t.Errorf("a non-text part vanished silently: %q", result.Content)
	}
}

// The acceptance criterion for A8-06, and the reason this package exists in the shape it does.
//
// A server describes its own tools. If those descriptions decided how strictly the tool was
// governed, the only server that would get the strict treatment is an honest one, and a hostile or
// merely wrong server would govern itself. So the server's claim is carried as text and decides
// nothing.
func TestAServerCannotLowerItsOwnPermissionRequirement(t *testing.T) {
	session := connect(t, "trustme", "claims-read-only")
	tool := only(t, session)

	if tool.Kind() != core.ToolExecute {
		t.Fatalf("kind = %q for a tool the server called read only, want %q",
			tool.Kind(), core.ToolExecute)
	}
	// The claim is still shown, because it is useful to a model choosing between tools. It just
	// does not decide anything.
	if !strings.Contains(tool.Description(), "read only") {
		t.Errorf("the server's own description of the tool was dropped: %q", tool.Description())
	}
}

// "With no exemption" is the other half of the criterion, so it is asserted against the real
// permission model rather than by reading the kind and trusting the rest.
func TestAThirdPartyToolGetsTheSameDecisionAsABuiltInOne(t *testing.T) {
	session := connect(t, "docs", "normal")
	tool := only(t, session)

	request := permission.Request{
		AgentID:   "a1",
		SessionID: "s1",
		Tool:      tool.Name(),
		Kind:      tool.Kind(),
	}

	for _, tc := range []struct {
		level core.TrustLevel
		want  permission.Outcome
	}{
		{core.TrustReadOnly, permission.Deny},
		{core.TrustConfined, permission.Deny},
		{core.TrustStandard, permission.Ask},
		{core.TrustBroad, permission.Allow},
	} {
		got := permission.Decide(request, tc.level, permission.NewGrants())
		if got.Outcome != tc.want {
			t.Errorf("%s trust: outcome = %s, want %s (%s)",
				tc.level, got.Outcome, tc.want, got.Reason)
		}
	}
}

// One server failing must cost that server and nothing else. Written with the broken one first,
// because the obvious implementation returns on the first error and would pass with it last.
func TestAFailingServerDegradesThatServerOnly(t *testing.T) {
	set := ConnectAll(context.Background(), []Spec{
		serverSpec("broken", "refuses-to-start"),
		serverSpec("working", "normal"),
	})
	t.Cleanup(set.Close)

	if len(set.Failures) != 1 || set.Failures[0].Server != "broken" {
		t.Fatalf("failures = %+v, want exactly the broken one", set.Failures)
	}
	// The reason the server gave has to survive, or the user gets an exit status and six plausible
	// causes to choose between.
	if !strings.Contains(set.Failures[0].Err.Error(), "widget backend") {
		t.Errorf("what the server printed before dying was lost: %v", set.Failures[0].Err)
	}

	tools := set.Tools()
	if len(tools) != 1 || tools[0].Name() != "mcp__working__search" {
		t.Fatalf("tools = %v, want the working server's one tool", names(tools))
	}
}

// Two servers offering the same tool name is the ordinary case, not the exotic one. Without
// namespacing the registry refuses the second and that server silently contributes nothing.
func TestTwoServersCanOfferTheSameToolName(t *testing.T) {
	set := ConnectAll(context.Background(), []Spec{
		serverSpec("alpha", "normal"),
		serverSpec("beta", "normal"),
	})
	t.Cleanup(set.Close)

	registry := core.NewToolRegistry()
	for _, tool := range set.Tools() {
		if err := registry.Register(tool); err != nil {
			t.Fatalf("Register(%s): %v", tool.Name(), err)
		}
	}
	if len(registry.Tools()) != 2 {
		t.Fatalf("registered %d tools, want 2: %v", len(registry.Tools()), names(set.Tools()))
	}
}

// A server is free to send names and schemas that no provider will accept. Every one of those has
// to cost that entry at most, because a rejected request fails the whole turn and every tool in it.
func TestAHostileToolListDoesNotPoisonTheRegistry(t *testing.T) {
	session := connect(t, "hostile", "hostile-names")

	registry := core.NewToolRegistry()
	// The built in tool the server is trying to shadow, registered first.
	registry.MustRegister(&stubTool{name: "run_command", kind: core.ToolExecute})

	for _, tool := range session.Tools() {
		if err := registry.Register(tool); err != nil {
			t.Errorf("Register(%s): %v", tool.Name(), err)
		}
		for _, r := range tool.Name() {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			default:
				t.Errorf("the tool name %q contains %q, which providers reject", tool.Name(), r)
			}
		}
		// Whatever the server sent, what reaches the provider has to be an object schema or the
		// request is rejected as a whole.
		var probe map[string]any
		if err := json.Unmarshal(tool.Schema(), &probe); err != nil {
			t.Errorf("%s has an unusable schema: %v", tool.Name(), err)
		} else if probe["type"] != "object" {
			t.Errorf("%s has schema type %v, want object", tool.Name(), probe["type"])
		}
	}

	// The nameless one cannot become a tool, and the other four can.
	if got := len(session.Tools()); got != 4 {
		t.Errorf("adapted %d tools, want 4: %v", got, names(session.Tools()))
	}
	// And the built in was not replaced.
	if tool, _ := registry.Get("run_command"); tool.Kind() != core.ToolExecute {
		t.Error("the built in run_command was displaced by the server's")
	}
	if _, shadowed := registry.Get("mcp__hostile__run_command"); !shadowed {
		t.Error("the server's run_command was not namespaced out of the way")
	}
}

// A server that dies mid session has to fail its own calls and nothing else. A Go error rather than
// a result, because the tool did not run, and the audit trail distinguishes those.
func TestAServerThatDiesFailsItsOwnCallsWithoutHanging(t *testing.T) {
	session := connect(t, "fragile", "dies-on-call")
	tool := only(t, session)

	done := make(chan error, 1)
	go func() {
		_, err := tool.Run(context.Background(), json.RawMessage(`{"query":"x"}`))
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a call to a dead server succeeded")
		}
		if !strings.Contains(err.Error(), "fragile") {
			t.Errorf("the error does not name the server: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a call to a dead server never returned")
	}
}

// A server that accepts a call and then says nothing is the failure that costs the most, because an
// agent waiting on it looks exactly like an agent thinking.
func TestASilentServerTimesOutRatherThanWaitingForever(t *testing.T) {
	spec := serverSpec("slow", "silent-on-call")
	spec.Timeout = 300 * time.Millisecond

	session, err := Connect(context.Background(), spec)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(session.Close)

	start := time.Now()
	if _, err := only(t, session).Run(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("a call to a silent server succeeded")
	} else if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("error = %v, want it to say the server did not answer", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("waited %s on a 300ms timeout", elapsed)
	}
}

// A tool result is charged again on every subsequent turn, so an unbounded one is not one large
// message, it is a large message billed repeatedly until the conversation is compacted.
func TestAFloodOfOutputIsBounded(t *testing.T) {
	session := connect(t, "loud", "floods")

	result, err := only(t, session).Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Content) > maxResultBytes+512 {
		t.Errorf("result is %d bytes against a limit of %d", len(result.Content), maxResultBytes)
	}
	// Bounded silently would leave the model acting on a truncated answer it believes is complete.
	if !strings.Contains(result.Content, "truncated") && !strings.Contains(result.Content, "dropped") {
		t.Error("the result was cut without saying so")
	}
}

// A tool that ran and reported a failure is a result, not an error. The model can act on it, and
// the audit trail should say it ran.
func TestAToolThatFailsIsAResultRatherThanAnError(t *testing.T) {
	session := connect(t, "empty", "fails")

	result, err := only(t, session).Run(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("a tool that ran and failed produced a Go error: %v", err)
	}
	if !result.IsError {
		t.Error("the failure was reported as a success")
	}
	if !strings.Contains(result.Content, "no such record") {
		t.Errorf("what the server said was lost: %q", result.Content)
	}
}

// Quitting must not leave a server running. The same property A9-01 asserts for test commands, and
// it matters more here because these processes are started for the life of a session.
// A server is very often a launcher rather than the program: `npx` in front of node, a wrapper
// script in front of a runtime. Waiting on the process Canopy started says nothing about the process
// that one started, and those were left running for the rest of the machine's uptime, holding
// whatever they had open.
func TestClosingASessionStopsWhatTheServerStartedToo(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")

	spec := serverSpec("launcher", "spawns-a-child")
	spec.Env = append(spec.Env, pidFileEnv+"="+pidFile)

	session, err := Connect(context.Background(), spec)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	var child int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if child, err = strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child == 0 {
		session.Close()
		t.Skip("the server never reported a child pid")
	}
	if !stillRunning(child) {
		session.Close()
		t.Skip("the child was gone before the test began, so this proves nothing")
	}

	server := session.cmd.Process.Pid
	session.Close()

	// Cleanup regardless, so a failure here does not leave a process behind for the next run.
	t.Cleanup(func() {
		if process, err := os.FindProcess(child); err == nil {
			_ = process.Kill()
		}
	})

	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !stillRunning(child) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the server (pid %d) was stopped but the process it started (pid %d) is still running",
		server, child)
}

func TestClosingASessionStopsTheServer(t *testing.T) {
	session := connect(t, "docs", "normal")
	pid := session.cmd.Process.Pid

	session.Close()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !stillRunning(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the server (pid %d) is still running after Close", pid)
}

// Close is called both by the failure path in Connect and by ordinary teardown, and neither should
// have to know whether the other already did.
func TestCloseIsSafeToCallTwice(t *testing.T) {
	session := connect(t, "docs", "normal")
	session.Close()
	session.Close()
}

// A handshake that never completes must not hold up starting an agent.
func TestAServerThatNeverFinishesTheHandshakeIsGivenUpOn(t *testing.T) {
	if testing.Short() {
		t.Skip("this one waits out the start timeout")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	session, err := Connect(ctx, serverSpec("mute", "silent-handshake"))
	if err == nil {
		session.Close()
		t.Fatal("a server that never answered the handshake connected")
	}
	if elapsed := time.Since(start); elapsed > startTimeout {
		t.Errorf("waited %s, longer than the start timeout", elapsed)
	}
}

// names is for failure messages, so a mismatch says which tools were there.
func names(tools []core.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tool := range tools {
		out = append(out, tool.Name())
	}
	return out
}

// stubTool stands in for a built in, to prove a server cannot displace one.
type stubTool struct {
	name string
	kind core.ToolKind
}

func (s *stubTool) Name() string            { return s.name }
func (s *stubTool) Description() string     { return "a built in tool" }
func (s *stubTool) Kind() core.ToolKind     { return s.kind }
func (s *stubTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *stubTool) Run(context.Context, json.RawMessage) (core.ToolResult, error) {
	return core.ToolResult{Content: "built in"}, nil
}
