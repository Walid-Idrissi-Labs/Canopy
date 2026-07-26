package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Redacted is what a secret renders as anywhere it is printed.
const Redacted = "[redacted]"

// Secret holds a credential value that must never be printed, logged or serialised by accident.
//
// The protection is structural rather than procedural. The value lives in an unexported field, and
// every path Go uses to turn a value into text is overridden to return [redacted]: String for
// fmt's %s and %v, Format for the verbs that bypass Stringer such as %#v and %q, GoString for
// %#v, and MarshalJSON for anything that gets encoded. There is exactly one way to get the value
// out, and it is called Reveal, so a reviewer can grep for it and a careless line cannot leak.
//
// The reason for going this far: a leaked credential is unrecoverable. Every other kind of bug in
// this project can be fixed after it is found. That one cannot, so it gets designed out rather
// than tested for.
type Secret struct {
	value string
}

// NewSecret wraps a credential value.
func NewSecret(value string) Secret {
	return Secret{value: value}
}

// Reveal returns the underlying value.
//
// This is the only way to get it, and it is deliberately conspicuous. Call it as late as possible,
// ideally at the point of building the request, and never store the result anywhere that outlives
// the call.
func (s Secret) Reveal() string {
	return s.value
}

// IsZero reports whether no value was ever set.
func (s Secret) IsZero() bool {
	return s.value == ""
}

// Fingerprint returns a short, non reversible identifier for this secret.
//
// It exists so a user can tell two credentials apart in a list without either of them being
// displayed. Truncated to twelve hex characters, which is far too little to attack the input and
// far more than enough to distinguish a handful of keys.
func (s Secret) Fingerprint() string {
	if s.IsZero() {
		return ""
	}
	sum := sha256.Sum256([]byte(s.value))
	return hex.EncodeToString(sum[:])[:12]
}

// String implements fmt.Stringer, covering %s and %v.
func (s Secret) String() string { return Redacted }

// GoString implements fmt.GoStringer, covering %#v.
func (s Secret) GoString() string { return Redacted }

// Format covers the verbs that ignore Stringer, notably %q and %x.
//
// Without this a single fmt.Sprintf("%q", secret) would print the credential, which is exactly the
// sort of line that gets written while debugging and then committed.
func (s Secret) Format(f fmt.State, verb rune) {
	_, _ = f.Write([]byte(Redacted))
}

// MarshalJSON ensures a secret cannot be serialised, whether directly or as a struct field.
func (s Secret) MarshalJSON() ([]byte, error) {
	return json.Marshal(Redacted)
}

// UnmarshalJSON refuses to load a secret from JSON.
//
// Credentials come from the keychain and from a prompt, never from a config file or an API
// payload. Accepting one here would create a supported path for writing a key into a file that
// someone then commits.
func (s *Secret) UnmarshalJSON([]byte) error {
	return fmt.Errorf("secrets cannot be read from JSON, load them from the key store instead")
}
