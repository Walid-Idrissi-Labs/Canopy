package chat

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
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
	ActionGreen  = "green"
	ActionKeys   = "keys"
	ActionModel  = "model"
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

	case "mode":
		m.describeOrSetMode(arguments)

	case "steer":
		m.steer(arguments)

	case "btw":
		return true, m.aside(arguments)

	case "context":
		m.notice = m.contextUse()

	case "trail":
		m.notice = m.toolTrail()

	case "tasks":
		m.notice = m.taskSummary()

	case "pickup":
		m.notice = "come back to this conversation with\n" +
			"canopy pickup " + session.Code(m.sessionID)

	case "theme":
		m.notice = m.switchTheme(arguments)

	case "fork":
		m.forkHere()

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

// describeOrSetMode says which mode this is, or moves to a named one.
//
// Bare it lists the ladder with what each one may change, which is the question somebody actually
// has when they type it: not "what is it called" but "what is it allowed to do to my code".
func (m *Model) describeOrSetMode(name string) {
	if name != "" {
		m.setMode(strings.ToLower(name))
		return
	}

	current := m.Mode()
	// The selection gets a marker of its own rather than the same one. Two rungs claiming to be the
	// current mode would be the listing disagreeing with itself about which one is enforced.
	selecting, _ := m.Selecting()

	lines := make([]string, 0, len(core.Modes())+1)
	for _, mode := range core.Modes() {
		marker := "  "
		if mode.Name == selecting {
			marker = "~ "
		}
		if mode.Name == current {
			marker = "> "
		}
		lines = append(lines, marker+mode.Name+"  "+mode.Description)
	}
	lines = append(lines,
		"  shift+tab moves between them, and the one it stops on takes hold a moment later, mid reply")
	m.notice = strings.Join(lines, "\n")
}

// steer corrects the agent without throwing away what it has done.
//
// The distinction between this and escape is the whole point, and it is worth restating here because
// the two look interchangeable from the outside. Escape stops the turn now and keeps whatever text
// arrived. This queues the correction and lets the turn in flight finish, so an agent that is three
// tool calls in and has just read the wrong file is told at the next place where being told is
// possible, rather than being made to start again and rebuild the reasoning that got it there.
func (m *Model) steer(guidance string) {
	if guidance == "" {
		m.err = "what should it do differently? For example `/steer use the existing parser`"
		return
	}
	if err := m.engine.Steer(m.sessionID, guidance); err != nil {
		m.err = err.Error()
		return
	}
	// No notice in either case, because both outcomes are visible as themselves from this keystroke
	// on: guidance queued behind a running turn sits in the steering pane above the box until it is
	// delivered, and guidance sent to an idle agent appears in the transcript as the message it
	// became. A sentence describing either would be the screen saying what it is already showing.
	m.refresh()
}

// aside asks something about the conversation without joining it.
//
// The counterpart to steering, and the difference is worth keeping straight because both are things
// you type while an agent is working. Steering changes what it does. This changes nothing: the
// question and its answer are never recorded, no turn is created, and an agent working at the time
// goes on working. The model in the next real turn has no idea it was asked.
func (m *Model) aside(question string) tea.Cmd {
	if question == "" {
		// Bare, it reopens the panel of everything asked so far, which is how an answer from
		// twenty minutes ago is found again. Only when nothing has been asked yet does it explain
		// itself instead.
		if len(m.asides) > 0 {
			m.btwOpen = !m.btwOpen
			m.btwScroll = 0
			return nil
		}
		m.err = "what would you like to know? For example `/btw which file holds the parser`"
		return nil
	}

	engine, sessionID := m.engine, m.sessionID
	m.notice = "asking, and this changes nothing"
	m.err = ""

	return func() tea.Msg {
		answer, err := engine.Aside(context.Background(), sessionID, question)
		return asideMsg{question: question, answer: answer, err: err}
	}
}

// asideMsg carries a side answer back into the update loop.
type asideMsg struct {
	question string
	answer   string
	err      error
}

