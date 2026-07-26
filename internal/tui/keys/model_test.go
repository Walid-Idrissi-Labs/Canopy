package keys

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

const canary = "sk-ant-api03-TYPED-INTO-THE-TUI-MUST-NOT-RENDER"

var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiCodes.ReplaceAllString(s, "") }

// stubStore records what it was asked to do, without any keychain involved.
type stubStore struct {
	keys     []core.KeyMetadata
	putErr   error
	lastPut  core.KeyMetadata
	lastSeen core.Secret
	insecure bool
}

func (s *stubStore) List() ([]core.KeyMetadata, error) { return s.keys, nil }

func (s *stubStore) Put(meta core.KeyMetadata, secret core.Secret) (core.KeyMetadata, error) {
	if s.putErr != nil {
		return core.KeyMetadata{}, s.putErr
	}
	meta.Fingerprint = secret.Fingerprint()
	meta.CreatedAt = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	s.lastPut, s.lastSeen = meta, secret
	s.keys = append(s.keys, meta)
	return meta, nil
}

func (s *stubStore) Remove(ref core.KeyRef) error {
	remaining := s.keys[:0]
	for _, k := range s.keys {
		if k.Ref.Name != ref.Name {
			remaining = append(remaining, k)
		}
	}
	s.keys = remaining
	return nil
}

func (s *stubStore) BackendName() string        { return "test-backend" }
func (s *stubStore) UsingInsecureBackend() bool { return s.insecure }

