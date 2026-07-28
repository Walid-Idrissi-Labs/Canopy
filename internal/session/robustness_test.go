package session

// The robustness sweep at the level somebody actually experiences it: several agents finishing at
// once, and quitting while one of them is running a command.
//
// The pieces underneath have their own tests. These exist because the pieces are assembled here, and
// an assembly is where a guarantee gets lost: the broker never drops a final event, and the engine
// is the only thing that decides to send one.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/agent"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

// Ten sessions all answering at once is a fan out, which is the case this product exists for. Every
// one of them has to say how it ended: a turn whose final event went missing sits on screen
// streaming forever, and the snapshot being right underneath does not help somebody who has no
// reason to look again.
func TestEveryTurnSaysHowItEndedWhenManySessionsFinishAtOnce(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("done")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	// Subscribed before anything is sent, which is the order the contract asks for: take a snapshot,
	// subscribe from its sequence, and nothing slips through the gap.
	events := e.Events(0)

	const sessions = 10
	type sent struct{ session, turn string }
	started := make([]sent, 0, sessions)

	var wg sync.WaitGroup
	var mu sync.Mutex
	for range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := e.Create("claude", "m")
			turnID, err := e.Send(s.ID, "hello")
			if err != nil {
				t.Errorf("Send: %v", err)
				return
			}
			mu.Lock()
			started = append(started, sent{session: s.ID, turn: turnID})
			mu.Unlock()
		}()
	}
	wg.Wait()

	for _, s := range started {
		waitForTurn(t, e, s.session, s.turn)
	}

	finals := map[string]int{}
	deadline := time.After(3 * time.Second)
	for len(finals) < sessions {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("the event stream closed before every turn had reported how it ended")
			}
			if ev.Kind == core.EventTurnUpdated && ev.Final {
				finals[ev.SessionID+"|"+ev.TurnID]++
			}
		case <-deadline:
			t.Fatalf("%d turns of %d reported how they ended", len(finals), sessions)
		}
	}

	for _, s := range started {
		key := s.session + "|" + s.turn
		switch finals[key] {
		case 1:
		case 0:
			t.Errorf("%s never reported how it ended", key)
		default:
			// Worth asserting in the same place. A turn that ends twice reads as a second answer
			// arriving, which is a different bug with the same cause: something other than finish
			// deciding a turn is over.
			t.Errorf("%s reported how it ended %d times", key, finals[key])
		}
	}
}

// sleeper is a tool that starts a command and waits for it, which is what running a test suite or a
// build looks like from the engine's side.
//
// A real command rather than a sleep in Go, because the property under test is about a child
// process rather than about a goroutine: quitting has to take the process with it.
type sleeper struct {
	dir     string
	command string
}

func (s *sleeper) Name() string            { return "run_command" }
func (s *sleeper) Kind() core.ToolKind     { return core.ToolExecute }
func (s *sleeper) Description() string     { return "Run a command." }
func (s *sleeper) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (s *sleeper) Run(ctx context.Context, _ json.RawMessage) (core.ToolResult, error) {
	result, err := exec.Run(ctx, "/bin/sh", []string{"-c", s.command}, exec.Options{
		Dir: s.dir, Timeout: time.Minute,
	})
	if err != nil {
		return core.ToolResult{Content: err.Error(), IsError: true}, nil
	}
	return core.ToolResult{Content: result.Output}, nil
}

// asking is a client that requests the tool once and then answers, which is the shortest exchange
// that actually runs one. A client that asked for it every time would loop until the step bound.
type askingClient struct {
	name string

	mu    sync.Mutex
	asked bool
}

func (c *askingClient) Name() string { return c.name }

func (c *askingClient) Stream(context.Context, core.Request) (core.Stream, error) {
	c.mu.Lock()
	first := !c.asked
	c.asked = true
	c.mu.Unlock()

	if first {
		return &scriptedStream{events: []core.StreamEvent{
			{Kind: core.EventToolCall, ToolCall: &core.ToolCall{
				ID: "call-1", Name: "run_command", Input: json.RawMessage(`{}`),
			}},
			{Kind: core.EventDone, StopReason: core.StopToolUse},
		}}, nil
	}
	return &scriptedStream{events: reply("finished")}, nil
}

// Quitting while an agent is running a command has to take the command with it, and everything the
// command started. Otherwise the build an agent kicked off carries on holding the port, and the next
// run fails with "address already in use" for reasons nobody can see.
func TestQuittingTakesACommandAnAgentStartedWithIt(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-still-running")

	tools := core.NewToolRegistry()
	tools.MustRegister(&sleeper{
		dir: dir,
		// A worker in the background and a shell waiting on it, which is every test runner. If only
		// the shell is stopped, the worker carries on writing and the file keeps changing.
		//
		// SIGTERM is ignored on purpose, by the shell and by the worker that inherits the disposition
		// from it, so nothing here can stop before the escalation that follows a quarter of a second
		// later. That is what makes the difference visible: a Close that returned as soon as it had
		// asked would return while the worker is demonstrably still writing.
		command: fmt.Sprintf(
			`trap '' TERM; (i=0; while [ $i -lt 400 ]; do echo $i > %q; sleep 0.02; i=$((i+1)); done) & wait`,
			marker),
	})

	e := New(fixedResolver{client: &askingClient{name: "claude"}, id: anthropicID()})
	e.WithTools(tools, core.TrustBroad, agent.ApproverFunc(
		func(context.Context, permission.Request, permission.Decision) bool { return true }))

	s := e.Create("claude", "m")
	if _, err := e.Send(s.ID, "run the suite"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait until the command is genuinely running, or this would be asserting about a process that
	// had not started.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent's command never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	e.Close()

	// Read straight after Close returns, with nothing in between. That is the moment the program
	// exits at, so anything still moving here is a process quitting left behind.
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if string(before) != string(after) {
		t.Errorf("a process an agent started outlived quitting: the marker went from %q to %q",
			strings.TrimSpace(string(before)), strings.TrimSpace(string(after)))
	}
}
