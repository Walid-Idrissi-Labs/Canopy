package session

import (
	"context"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
)

func approvalRequest() (permission.Request, permission.Decision) {
	req := permission.Request{
		AgentID: "s1", SessionID: "s1",
		Tool: "run_command", Kind: core.ToolExecute, Command: "make test",
	}
	return req, permission.Decide(req, core.TrustStandard, permission.NewGrants())
}

// The loop blocks in the background and the interface must never block. The question has to arrive
// in the snapshot, be answerable from the event loop, and unblock the loop.
func TestAQuestionReachesTheInterfaceAndTheAnswerReachesTheLoop(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	req, decision := approvalRequest()

	answered := make(chan bool, 1)
	go func() { answered <- e.Approve(context.Background(), req, decision) }()

	// The question shows up where the interface will find it.
	prompt := waitForPrompt(t, e, "s1")
	if prompt.Request.Command != "make test" {
		t.Errorf("prompt = %+v", prompt)
	}
	if prompt.Scope().Command != "make test" {
		t.Errorf("the prompt has to show what approving would cover, got %q", prompt.Scope())
	}
	if !e.AwaitingApproval("s1") {
		t.Error("the session should read as waiting on a person")
	}

	if !e.Answer("s1", true, false) {
		t.Fatal("Answer found nothing to answer")
	}

	select {
	case approved := <-answered:
		if !approved {
			t.Error("the loop was told no despite the answer being yes")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the loop is still blocked after being answered")
	}

	if e.AwaitingApproval("s1") {
		t.Error("the session still reads as waiting after being answered")
	}
}

func TestAnsweringNoRefusesTheCall(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	req, decision := approvalRequest()
	answered := make(chan bool, 1)
	go func() { answered <- e.Approve(context.Background(), req, decision) }()

	waitForPrompt(t, e, "s1")
	e.Answer("s1", false, false)

	select {
	case approved := <-answered:
		if approved {
			t.Error("answering no approved the call")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the loop is still blocked")
	}
}

// Without a way to say "and stop asking", somebody watching an agent make thirty edits answers
// thirty prompts, and the thirtieth is not being read.
func TestRememberingWidensTheApprovalToLaterCalls(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	req, decision := approvalRequest()

	first := make(chan bool, 1)
	go func() { first <- e.Approve(context.Background(), req, decision) }()
	waitForPrompt(t, e, "s1")
	e.Answer("s1", true, true)
	<-first

	// The same call again is now covered without asking, which is what the grant is for.
	grants := e.grantsFor("s1")
	if !grants.Covers(req, decision.Scope) {
		t.Error("remembering the approval did not cover the same call again")
	}
}

// The default is the opposite: approving once covers this call and nothing else.
func TestApprovingWithoutRememberingDoesNotWiden(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	req, decision := approvalRequest()
	answered := make(chan bool, 1)
	go func() { answered <- e.Approve(context.Background(), req, decision) }()

	waitForPrompt(t, e, "s1")
	e.Answer("s1", true, false)
	<-answered

	if e.grantsFor("s1").Covers(req, decision.Scope) {
		t.Error("a one time approval was remembered anyway, so the next identical call would run " +
			"without being shown")
	}
}

// They never answered, and a cancelled turn should not leave a command running behind it.
func TestCancellingWhileWaitingRefuses(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, decision := approvalRequest()

	answered := make(chan bool, 1)
	go func() { answered <- e.Approve(ctx, req, decision) }()

	waitForPrompt(t, e, "s1")
	cancel()

	select {
	case approved := <-answered:
		if approved {
			t.Error("cancelling while a question was open approved it")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling did not unblock the waiting loop")
	}

	// And the question is taken down, or the interface keeps showing a prompt nobody can answer.
	deadline := time.After(2 * time.Second)
	for e.AwaitingApproval("s1") {
		select {
		case <-deadline:
			t.Fatal("the prompt is still showing after the turn was cancelled")
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// Somebody is already being asked something, and silently dropping their question to ask a
// different one is worse than declining the second.
func TestASecondQuestionDoesNotReplaceTheFirst(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	req, decision := approvalRequest()
	first := make(chan bool, 1)
	go func() { first <- e.Approve(context.Background(), req, decision) }()
	waitForPrompt(t, e, "s1")

	other := req
	other.Command = "rm -rf /"
	if e.Approve(context.Background(), other, decision) {
		t.Error("a second question was approved while another was open")
	}

	// The original is untouched and still answerable.
	prompt, ok := e.Pending("s1")
	if !ok || prompt.Request.Command != "make test" {
		t.Errorf("the original question was replaced: %+v", prompt)
	}
	e.Answer("s1", true, false)
	<-first
}

// A double keystroke should not send two answers, and the first is the one they meant.
func TestAnsweringTwiceIsIgnoredTheSecondTime(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	req, decision := approvalRequest()
	answered := make(chan bool, 1)
	go func() { answered <- e.Approve(context.Background(), req, decision) }()

	waitForPrompt(t, e, "s1")
	if !e.Answer("s1", true, false) {
		t.Fatal("the first answer was not accepted")
	}
	e.Answer("s1", false, false) // ignored either way

	if approved := <-answered; !approved {
		t.Error("the second answer overrode the first")
	}
}

// An interface that sends a stale answer, because the turn was cancelled between drawing the prompt
// and the keystroke, should be able to tell rather than silently doing nothing.
func TestAnsweringNothingReportsSo(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	if e.Answer("s1", true, false) {
		t.Error("Answer claimed to have answered a question that was not being asked")
	}
	if _, ok := e.Pending("s1"); ok {
		t.Error("a session with no question reported one")
	}
}

func waitForPrompt(t *testing.T, e *Engine, sessionID string) Prompt {
	t.Helper()

	deadline := time.After(3 * time.Second)
	for {
		if prompt, ok := e.Pending(sessionID); ok {
			return prompt
		}
		select {
		case <-deadline:
			t.Fatal("no question ever appeared")
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// requestFrom is a question raised by a named session, so several can be told apart.
func requestFrom(sessionID, command string) (permission.Request, permission.Decision) {
	req := permission.Request{
		AgentID: sessionID, SessionID: sessionID,
		Tool: "run_command", Kind: core.ToolExecute, Command: command,
	}
	return req, permission.Decide(req, core.TrustStandard, permission.NewGrants())
}

// The accessor D-47 needs: every question anywhere, oldest first, each named after the agent asking.
//
// Oldest first matters more than it looks. The prompts live in a map, so without the sequence they
// come back in whatever order the runtime feels like, and a panel showing "the one that has been
// waiting longest" would show a different agent on every frame.
func TestEveryPendingQuestionIsListedOldestFirstAndNamed(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	dir := t.TempDir()
	for _, name := range []string{"first", "second"} {
		if _, err := e.AddAgent(context.Background(), Agent{
			Name: name, KeyName: "claude", Model: "claude-opus-5", Dir: dir, Trust: core.TrustStandard,
		}); err != nil {
			t.Fatalf("AddAgent %s: %v", name, err)
		}
	}

	agents := e.Agents()
	if len(agents) != 2 {
		t.Fatalf("%d agents", len(agents))
	}

	// Asked in a known order, each one only after the previous is parked, so "oldest" is a fact
	// about the test rather than a race it happens to win.
	for _, agent := range agents {
		req, decision := requestFrom(agent.SessionID, "make "+agent.Name)
		go func() { _ = e.Approve(context.Background(), req, decision) }()
		waitForPrompt(t, e, agent.SessionID)
	}

	all := e.PendingAll()
	if len(all) != 2 {
		t.Fatalf("%d questions listed, want both", len(all))
	}
	if all[0].Agent != "first" || all[1].Agent != "second" {
		t.Errorf("the queue reads %q then %q, want the order they were asked in",
			all[0].Agent, all[1].Agent)
	}
	if all[0].SessionID != agents[0].SessionID {
		t.Errorf("the first question belongs to %q", all[0].SessionID)
	}
	if all[0].Request.Command != "make first" || all[0].Scope().Command != "make first" {
		t.Errorf("the listed question lost its detail: %+v", all[0])
	}

	// Answered questions leave the list, so a panel reading it does not offer to answer something
	// that has already been dealt with somewhere else.
	if !e.Answer(agents[0].SessionID, true, false) {
		t.Fatal("Answer found nothing to answer")
	}
	deadline := time.After(3 * time.Second)
	for len(e.PendingAll()) != 1 {
		select {
		case <-deadline:
			t.Fatalf("the answered question is still listed: %+v", e.PendingAll())
		case <-time.After(2 * time.Millisecond):
		}
	}
	if remaining := e.PendingAll(); remaining[0].Agent != "second" {
		t.Errorf("the wrong question was left: %+v", remaining)
	}

	_ = e.Answer(agents[1].SessionID, false, false)
}

// A conversation with no agent record still has to be identifiable, or a question from one surfaces
// as a blank name that tells nobody which screen to go to.
func TestAQuestionFromAnUnnamedConversationIsStillNamed(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	req, decision := requestFrom("session-orphan", "make test")
	go func() { _ = e.Approve(context.Background(), req, decision) }()
	waitForPrompt(t, e, "session-orphan")

	all := e.PendingAll()
	if len(all) != 1 || all[0].Agent != "session-orphan" {
		t.Errorf("an unnamed conversation is listed as %+v", all)
	}
	_ = e.Answer("session-orphan", false, false)
}