func typeRunes(m Model, text string) Model {
	for _, r := range text {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func press(m Model, t tea.KeyType) Model {
	m, _ = m.Update(tea.KeyMsg{Type: t})
	return m
}

func key(m Model, s string) Model {
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return m
}

// The A1-05 acceptance criterion: a credential can be added end to end without leaving the
// interface, and never appears in a rendered frame while it is being typed.
func TestAddACredentialWithoutLeavingTheInterface(t *testing.T) {
	store := &stubStore{}
	m := New(store)

	if !m.IsEmpty() {
		t.Fatal("a fresh store has no credentials")
	}

	m = key(m, "a")
	m = typeRunes(m, "claude")
	m = press(m, tea.KeyEnter)

	// Provider list, anthropic is first.
	m = press(m, tea.KeyEnter)

	// Now the secret, typed a character at a time. Every intermediate frame is checked, because
	// the leak that matters is the one visible mid keystroke, not the one at the end.
	for _, r := range canary {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		assertNoCanary(t, "while typing", m.View())
	}
	m = press(m, tea.KeyEnter)

	if store.lastPut.Ref.Name != "claude" {
		t.Errorf("stored name = %q, want claude", store.lastPut.Ref.Name)
	}
	if store.lastPut.Ref.Provider != core.ProviderAnthropic {
		t.Errorf("stored provider = %q", store.lastPut.Ref.Provider)
	}
	if store.lastSeen.Reveal() != canary {
		t.Error("the store did not receive the typed value")
	}
	if m.IsEmpty() {
		t.Error("the credential should now be listed")
	}
	assertNoCanary(t, "after storing", m.View())
}

// The typed value must not survive in the model once it has been handed over, so that later code
// rendering this screen has nothing to accidentally print.
func TestTypedSecretIsClearedAfterStoring(t *testing.T) {
	store := &stubStore{}
	m := New(store)

	m = key(m, "a")
	m = typeRunes(m, "claude")
	m = press(m, tea.KeyEnter)
	m = press(m, tea.KeyEnter)
	m = typeRunes(m, canary)
	m = press(m, tea.KeyEnter)

	if m.draftSecret != "" {
		t.Error("the typed credential is still held in the model after being stored")
	}
}

// Also cleared when the store rejects it. Leaving it behind on the error path is the easy mistake,
// since the obvious implementation only clears on success.
func TestTypedSecretIsClearedEvenWhenStoringFails(t *testing.T) {
	store := &stubStore{putErr: errFake}
	m := New(store)

	m = key(m, "a")
	m = typeRunes(m, "claude")
	m = press(m, tea.KeyEnter)
	m = press(m, tea.KeyEnter)
	m = typeRunes(m, canary)
	m = press(m, tea.KeyEnter)

	if m.draftSecret != "" {
		t.Error("the typed credential survived a failed store")
	}
	assertNoCanary(t, "after a failed store", m.View())
	if m.err == nil {
		t.Error("the failure should be visible")
	}
}

func TestEscapeClearsTheDraft(t *testing.T) {
	m := New(&stubStore{})

	m = key(m, "a")
	m = typeRunes(m, "claude")
	m = press(m, tea.KeyEnter)
	m = press(m, tea.KeyEnter)
	m = typeRunes(m, canary)
	m = press(m, tea.KeyEsc)

	if m.draftSecret != "" {
		t.Error("cancelling left the typed credential in the model")
	}
	if m.Adding() {
		t.Error("cancelling should return to the list")
	}
	assertNoCanary(t, "after cancelling", m.View())
}

func TestOnlyTheLengthOfTheSecretIsShown(t *testing.T) {
	m := New(&stubStore{})
	m = key(m, "a")
	m = typeRunes(m, "claude")
	m = press(m, tea.KeyEnter)
	m = press(m, tea.KeyEnter)
	m = typeRunes(m, "abcde")

	view := plain(m.View())
	if !strings.Contains(view, "*****") {
		t.Errorf("five typed characters should show as five dots:\n%s", view)
	}
	if strings.Contains(view, "abcde") {
		t.Errorf("the typed value is visible:\n%s", view)
	}
}

func TestInvalidNameIsRejectedInPlace(t *testing.T) {
	m := New(&stubStore{})

	m = key(m, "a")
	m = typeRunes(m, "Not A Valid Name")
	m = press(m, tea.KeyEnter)

	if m.err == nil {
		t.Fatal("an invalid name should be rejected")
	}
	if m.mode != modeName {
		t.Error("rejection should stay on the name field rather than advancing")
	}
}

// Pasting a credential into the name field is the mistake worth catching, and the error must not
// echo what was pasted.
func TestCredentialPastedIntoTheNameFieldIsRefusedWithoutEchoing(t *testing.T) {
	m := New(&stubStore{})

	m = key(m, "a")
	m = typeRunes(m, canary)
	m = press(m, tea.KeyEnter)

	if m.err == nil {
		t.Fatal("a credential used as a name should be rejected")
	}
	if !strings.Contains(m.err.Error(), "looks like a credential") {
		t.Errorf("the error should explain what happened, got: %v", m.err)
	}
	assertNoCanary(t, "name rejection", m.View())
	assertNoCanary(t, "name rejection error", m.err.Error())
}

func TestBaseURLRequestedOnlyWhenNeeded(t *testing.T) {
	store := &stubStore{}
	m := New(store)

	m = key(m, "a")
	m = typeRunes(m, "kimi")
	m = press(m, tea.KeyEnter)

	// Move to openai-compatible, which needs an endpoint.
	m = key(m, "j")
	m = press(m, tea.KeyEnter)

	if m.mode != modeBaseURL {
		t.Fatalf("openai-compatible should ask for a base URL, mode = %v", m.mode)
	}

	// An empty endpoint is refused rather than stored as blank.
	m = press(m, tea.KeyEnter)
	if m.err == nil {
		t.Error("an empty base URL should be refused")
	}

	m = typeRunes(m, "https://example.invalid/v1")
	m = press(m, tea.KeyEnter)
	if m.mode != modeSecret {
		t.Fatalf("mode after a base URL = %v, want the secret prompt", m.mode)
	}

	m = typeRunes(m, canary)
	m = press(m, tea.KeyEnter)

	if store.lastPut.BaseURL != "https://example.invalid/v1" {
		t.Errorf("BaseURL = %q", store.lastPut.BaseURL)
	}
}

func TestRemoveConfirmsFirst(t *testing.T) {
	store := &stubStore{keys: []core.KeyMetadata{
		{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
	}}
	m := New(store)

	m = key(m, "d")
	if m.mode != modeConfirmRemove {
		t.Fatal("removal should ask first")
	}
	if !strings.Contains(plain(m.View()), "Remove") {
		t.Error("the confirmation should be visible")
	}

	m = key(m, "n")
	if len(store.keys) != 1 {
		t.Error("declining should not remove anything")
	}

	m = key(m, "d")
	m = key(m, "y")
	if len(store.keys) != 0 {
		t.Error("confirming should remove")
	}
}

// The empty state is the first thing a new user sees, so it has to say what to do rather than
// showing an empty table.
func TestEmptyStateExplainsWhatToDo(t *testing.T) {
	view := plain(New(&stubStore{}).View())

	if !strings.Contains(view, "No credentials yet") {
		t.Errorf("the empty state should say so:\n%s", view)
	}
	if !strings.Contains(view, "Press a to add one") {
		t.Errorf("the empty state should say what to do next:\n%s", view)
	}
	if !strings.Contains(view, "canopy keys add") {
		t.Errorf("the empty state should mention the command line path too:\n%s", view)
	}
}

func TestInsecureBackendIsVisibleOnEveryFrame(t *testing.T) {
	m := New(&stubStore{insecure: true})
	view := plain(m.View())
	if !strings.Contains(view, "unencrypted") {
		t.Errorf("the insecure backend should be called out:\n%s", view)
	}

	// Still visible mid add, not only on the list.
	m = key(m, "a")
	if !strings.Contains(plain(m.View()), "unencrypted") {
		t.Error("the warning should persist while adding")
	}
}

func TestListNeverShowsAValue(t *testing.T) {
	store := &stubStore{}
	m := New(store)

	m = key(m, "a")
	m = typeRunes(m, "claude")
	m = press(m, tea.KeyEnter)
	m = press(m, tea.KeyEnter)
	m = typeRunes(m, canary)
	m = press(m, tea.KeyEnter)

	view := plain(m.View())
	assertNoCanary(t, "list", view)
	if !strings.Contains(view, core.NewSecret(canary).Fingerprint()) {
		t.Error("the list should show a fingerprint so keys can be told apart")
	}
}

func assertNoCanary(t *testing.T, surface, text string) {
	t.Helper()
	got := plain(text)
	for _, fragment := range []string{canary, "TYPED-INTO-THE-TUI", "sk-ant-api03"} {
		if strings.Contains(got, fragment) {
			t.Fatalf("%s leaked %q:\n%s", surface, fragment, got)
		}
	}
}

var errFake = fakeError("the store said no")

type fakeError string

func (e fakeError) Error() string { return string(e) }
