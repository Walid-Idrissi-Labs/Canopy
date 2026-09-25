package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// runAttach connects to the canopy serve running for this project. With no argument it lists the
// conversations there; with a code it picks one up, replaying what happened and streaming what is
// still happening; with "new" it starts one. Lines typed are prompts, questions are answered y or
// n, and leaving (ctrl+c or ctrl+d) leaves the agent working.
func runAttach(args []string, stdin io.Reader, out, errOut io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitUsage
	}
	path, err := serveSocket(dir)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "canopy serve is not running for this project; start it with canopy serve")
		return exitFailed
	}
	defer func() { _ = conn.Close() }()
	target := ""
	if len(args) > 0 {
		target = args[0]
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	return attachTo(conn, dir, target, stdin, out, errOut, interrupt)
}

// rpcClient is the client end of the protocol canopy serve speaks.
type rpcClient struct {
	w      io.Writer
	writeM sync.Mutex
	ids    atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan rpcReply

	// incoming carries what the server sends unasked: updates and questions.
	incoming chan map[string]json.RawMessage
	closed   chan struct{}
}

type rpcReply struct {
	Result json.RawMessage
	Error  *rpcError
}

type rpcError struct {
	Message string `json:"message"`
}

func newRPCClient(rw io.ReadWriter) *rpcClient {
	c := &rpcClient{w: rw, pending: map[int64]chan rpcReply{}, incoming: make(chan map[string]json.RawMessage, 256),
		closed: make(chan struct{})}
	go func() {
		defer close(c.closed)
		scanner := bufio.NewScanner(rw)
		scanner.Buffer(make([]byte, 1<<20), 16<<20)
		for scanner.Scan() {
			var m map[string]json.RawMessage
			if json.Unmarshal(scanner.Bytes(), &m) != nil {
				continue
			}
			if _, isCall := m["method"]; isCall {
				c.incoming <- m
				continue
			}
			var id int64
			if json.Unmarshal(m["id"], &id) != nil {
				continue
			}
			var reply rpcReply
			reply.Result = m["result"]
			if raw, ok := m["error"]; ok {
				reply.Error = &rpcError{}
				_ = json.Unmarshal(raw, reply.Error)
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- reply
			}
		}
	}()
	return c
}

func (c *rpcClient) write(v any) {
	line, _ := json.Marshal(v)
	c.writeM.Lock()
	defer c.writeM.Unlock()
	_, _ = c.w.Write(append(line, '\n'))
}

// start sends a request and returns where its reply will arrive.
func (c *rpcClient) start(method string, params any) <-chan rpcReply {
	id := c.ids.Add(1)
	ch := make(chan rpcReply, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return ch
}

// call sends a request and waits for its reply.
func (c *rpcClient) call(method string, params any, result any) error {
	select {
	case reply := <-c.start(method, params):
		if reply.Error != nil {
			return errors.New(reply.Error.Message)
		}
		if result != nil {
			return json.Unmarshal(reply.Result, result)
		}
		return nil
	case <-c.closed:
		return errors.New("canopy serve closed the connection")
	}
}

func attachTo(rw io.ReadWriter, dir, target string, stdin io.Reader, out, errOut io.Writer,
	interrupt <-chan os.Signal) int {
	client := newRPCClient(rw)
	if err := client.call("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}, nil); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	if target == "" {
		return listServed(client, out, errOut)
	}

	var sessionID string
	if target == "new" {
		var created struct {
			SessionID string `json:"sessionId"`
		}
		if err := client.call("session/new", map[string]any{"cwd": dir, "mcpServers": []any{}}, &created); err != nil {
			_, _ = fmt.Fprintln(errOut, err)
			return exitFailed
		}
		sessionID = created.SessionID
	} else {
		sessionID = session.SessionID(target)
		// The replay arrives as updates while this waits, so they are drawn as they come.
		loaded := client.start("session/load", map[string]any{"sessionId": sessionID, "cwd": dir, "mcpServers": []any{}})
		for waiting := true; waiting; {
			select {
			case reply := <-loaded:
				if reply.Error != nil {
					_, _ = fmt.Fprintln(errOut, reply.Error.Message)
					return exitFailed
				}
				waiting = false
			case m := <-client.incoming:
				drawServed(m, out)
			case <-client.closed:
				_, _ = fmt.Fprintln(errOut, "canopy serve closed the connection")
				return exitFailed
			}
		}
	}
	code := session.Code(sessionID)
	_, _ = fmt.Fprintf(out, "\nconversation %s. Type a prompt; /cancel stops the turn, /mode NAME switches mode, "+
		"ctrl+d leaves it running.\n> ", code)

	lines := make(chan string)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdin)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	var questions []map[string]json.RawMessage
	var turn <-chan rpcReply
	leave := func() int {
		_, _ = fmt.Fprintf(out, "\nleft conversation %s running; canopy attach %s picks it up\n", code, code)
		return exitOK
	}
	for {
		select {
		case <-interrupt:
			return leave()
		case <-client.closed:
			_, _ = fmt.Fprintln(errOut, "\ncanopy serve closed the connection")
			return exitFailed
		case m := <-client.incoming:
			var method string
			_ = json.Unmarshal(m["method"], &method)
			if method == "session/request_permission" {
				questions = append(questions, m)
				if len(questions) == 1 {
					askServed(m, out)
				}
				continue
			}
			drawServed(m, out)
		case reply := <-turn:
			turn = nil
			if reply.Error != nil {
				_, _ = fmt.Fprintf(out, "\n%s\n> ", terminalText(reply.Error.Message))
				continue
			}
			var result struct {
				StopReason string `json:"stopReason"`
			}
			_ = json.Unmarshal(reply.Result, &result)
			if result.StopReason != "end_turn" {
				_, _ = fmt.Fprintf(out, "\n(%s)", terminalText(result.StopReason))
			}
			_, _ = fmt.Fprint(out, "\n> ")
		case line, ok := <-lines:
			if !ok {
				return leave()
			}
			line = strings.TrimSpace(line)
			switch {
			case len(questions) > 0:
				// An answer: only y or yes allows; anything else refuses.
				option := "reject"
				if answer := strings.ToLower(line); answer == "y" || answer == "yes" {
					option = "allow"
				}
				client.write(map[string]any{"jsonrpc": "2.0", "id": questions[0]["id"],
					"result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": option}}})
				questions = questions[1:]
				if len(questions) > 0 {
					askServed(questions[0], out)
				}
			case line == "":
			case line == "/cancel":
				client.write(map[string]any{"jsonrpc": "2.0", "method": "session/cancel",
					"params": map[string]any{"sessionId": sessionID}})
			case strings.HasPrefix(line, "/mode "):
				if err := client.call("session/set_mode", map[string]any{"sessionId": sessionID,
					"modeId": strings.TrimSpace(strings.TrimPrefix(line, "/mode "))}, nil); err != nil {
					_, _ = fmt.Fprintf(out, "%s\n", terminalText(err.Error()))
				}
				_, _ = fmt.Fprint(out, "> ")
			case turn != nil:
				_, _ = fmt.Fprint(out, "(the agent is still working; /cancel stops it)\n")
			default:
				turn = client.start("session/prompt", map[string]any{"sessionId": sessionID,
					"prompt": []any{map[string]any{"type": "text", "text": line}}})
			}
		}
	}
}

