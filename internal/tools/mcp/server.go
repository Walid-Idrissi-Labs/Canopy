package mcp

// Starting a server, shaking hands, and asking what it can do.
//
// The failure handling here is the point of the file. A server is somebody else's program and it
// will fail in every way a program can: not installed, installed and crashing, starting and never
// answering, answering and then dying. Each of those has to end with Canopy still working and the
// user told which server broke and how, because "no tools appeared" with no explanation is
// indistinguishable from having configured nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	execpkg "github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// protocolVersion is what Canopy asks for during the handshake.
//
// The server replies with the version it will actually speak, which may be older, and Canopy
// accepts that rather than refusing. Refusing on a mismatch would mean a Canopy release stops
// talking to servers that work perfectly well the day somebody publishes a new revision of the
// specification, and the parts used here, initialize, tools/list and tools/call, have been stable
// across every revision so far. The negotiated version is recorded so a genuine incompatibility is
// diagnosable rather than mysterious.
const protocolVersion = "2025-06-18"

// defaultTimeout bounds the handshake and each call when a server does not set one.
//
// A server that accepts a request and then goes quiet is the failure that costs the most, because
// an agent waiting on it looks exactly like an agent thinking. The same argument as the provider
// stall watchdog in A2-06, and the same order of magnitude.
const defaultTimeout = 60 * time.Second

// startTimeout bounds getting to a usable connection, separately from a call.
const startTimeout = 30 * time.Second

// maxStderrBytes bounds what is kept from a server's diagnostics.
//
// Kept at all because it is almost always the actual explanation: a server that cannot start prints
// why to stderr and exits, and without this the user gets "exit status 1".
const maxStderrBytes = 8 * 1024

// Spec is one server to start.
//
// Deliberately not config.MCPServer. This package should be usable without a project file, and a
// dependency from the tool layer to the configuration layer is one more edge than the job needs.
type Spec struct {
	Name    string
	Command string
	Args    []string
	Env     []string
	Dir     string
	Timeout time.Duration
}

// Session is a live connection to one server.
type Session struct {
	spec Spec
	cmd  *exec.Cmd

	// child owns the reap, which is what makes stopping the server's own children safe. See D-37.
	child *execpkg.Child

	rpc   *client
	tools []core.Tool

	// serverInfo and negotiated are what the server said it was, for display and for diagnosing a
	// version mismatch. Neither is trusted for anything that affects permissions.
	serverInfo serverInfo
	negotiated string

	// incomplete says what the tool list left out, and is empty when it left out nothing.
	incomplete string

	stderr *boundedBuffer

	closeOnce sync.Once
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ServerInfo      serverInfo      `json:"serverInfo"`
	Instructions    string          `json:"instructions"`
}

type toolsListResult struct {
	Tools      []descriptor `json:"tools"`
	NextCursor string       `json:"nextCursor"`
}

// descriptor is a tool as the server describes it.
//
// Annotations are read and shown and never acted on. See kindFor in tool.go.
type descriptor struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Annotations struct {
		ReadOnlyHint    bool `json:"readOnlyHint"`
		DestructiveHint bool `json:"destructiveHint"`
	} `json:"annotations"`
}

// Connect starts a server and returns its tools.
//
// An error means this server is unusable and contributes nothing. It never means Canopy is
// unusable, which is why the caller is ConnectAll rather than anything that would propagate.
func Connect(ctx context.Context, spec Spec) (*Session, error) {
	if spec.Name == "" {
		return nil, fmt.Errorf("an MCP server needs a name")
	}
	if spec.Command == "" {
		return nil, fmt.Errorf("the MCP server %q has no command", spec.Name)
	}

	// Bounded separately from the caller's context. A server that hangs during the handshake must
	// not hold up starting an agent, and the caller's context usually has no deadline at all.
	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()

	// The process outlives this function, so it is tied to the background rather than to startCtx,
	// which is about to expire. Close is what ends it.
	cmd := exec.Command(spec.Command, spec.Args...)
	cmd.Dir = spec.Dir
	if len(spec.Env) > 0 {
		cmd.Env = append(os.Environ(), spec.Env...)
	}
	// Without this, a server that keeps its stdout open after being asked to stop makes Wait block
	// forever and the shutdown path never returns.
	cmd.WaitDelay = 5 * time.Second

	// Its own process group, so that what the server started can be stopped with it. A server is
	// very often a launcher rather than the program itself, and killing `npx` does nothing to node.
	execpkg.Contain(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("could not open a pipe to %q: %w", spec.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("could not open a pipe from %q: %w", spec.Name, err)
	}
	stderr := &boundedBuffer{limit: maxStderrBytes}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		// The common case by a distance: the command is not installed. Say so in those words,
		// because "fork/exec: no such file or directory" sends people looking at the wrong thing.
		return nil, fmt.Errorf("could not start %q (%s): %w", spec.Name, spec.Command, err)
	}

	session := &Session{
		spec:   spec,
		cmd:    cmd,
		child:  execpkg.Started(cmd),
		rpc:    newClient(stdout, stdin),
		stderr: stderr,
	}

	if err := session.handshake(startCtx); err != nil {
		session.Close()
		return nil, session.explain(err)
	}

	descriptors, incomplete, err := session.list(startCtx)
	if err != nil {
		session.Close()
		return nil, session.explain(fmt.Errorf("could not list the tools on %q: %w", spec.Name, err))
	}

	session.incomplete = incomplete
	session.tools = adapt(session, descriptors)
	return session, nil
}

