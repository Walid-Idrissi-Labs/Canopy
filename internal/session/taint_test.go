package session

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/agent"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/git"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/provider/anthropic"
)

// sequenceClient answers each request with the next scripted reply.
type sequenceClient struct {
	mu      sync.Mutex
	replies [][]core.StreamEvent
}

func (c *sequenceClient) Name() string { return "claude" }

func (c *sequenceClient) Stream(ctx context.Context, _ core.Request) (core.Stream, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	events := reply("done")
	if len(c.replies) > 0 {
		events, c.replies = c.replies[0], c.replies[1:]
	}
	return &scriptedStream{events: events, ctx: ctx}, nil
}

func calls(id, name, input string) []core.StreamEvent {
	return []core.StreamEvent{
		{Kind: core.EventToolCall, ToolCall: &core.ToolCall{ID: id, Name: name, Input: []byte(input)}},
		{Kind: core.EventDone, StopReason: core.StopToolUse},
	}
}

// At broad trust a shell command runs without asking, until the conversation has fetched a page:
// from then on a command that could send data out is asked about, in later turns too.
func TestAFetchedPageMakesExfiltrationAsk(t *testing.T) {
	client := &sequenceClient{replies: [][]core.StreamEvent{
		calls("c1", "shell", `{"command":"curl https://example.test"}`), reply("ran it"),
		calls("c2", "fetch_url", `{"url":"https://example.test"}`), reply("read it"),
		calls("c3", "shell", `{"command":"curl https://example.test"}`), reply("ran it again"),
	}}
	registry := core.NewToolRegistry()
	registry.MustRegister(&kindTool{name: "shell", kind: core.ToolExecute})
	registry.MustRegister(&kindTool{name: "fetch_url", kind: core.ToolNetwork})
	var mu sync.Mutex
	var asked []string
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)
	e.WithTools(registry, core.TrustBroad, agent.ApproverFunc(
		func(_ context.Context, req permission.Request, _ permission.Decision) bool {
			mu.Lock()
			asked = append(asked, req.Tool)
			mu.Unlock()
			return true
		}))
	e.WithCheckpoints(git.NewTaker(t.TempDir()))
	s := e.Create("claude", "claude-opus-5")
	for _, text := range []string{"run it", "read the page", "run it again"} {
		id, err := e.Send(s.ID, text)
		if err != nil {
			t.Fatal(err)
		}
		waitForTurn(t, e, s.ID, id)
	}
	mu.Lock()
	defer mu.Unlock()
	// The fetch itself always asks; the first curl ran unasked; the one after the fetch asked.
	want := []string{"fetch_url", "shell"}
	if len(asked) != len(want) || asked[0] != want[0] || asked[1] != want[1] {
		t.Fatalf("asked about %v, want %v", asked, want)
	}
}

