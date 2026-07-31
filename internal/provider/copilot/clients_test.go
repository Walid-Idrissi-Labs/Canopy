package copilot

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// The resource story, which is what the first build of this route got wrong.
//
// It claimed that a session per conversation and a process per client both end at Engine.Close. That
// was true of the main conversation and of nothing else: every aside, every compaction and `canopy
// ask` built a client that nothing closed, and the cache of held conversations never evicted
// anything at all. The tests below hold the fix, and the first one holds it against the next person
// rather than against the three paths that were wrong on the day.

// The property that makes the rest unmissable rather than merely fixed.
//
// A Client is a resident CLI process and a session GitHub believes is open. If any code anywhere
// could make one, then cleanup would be a thing every future call path had to remember, which is
// exactly the arrangement that leaked on three paths out of four. So the pool is the only
// constructor: no exported function outside it hands one back, and no other file in this package
// builds one. That is what nothing outside this package can get wrong, because there is nothing else
// for it to call.
//
// Read from the source rather than asserted about behaviour, because "there is no other way to build
// one" is a property of the code and not of any value it computes. This fails the moment somebody
// adds a second constructor, which is the case that matters and the one a behavioural test of
// today's three paths would sail straight past.
func TestTheOnlyWayToBuildAClientIsOneTheCleanupIsHolding(t *testing.T) {
	literals, constructors := 0, 0
	for name, file := range sourceOf(t, ".") {
		ast.Inspect(file, func(node ast.Node) bool {
			switch found := node.(type) {
			case *ast.CompositeLit:
				if ident, ok := found.Type.(*ast.Ident); ok && ident.Name == "Client" {
					literals++
					if name != "clients.go" {
						t.Errorf("%s builds a Client of its own. Every client has to come from "+
							"Clients, because that is what closes it: one made anywhere else is a "+
							"process and a GitHub session that nothing in the program can reach",
							name)
					}
				}

			case *ast.FuncDecl:
				if !found.Name.IsExported() || !returnsClient(found) {
					return true
				}
				constructors++
				if receiver := receiverOf(found); receiver != "Clients" {
					t.Errorf("%s exports %s, which hands out a *Client and is not a method on "+
						"Clients. Anything that can hand one out has to be the thing that will "+
						"close it", name, found.Name.Name)
				}
			}
			return true
		})
	}

	if literals == 0 {
		t.Error("no Client is built anywhere in this package, so this test is checking nothing")
	}
	if constructors == 0 {
		t.Error("nothing exported hands back a *Client, so this test is checking nothing")
	}
}

// sourceOf parses this package's own source, tests excluded.
//
// os.ReadDir and ParseFile rather than the one call that does both, because that one is deprecated
// for a reason that applies here: it decides which files belong to a package without looking at
// build tags. Everything below is about what this package's shipped code does, so the file list has
// to be the shipped files.
func sourceOf(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	fileset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fileset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		files[name] = parsed
	}
	if len(files) == 0 {
		t.Fatalf("no source was found in %s, so this test proves nothing", dir)
	}
	return files
}

// returnsClient reports whether a function hands back a *Client.
func returnsClient(decl *ast.FuncDecl) bool {
	if decl.Type.Results == nil {
		return false
	}
	for _, result := range decl.Type.Results.List {
		star, ok := result.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "Client" {
			return true
		}
	}
	return false
}

// receiverOf names the type a method belongs to, or nothing for a plain function.
func receiverOf(decl *ast.FuncDecl) string {
	if decl.Recv == nil || len(decl.Recv.List) == 0 {
		return ""
	}
	switch receiver := decl.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if ident, ok := receiver.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.Ident:
		return receiver.Name
	}
	return ""
}

// poolOfFakes builds a pool that opens a fresh fake agent per conversation.
func poolOfFakes(t *testing.T) (*Clients, func() []*fakeAgent) {
	t.Helper()

	var mu sync.Mutex
	var opened []*fakeAgent
	clients := NewClients(WithOpener(func(context.Context, Conversation) (Agent, error) {
		agent := newFakeAgent()
		agent.onSend = replies("ok")
		mu.Lock()
		defer mu.Unlock()
		opened = append(opened, agent)
		return agent, nil
	}))
	t.Cleanup(func() { _ = clients.Close() })

	return clients, func() []*fakeAgent {
		mu.Lock()
		defer mu.Unlock()
		return append([]*fakeAgent(nil), opened...)
	}
}