// handshake performs initialize and the notification that follows it.
func (s *Session) handshake(ctx context.Context) error {
	raw, err := s.rpc.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		// Nothing is claimed that is not implemented. A client advertising sampling and then
		// refusing every sampling request is worse than one that never offered.
		"capabilities": map[string]any{},
		"clientInfo":   map[string]any{"name": "canopy", "version": "0.1"},
	})
	if err != nil {
		return fmt.Errorf("the handshake with %q failed: %w", s.spec.Name, err)
	}

	var result initializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("%q answered the handshake with something unreadable: %w", s.spec.Name, err)
	}
	s.serverInfo = result.ServerInfo
	s.negotiated = result.ProtocolVersion

	// Sent, not asked. The protocol requires it before any other request, and a server that is
	// waiting for it will answer tools/list with nothing or with an error.
	if err := s.rpc.notify("notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("could not finish the handshake with %q: %w", s.spec.Name, err)
	}
	return nil
}

// maxToolPages and maxTools bound what one server can contribute.
//
// Both are needed and they bound different things. The page count stops a server that keeps handing
// back a fresh cursor, which is an infinite loop in our process rather than in theirs. The tool count
// stops a server that terminates its pagination honestly and simply offers thousands of tools, which
// is not a loop but is every one of those definitions in every request for the rest of the
// conversation.
const (
	maxToolPages = 50
	maxTools     = 500
)

// list walks every page of tools/list.
//
// Paginated because the protocol is, and a server with more tools than fit in one page would
// otherwise contribute the first page and silently lose the rest.
//
// Returns a note describing what was left out, empty when the list is complete. Silence here was the
// actual defect: hitting the page bound returned the tools gathered so far and no indication that
// there were more, so a tool the model needed was missing and nothing anywhere said why. A missing
// capability that reports itself is a configuration problem; one that does not is a mystery.
func (s *Session) list(ctx context.Context) ([]descriptor, string, error) {
	var (
		all    []descriptor
		cursor string
	)
	for page := 0; page < maxToolPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}

		raw, err := s.rpc.call(ctx, "tools/list", params)
		if err != nil {
			return nil, "", err
		}

		var result toolsListResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, "", fmt.Errorf("the tool list was unreadable: %w", err)
		}
		all = append(all, result.Tools...)

		if len(all) >= maxTools {
			return all[:maxTools], fmt.Sprintf(
				"the server offers more than %d tools and the rest were not read", maxTools), nil
		}
		if result.NextCursor == "" || result.NextCursor == cursor {
			return all, "", nil
		}
		cursor = result.NextCursor
	}
	return all, fmt.Sprintf(
		"the server was still offering more after %d pages, so the rest were not read",
		maxToolPages), nil
}

// Tools returns what this server offers, already adapted and already namespaced.
func (s *Session) Tools() []core.Tool { return s.tools }

// Name is the configured name, not the one the server calls itself.
//
// The configured name is the one in the audit trail and the one the user wrote, and letting a
// server rename itself after connecting would mean the trail says something the config file does
// not.
func (s *Session) Name() string { return s.spec.Name }

// Incomplete says what this server's tool list left out, and is empty when it left out nothing.
//
// Read by the caller that started the server, so that a bound Canopy imposed is reported to the
// person who configured it rather than absorbed. A tool that is missing because of a limit here looks
// from the model's side exactly like a tool the server never offered.
func (s *Session) Incomplete() string { return s.incomplete }

// Describe is one line about what is on the other end, for the interface.
func (s *Session) Describe() string {
	info := s.serverInfo.Name
	if info == "" {
		info = s.spec.Command
	}
	if s.serverInfo.Version != "" {
		info += " " + s.serverInfo.Version
	}

	line := fmt.Sprintf("%s (%s, protocol %s, %d tools)",
		s.spec.Name, info, s.negotiated, len(s.tools))
	if s.incomplete != "" {
		line += ", incomplete: " + s.incomplete
	}
	return line
}

// politeExit is how long a server gets to leave on its own after its stdin closes, before the group
// it leads is signalled.
//
// Short, because a stdio server sees end of file and exits immediately. It is a fixed cost on every
// shutdown, which is why Set.Close stops its servers concurrently: the cost is paid once rather than
// once per server.
const politeExit = 250 * time.Millisecond

// Close stops the server and everything the server started.
//
// Closing stdin first is the graceful path and is what lets a well behaved server clean up. What
// follows is the part that was missing: a server is frequently a launcher, `npx` in front of node or
// a wrapper script in front of a runtime, and waiting on the process we started says nothing about
// the process it started. Those were left running, holding whatever they had open, for the rest of
// the machine's uptime.
//
// The ordering is the whole of it, and it is not the obvious one. Waiting first and signalling only
// if the wait times out cannot work here: stdout is a pipe this package owns rather than something
// Go copies in the background, so nothing holds Wait open and it returns the moment the leader exits,
// children or no children. By then the leader has been reaped and its group can no longer be
// addressed at all, per D-37. So the group is signalled first, while the leader is still unreaped and
// its process group id is therefore still its own, and only then is it waited on.
//
// Idempotent, because both the failure path in Connect and the ordinary teardown call this and
// neither should have to know whether the other already did.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		// End of file on stdin, which is the protocol's own way of saying goodbye and the only signal
		// a well behaved server should need.
		s.rpc.close()
		time.Sleep(politeExit)

		// SIGTERM to the group, and SIGKILL to it after the grace period. A server that has already
		// gone leaves a group holding nothing but its own unreaped leader, so this reaches exactly
		// the children that outlived it and nothing else.
		s.child.Stop()

		_ = s.child.Wait()
	})
}

// explain adds whatever the server printed before dying.
//
// Almost always the real explanation, and without it the user gets an exit status and no idea
// which of the six plausible causes it was.
func (s *Session) explain(err error) error {
	diagnostics := strings.TrimSpace(s.stderr.String())
	if diagnostics == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, oneLine(diagnostics))
}

func oneLine(text string) string {
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}
