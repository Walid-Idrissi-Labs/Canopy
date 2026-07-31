package session

import (
	"path/filepath"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// A credential's name is what a conversation writes down and what the resolver looks up on its next
// message, so renaming one in the key store and stopping there would leave every conversation on it
// pointing at a name nothing answers to.
func TestRenamingACredentialMovesEveryConversationOnIt(t *testing.T) {
	e := New(fixedResolver{client: &scriptedClient{name: "claude"}, id: anthropicID()})
	defer e.Close()

	on := e.Create("kimi", "moonshot-v1-8k")
	also := e.Create("kimi", "moonshot-v1-32k")
	elsewhere := e.Create("claude", "claude-opus-5")

	if moved := e.RenameCredential("kimi", "moonshot"); moved != 2 {
		t.Fatalf("%d conversations moved, want the two on that credential", moved)
	}

	for _, id := range []string{on.ID, also.ID} {
		s, ok := e.Session(id)
		if !ok {
			t.Fatalf("session %s vanished", id)
		}
		if s.KeyName != "moonshot" {
			t.Errorf("session %s still names %q", id, s.KeyName)
		}
	}

	// And a conversation on another credential is left where it is. A rename it has nothing to do
	// with must not drag it onto this one.
	other, _ := e.Session(elsewhere.ID)
	if other.KeyName != "claude" {
		t.Errorf("an unrelated conversation was moved to %q", other.KeyName)
	}
}

// Not refused mid answer, which is the one way this differs from a credential switch. A switch
// changes which key gets billed and must not happen part way through a reply; a rename changes the
// name and nothing else, and leaving it stale until the turn ends is the harm.
func TestRenamingACredentialIsNotRefusedByARunningTurn(t *testing.T) {
	// The gate holds the stream open, so the turn is genuinely in flight for as long as this test
	// needs it to be.
	gate := make(chan struct{})
	client := &scriptedClient{name: "claude", gate: gate, events: reply("done")}
	e := New(fixedResolver{client: client, id: anthropicID()})
	defer e.Close()

	created := e.Create("kimi", "moonshot-v1-8k")
	turnID, err := e.Send(created.ID, "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// A switch is refused here, which is the behaviour a rename must not be put behind.
	if err := e.UseCredential(created.ID, "claude", "claude-opus-5"); err == nil {
		t.Fatal("the engine allowed a credential switch mid answer, so this test proves nothing")
	}

	if moved := e.RenameCredential("kimi", "moonshot"); moved != 1 {
		t.Fatalf("%d conversations moved while a turn was in flight, want 1", moved)
	}

	close(gate)
	waitForTurn(t, e, created.ID, turnID)

	s, _ := e.Session(created.ID)
	if s.KeyName != "moonshot" {
		t.Errorf("the conversation with a turn in flight was left on %q", s.KeyName)
	}
}

// The database is the other half. `canopy keys rename` runs in a process with no engine at all, and
// what it has to move is what is on disk.
func TestRenamingACredentialMovesTheStoredConversations(t *testing.T) {
	storage, err := OpenStorage(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	defer func() { _ = storage.Close() }()

	for _, s := range []core.Session{
		{ID: "session-1", KeyName: "kimi", Model: "moonshot-v1-8k"},
		{ID: "session-2", KeyName: "kimi", Model: "moonshot-v1-32k"},
		{ID: "session-3", KeyName: "claude", Model: "claude-opus-5"},
	} {
		if err := storage.SaveSession(s); err != nil {
			t.Fatalf("SaveSession: %v", err)
		}
	}

	moved, err := storage.RenameCredential("kimi", "moonshot")
	if err != nil {
		t.Fatalf("RenameCredential: %v", err)
	}
	if moved != 2 {
		t.Errorf("%d rows moved, want the two on that credential", moved)
	}

	for id, want := range map[string]string{
		"session-1": "moonshot", "session-2": "moonshot", "session-3": "claude",
	} {
		loaded, err := storage.Load(id)
		if err != nil {
			t.Fatalf("Load %s: %v", id, err)
		}
		if loaded.KeyName != want {
			t.Errorf("%s came back on %q, want %q", id, loaded.KeyName, want)
		}
	}
}
