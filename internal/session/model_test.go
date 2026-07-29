package session

import (
	"context"
	"sync"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/pricing"
)

// recordingResolver remembers what each turn was resolved for, which is the only place a model
// change can be proved to have reached the wire rather than only the snapshot.
type recordingResolver struct {
	client *recordingClient

	mu    sync.Mutex
	asked [][2]string
}

func (r *recordingResolver) Resolve(name, model string) (core.ProviderClient, pricing.ModelID, error) {
	r.mu.Lock()
	r.asked = append(r.asked, [2]string{name, model})
	r.mu.Unlock()
	return r.client, pricing.ModelID{Provider: core.ProviderAnthropic, Model: model}, nil
}

func (r *recordingResolver) lastAsked() [2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.asked) == 0 {
		return [2]string{}
	}
	return r.asked[len(r.asked)-1]
}

// recordingClient is a provider that answers immediately and remembers the model it was sent.
type recordingClient struct {
	mu     sync.Mutex
	models []string
}

func (c *recordingClient) Name() string { return "recording" }

func (c *recordingClient) Stream(_ context.Context, req core.Request) (core.Stream, error) {
	c.mu.Lock()
	c.models = append(c.models, req.Model)
	c.mu.Unlock()
	return &scriptedStream{events: reply("done")}, nil
}

func (c *recordingClient) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.models...)
}

// The whole point of the picker: what it changes has to be what the next request is actually sent
// on. A model that moved in the snapshot and not on the wire is a screen that lies.
func TestChangingTheModelReachesTheNextRequest(t *testing.T) {
	client := &recordingClient{}
	resolver := &recordingResolver{client: client}
	e := New(resolver)
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	first, err := e.Send(session.ID, "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitForTurn(t, e, session.ID, first)

	if got := resolver.lastAsked(); got != [2]string{"claude", "claude-opus-5"} {
		t.Fatalf("the first turn resolved %v", got)
	}

	if err := e.UseCredential(session.ID, "personal", "claude-sonnet-5"); err != nil {
		t.Fatalf("UseCredential: %v", err)
	}

	second, err := e.Send(session.ID, "again")
	if err != nil {
		t.Fatalf("Send after the change: %v", err)
	}
	waitForTurn(t, e, session.ID, second)

	if got := resolver.lastAsked(); got != [2]string{"personal", "claude-sonnet-5"} {
		t.Errorf("the next turn resolved %v, want the credential and model that were chosen", got)
	}
	sent := client.sent()
	if len(sent) != 2 || sent[1] != "claude-sonnet-5" {
		t.Errorf("the models sent to the provider were %v", sent)
	}

	// And the conversation itself says so, so anything reading the session agrees with the wire.
	current, _ := e.Session(session.ID)
	if current.KeyName != "personal" || current.Model != "claude-sonnet-5" {
		t.Errorf("the session records %s on %s", current.KeyName, current.Model)
	}
}

// Mid answer it is refused rather than applied, because a reply paid for by one credential and
// attributed to another is a transcript that is wrong about what produced it. Applying at the next
// turn boundary is the whole design, so a refusal here has to be visible rather than silent.
func TestChangingTheModelMidAnswerIsRefused(t *testing.T) {
	gate := make(chan struct{})
	client := &scriptedClient{name: "claude", events: reply("slow"), gate: gate}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	session := e.Create("claude", "claude-opus-5")
	turnID, err := e.Send(session.ID, "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if err := e.UseCredential(session.ID, "personal", "claude-sonnet-5"); err == nil {
		t.Error("the model was changed part way through an answer")
	}

	close(gate)
	waitForTurn(t, e, session.ID, turnID)

	// And once the turn is done it goes through, which is what makes the refusal a wait rather than
	// a wall.
	if err := e.UseCredential(session.ID, "personal", "claude-sonnet-5"); err != nil {
		t.Errorf("the change was still refused after the turn finished: %v", err)
	}
}
