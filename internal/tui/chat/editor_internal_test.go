package chat

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What was written comes back, and the file is removed either way.
func TestTheEditedFileIsReadAndRemoved(t *testing.T) {
	dir := t.TempDir()
	for _, runErr := range []error{nil, errors.New("editor exited 1")} {
		path := filepath.Join(dir, "message.md")
		if err := os.WriteFile(path, []byte("a longer message\n\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		msg := editorFinished(path)(runErr).(editedMsg)
		if runErr == nil && (msg.err != nil || msg.text != "a longer message") {
			t.Fatalf("%+v", msg)
		}
		if runErr != nil && msg.err == nil {
			t.Fatal("a failed editor was taken as an edit")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("the edited file was left behind")
		}
	}
}

// EDITOR can carry arguments and the path can carry spaces, as git allows.
func TestTheEditorRunsThroughTheShell(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", `sh -c 'printf edited > "$1"' --`)
	path := filepath.Join(t.TempDir(), "with space.md")
	if err := editorShell(path).Run(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.TrimSpace(string(data)) != "edited" {
		t.Fatalf("the editor wrote %q", data)
	}
}
