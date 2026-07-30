package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The vendor's runtime, and the two decisions that shape how Canopy reaches it.
//
// Canopy discovers the Copilot CLI rather than bundling it. The SDK offers a build-time bundler that
// embeds a per-platform binary, and it was rejected on three counts, in increasing order of how much
// they matter. It would multiply the size of a release that today is one small static binary built
// with CGO_ENABLED=0. It would pin a vendor version Canopy would then have to chase, so a user's
// Copilot would be as old as their Canopy. And it would put a proprietary vendor binary inside
// Canopy's release archives, which is a redistribution question nobody has asked and Canopy would
// have to answer before every tag. Discovery costs one failure mode, an absent binary, and that
// failure is answerable with one sentence naming what to install, which is what missingCLI is.
//
// Canopy passes the token through the SDK's own field rather than through the environment. The SDK
// spawns the CLI with --auth-token-env and puts the value in the child's environment, so the token
// never appears in an argument list, which is the same reason Canopy has never accepted a secret on
// its own command line.

// CLIName is what the vendor's binary is called.
const CLIName = "copilot"

// CLIPathEnvVar is the SDK's own override, honoured here so that Canopy's discovery and the SDK's
// agree rather than one of them finding a binary the other does not.
const CLIPathEnvVar = "COPILOT_CLI_PATH"

// FindCLI locates the Copilot CLI, or says what to install.
//
// Run before the SDK is asked to start, because the SDK's own answer is
// `exec: "copilot": executable file not found in $PATH` wrapped in "failed to start CLI server",
// which is true and tells somebody who has never heard of the Copilot CLI nothing they can act on.
func FindCLI() (string, error) {
	if fromEnv := os.Getenv(CLIPathEnvVar); fromEnv != "" {
		if _, err := os.Stat(fromEnv); err != nil {
			return "", fmt.Errorf(
				"%s points at %s, which is not there: %w", CLIPathEnvVar, fromEnv, err)
		}
		return fromEnv, nil
	}
	path, err := exec.LookPath(CLIName)
	if err != nil {
		return "", missingCLI()
	}
	return path, nil
}

// missingCLI is the sentence somebody gets when the binary is not on the machine.
func missingCLI() error {
	return fmt.Errorf(
		"the GitHub Copilot CLI is not installed, and this route runs a turn by driving it. Install "+
			"it with `npm install -g @github/copilot`, or set %s to where it already lives. Canopy "+
			"does not ship a copy of it, so that your Copilot is the version GitHub currently "+
			"supports rather than the one Canopy was built against", CLIPathEnvVar)
}

// StateDir is where the vendor's runtime keeps whatever it keeps.
//
// Canopy's own directory rather than the runtime's default of ~/.copilot, for two reasons. The SDK
// panics in ModeEmpty unless it is given one, so it is not optional. And it is also what the session
// runs in: a working directory holding nothing means that the runtime's unconditional loading of
// instruction files from the working directory, which is documented as happening whatever else is
// switched off, finds nothing of the user's project to load.
func StateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding the configuration directory: %w", err)
	}
	dir := filepath.Join(base, "canopy", "copilot")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("preparing %s for the Copilot runtime: %w", dir, err)
	}
	return dir, nil
}

// EventKind is what an agent event carries.
type EventKind int

const (
	// EventText is a chunk of the reply.
	EventText EventKind = iota
	// EventThinking is a chunk of reasoning.
	EventThinking
	// EventToolCall is the agent asking Canopy to run one of Canopy's tools.
	EventToolCall
	// EventUsage is what the turn consumed, which the vendor reports per model call.
	EventUsage
	// EventIdle is the agent having nothing further to say until it is spoken to.
	EventIdle
	// EventFailed is the agent reporting that the turn cannot continue.
	EventFailed
)

// Event is one thing the vendor's agent said.
//
// A type of this package's own rather than the SDK's, because it is the seam every test in here
// drives. The SDK's event has forty variants and reaching most of them from a test means running a
// real CLI against a real subscription; this has five and a fake can produce all of them.
type Event struct {
	Kind  EventKind
	Text  string
	Call  *core.ToolCall
	Usage core.Usage
	Err   error
}

