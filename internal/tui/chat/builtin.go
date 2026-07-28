package chat

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
)

// The commands Canopy answers itself.
//
// The list and its descriptions live in internal/config, because the same list has to be reserved
// when a command file is read. What lives here is what each one does, which is the half that needs
// an engine and a screen.
//
// Most of them are also on a key. That is deliberate rather than redundant: a key is faster once you
// know it and undiscoverable until you do, and the slash menu is where somebody finds out the key
// exists. Two of them, undo and cost, are on no key at all and this is the only way to reach them.

// ActionMsg asks the screen above the chat for something a slash command named.
//
// A message rather than a method call. Which screen is in front has always been the application's
// decision, and a chat screen that could navigate would be the second place that decision was made.
type ActionMsg struct{ Action string }

// The actions the application answers.
const (
	ActionHelp   = "help"
	ActionNew    = "new"
	ActionAgents = "agents"
	ActionReview = "review"
	ActionKeys   = "keys"
)

// builtinInvocation reads a slash invocation, and whether it names a built-in.
//
// Two slashes are excluded here as everywhere else: it is the escape for a message that genuinely
// starts with one, and a person writing `//undo the migration` is talking about undoing, not asking
// Canopy to undo anything.
func builtinInvocation(input string) (name, arguments string, ok bool) {
	if !strings.HasPrefix(input, "/") || strings.HasPrefix(input, "//") {
		return "", "", false
	}
	name, arguments, _ = strings.Cut(strings.TrimPrefix(input, "/"), " ")
	if !config.IsBuiltin(name) {
		return "", "", false
	}
	return name, strings.TrimSpace(arguments), true
}

// runBuiltin answers a built-in command, and reports whether the name was one.
//
// Built-ins are matched before the user's own commands rather than after, and a command file that
// tries to define one of these names is refused when it is read. Precedence would be the wrong
// answer here: somebody typing /undo wants their workspace back, and a repository quietly redefining
// that is the one kind of surprise worth forbidding outright.
func (m *Model) runBuiltin(name, arguments string) (bool, tea.Cmd) {
	if !config.IsBuiltin(name) {
		return false, nil
	}

	m.err = ""
	m.notice = ""

	switch name {
	case "commands":
		m.notice = commandListing(m.commands)

	case "compact":
		updated, cmd := m.compact()
		*m = updated
		return true, cmd

	case "cost":
		m.notice = m.spending()

	case "undo":
		return true, m.undoLastTurn()

	default:
		// The rest belong to the screen around this one, which owns navigation.
		return true, func() tea.Msg { return ActionMsg{Action: name} }
	}

	_ = arguments
	return true, nil
}

// spending is what this conversation has cost, said in the three states it can be in.
//
// The same honesty A8-08 required of the run report. A conversation on a provider with no published
// prices has a real token count and an unknown cost, and printing a zero there would be a figure
// somebody could budget against.
func (m Model) spending() string {
	usage := m.session.Usage()
	if usage.TotalTokens() == 0 {
		return "nothing yet, this conversation has not sent anything"
	}

	tokens := fmt.Sprintf("%d tokens in %d turns", usage.TotalTokens(), len(m.session.Turns))
	if !usage.CostKnown {
		return tokens + ", cost unknown because this provider does not publish prices"
	}
	return fmt.Sprintf("%s, $%.4f", tokens, usage.CostUSD)
}

// undoLastTurn puts the workspace back as it was before the last turn.
//
// The conversation is left alone. Undoing the files and deleting the exchange are different things
// somebody might want, and doing both on one command means the record of what was tried is gone
// along with the attempt, which is the part worth keeping when something did not work.
//
// Done off the update loop, like compaction, because restoring a checkpoint runs git against a
// whole worktree. It is usually quick and it is not guaranteed to be, and the frame that must never
// block is the one somebody is looking at while it happens.
func (m *Model) undoLastTurn() tea.Cmd {
	if len(m.session.Turns) == 0 {
		m.err = "there is nothing to undo in this conversation yet"
		return nil
	}

	engine, sessionID := m.engine, m.sessionID
	turnID := m.session.Turns[len(m.session.Turns)-1].ID
	m.notice = "putting the workspace back"

	return func() tea.Msg {
		return undoneMsg{err: engine.Undo(context.Background(), sessionID, turnID)}
	}
}

// undoneMsg carries the outcome of an undo back into the update loop.
type undoneMsg struct{ err error }

// builtinItems are the built-ins as menu entries.
func builtinItems() []menuItem {
	out := make([]menuItem, 0, len(config.Builtins()))
	for _, builtin := range config.Builtins() {
		out = append(out, menuItem{
			name:        builtin.Name,
			description: builtin.Description,
			scope:       string(config.CommandBuiltin),
		})
	}
	return out
}

func commandListing(commands config.CommandSet) string {
	var lines []string
	for _, builtin := range config.Builtins() {
		lines = append(lines, fmt.Sprintf("/%s  %s (%s)",
			builtin.Name, builtin.Description, config.CommandBuiltin))
	}
	for _, command := range commands.All() {
		lines = append(lines, fmt.Sprintf("/%s  %s (%s)",
			command.Name, command.Description, command.Scope))
	}
	return strings.Join(lines, "\n")
}
