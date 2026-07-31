package codex

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Client drives the user's Codex app server over JSON-RPC.
//
// It satisfies core.ProviderClient, which D-51 warns against on the grounds that a delegated agent
// "is not what core.ProviderClient describes and should not be forced into its shape". Both are
// true at once, and S-04 reached the same conclusion on the Claude route. The interface fits the
// traffic: a request goes in, text and thinking stream back, a stop reason and a usage record come
// out. What it does not describe is the tool loop, and this client answers that by not having one,
// which is the honest shape of the thing rather than a shortcut. Nothing here forces a tool call
// into core's vocabulary; see reportItem in stream.go for why a delegated tool call is a notice.
//
// One process and one thread per turn, started by Stream and stopped by Close. Not a pooled
// connection, and the reason is that core.Request carries the whole conversation every time. A
// long-lived thread already holds the history, so sending the transcript into one would give the
// agent every earlier message twice, and sending only the last message into one would make the
// client's answers depend on a thread identity core.ProviderClient does not have.
type Client struct {
	install Installation

	// workspace is the directory the delegated thread is rooted at, which is what the app server's
	// sandbox and its own file tools are scoped to.
	workspace string

	// version identifies this build in the handshake, and travels upstream beside the originator.
	version string

	// launch is how the app server is started. Replaced in tests by an in-process server, which is
	// what makes every test in this package run on a machine with no Codex and no subscription.
	launch launcher
}

var _ core.ProviderClient = (*Client)(nil)

// Option configures a client.
type Option func(*Client)

// WithWorkspace roots the delegated thread at a directory.
//
// Defaults to the directory Canopy was started in. That default is right for the common case and
// wrong for a worktree, and it is the honest default rather than a good one: core.ProviderClient is
// handed a request and not a workspace, so nothing below this line knows where the work is. Whoever
// builds a client for an isolated agent should say so here.
func WithWorkspace(dir string) Option {
	return func(c *Client) { c.workspace = dir }
}

// WithVersion sets the version Canopy reports in the handshake.
func WithVersion(version string) Option {
	return func(c *Client) { c.version = version }
}

// New builds a client for a Codex that has already been found.
//
// Takes a discovered Installation rather than discovering one itself, so that the failure a user
// sees when Codex is missing happens where they asked for something ("add this credential") rather
// than in the middle of a turn.
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
		client.launch = spawn(install, client.workspace)
	}
	return client
}

// Name identifies this route for display and for attributing usage.
//
// Not "openai". The credential behind it is not an OpenAI API credential and the turn is not a call
// to OpenAI's API, and a name that said so would make a delegated turn indistinguishable from a
// metered one in every place a provider name is shown.
func (c *Client) Name() string { return "codex" }

