package keys

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

const canary = "sk-ant-api03-TYPED-INTO-THE-TUI-MUST-NOT-RENDER"

var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiCodes.ReplaceAllString(s, "") }

// stubStore records what it was asked to do, without any keychain involved.
type stubStore struct {
	keys     []core.KeyMetadata
	added    map[string][]catalog.Model
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
	if m.mode != modeModel {
		t.Fatalf("mode after a base URL = %v, want the model prompt", m.mode)
	}

	// A credential with no model on a provider that has no default cannot answer a single message,
	// and the far end reports it as a bad request rather than as a missing setting. So it is refused
	// here, where the person can still do something about it.
	m = press(m, tea.KeyEnter)
	if m.err == nil {
		t.Error("an openai-compatible credential with no model should be refused")
	}
	if m.mode != modeModel {
		t.Errorf("the refusal moved on anyway, mode = %v", m.mode)
	}

	m = typeRunes(m, "some/model-v1")
	m = press(m, tea.KeyEnter)
	if m.mode != modeSecret {
		t.Fatalf("mode after a model = %v, want the secret prompt", m.mode)
	}

	m = typeRunes(m, canary)
	m = press(m, tea.KeyEnter)

	if store.lastPut.BaseURL != "https://example.invalid/v1" {
		t.Errorf("BaseURL = %q", store.lastPut.BaseURL)
	}
	if store.lastPut.Model != "some/model-v1" {
		t.Errorf("Model = %q, so the credential was stored unable to answer anything", store.lastPut.Model)
	}
}

