package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// The Codex app server wire format, transcribed from the schema the binary generates for itself.
//
// Not remembered and not read off a blog post. `codex app-server generate-json-schema` writes the
// whole protocol out of the installed binary, so the names below are that binary's own spelling for
// the version on the machine they were taken from, and every one of them was then confirmed by
// driving a real app server and reading both directions of the traffic. Three things that a schema
// would not have settled came out of that and are recorded where they are used: the `initialize`
// result echoes back the composed user agent, so a client can read its own name back rather than
// hope; cancelling a login still produces an `account/login/completed`, so a waiter is always
// released; and a turn's tokens arrive on a notification rather than on the `turn/start` result.
//
// Only the part of the protocol Canopy uses is here. The filesystem, terminal, plugin, marketplace,
// realtime and MCP methods exist and are deliberately absent: Canopy asks for none of them, and a
// server request Canopy has nothing honest to answer is answered as "method not found" rather than
// stubbed. See stream.go.

const (
	methodInitialize  = "initialize"
	methodInitialized = "initialized"

	methodAccountRead       = "account/read"
	methodAccountRateLimits = "account/rateLimits/read"
	methodLoginStart        = "account/login/start"
	methodLoginCancel       = "account/login/cancel"
	methodLogout            = "account/logout"

	methodThreadStart   = "thread/start"
	methodTurnStart     = "turn/start"
	methodTurnInterrupt = "turn/interrupt"

	// Travelling the other way, from the app server to Canopy. In the same block because the wire
	// does not care which direction a name is used in, and splitting them invites the belief that
	// it does.
	notifyLoginCompleted   = "account/login/completed"
	notifyItemStarted      = "item/started"
	notifyItemCompleted    = "item/completed"
	notifyAgentMessage     = "item/agentMessage/delta"
	notifyReasoningSummary = "item/reasoning/summaryTextDelta"
	notifyReasoningText    = "item/reasoning/textDelta"
	notifyTokenUsage       = "thread/tokenUsage/updated"
	notifyTurnCompleted    = "turn/completed"
	notifyError            = "error"

	requestApproveCommand    = "item/commandExecution/requestApproval"
	requestApproveFileChange = "item/fileChange/requestApproval"
)

// jsonRPCVersion is the only value the app server accepts, and the only one Canopy sends.
const jsonRPCVersion = "2.0"

// methodNotFound is the JSON-RPC code for a method the receiver does not implement.
const methodNotFound = -32601

// Turn statuses, as the `status` field of a Turn.
const (
	turnCompleted   = "completed"
	turnInterrupted = "interrupted"
	turnFailed      = "failed"
	turnInProgress  = "inProgress"
)

// Thread item types, as the `type` field of the item on an item/* notification.
//
// Every one of these except agentMessage and reasoning becomes a notice. That is the load-bearing
// decision in this package rather than a formatting choice, and the reason is in reportItem.
const (
	itemUserMessage      = "userMessage"
	itemAgentMessage     = "agentMessage"
	itemReasoning        = "reasoning"
	itemPlan             = "plan"
	itemCommandExecution = "commandExecution"
	itemFileChange       = "fileChange"
	itemMCPToolCall      = "mcpToolCall"
	itemDynamicToolCall  = "dynamicToolCall"
	itemWebSearch        = "webSearch"
	itemImageView        = "imageView"
	itemContextCompact   = "contextCompaction"
)

// decisionDecline is the `decision` field of an approval response, and the only one Canopy sends.
//
// The protocol offers six: accept, acceptForSession, two amendment forms that persist a rule, and
// two refusals. The refusals differ in what happens next, and the choice between them is a real one
// rather than a coin toss: decline lets the agent carry on and find another way, cancel stops the
// whole turn. Canopy declines, because it is refusing to vouch for one call rather than objecting
// to the work. The others are named here and not defined, so that nobody reading this has to go and
// check whether one of them was quietly used somewhere.
const decisionDecline = "decline"

// message is one JSON-RPC frame in either direction.
//
// One struct for requests, responses and notifications rather than three, because the wire really
// is one shape and the distinctions are which fields are present: an id and a method is a request,
// an id and a result is a response, a method with no id is a notification.
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

