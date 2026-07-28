package session_test

import (
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// M-08. The code printed on the way out is the whole resume path until there is a chat picker, so
// what matters is that it survives the round trip: whatever Canopy prints has to name the same
// conversation when it is typed back in.
func TestACodeNamesTheConversationItCameFrom(t *testing.T) {
	for _, id := range []string{"session-1", "session-7", "session-4096"} {
		if got := session.SessionID(session.Code(id)); got != id {
			t.Errorf("the code for %s comes back as %s", id, got)
		}
	}
}

// The full id is accepted too. It is what `canopy search` prints and what turns up in anything
// pasted from a log, so somebody typing the longer form has not made a mistake.
func TestTheFullIdIsAcceptedAsWellAsTheCode(t *testing.T) {
	if got := session.SessionID("session-7"); got != "session-7" {
		t.Errorf("the full id came back as %q", got)
	}
	if got := session.SessionID("  7 "); got != "session-7" {
		t.Errorf("a code with spaces around it came back as %q", got)
	}
}

// Nothing typed names nothing, rather than naming the conversation called "session-".
func TestAnEmptyCodeNamesNothing(t *testing.T) {
	for _, code := range []string{"", "   "} {
		if got := session.SessionID(code); got != "" {
			t.Errorf("%q was turned into %q", code, got)
		}
	}
}