// Agent is one conversation with the vendor's agent, as this package needs it.
//
// Deliberately narrow. Everything the SDK can do that Canopy does not use, which is most of it, is
// absent here, so a reader can see the whole of what a delegated conversation consists of: say
// something, answer a tool call, listen, stop, close.
type Agent interface {
	// Send delivers one message. What comes back arrives on Events.
	Send(ctx context.Context, prompt string) error

	// Answer resolves a tool call the agent asked for and Canopy has now run. A non-empty failure is
	// what a refused or failed tool reports, and the agent gets to see it and adjust rather than
	// waiting for a result that never comes.
	Answer(ctx context.Context, callID, result, failure string) error

	// Events is everything the agent says, in order. Closed when the conversation ends.
	Events() <-chan Event

	// Abort stops the turn in flight and leaves the conversation usable.
	Abort(ctx context.Context) error

	// Close ends the conversation and releases the process behind it.
	Close() error
}

// Conversation is what a session needs to know about itself when it is created.
type Conversation struct {
	// Token is the user's own grant. It is what makes the turn bill their seat.
	Token core.Secret
	// Model is which model to run, empty for the vendor's own choice.
	Model string
	// Effort maps onto the vendor's reasoning effort.
	Effort core.Effort
	// DisableThinking suppresses the reasoning summary.
	DisableThinking bool
	// System is Canopy's system prompt, appended to the vendor's rather than replacing it.
	System string
	// Tools are Canopy's own tools, declared to the agent and implemented by Canopy.
	Tools []core.ToolDefinition
	// CLIPath overrides discovery. For tests and for a machine with the binary somewhere unusual.
	CLIPath string
	// StateDir overrides StateDir. For tests, so a run leaves nothing in somebody's config directory.
	StateDir string
}

// Open starts a runtime and a session on it, and is what a Client uses unless a test replaced it.
func Open(ctx context.Context, conversation Conversation) (Agent, error) {
	path := conversation.CLIPath
	if path == "" {
		found, err := FindCLI()
		if err != nil {
			return nil, err
		}
		path = found
	}

	state := conversation.StateDir
	if state == "" {
		dir, err := StateDir()
		if err != nil {
			return nil, err
		}
		state = dir
	}

	client := sdk.NewClient(&sdk.ClientOptions{
		Connection: sdk.StdioConnection{Path: path},
		// The safe-default mode, and the single most important line in this package. It is what
		// takes bash, edit, grep, web_fetch and the rest away from the model, strips the host
		// environment from the system message, and forces file hooks, host git operations, the
		// cross-session store, skills and memory off. Without it Canopy would be handing somebody
		// else's agent a shell in the user's repository and calling it a provider.
		Mode:          sdk.ModeEmpty,
		BaseDirectory: state,
		// Both, rather than only the token. UseLoggedInUser already defaults to false when a token is
		// given, and saying it out loud is what stops a future SDK default from quietly falling back
		// to whatever `gh` is signed in as, which would bill an account the user did not choose.
		GitHubToken:     conversation.Token.Reveal(),
		UseLoggedInUser: sdk.Bool(false),
	})

	if err := client.Start(ctx); err != nil {
		client.ForceStop()
		return nil, startFailure(err)
	}

	session, err := client.CreateSession(ctx, sessionConfig(conversation, state))
	if err != nil {
		client.ForceStop()
		return nil, classify(err)
	}

	return newSDKAgent(client, session), nil
}

// AllowedTools is the allowlist a Copilot session runs under, and the reason it is not empty.
//
// ModeEmpty refuses to create a session at all unless AvailableTools is set, which is the SDK
// making the decision explicit rather than defaulting it. The obvious value, an empty list, is
// wrong: it excludes Canopy's own tools along with the vendor's, so a conversation with tools would
// silently become one without. "custom:*" is every tool registered through SessionConfig.Tools,
// which is Canopy's and nobody else's, and it names no built-in and no MCP source, so there is
// nothing for the vendor's own tooling to enter through.
//
// Exported so that a test can assert on the thing that ships rather than on a copy of it.
func AllowedTools(tools []core.ToolDefinition) []string {
	if len(tools) == 0 {
		// A conversation with no tools is a legitimate thing to want, and this is how it is said. Not
		// nil: nil is what ModeEmpty rejects, and the difference between the two is the difference
		// between a session with no tools and no session at all.
		return []string{}
	}
	return sdk.NewToolSet().AddCustom("*").ToSlice()
}

