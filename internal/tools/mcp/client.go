package mcp

// JSON-RPC 2.0 over a pipe, which is what MCP's stdio transport is.
//
// One line per message, requests carry an id and replies come back out of order, so this is a
// correlation problem before it is a protocol problem: a reader goroutine owns the pipe and hands
// each reply to whoever is waiting on that id. Callers never touch the pipe.
//
// Everything read here was written by a program we did not write, so every bound is deliberate. A
// line has a maximum length, a reply has a maximum size, and a call has a deadline. A server that
// sends a gigabyte on one line is not a hypothetical failure mode, it is a bug in somebody else's
// server that would otherwise be Canopy running out of memory.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

// maxLineBytes bounds a single JSON-RPC message.
//
// Generous, because a tool result legitimately carries a file, and finite, because the process on
// the other end is not ours. A server that exceeds it fails that server rather than the program.
const maxLineBytes = 8 * 1024 * 1024

// errClosed is returned to everyone still waiting when the transport goes away.
var errClosed = errors.New("the server connection closed")

// message is a JSON-RPC 2.0 frame.
//
// One type for requests, responses and notifications rather than three, because the wire format
// genuinely is one shape with optional fields, and three types would mean deciding which one an
// incoming line is before it has been parsed.
type message struct {
	JSONRPC string `json:"jsonrpc"`

	// ID is kept as raw bytes rather than a number.
	//
	// The protocol allows a string or a number, and the ids Canopy generates are its own business
	// but the ids a server generates are not. Decoding into an int64 meant a server request carrying
	// a string id failed to parse as a whole and was dropped, which looks from the server's side like
	// a client that never answers. Raw bytes correlate by comparison and echo back byte for byte.
	ID json.RawMessage `json:"id,omitempty"`

	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// methodNotFound is the protocol's code for a method the receiver does not implement.
const methodNotFound = -32601

// encodeID renders one of our own request ids.
func encodeID(id int64) json.RawMessage {
	return json.RawMessage(strconv.FormatInt(id, 10))
}

// ourID reads an id back, and reports whether it is one this client could have issued.
//
// Anything that is not a plain integer was not generated here, so it belongs to no waiter. That is a
// correlation answer rather than a validation one: a server is free to use string ids for its own
// requests, and this only ever asks whether a given id is ours.
func ourID(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, false
	}
	return id, true
}

// rpcError is the protocol's own error shape.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the server returned error %d", e.Code)
	}
	return e.Message
}

// client is a JSON-RPC connection over a pipe.
type client struct {
	out io.WriteCloser
	in  io.ReadCloser

	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	waiting map[int64]chan message
	closed  bool
	// readErr is why the reader stopped, kept so a caller gets "the server exited" rather than the
	// generic closed message when that is what actually happened.
	readErr error

	done chan struct{}
}

// newClient starts the reader and returns a usable connection.
func newClient(in io.ReadCloser, out io.WriteCloser) *client {
	c := &client{
		in:      in,
		out:     out,
		waiting: map[int64]chan message{},
		done:    make(chan struct{}),
	}
	go c.read()
	return c
}

