package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstructionsAreGatheredInPrecedenceOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	write := func(name, body string) {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("AGENTS.md", "use tabs")
	write("CLAUDE.md", "run make test")
	got, err := LoadInstructions(dir, Project{Instructions: "no em dashes"})
	if err != nil {
		t.Fatal(err)
	}
	a, c, j := strings.Index(got.Text, "use tabs"), strings.Index(got.Text, "run make test"), strings.Index(got.Text, "no em dashes")
	if a < 0 || c < 0 || j < 0 || a >= c || c >= j {
		t.Fatalf("instructions are missing or out of order:\n%s", got.Text)
	}
	if len(got.Sources) != 3 {
		t.Fatalf("sources = %+v, want three named sources", got.Sources)
	}
}

// Oversized instructions are refused by name rather than cut: half a rule book is a different rule
// book.
func TestOversizedInstructionsAreRefusedNotTruncated(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(strings.Repeat("x", MaxInstructionBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadInstructions(dir, Project{})
	if !errors.Is(err, ErrInstructionsTooLarge) || got.Text != "" {
		t.Fatalf("an oversized file was not refused: %v, %d bytes kept", err, len(got.Text))
	}
	if !strings.Contains(err.Error(), "AGENTS.md") {
		t.Fatalf("the error does not name the file: %v", err)
	}
}

// A repository cannot send a file from outside itself by making an instruction file a symlink.
func TestAnInstructionFileSymlinkIsNotFollowed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("SECRET-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	got, err := LoadInstructions(dir, Project{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Text, "SECRET-CONTENT") || len(InstructionFiles(dir)) != 0 {
		t.Fatalf("a symlinked instruction file was followed: %q", got.Text)
	}
}
