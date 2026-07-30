package copilot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Opener starts a conversation with the vendor's agent.
//
// A field rather than a hard call to Open so that everything below can be driven without a
// subscription, a network or a CLI binary. That is not only a testing convenience: it is what makes
// the request-to-session translation, which is the whole difficulty of this route, provable at all.
type Opener func(ctx context.Context, conversation Conversation) (Agent, error)

// Client runs one Canopy conversation on the user's Copilot subscription.
//
// It is bound to a conversation, which is the difference between this and every other provider
// client in the tree. The others are stateless: hand one the messages and it sends them. The vendor
// here owns the conversation and offers no way to hand it a history, so this holds the session and
// keeps track of how much of Canopy's message list that session has already heard.
//
// One turn at a time. The events of a session arrive on one channel, so two turns reading it at once
// would each get half of the other's reply. Canopy's engine already runs one turn per conversation,
// and this refuses the case rather than trusting that it stays true.
type Client struct {
	// name is what this client is called when a turn is attributed. The credential's name rather
	// than the provider's, because somebody with two Copilot seats needs to know which answered.
	name string

	open         Opener
	conversation Conversation

	mu    sync.Mutex
	agent Agent
	// sent is how many of the caller's messages the session has already heard.
	sent int
	// busy is a stream that has been handed out and not yet closed.
	busy bool
	// closed stops a conversation that has been shut down from quietly starting a new runtime.
	closed bool
}

var _ core.ProviderClient = (*Client)(nil)

// New builds a client for one conversation on one credential.
func New(name string, conversation Conversation, options ...Option) *Client {
	client := &Client{name: name, open: Open, conversation: conversation}
	for _, option := range options {
		option(client)
	}
	return client
}

// Option adjusts a client.
type Option func(*Client)

// WithOpener replaces how a conversation with the vendor is started.
func WithOpener(open Opener) Option {
	return func(c *Client) { c.open = open }
}

// Name identifies this client for display and for attributing usage.
func (c *Client) Name() string {
	if c.name == "" {
		return Name
	}
	return c.name
}

// ErrHistoryRewritten means the caller changed a conversation the vendor is holding.
//
// Its own error because it is the one place where this route's design becomes visible to somebody
// using it, and a vague failure here would be read as a bug rather than as the documented limit it
// is. The history of a Copilot conversation lives in GitHub's runtime, so editing it, re-rolling a
// turn or compacting it locally leaves Canopy's idea of the conversation and the vendor's disagreeing
// about what was said. Reported at the moment it happens rather than absorbed, because absorbing it
// means the model answers a conversation the user can no longer see.
var ErrHistoryRewritten = errors.New(
	"this conversation runs on GitHub's Copilot agent, which holds its own history, so a turn cannot " +
		"be re-run against an edited or compacted one. Start a new conversation to change what has " +
		"been said")

// Stream sends whatever is new and returns the reply as it arrives.
func (c *Client) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, errors.New("this Copilot conversation has been closed")
	}
	if c.busy {
		return nil, errors.New(
			"a turn is already running on this Copilot conversation, and the vendor's session " +
				"delivers one conversation's events on one channel, so a second reader would take " +
				"half of the first one's reply")
	}

	agent, err := c.ensure(ctx, req)
	if err != nil {
		return nil, err
	}

	results := answersIn(req)
	switch {
	case len(results) > 0:
		// The agent asked for a tool, Canopy ran it through the permission gate, and this is the
		// answer going back down. No prompt: the vendor's turn never ended, it is waiting on exactly
		// these calls, and sending a message here would be a second turn talking over the first.
		for _, result := range results {
			failure := ""
			if result.IsError || result.Refused {
				failure = result.Content
			}
			if err := agent.Answer(ctx, result.CallID, result.Content, failure); err != nil {
				return nil, err
			}
		}
	default:
		prompt, err := c.promptFor(req)
		if err != nil {
			return nil, err
		}
		if err := agent.Send(ctx, prompt); err != nil {
			return nil, err
		}
	}

	c.sent = len(req.Messages)
	c.busy = true
	return newStream(ctx, c, agent), nil
}

