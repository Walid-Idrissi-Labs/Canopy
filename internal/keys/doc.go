// Package keys stores provider credentials.
//
// Secrets live in the OS keychain, macOS Keychain or the Linux secret service. A file backend
// exists only as an explicit, loudly warned opt in, never as a silent fallback, because writing
// plaintext credentials to disk when the keychain was awkward is the kind of shortcut that stays
// invisible until it is a headline.
//
// Nothing here returns a secret into a type that can be logged, serialised or put in an event.
// See core.Secret and core.KeyRef.
//
// Filled in by A1-02.
package keys
