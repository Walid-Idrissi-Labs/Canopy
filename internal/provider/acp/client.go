package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	canopyexec "github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// authRequiredCode is the ACP error a bridge returns when nobody is signed in.
//
// In the protocol's reserved range rather than a JSON-RPC standard code, which is why it is written
// down here with its name attached: -32000 on its own reads like an implementation detail and is in
// fact the one error on this route that means "go and sign in".
const authRequiredCode = -32000

// Client drives the user's Claude Code over ACP.
//
// It satisfies core.ProviderClient, and that is a decision worth flagging rather than passing over,
// because D-51 says a delegated agent "is not what core.ProviderClient describes and should not be
// forced into its shape". Both are true at once. The interface fits the traffic well enough:
// a request goes in, text and thinking stream back, a stop reason and a usage record come out.
// What it does not describe is the tool loop, and this client answers that by not having one, which
// is the honest shape of the thing rather than a shortcut. Nothing here forces a tool call into
// core's vocabulary; see stream.go for why a delegated tool call is a notice.
//
// One process per turn, started by Stream and stopped by Close. Not a pooled connection, and the
// reason is that core.Request carries the whole conversation every time. A long-lived ACP session
// already holds the history, so sending the transcript into one would give the agent every earlier
// message twice, and sending only the last message into one would make the client's answers depend on
// a session identity core.ProviderClient does not have.
type Client struct {
	install Installation

	// workspace is the directory a delegated session is rooted at, which is what the agent's own
	// file tools are scoped to.
	workspace string

	// version identifies this build in the ACP handshake.
	version string

	// launch is how the bridge is started. Replaced in tests by an in-process agent, which is what
	// makes every test in this package run on a machine with no Claude Code and no subscription.
	launch func(ctx context.Context) (*process, error)
}

var _ core.ProviderClient = (*Client)(nil)

// Option configures a client.
type Option func(*Client)

// WithWorkspace roots the delegated session at a directory.
//
// Defaults to the directory Canopy was started in. That default is right for the common case and
// wrong for a worktree, and it is the honest default rather than a good one: core.ProviderClient is
// handed a request and not a workspace, so nothing below this line knows where the work is. Whoever
// builds a client for an isolated agent should say so here.
func WithWorkspace(dir string) Option {
	return func(c *Client) { c.workspace = dir }
}

// WithVersion sets the version Canopy reports in the ACP handshake.
func WithVersion(version string) Option {
	return func(c *Client) { c.version = version }
}

// New builds a client for a Claude Code that has already been found and is already signed in.
//
// Takes a discovered Installation rather than discovering one itself, so that the failure a user
// sees when Claude Code is missing happens where they asked for something ("add this credential")
// rather than in the middle of a turn.
func New(install Installation, opts ...Option) *Client {
	client := &Client{install: install, version: "dev"}
	for _, opt := range opts {
		opt(client)
	}
	if client.workspace == "" {
		if dir, err := os.Getwd(); err == nil {
			client.workspace = dir
		}
	}
	if client.launch == nil {
		client.launch = client.spawn
	}
	return client
}

// Name identifies this route for display and for attributing usage.
//
// Not "anthropic". The credential behind it is not an Anthropic credential and the turn is not a
// call to Anthropic's API, and a name that said so would make a delegated turn indistinguishable
// from a metered one in every place a provider name is shown.
func (c *Client) Name() string { return "claude-code" }

// Account is who the delegated Claude Code is signed in as.
func (c *Client) Account() Account { return c.install.Account }

// Stream sends a turn to the delegated agent and returns the reply as it arrives.
func (c *Client) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	if err := validate(req); err != nil {
		return nil, err
	}

	child, err := c.launch(ctx)
	if err != nil {
		return nil, err
	}

	s := &stream{
		ctx:   ctx,
		conn:  newConn(child.stdout, child.stdin),
		child: child,
		done:  make(chan struct{}),
	}
	s.watchTermination()

	if err := c.begin(s, req); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// validate checks what a delegated turn needs, which is not quite what core.Request.Validate checks.