// The list was somewhere to add and remove credentials and nowhere to pick one. With two stored and
// no way to choose, nothing could run at all.
func TestACredentialCanBeChosenAndItsModelChanged(t *testing.T) {
	store := &stubStore{keys: []core.KeyMetadata{
		{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
		{Ref: core.KeyRef{Name: "nim", Provider: core.ProviderOpenAICompatible}, Model: "old/model"},
	}}
	m := New(store)

	if _, picked := m.Chosen(); picked {
		t.Error("a credential was chosen before anybody chose one")
	}

	m = key(m, "j")
	m = press(m, tea.KeyEnter)

	chosen, picked := m.Chosen()
	if !picked || chosen != "nim" {
		t.Fatalf("Chosen() = %q %v", chosen, picked)
	}

	// Changing the model must not require the secret again. Somebody fixing a typo in a model id
	// should not have to go and find their API key.
	m = key(m, "m")
	if m.mode != modeModelPick {
		t.Fatalf("m on the list landed on mode %v", m.mode)
	}
	// This key points at an endpoint nobody here has a lineup for, so the only row is the one that
	// takes a typed id, and it is where the cursor already is.
	m = press(m, tea.KeyEnter)
	if m.mode != modeModel {
		t.Fatalf("the typed row landed on mode %v", m.mode)
	}
	m = typeRunes(m, "new/model")
	m = press(m, tea.KeyEnter)

	if m.mode != modeList {
		t.Errorf("changing a model landed on mode %v, want back at the list", m.mode)
	}
	if m.ModelFor("nim") != "new/model" {
		t.Errorf("the model is %q after the change", m.ModelFor("nim"))
	}
	if store.lastPut.Ref.Name != "" {
		t.Error("changing a model went through Put, which would need the secret again")
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

func (s *stubStore) SetModel(ref core.KeyRef, model string) error {
	for i := range s.keys {
		if s.keys[i].Ref.Name == ref.Name {
			s.keys[i].Model = model
			return nil
		}
	}
	return errors.New("no such credential")
}

func (s *stubStore) Models(ref core.KeyRef) ([]catalog.Model, error) {
	return s.added[ref.Name], nil
}

func (s *stubStore) AddModel(ref core.KeyRef, id, name string) error {
	if s.added == nil {
		s.added = map[string][]catalog.Model{}
	}
	s.added[ref.Name] = append(s.added[ref.Name], catalog.Model{ID: id, Name: name})
	return nil
}

// The credential screen carried its own four colours and therefore ignored the selected theme
// entirely, which is the same bug that was found in internal/tui/styles.go. Two copies of one
// mistake in one tree is the argument for asserting it per package rather than once centrally:
// nothing about finding the first would have led anybody to the second.
func TestTheCredentialScreenFollowsTheSelectedTheme(t *testing.T) {
	key := func(style lipgloss.Style) string {
		fg := style.GetForeground()
		if fg == nil {
			return "none"
		}
		// The value the style holds, not what it renders. Under go test lipgloss finds no terminal,
		// renders with the styling stripped and resolves every adaptive colour to black, so both of
		// the obvious comparisons would compare two identical things and pass regardless.
		return fmt.Sprintf("%#v", fg)
	}

	for _, tc := range []struct {
		name  string
		style themed
	}{
		{"muted", styleMuted}, {"error", styleErr},
		{"ok", styleOK}, {"warning", styleWarn},
	} {
		theme.Set(theme.Default)
		coloured := key(tc.style())
		theme.Set(theme.Monochrome)
		mono := key(tc.style())
		theme.Set(theme.Default)

		if coloured == mono {
			t.Errorf("%s is the same colour under both themes, so it is not themed", tc.name)
		}
	}
}

// An anthropic key can be pointed somewhere else with no setup at all, which is the whole argument
// for shipping a list rather than asking everybody to remember model ids.
func TestTheModelKeyOffersTheCatalogBeforeTheKeyboard(t *testing.T) {
	store := &stubStore{keys: []core.KeyMetadata{
		{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}, Model: "claude-opus-5"},
	}}
	m := New(store)

	m = key(m, "m")
	if m.mode != modeModelPick {
		t.Fatalf("m landed on mode %v, want the list", m.mode)
	}

	view := plain(m.View())
	for _, want := range []string{"claude-sonnet-5", "claude-haiku-4-5", "something else, type it"} {
		if !strings.Contains(view, want) {
			t.Errorf("the list is missing %q:\n%s", want, view)
		}
	}
	// The current model is marked with a character, not only with a colour, so the mark survives a
	// terminal with no colour in it. D-10.
	if !strings.Contains(view, "* Claude Opus 5") {
		t.Errorf("the current model is not marked:\n%s", view)
	}

	// It opens on what it is already set to, so moving one row and taking it is a deliberate change.
	m = key(m, "j")
	m = press(m, tea.KeyEnter)

	if m.mode != modeList {
		t.Errorf("taking a model landed on mode %v, want back at the list", m.mode)
	}
	if got := m.ModelFor("claude"); got != "claude-opus-4-8" {
		t.Errorf("the model is %q after picking the row under the current one", got)
	}
	if store.lastPut.Ref.Name != "" {
		t.Error("picking a model went through Put, which would need the secret again")
	}
}

// The catalog is a convenience and never a gate. The day the list is wrong is the day it would stand
// between somebody and the one model they actually want, so the typed id has to keep working.
func TestAModelOnNoListCanStillBeTyped(t *testing.T) {
	store := &stubStore{keys: []core.KeyMetadata{
		{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}, Model: "claude-opus-5"},
	}}
	m := New(store)

	m = key(m, "m")
	for range len(m.modelChoices) {
		m = key(m, "j")
	}
	m = press(m, tea.KeyEnter)
	if m.mode != modeModel {
		t.Fatalf("the last row landed on mode %v, want the text field", m.mode)
	}

	m = typeRunes(m, "claude-something-unreleased")
	m = press(m, tea.KeyEnter)

	if got := m.ModelFor("claude"); got != "claude-something-unreleased" {
		t.Errorf("the typed model was not stored, the key talks to %q", got)
	}
	// And it joins the list, so the second time it is a row rather than something to retype exactly.
	added := store.added["claude"]
	if len(added) != 1 || added[0].ID != "claude-something-unreleased" {
		t.Errorf("a typed model was not remembered: %+v", added)
	}
}

// An endpoint nobody here has a lineup for offers only what its owner added, and says so when that
// is nothing. A one row list with no explanation reads as a program that has lost the rest.
func TestAKeyOnAnUnknownEndpointOffersOnlyWhatItsOwnerAdded(t *testing.T) {
	store := &stubStore{
		keys: []core.KeyMetadata{{
			Ref:     core.KeyRef{Name: "nim", Provider: core.ProviderOpenAICompatible},
			BaseURL: "https://api.moonshot.cn/v1",
			Model:   "moonshot-v1-8k",
		}},
	}
	m := New(store)

	m = key(m, "m")
	if view := plain(m.View()); !strings.Contains(view, "knows no models for this endpoint") {
		t.Errorf("an empty list did not say why it is empty:\n%s", view)
	}

	// With something added it is offered, and the name is shown beside the id rather than instead of
	// it, since the id is what goes on the wire.
	store.added = map[string][]catalog.Model{
		"nim": {{ID: "minimaxai/minimax-m2.7", Name: "MiniMax M2.7"}},
	}
	m = press(m, tea.KeyEsc)
	m = key(m, "m")

	view := plain(m.View())
	if !strings.Contains(view, "MiniMax M2.7") || !strings.Contains(view, "minimaxai/minimax-m2.7") {
		t.Errorf("the added model is not shown with both its name and its id:\n%s", view)
	}

	m = press(m, tea.KeyEnter)
	if got := m.ModelFor("nim"); got != "minimaxai/minimax-m2.7" {
		t.Errorf("picking the named entry stored %q, want the id", got)
	}
}

// Leaving a model edit must not leave the screen believing it is still in one, or the next
// credential added on a provider that asks for a model is stored as an edit of somebody else.
func TestLeavingAModelEditDoesNotPoisonTheNextAdd(t *testing.T) {
	store := &stubStore{keys: []core.KeyMetadata{
		{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}},
	}}
	m := New(store)

	m = key(m, "m")
	m = press(m, tea.KeyEsc)

	m = key(m, "a")
	m = typeRunes(m, "nim")
	m = press(m, tea.KeyEnter)
	m = key(m, "j")
	m = press(m, tea.KeyEnter)
	m = typeRunes(m, "https://api.moonshot.cn/v1")
	m = press(m, tea.KeyEnter)
	m = typeRunes(m, "moonshot-v1-8k")
	m = press(m, tea.KeyEnter)

	if m.mode != modeSecret {
		t.Fatalf("the add flow landed on mode %v after the model, want the secret prompt", m.mode)
	}
	m = typeRunes(m, canary)
	m = press(m, tea.KeyEnter)

	if store.lastPut.Ref.Name != "nim" {
		t.Errorf("the credential was not stored, Put saw %q", store.lastPut.Ref.Name)
	}
}

// The picker says when the list it is offering was last checked, and says out loud when that was
// long enough ago that the row underneath, the one that takes anything typed, is the row that
// matters.
func TestThePickerSaysWhenTheCatalogHasGoneStale(t *testing.T) {
	fresh := catalog.AsOf
	t.Cleanup(func() { catalog.AsOf = fresh })

	store := &stubStore{keys: []core.KeyMetadata{
		{Ref: core.KeyRef{Name: "claude", Provider: core.ProviderAnthropic}, Model: "claude-opus-5"},
	}}
	m := key(New(store), "m")

	if view := plain(m.View()); strings.Contains(view, "may be missing models") {
		t.Errorf("a fresh list called itself stale:\n%s", view)
	}

	catalog.AsOf = time.Now().Add(-2 * catalog.MaxAge)
	m = key(New(store), "m")

	view := plain(m.View())
	if !strings.Contains(view, "may be missing models") {
		t.Errorf("a stale list said nothing about it:\n%s", view)
	}
	// Still offered, because stale is a caveat and never a gate.
	if !strings.Contains(view, "claude-sonnet-5") {
		t.Errorf("a stale list stopped offering anything:\n%s", view)
	}
}
