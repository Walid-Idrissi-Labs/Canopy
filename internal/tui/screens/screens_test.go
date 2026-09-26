package screens

import (
	"os"
	"path/filepath"
	"testing"
)

// A frame is kept, colour and all, only when a directory is named.
func TestAFrameIsKeptOnlyWhenAsked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(DirEnvVar, "")
	if err := Keep("first", "\x1b[32mhi\x1b[0m"); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("a frame was written with no directory named")
	}
	t.Setenv(DirEnvVar, dir)
	if err := Keep("first", "\x1b[32mhi\x1b[0m"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "first.ansi"))
	if err != nil || string(got) != "\x1b[32mhi\x1b[0m" {
		t.Fatalf("kept %q, %v", got, err)
	}
}
