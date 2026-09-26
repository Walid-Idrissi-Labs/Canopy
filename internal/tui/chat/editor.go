package chat

// ctrl+x ctrl+e: the message box in a real editor, for a message long enough to want one.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editedMsg is the box's text back from the editor.
type editedMsg struct {
	text string
	err  error
}

// editorShell is the editor to run on path, through the shell as git runs it, so an EDITOR with
// arguments or a quoted path works: VISUAL, then EDITOR, then vi.
func editorShell(path string) *exec.Cmd {
	editor := "vi"
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			editor = value
			break
		}
	}
	return exec.Command("/bin/sh", "-c", editor+` "$1"`, "canopy-editor", path)
}

// openEditor writes the box to a file only this user can read, hands the terminal to the editor on
// it, and reads it back when the editor exits.
func openEditor(text string) tea.Cmd {
	// A directory of its own, made fresh with a name nobody could have guessed and only this user
	// able to open, so on a shared /tmp nobody else can put a file, or a link, where it goes.
	dir, err := os.MkdirTemp("", "canopy-message-")
	if err != nil {
		return func() tea.Msg { return editedMsg{err: err} }
	}
	file, err := os.CreateTemp(dir, "message-*.md")
	if err != nil {
		return func() tea.Msg { return editedMsg{err: err} }
	}
	path := file.Name()
	_, err = file.WriteString(text)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return func() tea.Msg { return editedMsg{err: err} }
	}
	return tea.ExecProcess(editorShell(path), editorFinished(path))
}

// editorFinished reads the edited file back and removes it, whether or not the editor succeeded.
func editorFinished(path string) func(error) tea.Msg {
	return func(runErr error) tea.Msg {
		defer func() { _ = os.RemoveAll(filepath.Dir(path)) }()
		if runErr != nil {
			return editedMsg{err: runErr}
		}
		data, err := os.ReadFile(path)
		return editedMsg{text: strings.TrimRight(string(data), "\n"), err: err}
	}
}
