package keys

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

// service is the keychain service name every Canopy credential is filed under.
const service = "canopy"

// ErrNotFound is returned when a credential is not in the backend.
var ErrNotFound = errors.New("credential not found")

// Backend is where secret values actually live.
//
// It deals in plain strings because that is what the operating system APIs take. Everything above
// this interface uses core.Secret, and the conversion happens at the boundary in store.go so the
// window in which a bare credential exists as an ordinary string is as small as possible.
type Backend interface {
	// Name identifies the backend for display, so a user can tell where their credentials are.
	Name() string
	// Set stores a secret under an account name.
	Set(account, secret string) error
	// Get retrieves a secret, returning ErrNotFound if it is absent.
	Get(account string) (string, error)
	// Delete removes a secret. Deleting something absent is not an error.
	Delete(account string) error
}

// keychainBackend stores secrets in the operating system credential store: the macOS Keychain, or
// the Secret Service on Linux.
//
// Worth recording, because it was checked rather than assumed: on macOS the underlying library
// pipes its command through the `security` binary's stdin rather than passing the credential as an
// argument, so the secret never appears in the process list where other users could read it.
type keychainBackend struct{}

func (keychainBackend) Name() string { return "os-keychain" }

func (keychainBackend) Set(account, secret string) error {
	if err := keyring.Set(service, account, secret); err != nil {
		return fmt.Errorf("storing in the OS keychain: %w", err)
	}
	return nil
}

func (keychainBackend) Get(account string) (string, error) {
	secret, err := keyring.Get(service, account)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return "", ErrNotFound
	case err != nil:
		return "", fmt.Errorf("reading from the OS keychain: %w", err)
	}
	return secret, nil
}

func (keychainBackend) Delete(account string) error {
	err := keyring.Delete(service, account)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("removing from the OS keychain: %w", err)
	}
	return nil
}

// fileBackend stores secrets in a mode 0600 file.
//
// This exists only because some environments genuinely have no credential store: a container, a CI
// runner, a headless machine with no D-Bus session. It is never selected automatically. Falling
// back to plaintext on disk because the keychain was awkward is the kind of shortcut that stays
// invisible until it is a headline, so choosing it has to be a decision somebody made on purpose
// and can be reminded of.
type fileBackend struct {
	path string
	mu   sync.Mutex
}

func (f *fileBackend) Name() string { return "file (insecure)" }

func (f *fileBackend) read() (map[string]string, error) {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the credential file: %w", err)
	}
	secrets := map[string]string{}
	if err := json.Unmarshal(data, &secrets); err != nil {
		return nil, fmt.Errorf("the credential file at %s is corrupt: %w", f.path, err)
	}
	return secrets, nil
}

func (f *fileBackend) write(secrets map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return fmt.Errorf("creating the credential directory: %w", err)
	}
	data, err := json.Marshal(secrets)
	if err != nil {
		return fmt.Errorf("encoding credentials: %w", err)
	}
	return writeFileAtomic(f.path, data, 0o600)
}

func (f *fileBackend) Set(account, secret string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	secrets, err := f.read()
	if err != nil {
		return err
	}
	secrets[account] = secret
	return f.write(secrets)
}

func (f *fileBackend) Get(account string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	secrets, err := f.read()
	if err != nil {
		return "", err
	}
	secret, ok := secrets[account]
	if !ok {
		return "", ErrNotFound
	}
	return secret, nil
}

func (f *fileBackend) Delete(account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	secrets, err := f.read()
	if err != nil {
		return err
	}
	if _, ok := secrets[account]; !ok {
		return nil
	}
	delete(secrets, account)
	return f.write(secrets)
}

// memoryBackend keeps secrets in memory and never persists them. For tests.
type memoryBackend struct {
	mu      sync.Mutex
	secrets map[string]string
}

// NewMemoryBackend returns a backend that keeps secrets in memory only.
//
// Exported so tests elsewhere can exercise the key store without touching the real keychain,
// which would otherwise leave test credentials on a developer's machine.
func NewMemoryBackend() Backend {
	return &memoryBackend{secrets: map[string]string{}}
}

func (m *memoryBackend) Name() string { return "memory (test only)" }

func (m *memoryBackend) Set(account, secret string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[account] = secret
	return nil
}

func (m *memoryBackend) Get(account string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	secret, ok := m.secrets[account]
	if !ok {
		return "", ErrNotFound
	}
	return secret, nil
}

func (m *memoryBackend) Delete(account string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.secrets, account)
	return nil
}

// writeFileAtomic writes to a temporary file and renames it into place.
//
// A crash halfway through writing credentials or their metadata would otherwise leave a truncated
// file, and the recovery from that is "your keys are gone", which is not a recovery.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("creating a temporary file: %w", err)
	}
	tmpName := tmp.Name()

	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("setting permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("writing: %w", err)
	}
	// Flush to disk before renaming. Without this a crash can leave the rename visible while the
	// contents are not, which is the same lost-credentials outcome by a slower route.
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("syncing: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
