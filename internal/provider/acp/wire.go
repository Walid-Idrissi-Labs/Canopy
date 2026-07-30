package acp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// The ACP v1 wire format, transcribed from the published schema rather than remembered.
//
// Every name below is the schema's own spelling. The method names come from the constants the
// specification's reference implementation exports, the field names from the JSON Schema it
// generates, and the whole set was then confirmed by running a real bridge and reading what it said.
// Two of these are easy to get subtly wrong and both were checked rather than assumed: the stop
// reasons are snake_case words on the `session/prompt` result, not on a notification, and the
// discriminator on a session update is the string field `sessionUpdate` sitting beside that
// variant's own fields rather than wrapping them.
//
// Only the part of the protocol Canopy uses is here. The terminal, filesystem, elicitation, MCP and
// provider-routing methods exist and are deliberately absent: Canopy advertises none of the
// capabilities that would let a bridge call them, so a bridge that calls one anyway is answered with
// "method not found", which is the truthful answer rather than a stub that pretends.

const (
	// protocolVersion is the version Canopy speaks. An integer, bumped only for breaking changes.
	//
	// Pinned to 1 rather than tracking whatever the bridge offers. ACP v2 exists as a draft, dated
	// 2026-07-20, whose own announcement says not to ship it by default before it stabilises, and a
	// client that sends the highest number it has heard of is a client that changes behaviour when
	// somebody else releases something.
	protocolVersion = 1

	methodInitialize      = "initialize"
	methodSessionNew      = "session/new"
	methodSessionPrompt   = "session/prompt"
	methodSessionCancel   = "session/cancel"
	methodSetConfigOption = "session/set_config_option"

	// methodSessionUpdate and methodRequestPermission travel the other way, from the bridge to
	// Canopy. They are here rather than in a separate block because the wire does not care which
	// direction a name is used in, and splitting them invites the belief that it does.
	methodSessionUpdate     = "session/update"
	methodRequestPermission = "session/request_permission"
)

// Stop reasons, as the `stopReason` field of a session/prompt result.
const (
	stopEndTurn         = "end_turn"
	stopMaxTokens       = "max_tokens"
	stopMaxTurnRequests = "max_turn_requests"
	stopRefusal         = "refusal"
	stopCancelled       = "cancelled"
)

// Session update discriminators, as the `sessionUpdate` field.
const (
	updateUserMessageChunk  = "user_message_chunk"
	updateAgentMessageChunk = "agent_message_chunk"
	updateAgentThoughtChunk = "agent_thought_chunk"
	updateToolCall          = "tool_call"
	updateToolCallUpdate    = "tool_call_update"
	updateUsage             = "usage_update"
)

// methodNotFound is the JSON-RPC code for a method the receiver does not implement.
const methodNotFound = -32601

// message is one JSON-RPC frame in either direction.
//
// One struct for requests, responses and notifications rather than three, because the wire really is
// one shape and the distinctions are which fields are present: an id and a method is a request, an
// id and a result is a response, a method with no id is a notification. Decoding into three types
// would mean guessing which one to try first.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

// initializeParams is what Canopy tells a bridge about itself.
//
// The capabilities are the interesting part and every one of them is false on purpose. `fs` false
// means the bridge does not route file reads and writes back through Canopy, and `terminal` false
// means it does not route commands. Neither of those disables anything: Claude Code has its own file
// and shell tools and will use them. What the flags decide is whether the work passes through
// Canopy's process on the way, and advertising a capability Canopy would then have to gate is how a
// client ends up standing in as somebody's approver for a tool call it does not understand. See
// requestPermission for the rest of that argument.
type initializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities clientCapabilities `json:"clientCapabilities"`
	ClientInfo         implementation     `json:"clientInfo"`
}

type clientCapabilities struct {
	FS       fsCapability `json:"fs"`
	Terminal bool         `json:"terminal"`
}

type fsCapability struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type initializeResult struct {
	ProtocolVersion int             `json:"protocolVersion"`
	AgentInfo       *implementation `json:"agentInfo,omitempty"`
	AuthMethods     []authMethod    `json:"authMethods"`
}

type authMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// newSessionParams asks for a conversation rooted at a directory.
//
// McpServers is present and always empty, and the empty list is the answer to half of Q-23. It is
// the only door ACP v1 offers for handing a client's own tools to an agent, Canopy's tools are not
// an MCP server, and so "does Canopy offer its tools to a delegated turn" is settled by the protocol
// before it is settled by taste. Sent explicitly rather than omitted so that the absence is a
// statement in the traffic rather than a default somebody has to know about.
type newSessionParams struct {
	CWD        string   `json:"cwd"`
	MCPServers []string `json:"mcpServers"`
}

type newSessionResult struct {
	SessionID     string         `json:"sessionId"`
	ConfigOptions []configOption `json:"configOptions,omitempty"`
}