// initializeParams is what Canopy tells the app server about itself.
//
// ClientInfo.Name is the whole ethical surface of this route and is documented on the constant it
// is set from. Capabilities is deliberately the zero value in every field: experimentalApi opts
// into methods whose shape is allowed to change without notice, and requestAttestation opts into
// being asked to generate an upstream attestation header, which is a thing Canopy has no business
// producing on somebody else's behalf.
type initializeParams struct {
	ClientInfo   clientInfo              `json:"clientInfo"`
	Capabilities *initializeCapabilities `json:"capabilities,omitempty"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Title   string `json:"title,omitempty"`
}

type initializeCapabilities struct {
	ExperimentalAPI    bool `json:"experimentalApi"`
	RequestAttestation bool `json:"requestAttestation"`
}

// initializeResult is what the app server answers with.
//
// UserAgent is the field that makes this route auditable from inside. The app server composes the
// upstream user agent from the name the client just gave it, so reading it back is Canopy checking
// what it is about to be identified as rather than trusting that the value it sent was used. See
// checkIdentity.
type initializeResult struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
}

// loginStartParams asks the app server to sign somebody in.
//
// Type is one of loginChatGPT or loginDeviceCode. The third mode the protocol has, chatgptAuthTokens,
// is not here and is not reachable from this package: its own schema describes it as unstable, for
// OpenAI internal use, and not to be used, and it exists for hosts that already own the token
// lifecycle. Owning that lifecycle is precisely the liability this route was chosen to avoid.
type loginStartParams struct {
	Type string `json:"type"`
}

const (
	// loginChatGPT opens a browser flow whose callback the app server hosts itself, on its own
	// loopback port. That is what lets phase S constraint 5 survive this task: no listener of
	// Canopy's own is opened anywhere on this route.
	loginChatGPT = "chatgpt"

	// loginDeviceCode prints a short code to type at a URL, for a machine with no browser, which is
	// most of the machines a coding agent runs on.
	loginDeviceCode = "chatgptDeviceCode"
)

// loginStartResult covers both flows, since the discriminator is the type field and the fields
// beside it differ.
type loginStartResult struct {
	Type    string `json:"type"`
	LoginID string `json:"loginId"`

	// AuthURL is set for the browser flow.
	AuthURL string `json:"authUrl,omitempty"`

	// VerificationURL and UserCode are set for the device code flow.
	VerificationURL string `json:"verificationUrl,omitempty"`
	UserCode        string `json:"userCode,omitempty"`
}

type loginCancelParams struct {
	LoginID string `json:"loginId"`
}

// loginCompletedParams is how a sign-in ends, in every case including cancellation.
//
// Confirmed rather than assumed, because it decides whether a waiting client can ever be left
// hanging: cancelling a login produces one of these with Success false, so there is exactly one
// event that ends a wait and no path that ends it silently.
type loginCompletedParams struct {
	LoginID string `json:"loginId,omitempty"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// accountReadParams asks who is signed in.
//
// RefreshToken is left false by this package and the reason is worth stating where somebody would
// otherwise turn it on. Setting it makes the app server renew its stored grant before answering,
// which is the right thing when a turn is about to run and the wrong thing when a person is only
// asking a question: on a route that rotates refresh tokens, a probe that renews spends the token
// the user's own `codex` was going to use. See Report in account.go for where it is turned on.
type accountReadParams struct {
	RefreshToken bool `json:"refreshToken,omitempty"`
}

type accountReadResult struct {
	Account            *accountInfo `json:"account"`
	RequiresOpenAIAuth bool         `json:"requiresOpenaiAuth"`
}

// accountInfo is who the app server says is signed in.
//
// Three shapes share one field: an apiKey account carries only its type, a chatgpt account carries
// an email and a plan, and an amazonBedrock account carries only its type. Decoded into one struct
// with optional fields rather than three, because every caller wants the same two questions
// answered and a type switch here would push it onto them.
type accountInfo struct {
	Type     string `json:"type"`
	Email    string `json:"email,omitempty"`
	PlanType string `json:"planType,omitempty"`
}

const (
	accountChatGPT = "chatgpt"
	accountAPIKey  = "apiKey"
)

// rateLimitsResult is what the subscription's limits look like right now.
//
// RateLimits is documented in the schema as the backward-compatible single-bucket view of
// RateLimitsByLimitID, so it is what this reads. The multi-bucket map is carried too, because a
// bucket named for something other than "codex" is a fact about the account that the flat view
// silently drops.
type rateLimitsResult struct {
	RateLimits         rateLimitSnapshot            `json:"rateLimits"`
	RateLimitsByLimit  map[string]rateLimitSnapshot `json:"rateLimitsByLimitId,omitempty"`
	RateLimitResetCred *resetCredits                `json:"rateLimitResetCredits,omitempty"`
}

type rateLimitSnapshot struct {
	LimitID              string           `json:"limitId,omitempty"`
	LimitName            string           `json:"limitName,omitempty"`
	PlanType             string           `json:"planType,omitempty"`
	Primary              *rateLimitWindow `json:"primary,omitempty"`
	Secondary            *rateLimitWindow `json:"secondary,omitempty"`
	Credits              *creditsSnapshot `json:"credits,omitempty"`
	RateLimitReachedType string           `json:"rateLimitReachedType,omitempty"`
}

type rateLimitWindow struct {
	UsedPercent        int   `json:"usedPercent"`
	WindowDurationMins int64 `json:"windowDurationMins,omitempty"`
	ResetsAt           int64 `json:"resetsAt,omitempty"`
}

type creditsSnapshot struct {
	HasCredits bool   `json:"hasCredits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
}

type resetCredits struct {
	AvailableCount int64 `json:"availableCount"`
}

// threadStartParams opens a conversation.
//
// ApprovalPolicy and Sandbox are the two fields that decide what a delegated turn is allowed to do
// on somebody's machine, and both are argued for on the constants they are set from rather than
// here, because the argument is about Q-23 rather than about JSON.
type threadStartParams struct {
	CWD            string `json:"cwd,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Sandbox        string `json:"sandbox,omitempty"`
	Model          string `json:"model,omitempty"`

	// Ephemeral keeps the conversation out of the user's saved session list.
	//
	// True, always. Canopy already keeps the transcript and hands the whole of it over on every
	// turn, so a rollout file per turn in ~/.codex/sessions would be a second copy of somebody's
	// conversation, written somewhere they did not ask for it and would not think to clear.
	Ephemeral bool `json:"ephemeral"`

	// DeveloperInstructions is where a Canopy agent profile's system prompt goes.
	//
	// Advice rather than instructions, and worth writing down here rather than leaving to be
	// discovered: the app server has its own base instructions and this does not replace them.
	DeveloperInstructions string `json:"developerInstructions,omitempty"`
}

const (
	// approvalOnRequest lets the agent ask, which is what makes Canopy's refusal visible.
	//
	// The alternative, "never", reads as safer and is not: it tells the app server to stop asking
	// and to get on with whatever the sandbox permits, so the calls Canopy would have declined
	// simply happen without anybody being told. Asking and declining is the arrangement where the
	// user can see what was refused, and it is the same answer S-04 reached on the Claude route.
	approvalOnRequest = "on-request"

	// sandboxReadOnly is what the delegated agent is allowed to touch.
	//
	// The honest setting for a route where Canopy declines every approval. Anything wider would be
	// Canopy granting write access to a tool loop it has no say over, on the strength of a
	// permission mode displayed on a screen that does not govern this turn, which is exactly what
	// Q-23 forbids. It makes a delegated turn a weaker agent than a metered one, and saying so is
	// the point rather than the cost.
	sandboxReadOnly = "read-only"
)

type threadStartResult struct {
	Thread          threadInfo `json:"thread"`
	Model           string     `json:"model,omitempty"`
	ModelProvider   string     `json:"modelProvider,omitempty"`
	ReasoningEffort string     `json:"reasoningEffort,omitempty"`
}

type threadInfo struct {
	ID string `json:"id"`
}

// turnStartParams asks the question.
type turnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []userInput `json:"input"`
	Model    string      `json:"model,omitempty"`
	Effort   string      `json:"effort,omitempty"`
}

