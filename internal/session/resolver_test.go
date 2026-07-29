package session

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/keys"
)

// The message a first run ends on, so the key it names has to be the key that works.
//
// It said "press k", which opens the credential screen from the worktree monitor and does nothing
// at all in a conversation, which is where somebody reads this. A message that names the wrong key
// teaches people that the program's instructions are not worth following.
func TestTheEmptyCredentialMessageNamesTheKeyThatOpensTheList(t *testing.T) {
	store := keys.NewStore(keys.NewMemoryBackend(), filepath.Join(t.TempDir(), "keys.json"))

	_, _, err := NewKeyResolver(store).Resolve("", "claude-opus-5")
	if err == nil {
		t.Fatal("resolving with nothing stored should refuse and say what to do about it")
	}
	if !strings.Contains(err.Error(), "press ctrl+k") {
		t.Errorf("the message is %q, and ctrl+k is what opens the credential screen", err)
	}
}
