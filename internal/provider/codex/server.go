package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	canopyexec "github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// Originator is the name Canopy gives the app server for itself, and it is the whole ethical
// surface of this route.
//
// The app server takes this value and makes it the originator it sends upstream, so it is not a
// label for a log, it is who OpenAI is told is calling. It must be Canopy's own name. Sending
// `codex_cli_rs`, or any other client's, would be impersonating a product to obtain its treatment,
// which is the one behaviour OpenAI's terms plausibly reach and the one thing none of the
// established projects on this path do. A route chosen because it is defensible stops being
// defensible the moment it lies about who is calling.
//
// OpenAI ask integrations intended for enterprise use to contact them to be added to a list of
// known clients. That is a thing for a person to do rather than a thing to build, and it is
// recorded in the phase's documentation task rather than here.
const Originator = "canopy"

// process is a running app server and the three streams that matter.
type process struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *bounded

	// stop ends the app server and everything it started. Safe to call more than once.
	stop func()
}

// session is one connection to a running app server.
//
// One process per thing Canopy wants, rather than one long-lived server. A sign-in and a turn are
// unrelated pieces of work with different lifetimes, and a shared process would mean a turn holding
// the connection a cancelled sign-in is trying to close. The cost is a process start, which on this
// route is dwarfed by the model call it precedes.
type session struct {
	conn  *conn
	child *process

	// userAgent is what the app server said it will identify Canopy as upstream, read back from the
	// handshake rather than assumed. See checkIdentity.
	userAgent string

	closeOnce sync.Once
}

// launcher starts an app server. Replaced in tests by an in-process server, which is what lets every
// test here run on a machine with no Codex and no ChatGPT subscription.
type launcher func(ctx context.Context) (*process, error)

// start brings an app server up and completes the handshake.
func start(ctx context.Context, launch launcher, version string) (*session, error) {
	child, err := launch(ctx)
	if err != nil {
		return nil, err
	}

	s := &session{conn: newConn(child.stdout, child.stdin), child: child}
	if err := s.handshake(version); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// handshake says who Canopy is and checks what came back.
//
// The check is the part worth reading. The app server composes the upstream user agent out of the
// name it was just given, and returns it, so Canopy can read back what it is about to be identified
// as instead of trusting that the field it filled in was the field that mattered. If what comes back
// does not lead with Canopy's own name then something between here and the wire has substituted an
// identity, and the honest response is to stop rather than to run a turn under a name Canopy did not
// choose. It has never fired in practice, which is the point: the promise in this package's doc
// comment is worth what it can be checked for.
func (s *session) handshake(version string) error {
	id, err := s.conn.send(methodInitialize, initializeParams{
		ClientInfo: clientInfo{
			Name: Originator,
			// A version beside the name, because the app server folds both into what it sends
			// upstream and backend model routing has been observed to resolve differently for a
			// caller that named no version. Canopy's own build, never Codex's.
			Version: version,
			Title:   "Canopy",
		},
		// Every capability off. experimentalApi opts into methods whose shape may change without
		// notice, and requestAttestation opts into being asked to produce an upstream attestation
		// header, which is not something Canopy has any business generating on somebody's behalf.
		Capabilities: &initializeCapabilities{},
	})
	if err != nil {
		return err
	}

	raw, err := s.await(id)
	if err != nil {
		return s.startupError(err)
	}

	var result initializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("the Codex app server answered the handshake with something unreadable: %w", err)
	}
	if err := checkIdentity(result.UserAgent); err != nil {
		return err
	}
	s.userAgent = result.UserAgent

	// The protocol's own second step, and the app server holds some work until it arrives. A
	// notification rather than a request, so there is nothing to wait for.
	return s.conn.notify(methodInitialized, struct{}{})
}

// checkIdentity holds Canopy to its own name.
//
// The user agent the app server composes leads with the originator it will send, so the test is
// whether that first token is Canopy's. An empty user agent is accepted, because a version of the
// app server that does not report one is a version this build has not seen rather than one that is
// lying, and refusing to run against it would be treating silence as evidence.
func checkIdentity(userAgent string) error {
	if userAgent == "" {
		return nil
	}
	leading := userAgent
	if slash := strings.IndexAny(leading, "/ "); slash >= 0 {
		leading = leading[:slash]
	}
	if leading == Originator {
		return nil
	}
	return fmt.Errorf(
		"the Codex app server says it will identify this client to OpenAI as %q, and Canopy asked to "+
			"be identified as %q. Canopy will not run a turn under another client's name, so this "+
			"stops here", leading, Originator)
}

// startupError turns a dead app server into a sentence about the app server.
//
// A process that exits during the handshake closes its pipes, so what arrives here is EOF, which
// says nothing at all. Whatever it printed on the way out is what actually explains it.
func (s *session) startupError(err error) error {
	if said := lastLine(s.child.stderr.String()); said != "" {
		return fmt.Errorf("the Codex app server did not start: %s", said)
	}
	if errors.Is(err, io.EOF) {
		return fmt.Errorf(
			"the Codex app server stopped without answering the handshake and without saying why. " +
				"Check that `codex app-server` runs on its own")
	}
	return fmt.Errorf("talking to the Codex app server: %w", err)
}

