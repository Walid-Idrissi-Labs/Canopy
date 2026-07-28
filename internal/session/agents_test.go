package session

import (
	"context"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

func engineWithAgents(t *testing.T, names ...string) (*Engine, *scriptedClient) {
	t.Helper()

	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)

	for _, name := range names {
		if _, err := e.AddAgent(context.Background(), Agent{Name: name, KeyName: "claude", Model: "claude-opus-5"}); err != nil {
			t.Fatalf("AddAgent(%s): %v", name, err)
		}
	}
	return e, client
}

// Several agents running at once must not reach into each other's conversations.
func TestAgentsHaveTheirOwnConversations(t *testing.T) {
	e, client := engineWithAgents(t, "parser", "docs")

	parser, _ := e.Agent("parser")
	docs, _ := e.Agent("docs")

	if parser.SessionID == docs.SessionID {
		t.Fatal("two agents share a session, so their conversations would interleave")
	}

	client.events = reply("the parser's answer")
	turnID, err := e.Send(parser.SessionID, "what is wrong with the parser")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, parser.SessionID, turnID)

	other, _ := e.Session(docs.SessionID)
	if len(other.Turns) != 0 {
		t.Errorf("a turn sent to one agent appeared in another: %+v", other.Turns)
	}
}

// One agent on Claude and one on a local model is a configuration rather than a fork, which is
// where the named key model from A1 pays off.
func TestEachAgentCarriesItsOwnCredentialAndModel(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("ok")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	if _, err := e.AddAgent(context.Background(), Agent{Name: "big", KeyName: "claude", Model: "claude-opus-5"}); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}
	if _, err := e.AddAgent(context.Background(), Agent{Name: "local", KeyName: "ollama", Model: "qwen3:30b"}); err != nil {
		t.Fatalf("AddAgent: %v", err)
	}

	big, _ := e.Agent("big")
	local, _ := e.Agent("local")

	if big.KeyName == local.KeyName || big.Model == local.Model {
		t.Errorf("the agents share a credential or model: %+v %+v", big, local)
	}

	// And the sessions were created with them, so a turn uses the right one.
	bigSession, _ := e.Session(big.SessionID)
	if bigSession.KeyName != "claude" || bigSession.Model != "claude-opus-5" {
		t.Errorf("session = %+v, want the agent's credential and model", bigSession)
	}
}

// Two agents with one name means every later reference is ambiguous, including the ones in the
// audit trail, which is where ambiguity costs most.
func TestADuplicateAgentNameIsRefused(t *testing.T) {
	e, _ := engineWithAgents(t, "parser")

	if _, err := e.AddAgent(context.Background(), Agent{Name: "parser", KeyName: "claude"}); err == nil {
		t.Error("a second agent took an existing name")
	}
	if len(e.Agents()) != 1 {
		t.Errorf("%d agents, want the duplicate refused", len(e.Agents()))
	}
}

func TestAgentNamesAreConstrained(t *testing.T) {
	e, _ := engineWithAgents(t)

	for _, name := range []string{"", "   ", " leading", "trailing ", strings.Repeat("x", 40),
		"has\nnewline"} {
		if _, err := e.AddAgent(context.Background(), Agent{Name: name, KeyName: "claude"}); err == nil {
			t.Errorf("%q was accepted as an agent name", name)
		}
	}
}