// sessionConfig is everything Canopy asks of a vendor session.
//
// Every flag here that is already ModeEmpty's default is set again on purpose. ModeEmpty's defaults
// are the SDK's promise and these are Canopy's requirement, and the two being written down
// separately is what makes a future SDK release that relaxes one of them a compile-or-test problem
// rather than a silent widening of what somebody else's agent may do on a user's machine.
func sessionConfig(conversation Conversation, state string) *sdk.SessionConfig {
	config := &sdk.SessionConfig{
		// The name that reaches GitHub's User-Agent. Canopy says it is Canopy, which is the whole
		// difference between this route and one that impersonates an editor.
		ClientName: userAgent,
		Model:      conversation.Model,

		Tools:          declare(conversation.Tools),
		AvailableTools: AllowedTools(conversation.Tools),

		// Canopy's gate, at the vendor's boundary. Everything the agent wants to do arrives here
		// first, and everything that is not one of Canopy's own tools is refused, because there is
		// nothing else it could legitimately be asking for.
		OnPermissionRequest: refuseAnythingNotCanopys(conversation.Tools),

		// Deltas rather than whole messages. Without it the runtime answers non-streaming and the
		// reply lands in one piece at the end, which is a working turn and a dead-looking screen.
		Streaming: sdk.Bool(true),

		// The session runs in Canopy's own directory rather than the user's project. Nothing in the
		// session can read a file, so it needs no project to point at, and a working directory with
		// nothing in it is what makes the runtime's unconditional loading of instruction files from
		// the working directory find nothing of the user's to load.
		WorkingDirectory: state,
		ConfigDirectory:  state,

		EnableConfigDiscovery:              sdk.Bool(false),
		EnableFileHooks:                    sdk.Bool(false),
		EnableHostGitOperations:            sdk.Bool(false),
		EnableSessionStore:                 sdk.Bool(false),
		EnableSkills:                       sdk.Bool(false),
		EnableOnDemandInstructionDiscovery: sdk.Bool(false),
		SkipCustomInstructions:             sdk.Bool(true),
		SkipEmbeddingRetrieval:             sdk.Bool(true),
		EnableSessionTelemetry:             sdk.Bool(false),
		Memory:                             &sdk.MemoryConfiguration{Enabled: false},

		// The user's own grant, per session. This is the field that makes the turn bill their seat
		// rather than anything of Canopy's, which is the entire point of D-51's first route.
		GitHubToken: conversation.Token.Reveal(),
	}

	if effort := effortFor(conversation.Effort); effort != "" {
		config.ReasoningEffort = effort
	}
	if conversation.DisableThinking {
		config.ReasoningSummary = sdk.ReasoningSummaryNone
	}
	if conversation.System != "" {
		// Appended rather than replacing. Canopy's system prompt is a mode's rules, which is an
		// addition to how the vendor's agent behaves rather than a replacement for it, and replacing
		// the whole message would also throw away the tool instructions the agent needs to use
		// Canopy's own tools properly.
		config.SystemMessage = &sdk.SystemMessageConfig{Mode: "append", Content: conversation.System}
	}
	return config
}

// effortFor maps Canopy's effort onto the vendor's.
//
// EffortMax has no counterpart. It maps to xhigh, the highest the vendor offers, rather than being
// refused: somebody who asked for the most thinking available and got the most thinking available
// has been served, and failing a turn because two vocabularies have a different number of rungs
// would be pedantry with a cost.
func effortFor(effort core.Effort) string {
	switch effort {
	case core.EffortLow, core.EffortMedium, core.EffortHigh, core.EffortXHigh:
		return string(effort)
	case core.EffortMax:
		return string(core.EffortXHigh)
	default:
		return ""
	}
}