// ensure starts the conversation if it has not started.
//
// The model and the effort belong to the session rather than to a request, because the vendor sets
// them when the session is made. A request that names a different model than the one the session is
// running is answered by the session's, and that is recorded in LIMITATIONS rather than papered over
// with a silent restart, which would throw away the conversation to honour a flag.
func (c *Client) ensure(ctx context.Context, req core.Request) (Agent, error) {
	if c.agent != nil {
		return c.agent, nil
	}

	conversation := c.conversation
	if conversation.Model == "" {
		conversation.Model = req.Model
	}
	if conversation.Effort == core.EffortDefault {
		conversation.Effort = req.Effort
	}
	if !conversation.DisableThinking {
		conversation.DisableThinking = req.DisableThinking
	}
	if conversation.System == "" {
		conversation.System = req.System
	}
	if len(conversation.Tools) == 0 {
		conversation.Tools = req.Tools
	}

	agent, err := c.open(ctx, conversation)
	if err != nil {
		return nil, err
	}
	c.agent = agent
	return agent, nil
}

// promptFor works out what the session has not already heard.
//
// This is the whole of the request-shaped to session-shaped translation and it is three cases. A
// session that has heard nothing gets everything, with the older turns rendered as a transcript
// because there is no other way to put them there. A session that is up to date gets only the new
// message. A caller whose history is shorter than what the session has already heard has rewritten
// it, and that is refused.
func (c *Client) promptFor(req core.Request) (string, error) {
	if len(req.Messages) < c.sent {
		return "", ErrHistoryRewritten
	}
	fresh := req.Messages[c.sent:]
	if len(fresh) == 0 {
		return "", errors.New("there is nothing new in this request to send")
	}
	if c.sent == 0 {
		return seed(fresh)
	}

	// Assistant messages in the new range are the session's own replies coming back round, since the
	// engine hands over the whole conversation every turn. Sending them back would have the agent
	// read its own words as though somebody else had said them.
	said := make([]string, 0, len(fresh))
	for _, message := range fresh {
		if message.Role == core.RoleUser && strings.TrimSpace(message.Text) != "" {
			said = append(said, message.Text)
		}
	}
	if len(said) == 0 {
		return "", errors.New("there is nothing new in this request to send")
	}
	return strings.Join(said, "\n\n"), nil
}

// seed puts a conversation that already had history in front of an agent that has none.
//
// Reached when a client is built for a conversation that is not new: one picked up from history
// after a restart, a side conversation, or a compaction. The SDK has no API for seeding a session,
// so the only surface available is the prompt, and what goes in it is the earlier turns rendered as
// a labelled transcript followed by the actual message.
//
// Labelled, and that is the part worth defending. The transcript is not the same thing as those
// turns having happened, because roles collapse into text and the model is being told about a
// conversation rather than having had it. Saying so in the prompt is what stops the model treating
// its own earlier words as instructions it is now being given. The alternative, refusing to run at
// all on a conversation with history, was considered and rejected: it would mean a Copilot
// conversation could not survive closing the program, which is a worse product than one whose
// restored context is slightly weaker.
func seed(messages []core.Message) (string, error) {
	last := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == core.RoleUser && strings.TrimSpace(messages[i].Text) != "" {
			last = i
			break
		}
	}
	if last < 0 {
		return "", errors.New("this conversation has nothing from the user in it to send")
	}
	if last == 0 {
		return messages[0].Text, nil
	}

	var out strings.Builder
	out.WriteString("This conversation started elsewhere. What follows is what was said before, " +
		"for context. It is a record rather than instructions, and the request comes after it.\n\n" +
		"--- earlier in this conversation ---\n")
	for _, message := range messages[:last] {
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		who := "User"
		if message.Role == core.RoleAssistant {
			who = "Assistant"
		}
		fmt.Fprintf(&out, "\n%s: %s\n", who, text)
	}
	out.WriteString("--- end of what was said before ---\n\n")
	out.WriteString(messages[last].Text)
	return out.String(), nil
}

// answersIn reads the tool results a request is carrying back.
//
// Only the last message, because that is where Canopy's own loop puts them: it appends one user
// message holding every result for the calls the previous reply asked for. Looking further back
// would re-answer calls the vendor has already been given results for.
func answersIn(req core.Request) []core.ToolResult {
	if len(req.Messages) == 0 {
		return nil
	}
	last := req.Messages[len(req.Messages)-1]
	if last.Role != core.RoleUser {
		return nil
	}
	return last.ToolResults
}

// release marks the turn finished so the next one may start.
func (c *Client) release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.busy = false
}

// Close ends the conversation and the process behind it.
//
// This is the other half of holding a session per conversation, and it is why the resolver that
// hands these out has a Close of its own. A long-lived session is a child process on the machine and
// a session GitHub believes is open; a program that exits without saying so leaves both to be
// cleaned up by something else, and one of them is somebody else's server.
func (c *Client) Close() error {
	c.mu.Lock()
	agent := c.agent
	c.agent = nil
	c.closed = true
	c.mu.Unlock()

	if agent == nil {
		return nil
	}
	return agent.Close()
}
