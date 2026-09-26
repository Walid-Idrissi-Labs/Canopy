package chat

// "!command": run it in the project as you would in a terminal, see what it said, and have that go
// with your next message, so "!go test ./..." then "why does this fail" needs no pasting.

import (
	"context"
	"strings"
	"unicode/utf8"

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
		cut := len(output) - maxShellContext
		for cut < len(output) && !utf8.RuneStart(output[cut]) {
			cut++
		}
		output = output[cut:]
	}
	m.shellContext = append(m.shellContext, shellRun{command: msg.command, exit: r.ExitCode, output: output})
	// All the output waiting for the next message together is held to the same bound, oldest
	// dropped first.
	for total := contextSize(m.shellContext); total > maxShellContext*2 && len(m.shellContext) > 1; total = contextSize(m.shellContext) {
		m.shellContext = m.shellContext[1:]
	}
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
		// Angle brackets written as entities, so nothing in the output can close the frame, or open
		// one of its own, however it is spelled.
		b.WriteString(strings.NewReplacer("<", "&lt;", ">", "&gt;").Replace(run.output))
		b.WriteString("\n</shell-output>\n\n")
	}
	return b.String() + prompt
}

func quoteAttr(s string) string {
	return "\"" + strings.NewReplacer("\"", "&quot;", "\n", " ").Replace(s) + "\""
}

func contextSize(runs []shellRun) int {
	total := 0
	for _, run := range runs {
		total += len(run.output)
	}
	return total
}