// A search the provider ran counts as outside content too, and so does a parent's taint for the
// agents it starts, since the task it writes for them may carry what it read.
func TestASearchTaintsAndChildrenInheritIt(t *testing.T) {
	searched := append([]core.StreamEvent{{Kind: core.EventNotice, Text: anthropic.WebSearchNotice + "leaked docs"}},
		reply("found it")...)
	client := &sequenceClient{replies: [][]core.StreamEvent{searched}}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)
	if _, err := e.AddAgent(context.Background(), Agent{Name: "main", KeyName: "claude", Model: "claude-opus-5",
		Dir: t.TempDir(), Trust: core.TrustStandard}); err != nil {
		t.Fatal(err)
	}
	sessions := e.Sessions()
	id, err := e.Send(sessions[0].ID, "search for it")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, e, sessions[0].ID, id)
	if !e.tainted(sessions[0].ID) {
		t.Fatal("a conversation the provider searched for is not tainted")
	}
	created, err := e.Spawn(context.Background(), Dispatch{Count: 1, Profile: "claude", Task: "use what I found",
		Parent: sessions[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if !e.tainted(created[0].SessionID) {
		t.Fatal("an agent started by a tainted conversation is not tainted")
	}
}

// The taint is saved with the conversation: after a restart it holds even for a conversation whose
// own record shows nothing, one tainted only by the agent that started it.
func TestTaintSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	first, _ := asideEngine(t, path)
	s := first.Create("claude", "claude-opus-5")
	first.markTainted(s.ID)
	first.Close()

	second, _ := asideEngine(t, path)
	t.Cleanup(second.Close)
	if !second.tainted(s.ID) {
		t.Fatal("the taint was lost when Canopy restarted")
	}
}

// A dispatched agent that read outside content passes the taint to the conversation it reports to,
// which acts on its report.
func TestAChildPassesItsTaintToItsParent(t *testing.T) {
	e := New(fixedResolver{client: &sequenceClient{}, id: anthropicID()})
	t.Cleanup(e.Close)
	parent := e.Create("claude", "claude-opus-5")
	child := e.Create("claude", "claude-opus-5")
	e.markTainted(child.ID)
	e.taintParent(child.ID, parent.ID)
	if !e.tainted(parent.ID) {
		t.Fatal("the parent of a tainted agent is not tainted")
	}
}

// A turn whose only outside content was a web search, followed by a plain answer, is tainted after
// a restart too: the taint is saved when the search arrives, since the notice itself is not kept.
func TestASearchOnlyTurnIsTaintedAfterARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	searched := append([]core.StreamEvent{{Kind: core.EventNotice, Text: anthropic.WebSearchNotice + "docs"}},
		reply("here is what I found")...)
	first := New(fixedResolver{client: &sequenceClient{replies: [][]core.StreamEvent{searched}}, id: anthropicID()})
	if err := first.WithStorage(storage, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatal(err)
	}
	s := first.Create("claude", "claude-opus-5")
	id, err := first.Send(s.ID, "look it up")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, first, s.ID, id)
	first.Close()

	second, _ := asideEngine(t, path)
	t.Cleanup(second.Close)
	if !second.tainted(s.ID) {
		t.Fatal("a search's taint was lost on restart")
	}
}

// A fork carries what its source read, so it carries the taint.
func TestAForkOfATaintedConversationIsTainted(t *testing.T) {
	e := New(fixedResolver{client: &sequenceClient{}, id: anthropicID()})
	t.Cleanup(e.Close)
	s := e.Create("claude", "claude-opus-5")
	id, err := e.Send(s.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, e, s.ID, id)
	e.markTainted(s.ID)
	fork, err := e.Fork(s.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if !e.tainted(fork.ID) {
		t.Fatal("the fork of a tainted conversation is not tainted")
	}
}

// A network tool's result taints the conversation and the taint is saved as it arrives, so it holds
// after a restart even when that tool is not registered again, as with an MCP server not started.
func TestAToolResultsTaintSurvivesARestartWithoutTheTool(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	storage, err := OpenStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	client := &sequenceClient{replies: [][]core.StreamEvent{
		calls("c1", "fetch_url", `{"url":"https://example.test"}`), reply("read it"),
	}}
	first := New(fixedResolver{client: client, id: anthropicID()})
	if err := first.WithStorage(storage, func(err error) { t.Errorf("storage: %v", err) }); err != nil {
		t.Fatal(err)
	}
	registry := core.NewToolRegistry()
	registry.MustRegister(&kindTool{name: "fetch_url", kind: core.ToolNetwork})
	first.WithTools(registry, core.TrustStandard, agent.ApproverFunc(
		func(context.Context, permission.Request, permission.Decision) bool { return true }))
	s := first.Create("claude", "claude-opus-5")
	id, err := first.Send(s.ID, "read the page")
	if err != nil {
		t.Fatal(err)
	}
	waitForTurn(t, first, s.ID, id)
	first.Close()

	// No tools at all this time: nothing in the record can be recognised as outside content.
	second, _ := asideEngine(t, path)
	t.Cleanup(second.Close)
	if !second.tainted(s.ID) {
		t.Fatal("a fetched page's taint was lost when the tool was not there after a restart")
	}
}
