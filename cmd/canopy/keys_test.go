package main

import (
	"bytes"
	"strings"
	"testing"
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
