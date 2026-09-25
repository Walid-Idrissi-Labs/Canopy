package session

import (
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A retried turn is asked again as a new one, and the question goes to the model once.
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
