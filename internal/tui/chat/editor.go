package chat

// ctrl+x ctrl+e: the message box in a real editor, for a message long enough to want one.

import (
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editedMsg is the box's text back from the editor.
type editedMsg struct {
	text string
	err  error
}

// editorCommand is the editor to run, as the shell would choose it: VISUAL, then EDITOR, then vi.
func editorCommand() []string {
	for _, name := range []string{"VISUAL", "EDITOR"} {
		if fields := strings.Fields(os.Getenv(name)); len(fields) > 0 {
			return fields
		}
	}
	return []string{"vi"}
}

// openEditor writes the box to a file only this user can read, hands the terminal to the editor on
// it, and reads it back when the editor exits.
func openEditor(text string) tea.Cmd {
	file, err := os.CreateTemp("", "canopy-message-*.md")
	if err != nil {
		return func() tea.Msg { return editedMsg{err: err} }
	}
	path := file.Name()
	_, err = file.WriteString(text)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return func() tea.Msg { return editedMsg{err: err} }
	}
	argv := append(editorCommand(), path)
	cmd := exec.Command(argv[0], argv[1:]...)
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		defer func() { _ = os.Remove(path) }()
		if runErr != nil {
			return editedMsg{err: runErr}
		}
		data, err := os.ReadFile(path)
		return editedMsg{text: strings.TrimRight(string(data), "\n"), err: err}
	})
}
