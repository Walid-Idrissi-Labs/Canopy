package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
)

// The plural listing is the one that answers "what could this key run", and for an Anthropic key the
// answer has to be there before anybody has set anything up.
func TestListingModelsShowsTheCatalogAndWhatWasAdded(t *testing.T) {
	storeWithCanary(t)

	var out bytes.Buffer
	if err := runKeys([]string{"models", "claude"}, &out); err != nil {
		t.Fatalf("keys models: %v", err)
	}
	listing := out.String()
	for _, want := range []string{"claude-sonnet-5", "Claude Sonnet 5", "catalog", "last checked"} {
		if !strings.Contains(listing, want) {
			t.Errorf("the listing is missing %q:\n%s", want, listing)
		}
	}
	assertClean(t, "keys models", listing)

	// Adding is not selecting, and the output says so rather than leaving somebody to find out that
	// their next conversation is still on the old model.
	out.Reset()
	if err := runKeys([]string{"models", "add", "claude", "claude-something-unreleased", "The New One"}, &out); err != nil {
		t.Fatalf("keys models add: %v", err)
	}
	if !strings.Contains(out.String(), "canopy keys model claude") {
		t.Errorf("adding did not say how to select it:\n%s", out.String())
	}

	out.Reset()
	if err := runKeys([]string{"models", "claude"}, &out); err != nil {
		t.Fatalf("keys models after adding: %v", err)
	}
	listing = out.String()
	// The id is kept and the label is shown beside it, and the two sources stay told apart.
	for _, want := range []string{"claude-something-unreleased", "The New One", "added"} {
		if !strings.Contains(listing, want) {
			t.Errorf("the listing is missing %q:\n%s", want, listing)
		}
	}

	out.Reset()
	if err := runKeys([]string{"models", "remove", "claude", "claude-something-unreleased"}, &out); err != nil {
		t.Fatalf("keys models remove: %v", err)
	}
	out.Reset()
	if err := runKeys([]string{"models", "claude"}, &out); err != nil {
		t.Fatalf("keys models after removing: %v", err)
	}
	if strings.Contains(out.String(), "claude-something-unreleased") {
		t.Errorf("a removed model is still offered:\n%s", out.String())
	}
}

// The model a key actually talks to is marked, because a list of possibilities cannot say for itself
// which one is in force, and that is the fact somebody running this usually wants.
func TestTheListingMarksTheModelTheKeyTalksTo(t *testing.T) {
	storeWithCanary(t)

	if err := runKeys([]string{"model", "claude", "claude-sonnet-4-6"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("keys model: %v", err)
	}

	var out bytes.Buffer
	if err := runKeys([]string{"models", "claude"}, &out); err != nil {
		t.Fatalf("keys models: %v", err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "claude-sonnet-4-6") && strings.HasPrefix(line, "*") {
			return
		}
	}
	t.Errorf("the selected model is not marked:\n%s", out.String())
}

// Usage errors are worth their own test because this family has three shapes and the wrong one is
// otherwise reported as a key nobody can find.
func TestModelsRefusesWhatItCannotDo(t *testing.T) {
	storeWithCanary(t)

	for name, args := range map[string][]string{
		"no name":      {"models"},
		"add no id":    {"models", "add", "claude"},
		"remove no id": {"models", "remove", "claude"},
		"unknown key":  {"models", "nope"},
	} {
		var out bytes.Buffer
		if err := runKeys(args, &out); err == nil {
			t.Errorf("%s was accepted:\n%s", name, out.String())
		}
	}
}

// The listing says when the catalog was checked, and says out loud when that was long enough ago to
// matter. Without the second half the date is a number somebody has to subtract from today.
func TestTheModelListingSaysWhenTheCatalogHasGoneStale(t *testing.T) {
	storeWithCanary(t)

	fresh := catalog.AsOf
	t.Cleanup(func() { catalog.AsOf = fresh })

	var out bytes.Buffer
	if err := runKeys([]string{"models", "claude"}, &out); err != nil {
		t.Fatalf("keys models: %v", err)
	}
	if strings.Contains(out.String(), "may be missing models") {
		t.Errorf("a fresh catalog called itself stale:\n%s", out.String())
	}

	catalog.AsOf = time.Now().Add(-2 * catalog.MaxAge)
	out.Reset()
	if err := runKeys([]string{"models", "claude"}, &out); err != nil {
		t.Fatalf("keys models with a stale catalog: %v", err)
	}
	listing := out.String()
	if !strings.Contains(listing, "may be missing models") {
		t.Errorf("a stale catalog said nothing about it:\n%s", listing)
	}
	// And it still lists what it knows. Stale is a caveat, never a refusal.
	if !strings.Contains(listing, "claude-sonnet-5") {
		t.Errorf("a stale catalog stopped offering anything:\n%s", listing)
	}
}

// Renaming from the command line moves the credential and the conversations recorded on it. A
// rename that stopped at the key store would leave every one of them pointing at a name nothing
// answers to, and the failure would arrive one message later from somebody else's gateway.
func TestRenamingAKeyMovesItAndTheConversationsOnIt(t *testing.T) {
	store := storeWithCanary(t)
	t.Setenv(session.PathEnvVar, filepath.Join(t.TempDir(), "history.db"))

	// Seeded through the same path the command will use, so the test is about the command rather
	// than about which file it opened.
	path, err := session.DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	history, err := session.OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	if err := history.SaveSession(core.Session{
		ID: "session-1", KeyName: "claude", Model: "claude-opus-5"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	_ = history.Close()

	var out bytes.Buffer
	if err := runKeys([]string{"rename", "claude", "anthropic"}, &out); err != nil {
		t.Fatalf("keys rename: %v", err)
	}
	said := out.String()
	for _, want := range []string{"now called", "anthropic", "conversation"} {
		if !strings.Contains(said, want) {
			t.Errorf("the output is missing %q:\n%s", want, said)
		}
	}
	// And says what it cannot reach, which is a Canopy already running beside it holding the same
	// conversations in memory.
	if !strings.Contains(said, "restart") {
		t.Errorf("the output does not say a running Canopy will not follow:\n%s", said)
	}
	assertClean(t, "keys rename", said)

	// The credential moved, value and all, without ever being asked for again.
	secret, err := store.Get(core.KeyRef{Name: "anthropic"})
	if err != nil {
		t.Fatalf("the credential is not readable under its new name: %v", err)
	}
	if secret.Reveal() != canary {
		t.Error("the value changed in the move")
	}

	// And the conversation followed.
	reopened, err := session.OpenStorage(path)
	if err != nil {
		t.Fatalf("OpenStorage: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	loaded, err := reopened.Load("session-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.KeyName != "anthropic" {
		t.Errorf("the stored conversation still names %q", loaded.KeyName)
	}
}

// The value is never asked for and the arguments are two names, so there is nothing here that could
// put a credential into shell history. Worth a test because the whole `keys` command is built around
// that rule.
func TestRenamingAKeyRefusesWithoutTwoNames(t *testing.T) {
	storeWithCanary(t)

	for _, args := range [][]string{{"rename"}, {"rename", "claude"}, {"rename", "a", "b", "c"}} {
		var out bytes.Buffer
		if err := runKeys(args, &out); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
}