//
// The difference is the model, and it is the one place this route legitimately parts company with the
// contract every other provider follows. core.Request.Validate requires one, correctly, because for
// everybody else the model goes on the request and a request without one fails at the far end with a
// message about the request rather than about the setting. Here the model is Claude Code's own: it is
// asked for only when the bridge offers that exact choice, and a turn that names none is the ordinary
// case rather than a malformed one. Enforcing the rule would mean inventing a model id for a
// credential whose whole point is that the vendor picks, and then reporting that invention on screen.
//
// Everything else Validate holds that can matter here is held here. MaxTokens and DisableThinking are
// not checked because there is no field in the protocol to put them in and they are silently the
// agent's own; that is said in the doc comment on this package rather than enforced as an error,
// because refusing a request over a field that would have been ignored helps nobody.
func validate(req core.Request) error {
	fail := func(format string, args ...any) error {
		err := fmt.Errorf(format, args...)
		return &core.ProviderError{
			Kind: core.ErrInvalidRequest, Provider: "claude-code", Message: err.Error(), Err: err,
		}
	}

	if !req.Effort.Valid() {
		return fail("unknown effort %q", req.Effort)
	}
	if len(req.Messages) == 0 {
		return fail("at least one message is required")
	}
	if req.Messages[0].Role != core.RoleUser {
		return fail("the first message must be from the user, got %q", req.Messages[0].Role)
	}
	return nil
}

// begin does the handshake, opens a session, applies what it can of the request and asks the
// question. Everything up to the last step is synchronous, because a failure in any of it is a
// failure to start a turn rather than a turn that went wrong, and the two deserve different words.
func (c *Client) begin(s *stream, req core.Request) error {
	if err := c.initialize(s); err != nil {
		return err
	}
	// The opening notice is queued before the session exists, so that it stays in front of anything
	// the session setup has to say. A notice about which model answered is only readable after the
	// sentence explaining whose agent is answering at all.
	s.notices = append(s.notices, c.openingNotice())

	if err := c.newSession(s, req); err != nil {
		return err
	}

	id, err := s.conn.send(methodSessionPrompt, promptParams{
		SessionID: s.sessionID,
		Prompt:    prompt(req),
	})
	if err != nil {
		return err
	}
	s.promptID = id
	s.watchCancellation()
	return nil
}

func (c *Client) initialize(s *stream) error {
	id, err := s.conn.send(methodInitialize, initializeParams{
		ProtocolVersion: protocolVersion,
		ClientCapabilities: clientCapabilities{
			FS:       fsCapability{ReadTextFile: false, WriteTextFile: false},
			Terminal: false,
		},
		ClientInfo: implementation{Name: "canopy", Version: c.version},
	})
	if err != nil {
		return err
	}

	raw, err := s.await(id)
	if err != nil {
		return c.startupError(s, err)
	}

	var result initializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("%s answered the handshake with something unreadable: %w",
			c.install.Bridge, err)
	}

	// A version mismatch is the "too old" check, and it is a real answer rather than a version string
	// compared against a number somebody has to remember to update. A bridge that speaks only an
	// older protocol says so here, and a bridge from the future does too.
	if result.ProtocolVersion != protocolVersion {
		return fmt.Errorf(
			"%s speaks Agent Client Protocol version %d and this build of Canopy speaks version %d. "+
				"Update it with `npm install -g @agentclientprotocol/claude-agent-acp`, or update Canopy "+
				"if it is already newer than this build",
			c.install.Bridge, result.ProtocolVersion, protocolVersion)
	}
	return nil
}

// startupError turns a dead bridge into a sentence about the bridge.
//
// A process that exits during the handshake closes its pipes, so what arrives here is EOF, which
// says nothing at all. Whatever it printed on the way out is what actually explains it, so that is
// what gets shown.
func (c *Client) startupError(s *stream, err error) error {
	if said := strings.TrimSpace(s.child.stderr.String()); said != "" {
		return fmt.Errorf("%s did not start: %s", c.install.Bridge, said)
	}
	if errors.Is(err, io.EOF) {
		return fmt.Errorf(
			"%s stopped without answering the handshake and without saying why. Check that it runs on "+
				"its own, and that `claude` is signed in", c.install.Bridge)
	}
	return fmt.Errorf("talking to %s: %w", c.install.Bridge, err)
}

func (c *Client) newSession(s *stream, req core.Request) error {
	id, err := s.conn.send(methodSessionNew, newSessionParams{
		CWD: c.workspace,
		// Empty, always, and this is half the answer to Q-23. MCP servers are the only way ACP v1
		// lets a client offer its own tools to an agent; Canopy's tools are not an MCP server, so a
		// delegated turn has Claude Code's tools and none of Canopy's.
		MCPServers: []string{},
	})
	if err != nil {
		return err
	}

	raw, err := s.await(id)
	if err != nil {
		return c.sessionError(err)
	}

	var result newSessionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("%s opened a session and described it unreadably: %w", c.install.Bridge, err)
	}
	if result.SessionID == "" {
		return fmt.Errorf("%s opened a session with no identifier, so there is nothing to ask",
			c.install.Bridge)
	}
	s.sessionID = result.SessionID

	c.applySettings(s, result.ConfigOptions, req)
	return nil
}

