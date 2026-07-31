package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
)

// A fake Codex app server, in process, so that not one test in this package needs the Codex CLI
// installed or a ChatGPT plan to bill.
//
// It is a real JSON-RPC peer over a real pair of pipes rather than a mock of the client's own
// methods, which is the only version worth having: the thing most likely to be wrong about a
// protocol adapter is what it puts on the wire, and a mock that is handed Go structs never reads a
// byte of that. Every test below asserts against the frames that actually crossed.
//
// It is also modelled on the real one rather than on what would be convenient, in the one place that
// matters most here: `initialize` answers with a user agent composed from the name the client just
// sent, exactly as the real app server does, because that echo is what Canopy checks its own
// identity against and a fake that returned a fixed string would make the check untestable.
//
// What it deliberately does not do is validate Canopy's frames against the published schema. That is
// what the live test is for, and it is skipped by default for the reason internal/session/live_test.go
// gives: a scripted peer is written from the same understanding as the code it exercises, so if that
// understanding is wrong they are wrong together.
type appServer struct {
	t *testing.T

	// userAgent overrides the handshake answer. Empty means compose one the way the real server
	// does, which is what every test but the impersonation ones wants.
	userAgent string

	// account is what account/read answers with. Nil means nobody is signed in.
	account *accountInfo

	// limits is what account/rateLimits/read answers with.
	limits rateLimitsResult

	// login is what account/login/start answers with, and loginOutcome is the notification that
	// follows. A nil login makes the call fail the way a server with no network does.
	login        *loginStartResult
	loginOutcome *loginCompletedParams

	// threadModel is what thread/start says it settled on, so a test can prove the substitution
	// notice.
	threadModel string

	// script runs once turn/start has been answered, and is where a test says what the turn does.
	script func(s *appServer)

	// failThread makes thread/start refuse, with this message.
	failThread string
	// ignoreInitialize and ignoreInterrupt model an app server that is alive but unresponsive.
	ignoreInitialize bool
	ignoreInterrupt  bool

	in  *bufio.Reader
	out io.Writer

	mu        sync.Mutex
	writeMu   sync.Mutex
	received  []message
	stopped   bool
	threadID  string
	turnID    string
	turnReqID int64
	done      chan struct{}
}

// launch wires a fake app server to whatever is about to drive it.
//
// Two pipes and a goroutine, which is what a subprocess is minus the subprocess. The returned
// process carries a stop function so Close has something real to close, and stopping it closes the
// server's side, which is exactly how a killed app server looks to the client.
func (s *appServer) launcher() launcher {
	return func(ctx context.Context) (*process, error) {
		toServer, fromClient := io.Pipe()
		toClient, fromServer := io.Pipe()

		s.in = bufio.NewReader(toServer)
		s.out = fromServer
		s.done = make(chan struct{})
		s.threadID = "thread-1"
		s.turnID = "turn-1"

		go s.serve()

		return &process{
			stdin:  fromClient,
			stdout: toClient,
			stderr: &bounded{limit: 1024},
			stop: func() {
				s.mu.Lock()
				s.stopped = true
				s.mu.Unlock()
				_ = fromClient.Close()
				_ = fromServer.Close()
			},
		}, nil
	}
}

// attach makes a client use this fake instead of a child process.
func (s *appServer) attach(c *Client) { c.launch = s.launcher() }

// vendor is this fake dressed as something Canopy asks questions of.
func (s *appServer) vendor() Vendor { return Vendor{Version: "1.2.3", launch: s.launcher()} }

func (s *appServer) serve() {
	defer close(s.done)

	for {
		line, err := s.in.ReadBytes('\n')
		if len(line) == 0 && err != nil {
			return
		}
		var m message
		if err := json.Unmarshal(line, &m); err != nil {
			return
		}

		s.mu.Lock()
		s.received = append(s.received, m)
		s.mu.Unlock()

		if !s.handle(m) {
			return
		}
		if err != nil {
			return
		}
	}
}

// handle answers one frame, reporting whether to keep going.
func (s *appServer) handle(m message) bool {
	if m.Method == "" {
		// A response to something this server asked for, which is an answer and not a request.
		// Ignored rather than replied to: replying would mean the server answering the client's
		// answer, and the two would sit writing at each other forever.
		return true
	}

	switch m.Method {
	case methodInitialize:
		if s.ignoreInitialize {
			return true
		}
		var params initializeParams
		_ = json.Unmarshal(m.Params, &params)
		agent := s.userAgent
		if agent == "" {
			// Composed the way the real one composes it, from the name the client just gave.
			agent = fmt.Sprintf("%s/0.141.0 (Mac OS 26.0; arm64) (%s; %s)",
				params.ClientInfo.Name, params.ClientInfo.Name, params.ClientInfo.Version)
		}
		s.answer(m, initializeResult{UserAgent: agent, CodexHome: "/tmp/codex"})

	case methodInitialized:
		// A notification, so there is nothing to answer.

	case methodAccountRead:
		s.answer(m, accountReadResult{Account: s.account, RequiresOpenAIAuth: true})

	case methodAccountRateLimits:
		s.answer(m, s.limits)

	case methodLogout:
		s.answer(m, struct{}{})

	case methodLoginStart:
		if s.login == nil {
			s.fail(m, "no network")
			return true
		}
		s.answer(m, *s.login)
		if s.loginOutcome != nil {
			s.notify(notifyLoginCompleted, *s.loginOutcome)
		}

	case methodLoginCancel:
		s.answer(m, map[string]string{"status": "canceled"})
		s.notify(notifyLoginCompleted, loginCompletedParams{
			LoginID: s.login.LoginID, Success: false, Error: "Login was not completed"})

	case methodThreadStart:
		if s.failThread != "" {
			s.fail(m, s.failThread)
			return true
		}
		s.answer(m, threadStartResult{
			Thread: threadInfo{ID: s.threadID},
			Model:  s.threadModel,
		})

	case methodTurnStart:
		s.mu.Lock()
		if m.ID != nil {
			s.turnReqID = *m.ID
		}
		s.mu.Unlock()
		s.answer(m, turnStartResult{Turn: turnInfo{ID: s.turnID, Status: turnInProgress}})
		if s.script != nil {
			// On its own goroutine, so the read loop keeps running while the script talks.
			//
			// Not tidiness. These pipes are unbuffered, so a script that sends an approval request
			// blocks until the client reads it, and the client's refusal then blocks until this
			// server reads that. Running the script inline makes those two waits each other's and
			// the test hangs. The real app server has an operating system pipe buffer underneath it
			// and would have hidden this until an exchange grew large enough to fill one, which is
			// the same hazard S-04 found on the Claude route from the other side.
			go s.script(s)
		}

	case methodTurnInterrupt:
		if !s.ignoreInterrupt {
			s.answer(m, struct{}{})
			s.emitTurn(turnInterrupted, nil)
		}

	default:
		if m.ID != nil {
			s.fail(m, "unknown method "+m.Method)
		}
	}
	return true
}