// userInput is one piece of a turn's input. Only text is sent: the other variants are images,
// skills and local files, none of which Canopy has anything to put in yet.
type userInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func textInput(text string) userInput { return userInput{Type: "text", Text: text} }

type turnStartResult struct {
	Turn turnInfo `json:"turn"`
}

type turnInfo struct {
	ID     string     `json:"id"`
	Status string     `json:"status"`
	Error  *turnError `json:"error,omitempty"`
}

type turnError struct {
	Message           string `json:"message"`
	AdditionalDetails string `json:"additionalDetails,omitempty"`
}

type turnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type turnCompletedParams struct {
	ThreadID string   `json:"threadId"`
	Turn     turnInfo `json:"turn"`
}

// errorNotification is a turn failing, which arrives beside the turn's own status rather than
// instead of it. WillRetry matters: the app server retries some failures itself, and reporting a
// retryable one as the end of the turn would be Canopy calling a turn dead that is still running.
type errorNotification struct {
	ThreadID  string    `json:"threadId"`
	TurnID    string    `json:"turnId"`
	Error     turnError `json:"error"`
	WillRetry bool      `json:"willRetry"`
}

type deltaNotification struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

type itemNotification struct {
	ThreadID string     `json:"threadId"`
	TurnID   string     `json:"turnId"`
	Item     threadItem `json:"item"`
}