// An aside, a compaction and `canopy ask` are each one question asked once. The session behind them
// has nobody to end it, and before this each one left a `copilot` process resident and a session
// GitHub believed was open for the life of the program.
func TestAOneShotEndsItsSessionWhenItsTurnDoes(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = replies("an answer")
	clients, _ := poolOn(t, agent)

	client, err := clients.Once("mycopilot", Conversation{Token: core.NewSecret("gho_TOKEN")})
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if clients.Live() != 1 {
		t.Fatalf("the pool is holding %d clients, want the one it just handed out", clients.Live())
	}

	ask(t, client, core.Request{Model: "m", Messages: []core.Message{user("hi")}})

	if agent.shutDown() != 1 {
		t.Errorf("the session was closed %d times after its only turn ended. Every aside and every "+
			"compaction takes this path, so one that does not close is a process per side question",
			agent.shutDown())
	}
	if clients.Live() != 0 {
		t.Errorf("%d clients are still held after the only turn on them ended", clients.Live())
	}
}

// The same client, asked twice. A one-shot is over when its turn is, and a second turn on it is a
// caller mistake rather than a conversation.
func TestAOneShotRefusesASecondTurnRatherThanReopeningTheSession(t *testing.T) {
	agent := newFakeAgent()
	agent.onSend = replies("an answer")
	clients, opened := poolOn(t, agent)

	client, err := clients.Once("mycopilot", Conversation{Token: core.NewSecret("gho_TOKEN")})
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	ask(t, client, core.Request{Model: "m", Messages: []core.Message{user("hi")}})

	if _, err := client.Stream(context.Background(), core.Request{
		Model: "m", Messages: []core.Message{user("hi"), assistant("an answer"), user("again")},
	}); err == nil {
		t.Error("a one-shot answered twice, so its session outlived the turn it was made for")
	}
	if opened() != 1 {
		t.Errorf("%d sessions were opened for one one-shot", opened())
	}
}

// A turn that never started still opened a session, and the caller has an error and no stream, so
// nothing else in the program could close it.
func TestAOneShotWhoseTurnFailedToStartDoesNotLeaveTheSessionOpen(t *testing.T) {
	agent := newFakeAgent()
	agent.sendErr = errors.New("the vendor refused the message")
	clients, _ := poolOn(t, agent)

	client, err := clients.Once("mycopilot", Conversation{Token: core.NewSecret("gho_TOKEN")})
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if _, err := client.Stream(context.Background(), core.Request{
		Model: "m", Messages: []core.Message{user("hi")},
	}); err == nil {
		t.Fatal("a send that failed was reported as a turn")
	}

	if agent.shutDown() != 1 {
		t.Errorf("the session was closed %d times after the turn failed to start", agent.shutDown())
	}
	if clients.Live() != 0 {
		t.Errorf("%d clients survived a turn that never started", clients.Live())
	}
}

// The three cases the closing has to be safe in, none of which is the ordinary one.
func TestClosingIsSafeTwiceOnASessionThatNeverStartedAndWithNoVendorInstalled(t *testing.T) {
	agent := newFakeAgent()
	clients, _ := poolOn(t, agent)

	// Never streamed on, so there is no session behind it and nothing to disconnect.
	quiet, err := clients.For("never-used", "mycopilot", Conversation{})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if err := quiet.Close(); err != nil {
		t.Errorf("closing a conversation that never started a session: %v", err)
	}
	if err := quiet.Close(); err != nil {
		t.Errorf("closing it a second time: %v", err)
	}
	if agent.shutDown() != 0 {
		t.Error("a conversation that never opened a session closed one anyway")
	}

	// The machine with no Copilot CLI on it, which is the failure this route is expected to have.
	absent := NewClients(WithOpener(func(context.Context, Conversation) (Agent, error) {
		return nil, missingCLI()
	}))
	client, err := absent.Once("mycopilot", Conversation{})
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if _, err := client.Stream(context.Background(), core.Request{
		Model: "m", Messages: []core.Message{user("hi")},
	}); err == nil || !strings.Contains(err.Error(), "npm install") {
		t.Errorf("a machine with no Copilot CLI reported %v", err)
	}
	if absent.Live() != 0 {
		t.Errorf("%d clients are held for a vendor that is not installed", absent.Live())
	}
	if err := absent.Close(); err != nil {
		t.Errorf("closing a pool whose sessions never opened: %v", err)
	}
	if err := absent.Close(); err != nil {
		t.Errorf("closing that pool a second time: %v", err)
	}
	if err := NewClients().Close(); err != nil {
		t.Errorf("closing a pool that was never used at all: %v", err)
	}
}

