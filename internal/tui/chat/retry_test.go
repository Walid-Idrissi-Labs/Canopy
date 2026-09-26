package chat_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

func failed(kind core.ProviderErrorKind, retryAfter time.Duration) *fakeEngine {
	t := turn("t1", "fix it", "", core.TurnFailed)
	t.Error, t.ErrorKind, t.RetryAfter, t.EndedAt = "it failed", kind, retryAfter, time.Now()
	return &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{t}}}
}

func opened(engine *fakeEngine) chat.Model {
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(120, 30)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	return m
}

// A failure trying again can fix offers enter; enter retries rather than sending an empty box.
func TestAFailedTurnIsRetriedWithEnter(t *testing.T) {
	engine := failed(core.ErrOverloaded, 0)
	m := opened(engine)
	if !strings.Contains(plain(m.Body()), "enter tries it again") {
		t.Fatalf("no retry offered:\n%s", plain(m.Body()))
	}
	_, _ = m.Update(keyCode(tea.KeyEnter))
	if engine.retried != 1 || len(engine.sent) != 0 {
		t.Fatalf("retried %d, sent %v", engine.retried, engine.sent)
	}
	// With another agent's question waiting on enter, enter answers that, and no card claims it.
	visited := failed(core.ErrOverloaded, 0)
	visited.waiting = []session.Waiting{{SessionID: "s2", Agent: "other",
		Request: permission.Request{SessionID: "s2", Tool: "run_command", Command: "make"}}}
	m = opened(visited)
	if strings.Contains(plain(m.Body()), "enter tries it again") {
		t.Fatal("the retry card offered enter while another agent's question waits on it")
	}
	_, _ = m.Update(keyCode(tea.KeyEnter))
	if visited.retried != 0 {
		t.Fatal("enter retried while another agent's question was waiting")
	}
	network := opened(failed(core.ErrNetwork, 0))
	if !strings.Contains(plain(network.Body()), "the network failed") {
		t.Fatalf("a network failure is not called one:\n%s", plain(network.Body()))
	}
}

// A refused credential is never offered a retry, and the credential is named.
func TestARefusedCredentialSaysWhatToFix(t *testing.T) {
	engine := failed(core.ErrAuthentication, 0)
	m := opened(engine)
	body := plain(m.Body())
	if strings.Contains(body, "tries it again") || !strings.Contains(body, "the credential claude was refused") {
		t.Fatalf("%s", body)
	}
	_, _ = m.Update(keyCode(tea.KeyEnter))
	if engine.retried != 0 {
		t.Fatal("an authentication failure was retried")
	}
}

// A rate limit counts down the wait the provider asked for.
func TestARateLimitCountsDown(t *testing.T) {
	engine := failed(core.ErrRateLimited, 30*time.Second)
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(120, 30)
	m, cmd := m.Update(chat.EventMsg{Event: core.Event{}})
	if !strings.Contains(plain(m.Body()), "asked to wait 30s") && !strings.Contains(plain(m.Body()), "asked to wait 29s") {
		t.Fatalf("no countdown:\n%s", plain(m.Body()))
	}
	if cmd == nil {
		t.Fatal("no timer was started to count down")
	}
}