// Creation order is the order somebody built them in and therefore the order they already have in
// their head. A list that rearranges itself is one you have to re read.
func TestAgentsComeBackInCreationOrder(t *testing.T) {
	e, _ := engineWithAgents(t, "zebra", "apple", "middle")

	var names []string
	for _, agent := range e.Agents() {
		names = append(names, agent.Name)
	}
	want := []string{"zebra", "apple", "middle"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}

// With eight agents running the useful question is not "where is the one called docs" but "which of
// these has stopped and cannot start again without me".
func TestTheAgentListPutsWhatNeedsAttentionFirst(t *testing.T) {
	e, client := engineWithAgents(t, "idle-one", "failed-one", "blocked-one")

	failed, _ := e.Agent("failed-one")
	client.openErr = &core.ProviderError{
		Kind: core.ErrAuthentication, Provider: "claude", Message: "rejected",
	}
	turnID, _ := e.Send(failed.SessionID, "go")
	waitForTurn(t, e, failed.SessionID, turnID)
	client.openErr = nil

	// And one genuinely waiting on a person.
	blocked, _ := e.Agent("blocked-one")
	req := permission.Request{
		AgentID: blocked.SessionID, SessionID: blocked.SessionID,
		Tool: "run_command", Kind: core.ToolExecute, Command: "make test",
	}
	go func() {
		_ = e.Approve(context.Background(), req,
			permission.Decide(req, core.TrustStandard, permission.NewGrants()))
	}()
	waitForPrompt(t, e, blocked.SessionID)

	statuses := e.AgentStatuses()
	if len(statuses) != 3 {
		t.Fatalf("%d statuses", len(statuses))
	}
	if statuses[0].Agent.Name != "blocked-one" {
		t.Errorf("first is %q, want the one waiting on a person", statuses[0].Agent.Name)
	}
	if statuses[0].State != core.AgentAwaitingPermission {
		t.Errorf("state = %s", statuses[0].State)
	}
	// And it says what it is waiting for, or the list tells you to go somewhere without saying why.
	if !strings.Contains(statuses[0].Waiting, "make test") {
		t.Errorf("waiting = %q, want what it is blocked on", statuses[0].Waiting)
	}
	if statuses[1].Agent.Name != "failed-one" {
		t.Errorf("second is %q, want the failed one", statuses[1].Agent.Name)
	}

	e.Answer(blocked.SessionID, false, false)
}

// Usage and cost are attributed per agent, which is the whole reason a credential belongs to an
// agent rather than to the program.
func TestUsageIsAttributedPerAgent(t *testing.T) {
	e, client := engineWithAgents(t, "busy", "quiet")

	busy, _ := e.Agent("busy")
	for i := 0; i < 3; i++ {
		client.events = reply("answer")
		turnID, err := e.Send(busy.SessionID, "question")
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		waitForTurn(t, e, busy.SessionID, turnID)
	}

	statuses := e.AgentStatuses()
	byName := map[string]AgentStatus{}
	for _, status := range statuses {
		byName[status.Agent.Name] = status
	}

	if byName["busy"].Turns != 3 {
		t.Errorf("busy did %d turns, want 3", byName["busy"].Turns)
	}
	if byName["quiet"].Turns != 0 {
		t.Errorf("quiet did %d turns, want none", byName["quiet"].Turns)
	}
	if byName["busy"].Usage.TotalTokens() == 0 {
		t.Error("no usage attributed to the agent that did the work")
	}
	if byName["quiet"].Usage.TotalTokens() != 0 {
		t.Error("usage attributed to an agent that did nothing")
	}
}

// An agent is a worker and its transcript is a record of what was done. Dismissing the worker is not
// a reason to burn the record.
func TestRemovingAnAgentKeepsItsConversation(t *testing.T) {
	e, client := engineWithAgents(t, "temporary")

	agent, _ := e.Agent("temporary")
	client.events = reply("something worth keeping")
	turnID, _ := e.Send(agent.SessionID, "do a thing")
	waitForTurn(t, e, agent.SessionID, turnID)

	if err := e.RemoveAgent("temporary"); err != nil {
		t.Fatalf("RemoveAgent: %v", err)
	}
	if _, ok := e.Agent("temporary"); ok {
		t.Error("the agent is still registered")
	}

	session, ok := e.Session(agent.SessionID)
	if !ok {
		t.Fatal("the conversation was destroyed with the agent")
	}
	if len(session.Turns) != 1 || session.Turns[0].Text != "something worth keeping" {
		t.Errorf("the transcript was altered: %+v", session.Turns)
	}

	if err := e.RemoveAgent("never-existed"); err == nil {
		t.Error("removing an agent that does not exist should be an error")
	}
}

// An agent is not a worktree. Coupling the two would make "run an agent" mean "make a branch".
func TestAnAgentNeedsNoWorktree(t *testing.T) {
	e, _ := engineWithAgents(t, "ordinary")

	agent, ok := e.Agent("ordinary")
	if !ok {
		t.Fatal("the agent was not registered")
	}
	if agent.Isolated {
		t.Error("an agent created without asking for isolation should not be isolated")
	}
	if agent.WorkspaceID != "" {
		t.Errorf("workspace = %q, want none", agent.WorkspaceID)
	}
}

func TestFindingTheAgentASessionBelongsTo(t *testing.T) {
	e, _ := engineWithAgents(t, "parser", "docs")

	parser, _ := e.Agent("parser")
	found, ok := e.AgentFor(parser.SessionID)
	if !ok || found.Name != "parser" {
		t.Errorf("AgentFor = %+v, %v", found, ok)
	}
	if _, ok := e.AgentFor("session-does-not-exist"); ok {
		t.Error("a session with no agent reported one")
	}
}

// M-08. Resuming registers the same agent onto a conversation that already exists, rather than
// starting a second one beside it.
//
// The alternative is what `canopy pickup` would do without this: open the conversation asked for,
// with a fresh empty agent sitting next to it in the agents list, having quietly created a
// conversation nobody wanted.
func TestAnAgentCanBeGivenAConversationItAlreadyHad(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)

	existing := e.Create("claude", "claude-opus-5")
	before := len(e.Sessions())

	agent, err := e.AddAgent(context.Background(), Agent{
		Name: "main", SessionID: existing.ID, KeyName: "claude", Model: "claude-opus-5",
	})
	if err != nil {
		t.Fatalf("resuming %s: %v", existing.ID, err)
	}
	if agent.SessionID != existing.ID {
		t.Errorf("the agent was put on %s rather than on %s", agent.SessionID, existing.ID)
	}
	if after := len(e.Sessions()); after != before {
		t.Errorf("resuming started a conversation as well: %d became %d", before, after)
	}
}