// sessionError names the sign-in remedy when that is what the bridge actually said.
func (c *Client) sessionError(err error) error {
	var rpc *rpcError
	if errors.As(err, &rpc) && rpc.Code == authRequiredCode {
		return fmt.Errorf(
			"%w. %s says nobody is signed in. Run `claude` and sign in again; Canopy holds no Claude "+
				"credential of yours and has none to offer", ErrNotSignedIn, c.install.Bridge)
	}
	return fmt.Errorf("opening a session with %s: %w", c.install.Bridge, err)
}

// applySettings asks for the model and effort the request named, and says so when it cannot.
//
// The ids are read off the session the bridge just opened rather than hard-coded, because ACP leaves
// them to the agent: "model" and "effort" are what this bridge happens to call them and nothing in
// the protocol promises that. A setting that is offered and matches is applied silently. One that is
// not is a notice, never a silent substitution, because the screen shows a model name and a model
// name that is not the model that answered is the kind of confident wrong answer this repository
// exists to avoid.
func (c *Client) applySettings(s *stream, options []configOption, req core.Request) {
	c.applySetting(s, options, "model", req.Model, "model")
	if req.Effort != core.EffortDefault {
		c.applySetting(s, options, "effort", string(req.Effort), "effort level")
	}
}

func (c *Client) applySetting(s *stream, options []configOption, id, want, noun string) {
	if want == "" {
		return
	}

	for _, option := range options {
		if option.ID != id {
			continue
		}
		for _, choice := range option.Options {
			if !strings.EqualFold(choice.Value, want) {
				continue
			}
			// Sent and then waited for, rather than fired off. Every request in this file is answered
			// before the next one is sent, and that is a rule about the transport rather than about
			// politeness: stdio pipes have a fixed buffer, and a client that keeps writing while
			// answers pile up unread deadlocks against an agent doing the same thing. It is also the
			// only way to know whether the setting took.
			requestID, err := s.conn.send(methodSetConfigOption, setConfigOptionParams{
				SessionID: s.sessionID,
				ConfigID:  id,
				Type:      "select",
				Value:     choice.Value,
			})
			if err == nil {
				_, err = s.await(requestID)
			}
			if err != nil {
				s.notices = append(s.notices, fmt.Sprintf(
					"Claude Code would not set the %s to %q for this turn, so it ran on its own "+
						"setting: %v", noun, choice.Value, err))
			}
			return
		}
		s.notices = append(s.notices, fmt.Sprintf(
			"Claude Code does not offer %q as a %s, so this turn ran on %s instead",
			want, noun, describeCurrent(option)))
		return
	}

	s.notices = append(s.notices, fmt.Sprintf(
		"Claude Code did not offer a choice of %s over ACP, so %q was not applied and the turn ran on "+
			"whatever Claude Code is set to", noun, want))
}

func describeCurrent(option configOption) string {
	if current, ok := option.CurrentValue.(string); ok && current != "" {
		return fmt.Sprintf("%q", current)
	}
	return "its own setting"
}

// openingNotice is what a reader is told before the first word of a delegated reply.
//
// It is not decoration and it is not a disclaimer. Q-23 forbids any screen showing a permission mode
// a delegated turn is not running under, and the conversation view shows Canopy's mode. This is the
// sentence that stops that display from being a lie, so it leads the turn rather than following it.
func (c *Client) openingNotice() string {
	who := c.install.Account.String()
	if c.install.Account.OnSubscription() {
		return fmt.Sprintf(
			"this turn runs on your own Claude Code, signed in as %s, and draws on that plan's usage "+
				"limits rather than on an API bill. Claude Code runs its own tools under its own "+
				"permissions: Canopy's tools, its trust levels and its approval prompts are not in the "+
				"path", who)
	}
	return fmt.Sprintf(
		"this turn runs on your own Claude Code, signed in as %s through %s rather than a Claude "+
			"subscription, so it is billed to that account per token. Claude Code runs its own tools "+
			"under its own permissions: Canopy's tools, its trust levels and its approval prompts are "+
			"not in the path", who, c.install.Account.Method)
}