// configOption is a session setting the bridge offers, such as which model answers.
//
// The ids are the agent's to choose, not the protocol's, which is why Canopy reads them off the
// session it just created instead of hard-coding "model". A setting Canopy asked for and the bridge
// does not offer is reported to the user rather than dropped, because the alternative is a screen
// naming one model while another one answers.
type configOption struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Type         string             `json:"type"`
	CurrentValue any                `json:"currentValue,omitempty"`
	Options      []configOptionItem `json:"options,omitempty"`
}

type configOptionItem struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

type setConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Type      string `json:"type"`
	Value     string `json:"value"`
}

type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

// contentBlock is one piece of a prompt. Only text is sent: images and embedded resources are
// capabilities Canopy does not have anything to put in yet.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func textBlock(text string) contentBlock { return contentBlock{Type: "text", Text: text} }

type promptResult struct {
	StopReason string `json:"stopReason"`

	// Usage is not in the ACP v1 schema. The Claude bridge sends it anyway, carrying the turn's real
	// token counts, and reading it is worth the caveat: without it a delegated turn reports no
	// tokens at all, and "this turn used nothing" is a worse thing to say than "this turn used what
	// the agent said it used". Read defensively for that reason, so a bridge that stops sending it,
	// or never sent it, is a turn with no token count rather than a turn that fails.
	Usage *promptUsage `json:"usage,omitempty"`
}

type promptUsage struct {
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	CachedReadTokens  int `json:"cachedReadTokens"`
	CachedWriteTokens int `json:"cachedWriteTokens"`
}

type cancelParams struct {
	SessionID string `json:"sessionId"`
}

// sessionNotification is the bridge streaming a turn back.
//
// The update is decoded twice on purpose: once for the discriminator and once for the variant, which
// is what the schema's shape requires, since each variant's fields sit beside `sessionUpdate` rather
// than under it.
type sessionNotification struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type updateKind struct {
	SessionUpdate string `json:"sessionUpdate"`
}

type contentChunk struct {
	Content contentBlock `json:"content"`
}

type toolCallUpdate struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status,omitempty"`
}

// permissionParams is the bridge asking Canopy to approve one of its own tool calls.
type permissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  toolCallUpdate     `json:"toolCall"`
	Options   []permissionOption `json:"options"`
}

type permissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type permissionResult struct {
	Outcome permissionOutcome `json:"outcome"`
}

type permissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// conn is JSON-RPC 2.0 over a pair of pipes.
//
// Reads are not locked and writes are, which is the asymmetry the traffic has. One goroutine drives
// the turn and does all the reading; writes come from that goroutine and also from whoever cancels
// it, and two interleaved JSON documents on one pipe is a stream neither side can recover.
type conn struct {
	out *bufio.Reader
	in  io.Writer

	writeMu sync.Mutex
	nextID  int64
}

func newConn(r io.Reader, w io.Writer) *conn {
	return &conn{out: bufio.NewReaderSize(r, 64*1024), in: w}
}

// send writes a request and returns the id it was given.
func (c *conn) send(method string, params any) (int64, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.nextID++
	id := c.nextID
	return id, c.write(message{JSONRPC: "2.0", ID: &id, Method: method, Params: encode(params)})
}

// notify writes a notification, which is a request nobody answers.
func (c *conn) notify(method string, params any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return c.write(message{JSONRPC: "2.0", Method: method, Params: encode(params)})
}

// reply answers a request the bridge sent.
func (c *conn) reply(id int64, result any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return c.write(message{JSONRPC: "2.0", ID: &id, Result: encode(result)})
}

// replyError answers a request Canopy will not serve.
func (c *conn) replyError(id int64, code int, text string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return c.write(message{JSONRPC: "2.0", ID: &id, Error: &rpcError{Code: code, Message: text}})
}

func (c *conn) write(m message) error {
	encoded, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encoding an %s message: %w", m.Method, err)
	}
	// One document per line, which is what ACP over stdio is. The newline is part of the framing and
	// not decoration, so it goes in the same write as the document: two writes could interleave with
	// a cancel from another goroutine even under the lock above, if the lock were ever dropped
	// between them by a later edit.
	if _, err := c.in.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("writing to the Claude Code bridge: %w", err)
	}
	return nil
}

// read returns the next frame, or an error when the bridge has stopped talking.
func (c *conn) read() (message, error) {
	for {
		line, err := c.out.ReadBytes('\n')
		if err != nil {
			if len(line) == 0 {
				return message{}, err
			}
			// A final line with no newline is still a document. Returned rather than discarded,
			// because the last thing a bridge says before exiting is usually the thing that explains
			// why it exited.
		}
		if len(trimSpace(line)) == 0 {
			if err != nil {
				return message{}, err
			}
			continue
		}
		var m message
		if decodeErr := json.Unmarshal(line, &m); decodeErr != nil {
			return message{}, fmt.Errorf(
				"the Claude Code bridge sent something that is not JSON-RPC: %w", decodeErr)
		}
		return m, nil
	}
}

func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// encode marshals params, returning nil for a nil value so the field is omitted rather than sent as
// the literal null. Marshalling here cannot fail for any type in this file, and a failure would show
// up as a missing params object rather than as a wrong one.
func encode(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}