// And a code that names nothing is refused rather than quietly turned into a new conversation,
// which looks exactly like history having been lost.
func TestResumingAConversationThatIsNotThereIsRefused(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("answer")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)

	_, err := e.AddAgent(context.Background(), Agent{
		Name: "main", SessionID: "session-404", KeyName: "claude",
	})
	if err == nil {
		t.Fatal("resuming a conversation that does not exist was allowed")
	}
	if len(e.Sessions()) != 0 {
		t.Errorf("a conversation was created while refusing to resume one: %d", len(e.Sessions()))
	}
}

// M-09. The mode's prompt reaches the model.
//
// Without this the level is enforced and never explained, so a planning agent tries to edit, is
// refused, tries again, and spends the turn thrashing against a boundary nobody told it about. The
// engine was not setting a system prompt at all before modes existed, so this is the whole of the
// wiring and worth pinning: it is invisible from the interface and would fail silently.
func TestTheModePromptIsSentAsTheSystemPrompt(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("a plan")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)

	created := e.Create("claude", "claude-opus-5")
	plan, _ := core.ModeByName(core.ModePlan)
	if err := e.SetMode(created.ID, plan); err != nil {
		t.Fatalf("SetMode: %v", err)
	}

	turnID, err := e.Send(created.ID, "what should we do about the parser")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, created.ID, turnID)

	client.mu.Lock()
	got := client.system
	client.mu.Unlock()

	if got != plan.Prompt {
		t.Errorf("the provider was sent system prompt %q, want the plan mode prompt", got)
	}
}

// And build sends none, which is the deliberate exception: it is the ordinary way to work, and
// describing it would spend context telling the model that nothing unusual is going on.
func TestBuildModeSendsNoSystemPrompt(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("done")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	t.Cleanup(e.Close)

	created := e.Create("claude", "claude-opus-5")
	turnID, err := e.Send(created.ID, "rename the field")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, created.ID, turnID)

	client.mu.Lock()
	got := client.system
	client.mu.Unlock()

	if got != "" {
		t.Errorf("build sent a system prompt: %q", got)
	}
}
