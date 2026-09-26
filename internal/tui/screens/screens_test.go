package screens

import (
	"os"
	"path/filepath"
	"testing"
)

// A frame is kept, colour and all, only when a directory is named, under the name of the package
// that drew it; with none named, nothing anywhere is written.
func TestAFrameIsKeptOnlyWhenAsked(t *testing.T) {
	// Every place a stray write could land, made fresh and watched.
	elsewhere := t.TempDir()
	t.Setenv("HOME", elsewhere)
	t.Setenv("TMPDIR", elsewhere)
	wd, _ := os.Getwd()
	t.Chdir(elsewhere)
	defer t.Chdir(wd)
	t.Setenv(DirEnvVar, "")
	if err := Keep("chat", "first", "\x1b[32mhi\x1b[0m"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("a frame was written with no directory named: %v", entries)
	}
	dir := filepath.Join(t.TempDir(), "frames")
	t.Setenv(DirEnvVar, dir)
	if err := Keep("chat", "first", "\x1b[32mhi\x1b[0m"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "chat-first.ansi"))
	if err != nil || string(got) != "\x1b[32mhi\x1b[0m" {
		t.Fatalf("kept %q, %v", got, err)
	}
}