// threadItem is one thing that happened during a turn.
//
// Decoded into a single struct across every variant rather than a discriminated union, because
// almost all of them become the same thing on Canopy's side: a notice. Only the type field and a
// handful of display strings are ever read.
type threadItem struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Text   string `json:"text,omitempty"`
	Status string `json:"status,omitempty"`

	Command string `json:"command,omitempty"`
	CWD     string `json:"cwd,omitempty"`
	Server  string `json:"server,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Query   string `json:"query,omitempty"`
	Path    string `json:"path,omitempty"`

	Changes map[string]json.RawMessage `json:"changes,omitempty"`
}

type tokenUsageParams struct {
	ThreadID   string      `json:"threadId"`
	TurnID     string      `json:"turnId"`
	TokenUsage threadUsage `json:"tokenUsage"`
}

// threadUsage carries both a running total for the thread and the last turn on its own.
//
// Last is what Canopy reports, and the distinction is not cosmetic. Total is cumulative across
// every turn in the thread, so reporting it as the turn's usage would make each turn look like it
// cost everything before it as well.
type threadUsage struct {
	Last               usageBreakdown `json:"last"`
	Total              usageBreakdown `json:"total"`
	ModelContextWindow int64          `json:"modelContextWindow,omitempty"`
}

type usageBreakdown struct {
	InputTokens           int64 `json:"inputTokens"`
	CachedInputTokens     int64 `json:"cachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	ReasoningOutputTokens int64 `json:"reasoningOutputTokens"`
	TotalTokens           int64 `json:"totalTokens"`
}

// approvalParams is the app server asking Canopy to vouch for one of its own tool calls.
//
// One struct for the command and the file-change flavours: they carry different detail and Canopy
// gives the same answer to both, so what is read is only enough to say in the notice what was
// refused.
type approvalParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Command  string `json:"command,omitempty"`
	CWD      string `json:"cwd,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type approvalResponse struct {
	Decision string `json:"decision"`
}

// conn is JSON-RPC 2.0 over a pair of pipes.
//
// Reads are not locked and writes are, which is the asymmetry the traffic has. One goroutine drives
// the exchange and does all the reading; writes come from that goroutine and also from whoever
// cancels it, and two interleaved JSON documents on one pipe is a stream neither side can recover.
type conn struct {
	out *bufio.Reader
	in  io.Writer

	writeMu sync.Mutex
	nextID  int64
}

func newConn(r io.Reader, w io.Writer) *conn {
	return &conn{out: bufio.NewReaderSize(r, 128*1024), in: w}
}

// send writes a request and returns the id it was given.
func (c *conn) send(method string, params any) (int64, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	c.nextID++
	id := c.nextID
	return id, c.write(message{JSONRPC: jsonRPCVersion, ID: &id, Method: method, Params: encode(params)})
}

// notify writes a notification, which is a request nobody answers.
func (c *conn) notify(method string, params any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return c.write(message{JSONRPC: jsonRPCVersion, Method: method, Params: encode(params)})
}

// reply answers a request the app server sent.
func (c *conn) reply(id int64, result any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return c.write(message{JSONRPC: jsonRPCVersion, ID: &id, Result: encode(result)})
}

// replyError answers a request Canopy will not serve.
func (c *conn) replyError(id int64, code int, text string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	return c.write(message{JSONRPC: jsonRPCVersion, ID: &id, Error: &rpcError{Code: code, Message: text}})
}

func (c *conn) write(m message) error {
	encoded, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encoding a %s message: %w", m.Method, err)
	}
	// One document per line, which is what this protocol is over stdio. The newline is part of the
	// framing rather than decoration, so it goes out in the same write as the document: two writes
	// could interleave with an interrupt from another goroutine if a later edit ever dropped the
	// lock between them.
	if _, err := c.in.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("writing to the Codex app server: %w", err)
	}
	return nil
}

// read returns the next frame, or an error when the app server has stopped talking.
func (c *conn) read() (message, error) {
	for {
		line, err := c.out.ReadBytes('\n')
		if err != nil && len(line) == 0 {
			return message{}, err
		}
		// A final line with no newline is still a document, so it is decoded rather than discarded:
		// the last thing a server says before exiting is usually the thing that explains why.
		if len(trimSpace(line)) == 0 {
			if err != nil {
				return message{}, err
			}
			continue
		}
		var m message
		if decodeErr := json.Unmarshal(line, &m); decodeErr != nil {
			return message{}, fmt.Errorf(
				"the Codex app server sent something that is not JSON-RPC: %w", decodeErr)
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
// the literal null. Marshalling cannot fail for any type in this file, and a failure would show up
// as a missing params object rather than as a wrong one.
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