// listServed prints the conversations canopy serve is running.
func listServed(client *rpcClient, out, errOut io.Writer) int {
	var listed struct {
		Sessions []struct {
			SessionID string `json:"sessionId"`
			Title     string `json:"title"`
			Meta      struct {
				Canopy struct {
					Mode     string `json:"mode"`
					Running  bool   `json:"running"`
					Waiting  bool   `json:"waiting"`
					Attached bool   `json:"attached"`
				} `json:"canopy"`
			} `json:"_meta"`
		} `json:"sessions"`
	}
	if err := client.call("session/list", map[string]any{}, &listed); err != nil {
		_, _ = fmt.Fprintln(errOut, err)
		return exitFailed
	}
	if len(listed.Sessions) == 0 {
		_, _ = fmt.Fprintln(out, "nothing is running; canopy attach new starts a conversation")
		return exitOK
	}
	for _, s := range listed.Sessions {
		state := "idle"
		switch {
		case s.Meta.Canopy.Waiting:
			state = "waiting on you"
		case s.Meta.Canopy.Running:
			state = "working"
		}
		if s.Meta.Canopy.Attached {
			state += ", attached"
		}
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		_, _ = fmt.Fprintf(out, "%-6s %-9s %-22s %s\n", session.Code(s.SessionID), s.Meta.Canopy.Mode, state,
			terminalText(title))
	}
	return exitOK
}

// drawServed prints one update as plain lines.
func drawServed(m map[string]json.RawMessage, out io.Writer) {
	var params struct {
		Update struct {
			Kind    string `json:"sessionUpdate"`
			Title   string `json:"title"`
			Status  string `json:"status"`
			Content json.RawMessage
		} `json:"update"`
	}
	if json.Unmarshal(m["params"], &params) != nil {
		return
	}
	u := params.Update
	var text struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(u.Content, &text)
	switch u.Kind {
	case "user_message_chunk":
		_, _ = fmt.Fprintf(out, "\n> %s\n", terminalText(text.Text))
	case "agent_message_chunk":
		_, _ = io.WriteString(out, terminalText(text.Text))
	case "tool_call":
		_, _ = fmt.Fprintf(out, "\n  - %s\n", terminalText(u.Title))
	case "tool_call_update":
		if u.Status == "failed" {
			_, _ = fmt.Fprint(out, "    (failed)\n")
		}
	}
}

// askServed puts a question to the person at the terminal.
func askServed(m map[string]json.RawMessage, out io.Writer) {
	var params struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
		Options []struct {
			OptionID string `json:"optionId"`
			Name     string `json:"name"`
		} `json:"options"`
	}
	_ = json.Unmarshal(m["params"], &params)
	why := ""
	for _, option := range params.Options {
		if option.OptionID == "reject" {
			why = strings.TrimPrefix(option.Name, "Reject: ")
		}
	}
	_, _ = fmt.Fprintf(out, "\nCanopy asks to run %s", terminalText(params.ToolCall.Title))
	if why != "" {
		_, _ = fmt.Fprintf(out, " (%s)", terminalText(why))
	}
	_, _ = fmt.Fprint(out, ". Allow? [y/N] ")
}
