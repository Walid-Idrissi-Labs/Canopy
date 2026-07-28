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
	spec  Spec
	cmd   *exec.Cmd
	rpc   *client
	tools []core.Tool

	// serverInfo and negotiated are what the server said it was, for display and for diagnosing a
	// version mismatch. Neither is trusted for anything that affects permissions.
	serverInfo serverInfo
	negotiated string

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
		rpc:    newClient(stdout, stdin),
		stderr: stderr,
	}

	if err := session.handshake(startCtx); err != nil {
		session.Close()
		return nil, session.explain(err)
	}

	descriptors, err := session.list(startCtx)
	if err != nil {
		session.Close()
		return nil, session.explain(fmt.Errorf("could not list the tools on %q: %w", spec.Name, err))
	}

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

// list walks every page of tools/list.
//
// Paginated because the protocol is, and a server with more tools than fit in one page would
// otherwise contribute the first page and silently lose the rest.
func (s *Session) list(ctx context.Context) ([]descriptor, error) {
	var (
		all    []descriptor
		cursor string
	)
	// Bounded rather than "until nextCursor is empty", because a server that returns the cursor it
	// was given is an infinite loop in our process, not in theirs.
	for page := 0; page < 50; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}

		raw, err := s.rpc.call(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}

		var result toolsListResult
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("the tool list was unreadable: %w", err)
		}
		all = append(all, result.Tools...)

		if result.NextCursor == "" || result.NextCursor == cursor {
			return all, nil
		}
		cursor = result.NextCursor
	}
	return all, nil
}

// Tools returns what this server offers, already adapted and already namespaced.
func (s *Session) Tools() []core.Tool { return s.tools }

// Name is the configured name, not the one the server calls itself.
//
// The configured name is the one in the audit trail and the one the user wrote, and letting a
// server rename itself after connecting would mean the trail says something the config file does
// not.
func (s *Session) Name() string { return s.spec.Name }

// Describe is one line about what is on the other end, for the interface.
func (s *Session) Describe() string {
	info := s.serverInfo.Name
	if info == "" {
		info = s.spec.Command
	}
	if s.serverInfo.Version != "" {
		info += " " + s.serverInfo.Version
	}
	return fmt.Sprintf("%s (%s, protocol %s, %d tools)", s.spec.Name, info, s.negotiated, len(s.tools))
}

// Close stops the server.
//
// Closing stdin first is the graceful path and is what lets a well behaved server clean up. The
// context cancel and WaitDelay behind it are for the one that does not, so shutdown is bounded
// either way. Idempotent, because both the failure path in Connect and the ordinary teardown call
// it and neither should have to know whether the other already did.
func (s *Session) Close() {
	s.closeOnce.Do(func() {
		s.rpc.close()

		done := make(chan struct{})
		go func() {
			_ = s.cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			// It ignored end of file on stdin. Nothing else to try that is still polite.
			if s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
			<-done
		}
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