// declare tells the agent what Canopy's tools are without giving it a way to run them.
//
// A nil Handler is the SDK's declaration-only form: the tool appears to the model, and a call to it
// arrives as an event and stays pending until somebody resolves it. That somebody is Canopy, one
// layer up, after the call has been through A4's trust level and, where the level requires it, past
// a person. Handing the SDK a handler here would run Canopy's tools inside the vendor's loop with
// Canopy's gate bypassed, which is the arrangement Q-23 warns about: a screen showing a permission
// mode the turn is not running under.
func declare(tools []core.ToolDefinition) []sdk.Tool {
	if len(tools) == 0 {
		return nil
	}
	declared := make([]sdk.Tool, 0, len(tools))
	for _, tool := range tools {
		var parameters map[string]any
		if len(tool.InputSchema) > 0 {
			// A schema that will not parse is dropped rather than sent. The runtime would reject the
			// whole session over one malformed tool, and losing one tool beats losing the
			// conversation. It cannot happen from Canopy's own registry, whose schemas are built
			// rather than typed, so this covers a caller rather than a user.
			if err := json.Unmarshal(tool.InputSchema, &parameters); err != nil {
				parameters = nil
			}
		}
		declared = append(declared, sdk.Tool{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  parameters,
		})
	}
	return declared
}

// refuseAnythingNotCanopys is the permission handler the session runs under.
//
// It approves Canopy's own tools, because approving them here is not the decision that matters: the
// call still comes back out to Canopy as a pending request and still goes through the trust level
// and the approver before anything happens. What this stops is everything else. ModeEmpty and the
// allowlist should already mean there is nothing else, and this is the third lock on that door,
// there because a permission request Canopy does not recognise arriving at all would mean one of the
// first two has stopped working, and the safe answer to that is no.
func refuseAnythingNotCanopys(tools []core.ToolDefinition) sdk.PermissionHandlerFunc {
	known := make(map[string]bool, len(tools))
	for _, tool := range tools {
		known[tool.Name] = true
	}
	return func(request sdk.PermissionRequest, _ sdk.PermissionInvocation) (rpc.PermissionDecision, error) {
		if custom, ok := request.(*rpc.PermissionRequestCustomTool); ok && known[custom.ToolName] {
			return &rpc.PermissionDecisionApproveOnce{}, nil
		}
		feedback := "Canopy runs this session with no tools of your own, so this was refused. " +
			"Use one of the tools Canopy provided."
		return &rpc.PermissionDecisionReject{Feedback: &feedback}, nil
	}
}

// sdkAgent is Agent over a real runtime.
type sdkAgent struct {
	client  *sdk.Client
	session *sdk.Session

	events chan Event

	unsubscribe func()
	closeOnce   sync.Once
}

var _ Agent = (*sdkAgent)(nil)

// eventBuffer is how far the vendor may run ahead of the reader.
//
// Small on purpose. The SDK dispatches handlers from one goroutine, so a full channel blocks that
// goroutine and the runtime stops producing, which is exactly the backpressure a turn wants: a
// reader that has stopped reading should slow the vendor down rather than fill memory with a reply
// nobody is looking at. Not zero, because a handler that blocks on every single delta would turn
// each token into a scheduling round trip.
const eventBuffer = 64

func newSDKAgent(client *sdk.Client, session *sdk.Session) *sdkAgent {
	agent := &sdkAgent{
		client:  client,
		session: session,
		events:  make(chan Event, eventBuffer),
	}
	agent.unsubscribe = session.On(agent.handle)
	return agent
}

