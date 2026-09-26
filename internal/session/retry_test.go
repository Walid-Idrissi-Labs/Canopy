package session

import (
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A retry asks the model to carry on from the failed turn, which stays in what it is sent: the
// question goes to the model once, with the retry after it.
func TestARetrySendsTheQuestionOnce(t *testing.T) {
	client := &scriptedClient{name: "claude", events: reply("done"),
		openErr: &core.ProviderError{Kind: core.ErrOverloaded, Message: "busy"}}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()
	session := e.Create("claude", "claude-opus-5")
	first, err := e.Send(session.ID, "fix the parser")
	if err != nil {
		t.Fatal(err)
	}
	failedTurn := waitForTurn(t, e, session.ID, first)
	if failedTurn.State != core.TurnFailed || failedTurn.ErrorKind != core.ErrOverloaded {
		t.Fatalf("the failure was recorded as %s %q", failedTurn.State, failedTurn.ErrorKind)
	}
	client.mu.Lock()
	client.openErr = nil
	client.mu.Unlock()
	second, err := e.Retry(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waitForTurn(t, e, session.ID, second).Text != "done" {
		t.Fatal("the retry did not answer")
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	asked := 0
	for _, m := range client.history {
		if m.Role == core.RoleUser && strings.Contains(m.Text, "fix the parser") {
			asked++
		}
	}
	if asked != 1 {
		t.Fatalf("the question went to the model %d times", asked)
	}
	if last := client.history[len(client.history)-1]; !strings.Contains(last.Text, "answer it now") ||
		!strings.Contains(last.Text, string(core.ErrOverloaded)) {
		t.Fatalf("the retry sent %q", last.Text)
	}
	s, _ := e.Session(session.ID)
	if !s.Turns[0].Retried {
		t.Fatal("the failed turn is not marked as retried")
	}
}

// What retrying cannot fix is refused with what to do instead, and a wait the provider asked for
// is kept.
func TestRetryRefusesWhatItCannotFix(t *testing.T) {
	for _, tc := range []struct {
		err  *core.ProviderError
		want string
	}{
		{&core.ProviderError{Kind: core.ErrAuthentication}, "credential was refused"},
		{&core.ProviderError{Kind: core.ErrRateLimited, RetryAfter: time.Hour}, "asked for"},
	} {
		client := &scriptedClient{name: "claude", openErr: tc.err}
		e := New(fixedResolver{client: client, id: anthropicID()})
		session := e.Create("claude", "claude-opus-5")
		id, _ := e.Send(session.ID, "go")
		waitForTurn(t, e, session.ID, id)
		if _, err := e.Retry(session.ID); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.err.Kind, err)
		}
		e.Close()
	}
}

// A retry the engine refuses leaves the failure as it was, to be tried again later.
func TestARefusedRetryLeavesTheFailureStanding(t *testing.T) {
	client := &scriptedClient{name: "claude", openErr: &core.ProviderError{Kind: core.ErrOverloaded}}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()
	session := e.Create("claude", "claude-opus-5")
	id, _ := e.Send(session.ID, "go")
	waitForTurn(t, e, session.ID, id)
	e.budgets.mu.Lock()
	e.budgets.session[session.ID] = &Budget{Limit: 1, Spent: 2}
	e.budgets.mu.Unlock()
	if _, err := e.Retry(session.ID); err == nil {
		t.Fatal("a retry over the cap was sent")
	}
	s, _ := e.Session(session.ID)
	if len(s.Turns) != 1 || s.Turns[0].Retried {
		t.Fatalf("turns %d, retried %v", len(s.Turns), s.Turns[0].Retried)
	}
}

// What a failed turn did before failing is kept in what the model is sent after a retry.
func TestARetriedTurnsStepsStayInTheContext(t *testing.T) {
	failed := core.Turn{ID: "t1", State: core.TurnFailed, Retried: true,
		Request: core.Message{Role: core.RoleUser, Text: "fix it"},
		Steps: []core.Message{
			{Role: core.RoleAssistant, ToolCalls: []core.ToolCall{{ID: "c1", Name: "edit_file"}}},
			{Role: core.RoleUser, ToolResults: []core.ToolResult{{CallID: "c1", Content: "edited"}}},
		}}
	history := core.Session{Turns: []core.Turn{failed}}.History()
	if len(history) != 3 || history[1].ToolCalls[0].Name != "edit_file" {
		t.Fatalf("history %+v", history)
	}
	if !strings.Contains(retryPrompt(failed), "Carry on") {
		t.Fatalf("a turn cut off partway is retried with %q", retryPrompt(failed))
	}
}