// Stream sends a turn to the delegated agent and returns the reply as it arrives.
func (c *Client) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	if err := validate(req); err != nil {
		return nil, err
	}

	session, err := start(ctx, c.launch, c.version)
	if err != nil {
		return nil, err
	}

	s := &stream{
		ctx:     ctx,
		session: session,
		emitted: map[string]int{},
		done:    make(chan struct{}),
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
// The difference is the model, and it is the one place this route legitimately parts company with
// the contract every other provider follows. core.Request.Validate requires one, correctly, because
// for everybody else the model goes on the request and a request without one fails at the far end
// with a message about the request rather than about the setting. Here the model is Codex's own
// unless a caller names one: a delegated credential stores none, so a turn that names none is the
// ordinary case rather than a malformed one. Enforcing the rule would mean inventing a model id for
// a credential whose whole point is that the vendor picks, and then reporting that invention on
// screen. The Claude route found this the same way, and by a live test rather than a scripted one.
//
// MaxTokens and DisableThinking are not checked because there is no field in the protocol to put
// them in and they are silently the agent's own. That is said in this package's doc comment rather
// than enforced as an error, because refusing a request over a field that would have been ignored
// helps nobody.
func validate(req core.Request) error {
	fail := func(format string, args ...any) error {
		err := fmt.Errorf(format, args...)
		return &core.ProviderError{
			Kind: core.ErrInvalidRequest, Provider: "codex", Message: err.Error(), Err: err,
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

// begin opens a thread, applies what it can of the request and asks the question.
//
// Everything up to the last step is synchronous, because a failure in any of it is a failure to
// start a turn rather than a turn that went wrong, and the two deserve different words.
func (c *Client) begin(s *stream, req core.Request) error {
	// Queued before the thread exists, so it stays in front of anything the setup has to say. A
	// notice about which model answered is only readable after the sentence explaining whose agent
	// is answering at all.
	s.notices = append(s.notices, c.openingNotice())

	var opened threadStartResult
	err := s.session.call(methodThreadStart, threadStartParams{
		CWD:            c.workspace,
		ApprovalPolicy: approvalOnRequest,
		Sandbox:        sandboxReadOnly,
		Model:          req.Model,
		Ephemeral:      true,
		// A Canopy agent profile's system prompt goes in as developer instructions and does not
		// replace Codex's own. There is no way over this protocol to replace them, which matters
		// enough to be written here rather than discovered: on this route a system prompt is advice
		// to a delegated agent, not its instructions.
		DeveloperInstructions: strings.TrimSpace(req.System),
	}, &opened)
	if err != nil {
		return c.threadError(err)
	}
	if opened.Thread.ID == "" {
		return fmt.Errorf("the Codex app server opened a thread with no identifier, so there is " +
			"nothing to ask")
	}
	s.threadID = opened.Thread.ID

	// Reported and never substituted silently, because the screen shows a model name and a model
	// name that is not the model that answered is the kind of confident wrong answer this
	// repository exists to avoid.
	if req.Model != "" && opened.Model != "" && !strings.EqualFold(opened.Model, req.Model) {
		s.notices = append(s.notices, fmt.Sprintf(
			"Codex does not offer %q, so this turn ran on %s instead", req.Model, opened.Model))
	}
	if req.Model == "" && opened.Model != "" {
		s.notices = append(s.notices, "Codex chose the model for this turn: "+opened.Model)
	}

	var started turnStartResult
	err = s.session.call(methodTurnStart, turnStartParams{
		ThreadID: s.threadID,
		Input:    []userInput{textInput(prompt(req))},
		Effort:   string(req.Effort),
	}, &started)
	if err != nil {
		return fmt.Errorf("asking the delegated Codex: %w", err)
	}
	s.turnID = started.Turn.ID

	s.watchCancellation()
	return nil
}

// threadError names the sign-in remedy when that is what the app server actually said.
func (c *Client) threadError(err error) error {
	said := err.Error()
	if strings.Contains(strings.ToLower(said), "unauthorized") ||
		strings.Contains(strings.ToLower(said), "not signed in") ||
		strings.Contains(strings.ToLower(said), "no account") {
		return fmt.Errorf(
			"%w. Sign in again with `canopy keys signin`, which hands you to OpenAI's own flow: "+
				"Canopy holds no ChatGPT credential of yours and has none to offer. (%s)",
			ErrNotSignedIn, said)
	}
	return fmt.Errorf("opening a thread with the Codex app server: %w", err)
}

// openingNotice is what a reader is told before the first word of a delegated reply.
//
// It is not decoration and it is not a disclaimer. Q-23 forbids any screen showing a permission
// mode a delegated turn is not running under, and the conversation view shows Canopy's mode. This is
// the sentence that stops that display from being a lie, so it leads the turn rather than following
// it.
func (c *Client) openingNotice() string {
	return "this turn runs on the Codex you signed in to through Canopy, and draws on that ChatGPT " +
		"plan's limits rather than on an API bill. Codex runs its own tools inside its own sandbox " +
		"and under its own permissions: Canopy's tools, its trust levels and its approval prompts " +
		"are not in the path, and Canopy declines every approval Codex asks it for"
}

// prompt flattens a Canopy request into the text a turn takes.
//
// A turn's input is content blocks and nothing else: no roles, no tool results, and no system field,
// since the system prompt went in as the thread's developer instructions. So the transcript is
// rendered as text, and the rendering is labelled rather than run together, because an agent handed
// an unlabelled wall of alternating voices answers the wrong one.
func prompt(req core.Request) string {
	var b strings.Builder

	last := req.Messages[len(req.Messages)-1]
	if earlier := transcript(req.Messages[:len(req.Messages)-1]); earlier != "" {
		b.WriteString("Earlier in this conversation:\n\n")
		b.WriteString(earlier)
		b.WriteString("\n\nWhat follows is the current message.\n\n")
	}
	b.WriteString(render(last))
	return b.String()
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