// contextUse is how much of the window this conversation is using.
//
// Worth a command as well as the meter in the header, because the meter says a percentage and the
// question people actually have when they look at it is whether they need to do something about it.
func (m Model) contextUse() string {
	use := m.session.ContextUse()
	if len(m.session.Turns) == 0 {
		return "nothing said yet, so the whole window is free"
	}
	if use.NeedsCompaction() {
		return use.String() + " used, worth running /compact before the next long turn"
	}
	return use.String() + " used, with room to keep going"
}

// toolTrail is what this agent actually did, and what it was not allowed to do.
//
// The refusals are the half worth having. A permission model nobody can inspect is a permission
// model nobody can trust, and "it did not do the thing I asked" is answered by a refused entry far
// more often than by anything the model said about it.
func (m Model) toolTrail() string {
	trail := m.engine.Trail()
	if trail == nil {
		return "no tool calls are being recorded for this conversation"
	}
	entries := trail.ForAgent(m.sessionID)
	if len(entries) == 0 {
		return "this agent has not called a tool yet"
	}

	// The tail, newest last, because the question is almost always about what just happened.
	if len(entries) > trailLines {
		entries = entries[len(entries)-trailLines:]
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		detail := entry.Tool
		if entry.Arguments != "" {
			detail += "  " + firstLine(entry.Arguments)
		}
		line := fmt.Sprintf("%-7s %s", entry.Outcome, detail)
		if entry.Outcome != permission.Allow && entry.Reason != "" {
			// The reason only on the ones that did not run. On an allowed call it is boilerplate
			// repeated on every line; on a refusal it is the entire content of the entry.
			line += "  (" + entry.Reason + ")"
		}
		lines = append(lines, truncate(line, m.width-2))
	}
	return strings.Join(lines, "\n")
}

// trailLines is how much of the trail one command prints.
//
// The tail rather than the whole thing, because a long turn makes hundreds of calls and a notice
// that fills the screen is one nobody reads. The full record is the audit trail's own to keep.
const trailLines = 12

// taskSummary is the agent's own plan, for the moments the pane is not on screen.
func (m Model) taskSummary() string {
	if len(m.session.Tasks) == 0 {
		return "the agent has not written down a plan for this conversation"
	}
	lines := make([]string, 0, len(m.session.Tasks))
	for _, task := range m.session.Tasks {
		line := "[" + task.State.Glyph() + "] " + task.Text
		if task.Outcome != "" {
			line += ", " + task.Outcome
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// switchTheme changes the palette, or says which ones there are.
//
// This exists because A9-03 shipped two themes and one way to reach the second, an environment
// variable read at startup. A setting you have to restart the program to change is one nobody tries,
// and a theme nobody can try is a theme that goes unmaintained.
func (m Model) switchTheme(name string) string {
	if name == "" {
		return "the palette is " + theme.Current().Palette.Name +
			", and /theme takes one of: " + strings.Join(theme.Names(), ", ")
	}
	palette, ok := theme.ByName(strings.ToLower(name))
	if !ok {
		return "there is no theme called " + name + ", try one of: " +
			strings.Join(theme.Names(), ", ")
	}
	theme.Set(palette)
	return "the palette is " + palette.Name
}

// forkHere branches the conversation at the last turn.
//
// The fork keeps everything said so far and the original is untouched, which is what makes this
// worth having: trying a second approach without losing the first is otherwise a matter of starting
// over and retyping the context that got you here.
func (m *Model) forkHere() {
	if len(m.session.Turns) == 0 {
		m.err = "there is nothing to branch from yet"
		return
	}

	through := m.session.Turns[len(m.session.Turns)-1].ID
	forked, err := m.engine.Fork(m.sessionID, through)
	if err != nil {
		m.err = err.Error()
		return
	}
	// The command on its own line, so it can never be broken across a wrap. Two halves of a command
	// are two things that each look like a shorter command, and this one exists to be copied.
	m.notice = "branched to a new conversation with everything said so far, reach it with ctrl+d or\n" +
		"canopy pickup " + session.Code(forked.ID)
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