// Shutdown arrives while somebody is mid-reply, because that is when people quit. The turn has to
// end rather than hang, and the reader has to be handed whatever arrived rather than a deadlock.
func TestClosingWhileATurnIsInFlightEndsTheTurnRatherThanHanging(t *testing.T) {
	agent := &partingAgent{events: make(chan Event, 8)}
	clients := NewClients(WithOpener(func(context.Context, Conversation) (Agent, error) {
		return agent, nil
	}))

	client, err := clients.For("conversation", "mycopilot", Conversation{})
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	stream, err := client.Stream(context.Background(), core.Request{
		Model: "m", Messages: []core.Message{user("hi")},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	if !stream.Next() || stream.Event().Kind != core.EventText {
		t.Fatal("the first chunk never arrived, so nothing is in flight")
	}

	done := make(chan core.StreamEvent, 1)
	go func() {
		for stream.Next() {
			if event := stream.Event(); event.Kind == core.EventDone {
				done <- event
				return
			}
		}
		done <- core.StreamEvent{}
	}()

	if err := clients.Close(); err != nil {
		t.Fatalf("closing while a turn was in flight: %v", err)
	}

	select {
	case event := <-done:
		if event.Kind != core.EventDone {
			t.Errorf("the turn ended as %+v rather than with a done event", event)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never ended after the conversation behind it was closed, so quitting " +
			"Canopy mid-reply would hang on the shutdown")
	}
	if clients.Live() != 0 {
		t.Errorf("%d clients survived the shutdown", clients.Live())
	}
}

// partingAgent is a fake whose events channel closes when the conversation does, which is what the
// real one does and is the only reason a reader blocked on it is released.
type partingAgent struct {
	events chan Event
	once   sync.Once
}

func (a *partingAgent) Send(_ context.Context, _ string) error {
	a.events <- Event{Kind: EventText, Text: "half an answer"}
	return nil
}

func (a *partingAgent) Answer(context.Context, string, string, string) error { return nil }
func (a *partingAgent) Events() <-chan Event                                 { return a.events }
func (a *partingAgent) Abort(context.Context) error                          { return nil }

func (a *partingAgent) Close() error {
	a.once.Do(func() { close(a.events) })
	return nil
}

// The cache evicts. Without this, ending, forking or walking away from a conversation left its
// process resident until the program exited, and a day's work accumulated them one at a time.
func TestHeldConversationsAreBoundedAndTheLeastRecentlyUsedGoesFirst(t *testing.T) {
	clients, agents := poolOfFakes(t)

	// One turn each, so every conversation has a real session behind it to lose.
	for i := range heldLimit {
		client, err := clients.For(fmt.Sprintf("conversation-%d", i), "mycopilot", Conversation{})
		if err != nil {
			t.Fatalf("For: %v", err)
		}
		ask(t, client, core.Request{Model: "m", Messages: []core.Message{user("hi")}})
	}
	if clients.Live() != heldLimit {
		t.Fatalf("%d conversations are held, want %d", clients.Live(), heldLimit)
	}

	// Asking for the oldest again makes it the most recently used, so the next one along is what
	// eviction should take. Without this the test would pass for a pool that evicted at random.
	oldest, err := clients.For("conversation-0", "mycopilot", Conversation{})
	if err != nil {
		t.Fatalf("For: %v", err)
	}

	if _, err := clients.For("one-too-many", "mycopilot", Conversation{}); err != nil {
		t.Fatalf("For: %v", err)
	}

	if clients.Live() > heldLimit {
		t.Errorf("%d conversations are held, and the limit is %d. An unbounded cache of these is a "+
			"resident CLI process per conversation somebody walked away from", clients.Live(), heldLimit)
	}

	opened := agents()
	if len(opened) < heldLimit {
		t.Fatalf("only %d sessions were opened", len(opened))
	}
	if opened[1].shutDown() != 1 {
		t.Error("the least recently used conversation was evicted from the map without its session " +
			"being closed, which leaves the process it was the whole point of tracking")
	}
	if opened[0].shutDown() != 0 {
		t.Error("the conversation somebody had just come back to was evicted, so the eviction is not " +
			"least-recently-used at all")
	}
	if again, err := clients.For("conversation-0", "mycopilot", Conversation{}); err != nil {
		t.Fatalf("For: %v", err)
	} else if again != oldest {
		t.Error("the most recently used conversation was thrown away")
	}
}

// Eviction is housekeeping and a turn is somebody's answer. A bound that took a session away
// mid-reply would lose the answer to the rule, so the bound gives way instead.
func TestEvictionNeverTakesASessionAwayFromATurnInFlight(t *testing.T) {
	clients, agents := poolOfFakes(t)

	streams := make([]core.Stream, 0, heldLimit)
	for i := range heldLimit {
		client, err := clients.For(fmt.Sprintf("conversation-%d", i), "mycopilot", Conversation{})
		if err != nil {
			t.Fatalf("For: %v", err)
		}
		// Handed out and not closed, which is what a turn in flight is.
		stream, err := client.Stream(context.Background(), core.Request{
			Model: "m", Messages: []core.Message{user("hi")},
		})
		if err != nil {
			t.Fatalf("Stream: %v", err)
		}
		streams = append(streams, stream)
	}

	if _, err := clients.For("one-too-many", "mycopilot", Conversation{}); err != nil {
		t.Fatalf("For: %v", err)
	}

	for i, agent := range agents() {
		if agent.shutDown() != 0 {
			t.Errorf("the session for conversation-%d was closed while its turn was still reading, "+
				"so somebody's reply was lost to a bound", i)
		}
	}
	if clients.Live() != heldLimit+1 {
		t.Errorf("%d conversations are held, want the limit exceeded rather than a live turn ended",
			clients.Live())
	}

	// Once the turns end, the bound reasserts itself rather than staying exceeded forever.
	for _, stream := range streams {
		_ = stream.Close()
	}
	if _, err := clients.For("later", "mycopilot", Conversation{}); err != nil {
		t.Fatalf("For: %v", err)
	}
	if clients.Live() > heldLimit {
		t.Errorf("%d conversations are held after the turns finished, and the limit is %d",
			clients.Live(), heldLimit)
	}
}

// A pool that has been closed starts nothing new. Without it a turn that arrived during shutdown
// would open a session nothing is left to close.
func TestAShutDownPoolStartsNoNewConversation(t *testing.T) {
	clients, _ := poolOn(t, newFakeAgent())
	if err := clients.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := clients.For("conversation", "mycopilot", Conversation{}); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("a held conversation was started on a closed pool: %v", err)
	}
	if _, err := clients.Once("mycopilot", Conversation{}); !errors.Is(err, ErrShuttingDown) {
		t.Errorf("a one-shot was started on a closed pool: %v", err)
	}
}

// A held conversation with no key would make every caller that has nothing to key on share one
// session, which is the aside-reads-somebody-elses-history bug wearing a different hat.
func TestAHeldConversationWithNoKeyIsRefusedRatherThanShared(t *testing.T) {
	clients, _ := poolOn(t, newFakeAgent())
	if _, err := clients.For("", "mycopilot", Conversation{}); err == nil {
		t.Error("a conversation with no key was held, so every caller with none shares one session")
	}
}