// handle turns one vendor event into at most one Canopy event.
//
// Runs on the SDK's dispatch goroutine, so it does the smallest possible amount of work and never
// touches anything a turn holds. Everything the type switch does not name is dropped, which is most
// of the forty variants and is deliberate: an event Canopy has no use for is not an error, and
// forwarding it would mean the stream had to know how to ignore things too.
func (a *sdkAgent) handle(event sdk.SessionEvent) {
	if event.AgentID != nil {
		// A sub-agent's output. Dropped rather than merged into the reply, because it is the vendor's
		// own agent talking to itself and presenting it as the answer would attribute one thing to
		// another. ModeEmpty leaves no way to spawn one, so this should never fire.
		return
	}

	switch data := event.Data.(type) {
	case *sdk.AssistantMessageDeltaData:
		a.emit(Event{Kind: EventText, Text: data.DeltaContent})

	case *sdk.AssistantReasoningDeltaData:
		a.emit(Event{Kind: EventThinking, Text: data.DeltaContent})

	case *sdk.ExternalToolRequestedData:
		arguments, err := json.Marshal(data.Arguments)
		if err != nil {
			arguments = []byte("{}")
		}
		// The vendor's request id rather than its tool call id, because the request id is what
		// resolves the call and Canopy has to be able to get back to it from a result that has
		// travelled through a tool registry and a permission prompt.
		a.emit(Event{Kind: EventToolCall, Call: &core.ToolCall{
			ID:    data.RequestID,
			Name:  data.ToolName,
			Input: arguments,
		}})

	case *sdk.AssistantUsageData:
		a.emit(Event{Kind: EventUsage, Usage: usageOf(data)})

	case *sdk.SessionIdleData:
		a.emit(Event{Kind: EventIdle})

	case *sdk.AbortData:
		a.emit(Event{Kind: EventFailed, Err: fmt.Errorf("the turn was stopped: %s", data.Reason)})

	case *sdk.SessionErrorData:
		a.emit(Event{Kind: EventFailed, Err: sessionError(data)})
	}
}

func (a *sdkAgent) emit(event Event) {
	defer func() {
		// A send on a channel closed by Close, which happens when a conversation is shut down while
		// the vendor is still talking. Recovered rather than guarded with a flag, because any flag
		// would be read before the send and closed after it.
		_ = recover()
	}()
	a.events <- event
}

func (a *sdkAgent) Events() <-chan Event { return a.events }

func (a *sdkAgent) Send(ctx context.Context, prompt string) error {
	if _, err := a.session.Send(ctx, sdk.MessageOptions{Prompt: prompt}); err != nil {
		return classify(err)
	}
	return nil
}

func (a *sdkAgent) Answer(ctx context.Context, callID, result, failure string) error {
	request := &rpc.HandlePendingToolCallRequest{RequestID: callID}
	if failure != "" {
		request.Error = &failure
	} else {
		request.Result = rpc.ExternalToolStringResult(result)
	}
	if _, err := a.session.RPC.Tools.HandlePendingToolCall(ctx, request); err != nil {
		return classify(err)
	}
	return nil
}

func (a *sdkAgent) Abort(ctx context.Context) error { return a.session.Abort(ctx) }

// Close ends the conversation and the process behind it.
//
// In this order, and the order is the point: stop listening, disconnect the session, stop the
// runtime, then close the channel. Closing the channel first would race the handler into a send on a
// closed channel, and stopping the runtime before disconnecting leaves the vendor with a session it
// believes is live.
func (a *sdkAgent) Close() error {
	var err error
	a.closeOnce.Do(func() {
		if a.unsubscribe != nil {
			a.unsubscribe()
		}
		err = errors.Join(a.session.Disconnect(), a.client.Stop())
		close(a.events)
	})
	return err
}

// usageOf reads what a model call consumed.
//
// Every field is a pointer and absent means the vendor did not say, which is not the same as zero.
// Canopy has a type for that distinction at the top, core.Usage.CostKnown, and nothing here sets it:
// what a Copilot turn costs in dollars is a question about a seat and a monthly allowance rather
// than about tokens, so a per-token figure would be a number somebody could act on and should not.
func usageOf(data *sdk.AssistantUsageData) core.Usage {
	usage := core.Usage{}
	if data.InputTokens != nil {
		usage.InputTokens = int(*data.InputTokens)
	}
	if data.OutputTokens != nil {
		usage.OutputTokens = int(*data.OutputTokens)
	}
	if data.CacheReadTokens != nil {
		usage.CacheReadTokens = int(*data.CacheReadTokens)
	}
	if data.CacheWriteTokens != nil {
		usage.CacheWriteTokens = int(*data.CacheWriteTokens)
	}
	return usage
}