func (s *appServer) answer(m message, result any) {
	if m.ID == nil {
		return
	}
	s.write(message{JSONRPC: jsonRPCVersion, ID: m.ID, Result: encode(result)})
}

func (s *appServer) fail(m message, text string) {
	if m.ID == nil {
		return
	}
	s.write(message{JSONRPC: jsonRPCVersion, ID: m.ID, Error: &rpcError{Code: -32000, Message: text}})
}

func (s *appServer) notify(method string, params any) {
	s.write(message{JSONRPC: jsonRPCVersion, Method: method, Params: encode(params)})
}

// request sends a server-initiated request and returns its id, so a test can look for the answer.
func (s *appServer) request(method string, params any) int64 {
	s.mu.Lock()
	s.turnReqID++
	id := 1000 + s.turnReqID
	s.mu.Unlock()
	s.write(message{JSONRPC: jsonRPCVersion, ID: &id, Method: method, Params: encode(params)})
	return id
}

// write puts one frame on the wire, under a lock.
//
// The lock matters here for the same reason it does in the client: the read loop and a running
// script both write, and two interleaved JSON documents on one pipe is a stream neither side can
// recover.
func (s *appServer) write(m message) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(append(encoded, '\n'))
}

// say streams a chunk of the reply, the way the real server does: as a delta on an item.
func (s *appServer) say(text string) {
	s.notify(notifyAgentMessage, deltaNotification{
		ThreadID: s.threadID, TurnID: s.turnID, ItemID: "msg-1", Delta: text,
	})
}

// think streams a chunk of reasoning.
func (s *appServer) think(text string) {
	s.notify(notifyReasoningSummary, deltaNotification{
		ThreadID: s.threadID, TurnID: s.turnID, ItemID: "reason-1", Delta: text,
	})
}

// did reports an item the delegated agent started, which is how every tool-shaped thing arrives.
func (s *appServer) did(item threadItem) {
	s.notify(notifyItemStarted, itemNotification{
		ThreadID: s.threadID, TurnID: s.turnID, Item: item,
	})
}

// spent reports the turn's tokens the way the real server does, on its own notification.
func (s *appServer) spent(in, out, cached int64) {
	s.notify(notifyTokenUsage, tokenUsageParams{
		ThreadID: s.threadID, TurnID: s.turnID,
		TokenUsage: threadUsage{
			Last: usageBreakdown{
				InputTokens: in, OutputTokens: out, CachedInputTokens: cached,
				TotalTokens: in + out,
			},
			// Deliberately different from Last, so a test can prove which one is reported.
			Total: usageBreakdown{InputTokens: 9999, OutputTokens: 9999, TotalTokens: 19998},
		},
	})
}

// finish ends the turn with a status.
func (s *appServer) finish(status string) { s.emitTurn(status, nil) }

func (s *appServer) emitTurn(status string, failure *turnError) {
	s.notify(notifyTurnCompleted, turnCompletedParams{
		ThreadID: s.threadID,
		Turn:     turnInfo{ID: s.turnID, Status: status, Error: failure},
	})
}

// sent returns every frame the client wrote, which is what these tests assert against.
func (s *appServer) sent() []message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]message(nil), s.received...)
}

// sentMethod returns the first frame naming a method, and whether there was one.
func (s *appServer) sentMethod(method string) (message, bool) {
	for _, m := range s.sent() {
		if m.Method == method {
			return m, true
		}
	}
	return message{}, false
}

// answered returns the client's reply to a server-initiated request.
func (s *appServer) answered(id int64) (message, bool) {
	for _, m := range s.sent() {
		if m.ID != nil && *m.ID == id && m.Method == "" {
			return m, true
		}
	}
	return message{}, false
}

// wasStopped reports whether the client shut the process down.
func (s *appServer) wasStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// chatgpt is the ordinary signed-in account these tests use.
func chatgpt(email, plan string) *accountInfo {
	return &accountInfo{Type: accountChatGPT, Email: email, PlanType: plan}
}