// prompt flattens a Canopy request into the blocks ACP accepts.
//
// ACP v1 prompts are content blocks and nothing else: no system field, no roles, no tool results. So
// the transcript is rendered as text, and the rendering is labelled rather than run together, because
// an agent handed an unlabelled wall of alternating voices answers the wrong one.
//
// The system prompt goes in as the first block and does not replace Claude Code's own. There is no
// way over this protocol to replace it, which matters enough to be written here rather than
// discovered: a Canopy agent profile's system prompt is advice to a delegated agent, not its
// instructions.
func prompt(req core.Request) []contentBlock {
	blocks := make([]contentBlock, 0, 3)
	if system := strings.TrimSpace(req.System); system != "" {
		blocks = append(blocks, textBlock(system))
	}

	last := req.Messages[len(req.Messages)-1]
	if earlier := transcript(req.Messages[:len(req.Messages)-1]); earlier != "" {
		blocks = append(blocks, textBlock(
			"Earlier in this conversation:\n\n"+earlier+"\n\nWhat follows is the current message."))
	}

	blocks = append(blocks, textBlock(render(last)))
	return blocks
}

func transcript(messages []core.Message) string {
	var b strings.Builder
	for _, message := range messages {
		text := render(message)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		switch message.Role {
		case core.RoleAssistant:
			b.WriteString("Assistant: ")
		default:
			b.WriteString("User: ")
		}
		b.WriteString(text)
	}
	return b.String()
}

// render turns one message into text, including any tool traffic it carries.
//
// A delegated turn produces no tool calls of Canopy's, so tool results reach here only from a
// conversation that ran on another credential first. Rendered rather than dropped, because dropping
// them leaves an assistant message saying "I will read the file" followed by nothing, and the agent
// picking the conversation up has to guess whether the file was read.
func render(message core.Message) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(message.Text))

	for _, call := range message.ToolCalls {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[called the tool %s with %s]", call.Name, string(call.Input))
	}
	for _, result := range message.ToolResults {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		switch {
		case result.Refused:
			fmt.Fprintf(&b, "[a tool call was refused: %s]", result.Content)
		case result.IsError:
			fmt.Fprintf(&b, "[a tool call failed: %s]", result.Content)
		default:
			fmt.Fprintf(&b, "[a tool call returned: %s]", result.Content)
		}
	}
	return b.String()
}

// process is a running bridge and the three streams that matter.
type process struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *bounded

	// stop ends the bridge and everything it started. Safe to call more than once.
	stop func()
}

// spawn starts the bridge as a child process.
//
// Through internal/exec's Contain and Child rather than os/exec's own kill, and the reason is the one
// that package was written for. The bridge is a Node program that starts the Claude Agent SDK, which
// starts more processes; killing only the one Canopy started leaves those running, holding the
// subscription's rate limit and whatever else, invisibly. Contain puts the whole thing in its own
// process group and Child.Stop signals the group, with the guarantee that no signal is sent after the
// leader has been reaped and its pid could belong to somebody else.
func (c *Client) spawn(ctx context.Context) (*process, error) {
	cmd := exec.Command(c.install.Bridge)
	cmd.Dir = c.workspace

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", c.install.Bridge, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", c.install.Bridge, err)
	}
	said := &bounded{limit: 8 * 1024}
	cmd.Stderr = said

	canopyexec.Contain(cmd)

	if err := cmd.Start(); err != nil {
		// The one place a missing binary could still surface as an exec error, and it does not: the
		// bridge was found by Discovery and has gone away or become unrunnable since, which is a
		// different sentence from "install this".
		return nil, fmt.Errorf(
			"could not start %s, which was found on this machine but did not run: %w. Reinstall it "+
				"with `npm install -g @agentclientprotocol/claude-agent-acp`", c.install.Bridge, err)
	}

	child := canopyexec.Started(cmd)
	var once sync.Once
	return &process{
		stdin:  stdin,
		stdout: stdout,
		stderr: said,
		stop: func() {
			once.Do(func() {
				// Stdin first, because closing it is how a well behaved stdio agent is asked to
				// finish, and a process that leaves on its own leaves cleanly. The signal follows for
				// the ones that do not.
				_ = stdin.Close()
				child.Stop()
				// child.Wait rather than cmd.Wait, and the difference is not stylistic. Stop leaves
				// an escalation waiting to send SIGKILL to the whole group once the grace period is
				// up, and only Child.Wait marks the leader reaped under the same lock that guards
				// that signal. Reaping through cmd.Wait releases the pid while the escalation still
				// believes the group is live, so the SIGKILL that follows lands on whichever group
				// the kernel handed the number to next. See D-37.
				_ = child.Wait()
			})
		},
	}, nil
}

// bounded keeps the first n bytes a process printed and drops the rest.
//
// The head rather than the tail, which is the opposite of what internal/exec keeps and is right for
// the opposite reason. This exists to explain a bridge that failed to start, and the sentence that
// explains that is the first one it printed; everything after it is consequences.
type bounded struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *bounded) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if room := b.limit - len(b.buf); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		b.buf = append(b.buf, p[:room]...)
	}
	return len(p), nil
}

func (b *bounded) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
