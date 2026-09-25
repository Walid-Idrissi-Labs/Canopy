package chat

// "!command": run it in the project as you would in a terminal, see what it said, and have that go
// with your next message, so "!go test ./..." then "why does this fail" needs no pasting.

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// ShellResult is what a command run from the box did.
type ShellResult struct {
	Output   string
	ExitCode int
	// Failed says it could not be run at all, or did not finish.
	Failed string
}

// shellDoneMsg carries a finished command back into the update loop.
type shellDoneMsg struct {
	command string
	result  ShellResult
}

// maxShellContext is how much of a command's output goes with the next message.
const maxShellContext = 16 << 10

// SetShell gives the box a way to run "!command" in the project. Nil makes "!" an ordinary message.
func (m *Model) SetShell(run func(ctx context.Context, command string) ShellResult) { m.shell = run }

// runShell starts a command typed after "!".
func (m Model) runShell(command string) (Model, tea.Cmd) {
	run := m.shell
	m.notice = "running " + command
	return m, func() tea.Msg {
		return shellDoneMsg{command: command, result: run(context.Background(), command)}
	}
}

// shellDone shows what a command said and keeps it for the next message.
func (m Model) shellDone(msg shellDoneMsg) Model {
	r := msg.result
	if r.Failed != "" {
		m.err = msg.command + ": " + r.Failed
		return m
	}
	output := terminalSafe(strings.TrimRight(r.Output, "\n"))
	if len(output) > maxShellContext {
		output = output[len(output)-maxShellContext:]
	}
	m.shellContext = append(m.shellContext, shellRun{command: msg.command, exit: r.ExitCode, output: output})
	lines := strings.Split(output, "\n")
	shown := lines
	if len(shown) > 12 {
		shown = shown[len(shown)-12:]
	}
	m.notice = "$ " + msg.command + "  (exit " + itoa(r.ExitCode) + ", goes with your next message)\n" +
		strings.Join(shown, "\n")
	return m
}

type shellRun struct {
	command string
	exit    int
	output  string
}

// withShellContext puts the commands run since the last message ahead of the next one, framed as
// output rather than as instructions.
func (m Model) withShellContext(prompt string) string {
	if len(m.shellContext) == 0 {
		return prompt
	}
	var b strings.Builder
	for _, run := range m.shellContext {
		b.WriteString("<shell-output command=" + quoteAttr(run.command) + " exit=\"" + itoa(run.exit) + "\">\n")
		b.WriteString(strings.ReplaceAll(run.output, "</shell-output>", "</shell-output >"))
		b.WriteString("\n</shell-output>\n\n")
	}
	return b.String() + prompt
}

func quoteAttr(s string) string {
	return "\"" + strings.NewReplacer("\"", "&quot;", "\n", " ").Replace(s) + "\""
}