// call sends a request and waits for its answer, serving whatever else arrives on the way.
//
// Every request in this package waits for its response before the next one is sent. That is a rule
// about the transport rather than about politeness: stdio pipes have a fixed buffer, and a client
// that keeps writing while answers pile up unread deadlocks against a server doing the same thing.
func (s *session) call(method string, params any, out any) error {
	id, err := s.conn.send(method, params)
	if err != nil {
		return err
	}
	raw, err := s.await(id)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("the Codex app server answered %s unreadably: %w", method, err)
	}
	return nil
}

// await reads until the answer to one request arrives.
//
// Anything else that turns up is handled by the session's own rule, which is to answer a server
// request Canopy cannot honestly serve and otherwise ignore. A turn overrides this by reading the
// stream itself; see stream.go.
func (s *session) await(id int64) (json.RawMessage, error) {
	for {
		m, err := s.conn.read()
		if err != nil {
			return nil, err
		}
		if m.ID != nil && *m.ID == id && m.Method == "" {
			if m.Error != nil {
				return nil, m.Error
			}
			return m.Result, nil
		}
		s.decline(m)
	}
}

// decline answers a request the app server sent outside a turn.
//
// Nothing outside a turn is a request Canopy has anything to say about, so it is answered as a
// method that is not there, which is the truthful answer rather than a stub that pretends.
func (s *session) decline(m message) {
	if m.ID != nil && m.Method != "" {
		_ = s.conn.replyError(*m.ID, methodNotFound, fmt.Sprintf(
			"canopy did not ask for %s and has nothing to answer it with", m.Method))
	}
}

// Close ends the app server and the process behind it.
//
// Required even after a clean exchange, because what is being released is a child process rather
// than a socket. An app server nobody stopped keeps running, holding whatever it started: on this
// route that includes the MCP servers named in the user's own config.toml.
func (s *session) Close() error {
	s.closeOnce.Do(func() { s.child.stop() })
	return nil
}

// spawn starts `codex app-server` as a child process.
//
// Through internal/exec's Contain and Started rather than os/exec's own kill, for the reason that
// package exists. The app server starts the MCP servers configured in the user's config.toml, which
// are further processes; killing only the one Canopy started leaves those running invisibly. Contain
// puts the whole thing in its own process group and Child.Stop signals the group, with the guarantee
// that no signal is sent after the leader has been reaped and its pid could belong to somebody else.
func spawn(install Installation, workspace string) launcher {
	return func(ctx context.Context) (*process, error) {
		cmd := exec.Command(install.Binary, "app-server")
		cmd.Dir = workspace

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("connecting to %s: %w", install.Binary, err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("connecting to %s: %w", install.Binary, err)
		}
		said := &bounded{limit: 8 * 1024}
		cmd.Stderr = said

		canopyexec.Contain(cmd)

		if err := cmd.Start(); err != nil {
			// The one place a missing binary could still surface as an exec error, and it does not:
			// this one was found by Discovery and has gone away or become unrunnable since, which is
			// a different sentence from "install this".
			return nil, fmt.Errorf(
				"could not start %s, which was found on this machine but did not run: %w. Reinstall "+
					"the Codex CLI", install.Binary, err)
		}

		child := canopyexec.Started(cmd)
		var once sync.Once
		return &process{
			stdin:  stdin,
			stdout: stdout,
			stderr: said,
			stop: func() {
				once.Do(func() {
					// Stdin first, because closing it is how a well behaved stdio server is asked to
					// finish, and a process that leaves on its own leaves cleanly. The signal follows
					// for the ones that do not.
					_ = stdin.Close()
					child.Stop()
					// child.Wait rather than cmd.Wait, and the difference is not stylistic. Stop
					// leaves an escalation waiting to send SIGKILL to the whole group once the grace
					// period is up, and only Child.Wait marks the leader reaped under the same lock
					// that guards that signal. Reaping through cmd.Wait releases the pid while the
					// escalation still believes the group is live, so the SIGKILL that follows lands
					// on whichever group the kernel handed the number to next. See D-37.
					_ = child.Wait()
				})
			},
		}, nil
	}
}

// bounded keeps the last n bytes a process printed and drops the rest.
//
// The tail rather than the head, which is the opposite of what the Claude route keeps and is right
// for a different reason. The app server logs while it runs, at a level the user's own config
// controls, so the first bytes are startup chatter and the sentence that explains a failure is the
// last one.
type bounded struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *bounded) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.limit; over > 0 {
		b.buf = b.buf[over:]
	}
	return len(p), nil
}

func (b *bounded) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// lastLine is the most recent non-empty thing a process said.
//
// Whole-output dumps are unreadable in an error, and the app server's logging is a stream of
// timestamped lines where the one that matters is the last.
func lastLine(said string) string {
	lines := strings.Split(strings.TrimSpace(said), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}