// read owns the pipe for the life of the connection.
func (c *client) read() {
	defer close(c.done)

	scanner := bufio.NewScanner(c.in)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg message
		if err := json.Unmarshal(line, &msg); err != nil {
			// A line we cannot parse is the server's bug, not a reason to tear down a connection
			// whose other calls are fine. Servers do print things to stdout that are not protocol,
			// which is against the specification and common anyway.
			continue
		}

		// What makes a frame a reply is that it carries no method, not that it carries an id.
		//
		// MCP is bidirectional, so a server may send requests of its own, and it numbers them from
		// its own counter starting at one, exactly as this client does. Matching on the id alone
		// therefore hands a server's request to whoever is waiting on the same number, which is a
		// caller receiving a frame with no result in it while its real reply arrives later, finds
		// nobody waiting, and is dropped. Two calls corrupted by one collision.
		if msg.Method != "" {
			c.serve(msg)
			continue
		}

		id, mine := ourID(msg.ID)
		if !mine {
			// A response to a request this client did not issue, or one with an id no waiter can be
			// holding. Nothing to deliver it to.
			continue
		}

		c.mu.Lock()
		ch, ok := c.waiting[id]
		delete(c.waiting, id)
		c.mu.Unlock()

		if ok {
			// Buffered by one at the point it was registered, so this never blocks even if the
			// caller has already given up and gone.
			ch <- msg
		}
	}

	err := scanner.Err()
	if err == nil {
		err = errClosed
	} else if errors.Is(err, bufio.ErrTooLong) {
		err = fmt.Errorf("the server sent a message longer than %d bytes", maxLineBytes)
	}
	c.fail(err)
}

// serve answers a request the server sent us.
//
// Canopy advertises no capabilities in the handshake, deliberately, so every request arriving from a
// server is for something that was never offered: sampling, roots, elicitation. The protocol's answer
// for that is method not found, and sending it matters more than it looks. A server that asked and is
// never answered waits, and a server waiting on a client is a server that has stopped serving tools.
//
// A notification carries no id and expects nothing back. tools/list_changed in particular is
// deliberately ignored rather than followed, per the package boundary.
func (c *client) serve(msg message) {
	if len(msg.ID) == 0 {
		return
	}

	// The id goes back exactly as it arrived. It is the server's, in the server's own format, and
	// re-encoding it is how a correlation identifier stops matching.
	_ = c.send(message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Error: &rpcError{
			Code:    methodNotFound,
			Message: "canopy advertises no capabilities, so it does not implement " + msg.Method,
		},
	}, nil)
}

// fail wakes everyone still waiting, so a dead server produces errors rather than a hung turn.
func (c *client) fail(err error) {
	c.mu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	c.closed = true
	waiting := c.waiting
	c.waiting = map[int64]chan message{}
	c.mu.Unlock()

	for _, ch := range waiting {
		ch <- message{Error: &rpcError{Message: err.Error()}}
	}
}

// call sends a request and waits for its reply.
func (c *client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	reply := make(chan message, 1)

	c.mu.Lock()
	if c.closed {
		err := c.readErr
		c.mu.Unlock()
		if err == nil {
			err = errClosed
		}
		return nil, err
	}
	c.waiting[id] = reply
	c.mu.Unlock()

	if err := c.send(message{JSONRPC: "2.0", ID: encodeID(id), Method: method}, params); err != nil {
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case msg := <-reply:
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil

	case <-ctx.Done():
		// Stop waiting, and stop holding the slot. The server may still answer, and the reader
		// dropping an unclaimed reply is the correct outcome: nobody is listening any more.
		c.mu.Lock()
		delete(c.waiting, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// notify sends a message with no id, which the protocol defines as expecting no reply.
func (c *client) notify(method string, params any) error {
	return c.send(message{JSONRPC: "2.0", Method: method}, params)
}

// send marshals and writes one frame.
func (c *client) send(msg message, params any) error {
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("could not encode the request: %w", err)
		}
		msg.Params = encoded
	}

	line, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("could not encode the request: %w", err)
	}

	// One writer at a time, because two goroutines writing interleaved halves of two JSON objects
	// produces a stream that is not JSON at all and a server that disconnects for no visible reason.
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if _, err := c.out.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("could not reach the server: %w", err)
	}
	return nil
}

// close shuts the connection down and releases anyone waiting.
//
// Closing the write side first is the graceful signal: a stdio server sees end of file on its stdin
// and exits by itself, which is how it gets the chance to clean up whatever it opened.
func (c *client) close() {
	c.writeMu.Lock()
	_ = c.out.Close()
	c.writeMu.Unlock()

	_ = c.in.Close()
	c.fail(errClosed)
}
