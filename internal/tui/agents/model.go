// Package agents is the view over several agents at once.
//
// Three ways of looking at the same thing, because they answer different questions and no single
// layout answers all three:
//
//   - **List** is "what is everyone doing", the one you come back to. One line each, ordered by
//     what needs you.
//   - **Split** is "watch two of them", for when two agents are working on related things and the
//     interesting part is how their answers differ.
//   - **Focus** is "read one properly", tabbing between them full width, for when a line of summary
//     is not enough.
//
// Switching between the three is one keystroke, because the question changes faster than anybody
// wants to renavigate.
package agents

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// Mode is which of the three layouts is showing.
type Mode int

const (
	// ModeList is one line per agent.
	ModeList Mode = iota
	// ModeSplit shows two agents side by side.
	ModeSplit
	// ModeFocus shows one agent full width.
	ModeFocus
)

func (m Mode) String() string {
	switch m {
	case ModeSplit:
		return "split"
	case ModeFocus:
		return "focus"
	default:
		return "list"
	}
}

// Engine is what this view needs from the session engine.
type Engine interface {
	AgentStatuses() []session.AgentStatus
	Session(id string) (core.Session, bool)

	// AddAgent starts a new agent. Its error is shown rather than swallowed, because the two
	// reasons it fails, a name already taken and a name that is not allowed, are both things the
	// person typing can fix immediately.
	//
	// The context is the engine's, for the isolated case where creating an agent has to make a
	// worktree first. This view only creates agents that work in the repository, so the call does no
	// git and does not block the event loop. An isolated agent started from here would have to go
	// through a command rather than straight from a keypress.
	AddAgent(ctx context.Context, agent session.Agent) (session.Agent, error)
}

// SwitchMsg asks the application to open an agent's conversation.
//
// A message rather than a direct call, because which screen is showing belongs to the application
// and a view that could change it would be a view that can put the program somewhere the
// application did not agree to.
type SwitchMsg struct {
	SessionID string
	AgentName string
}

// Model is the agents screen.
type Model struct {
	engine Engine
	mode   Mode

	// cursor is which agent is selected, as an index into the current ordering.
	//
	// An index rather than a name, and re anchored on every refresh, because the ordering moves:
	// an agent that starts waiting jumps to the top, and a cursor holding position 3 would follow
	// the position rather than the agent. Anchoring keeps it on the agent somebody was looking at.
	cursor   int
	anchored string

	width  int
	height int

	statuses []session.AgentStatus

	// The new-agent flow has two explicit steps. Naming collects an identity; confirmingDirect
	// makes the workspace consequence visible before AddAgent can run. Keeping the confirmation as
	// state rather than a line under the name is what makes it impossible to accept accidentally
	// with the same enter key that finished typing.
	naming           bool
	confirmingDirect bool
	draft            string
	err              string

	// defaults are what a new agent inherits, since there is nowhere to choose them yet.
	keyName string
	model   string
	dir     string
}

// New builds the agents view.
func New(engine Engine) Model {
	m := Model{engine: engine, width: 80, height: 24}
	m.refresh()
	return m
}

// SetDefaults says what a new agent inherits.
//
// Inherited from the agent you are looking at rather than asked for, because the first thing
// somebody wants from a second agent is another of what they already have. Choosing a different
// credential or model per agent is a real thing to want and belongs with a profile picker.
func (m *Model) SetDefaults(keyName, model, dir string) {
	m.keyName, m.model, m.dir = keyName, model, dir
}

// SetSize tells the model how much room it has.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// Update handles a keystroke.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		m.refresh()
		return m, nil
	}

	// The creation flow takes the keyboard while it is happening, or the letters of the name would
	// be read as layout commands and typing "split" would change the layout three times.
	if m.confirmingDirect {
		return m.confirmDirect(key)
	}
	if m.naming {
		return m.typeName(key)
	}

	switch key.String() {
	case "enter":
		// Switching is a message to the application rather than something this view does, since
		// which screen is showing is not this view's to decide.
		if status, selected := m.Selected(); selected {
			name, id := status.Agent.Name, status.Agent.SessionID
			return m, func() tea.Msg { return SwitchMsg{SessionID: id, AgentName: name} }
		}
	case "n":
		m.naming = true
		m.draft = ""
		m.err = ""
		return m, nil

	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "tab":
		// Tab moves between agents in focus and split, and cycles the layout in list. In list there
		// is nothing to tab between that j and k do not already do.
		if m.mode == ModeList {
			m.mode = ModeSplit
		} else {
			m.move(1)
		}
	case "1":
		m.mode = ModeList
	case "2":
		m.mode = ModeSplit
	case "3":
		m.mode = ModeFocus
	case "v":
		// One key that cycles, for people who would rather not remember three.
		m.mode = (m.mode + 1) % 3
	}
	return m, nil
}

// typeName handles the keys while a new agent is being named.
func (m Model) typeName(key tea.KeyMsg) (Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEsc:
		m.naming = false
		m.draft = ""
		return m, nil

	case tea.KeyEnter:
		// Guarded as well as constructed properly, because a screen that cannot create agents should
		// say so rather than take the program down. This is what the nil engine did before the
		// application was made to supply one.
		if m.engine == nil {
			m.err = "this view has no engine attached, so no agent can be created"
			return m, nil
		}
		if strings.TrimSpace(m.draft) == "" {
			m.err = "an agent needs a name"
			return m, nil
		}

		// Enter finishes the name; it does not create the agent. D-33 requires direct mode and the
		// exact workspace to be visible before write-capable work can begin.
		m.naming = false
		m.confirmingDirect = true
		m.err = ""
		return m, nil

	case tea.KeyBackspace:
		if runes := []rune(m.draft); len(runes) > 0 {
			m.draft = string(runes[:len(runes)-1])
		}
		return m, nil

	case tea.KeyRunes:
		m.draft += string(key.Runes)
		return m, nil

	case tea.KeySpace:
		m.draft += " "
		return m, nil
	}
	return m, nil
}

// confirmDirect owns the second, deliberately separate step of agent creation.
func (m Model) confirmDirect(key tea.KeyMsg) (Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.confirmingDirect = false
		m.naming = true
		m.err = ""
		return m, nil
	case "y":
		agent, err := m.engine.AddAgent(context.Background(), session.Agent{
			Name:    strings.TrimSpace(m.draft),
			KeyName: m.keyName,
			Model:   m.model,
			Dir:     m.dir,
		})
		if err != nil {
			// Return to the editable name with the failure visible. A duplicate name or validation
			// error is fixed there, not on a confirmation screen with no input field.
			m.confirmingDirect = false
			m.naming = true
			m.err = err.Error()
			return m, nil
		}
		m.confirmingDirect = false
		m.draft = ""
		m.err = ""
		m.anchored = agent.Name
		m.refresh()
	}
	return m, nil
}

// Naming reports whether the creation flow owns the keyboard, so application navigation cannot
// steal a name or a confirmation key.
func (m Model) Naming() bool { return m.naming || m.confirmingDirect }

// ConfirmingDirect lets the frame describe the safe keys for the second creation step.
func (m Model) ConfirmingDirect() bool { return m.confirmingDirect }

func (m *Model) move(by int) {
	if len(m.statuses) == 0 {
		return
	}
	m.cursor += by
	switch {
	case m.cursor < 0:
		m.cursor = len(m.statuses) - 1
	case m.cursor >= len(m.statuses):
		m.cursor = 0
	}
	m.anchored = m.statuses[m.cursor].Agent.Name
}

// refresh re-reads the agents and keeps the cursor on the one it was on.
//
// A zero value Model has no engine, which happens when the application is built without one. It
// renders as empty rather than panicking, because a screen nobody can reach should not be able to
// take the program down from a resize message.
func (m *Model) refresh() {
	if m.engine == nil {
		m.statuses = nil
		return
	}
	m.statuses = m.engine.AgentStatuses()

	if m.anchored == "" {
		if len(m.statuses) > 0 {
			m.anchored = m.statuses[0].Agent.Name
		}
		return
	}
	for i, status := range m.statuses {
		if status.Agent.Name == m.anchored {
			m.cursor = i
			return
		}
	}
	// The agent it was on has gone. Falling back to the top rather than to the same index, because
	// the same index is now a different agent and acting on it would act on the wrong one.
	m.cursor = 0
	if len(m.statuses) > 0 {
		m.anchored = m.statuses[0].Agent.Name
	}
}

// Mode is which layout is showing.
func (m Model) Mode() Mode { return m.mode }

// Selected is the agent under the cursor.
func (m Model) Selected() (session.AgentStatus, bool) {
	if m.cursor < 0 || m.cursor >= len(m.statuses) {
		return session.AgentStatus{}, false
	}
	return m.statuses[m.cursor], true
}

// Count is how many agents there are.
func (m Model) Count() int { return len(m.statuses) }

// Body renders the screen.
func (m Model) Body() string {
	if m.confirmingDirect {
		return m.directPrompt()
	}
	if m.naming {
		return m.namePrompt()
	}
	if m.engine == nil || len(m.statuses) == 0 {
		return m.empty()
	}
	switch m.mode {
	case ModeSplit:
		return m.split()
	case ModeFocus:
		return m.focus()
	default:
		return m.list()
	}
}

// namePrompt is the new agent flow.
func (m Model) namePrompt() string {
	t := theme.Current()

	var b strings.Builder
	b.WriteString(t.Title.Render("New agent"))
	b.WriteString("\n\n")
	b.WriteString(t.Muted.Render("What should it be called? Something you would say out loud."))
	b.WriteString("\n\n")
	b.WriteString("  " + t.Body.Render(m.draft) + t.Cursor.Render(" "))
	b.WriteString("\n\n")

	if m.err != "" {
		b.WriteString(t.Danger.Render("  " + m.err))
		b.WriteString("\n\n")
	}

	// Said plainly, because somebody naming their second agent has no other way to find out what it
	// will be using.
	if m.keyName != "" {
		b.WriteString(t.Muted.Render("  It will use " + m.keyName))
		if m.model != "" {
			b.WriteString(t.Muted.Render(" on " + m.model))
		}
		b.WriteString(t.Muted.Render(", the same as the one you are looking at."))
	}
	return b.String()
}

// directPrompt names the mode and exact workspace before AddAgent can run. It also says what the
// workspace root cannot promise: an enabled shell is still a process running as the user.
func (m Model) directPrompt() string {
	t := theme.Current()
	workspace := m.dir
	if strings.TrimSpace(workspace) == "" {
		workspace = "(current directory)"
	}

	var b strings.Builder
	b.WriteString(t.Title.Render("Create direct agent?"))
	b.WriteString("\n\n")
	b.WriteString(t.Warning.Render("  Direct mode"))
	b.WriteString("\n")
	b.WriteString(t.Body.Render("  Workspace: " + workspace))
	b.WriteString("\n\n")
	b.WriteString(t.Danger.Render(
		"  This agent works directly in this checkout, which may be the primary checkout."))
	b.WriteString("\n")
	b.WriteString(t.Muted.Render(
		"  Structured tools may modify it when trust permits. An enabled shell is not contained here."))
	b.WriteString("\n\n")
	b.WriteString(t.Key.Render("  y") + t.Body.Render(" create direct agent"))
	b.WriteString("\n")
	b.WriteString(t.Key.Render("  esc") + t.Body.Render(" go back"))
	return b.String()
}

func (m Model) empty() string {
	t := theme.Current()
	return t.Muted.Render("No agents yet.") + "\n\n" +
		t.Muted.Render("An agent is a conversation with a name and a credential of its own. ") + "\n" +
		t.Muted.Render("Start one with ") + t.Key.Render("n") + t.Muted.Render(".")
}

// list is one line per agent, ordered by what needs you.
func (m Model) list() string {
	t := theme.Current()

	var b strings.Builder
	for i, status := range m.statuses {
		selected := i == m.cursor

		marker := "  "
		if selected {
			marker = t.Key.Render("> ")
		}

		name := status.Agent.Name
		if selected {
			name = t.Selected.Render(name)
		} else {
			name = t.Body.Render(name)
		}

		b.WriteString(marker)
		b.WriteString(pad(name, 18))
		b.WriteString(stateBadge(status.State))
		b.WriteString("  ")
		b.WriteString(t.Muted.Render(m.detail(status)))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// detail is the right hand part of a list row.
//
// What it says depends on the state, because a blocked agent's most useful fact is what it is
// blocked on, and an idle one's is what it was last doing.
func (m Model) detail(status session.AgentStatus) string {
	switch {
	case status.Waiting != "":
		return "waiting: " + status.Waiting
	case status.Title != "":
		return truncate(status.Title, m.width-40)
	case status.Turns == 0:
		return "nothing yet"
	default:
		return fmt.Sprintf("%d turns", status.Turns)
	}
}

// stateBadge is the one word that says what an agent is doing.
//
// Colour and word together, never colour alone. The whole thing has to read correctly with colour
// disabled, and a row of coloured dots is meaningless in a pasted bug report.
func stateBadge(state core.AgentState) string {
	t := theme.Current()

	switch state {
	case core.AgentAwaitingPermission:
		return t.Warning.Render(pad("needs you", 12))
	case core.AgentFailed:
		return t.Danger.Render(pad("failed", 12))
	case core.AgentWorking:
		return t.Info.Render(pad("working", 12))
	case core.AgentStopped:
		return t.Muted.Render(pad("stopped", 12))
	default:
		return t.Muted.Render(pad("idle", 12))
	}
}

// split shows two agents side by side.
//
// Two rather than four, because a terminal split four ways gives each pane twenty columns, and
// twenty columns of a code discussion is not readable. Two is the most that stays useful.
func (m Model) split() string {
	left, right := m.pair()

	// One column of gap, and the divider drawn rather than implied, because two blocks of text
	// abutting each other read as one paragraph with strange line breaks.
	columnWidth := (m.width - 3) / 2
	if columnWidth < 20 {
		// Too narrow to split. Falling back to focus rather than drawing something unreadable is
		// the same reasoning as refusing to draw below the minimum terminal size.
		return m.focus()
	}

	leftLines := m.pane(left, columnWidth)
	rightLines := m.pane(right, columnWidth)

	t := theme.Current()
	height := max(len(leftLines), len(rightLines))

	var b strings.Builder
	for i := 0; i < height; i++ {
		b.WriteString(padPlain(lineAt(leftLines, i), columnWidth))
		b.WriteString(" " + t.Border.Render("│") + " ")
		b.WriteString(lineAt(rightLines, i))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// pair picks which two agents to show.
//
// The selected one and the next, so moving the cursor walks the pair along rather than jumping it,
// which is what somebody comparing a list of agents actually does.
func (m Model) pair() (session.AgentStatus, session.AgentStatus) {
	left := m.statuses[m.cursor]
	if len(m.statuses) == 1 {
		return left, session.AgentStatus{}
	}
	return left, m.statuses[(m.cursor+1)%len(m.statuses)]
}

// focus shows one agent using the full width.
func (m Model) focus() string {
	status, ok := m.Selected()
	if !ok {
		return m.empty()
	}
	return strings.Join(m.pane(status, m.width), "\n")
}

// pane renders one agent: who it is, what it is doing, and the tail of its conversation.
func (m Model) pane(status session.AgentStatus, width int) []string {
	t := theme.Current()

	if status.Agent.Name == "" {
		return []string{t.Muted.Render("(no other agent)")}
	}

	lines := []string{
		t.Selected.Render(status.Agent.Name) + "  " + stateBadge(status.State),
	}
	if status.Waiting != "" {
		lines = append(lines, t.Warning.Render(truncate("waiting: "+status.Waiting, width)))
	}
	lines = append(lines, t.Muted.Render(truncate(m.summary(status), width)))
	lines = append(lines, "")

	// The tail of the conversation, because what an agent is doing now is at the bottom of it and
	// the top is what it was asked half an hour ago.
	if m.engine == nil {
		return lines
	}
	session, ok := m.engine.Session(status.Agent.SessionID)
	if !ok || len(session.Turns) == 0 {
		lines = append(lines, t.Muted.Render("nothing said yet"))
		return lines
	}

	last := session.Turns[len(session.Turns)-1]
	lines = append(lines, t.Key.Render("> ")+t.Body.Render(truncate(last.Request.Text, width-2)))
	lines = append(lines, "")

	for _, line := range wrapPlain(tail(last.Text, 12), width) {
		lines = append(lines, t.Body.Render(line))
	}
	if !last.State.Whole() && last.State.Terminal() {
		// The same rule as the transcript: every state that is not complete leaves text that reads
		// as an answer and is not one.
		lines = append(lines, t.Warning.Render("["+string(last.State)+"]"))
	}
	return lines
}

func (m Model) summary(status session.AgentStatus) string {
	parts := []string{fmt.Sprintf("%d turns", status.Turns)}
	if status.Usage.TotalTokens() > 0 {
		parts = append(parts, fmt.Sprintf("%d tokens", status.Usage.TotalTokens()))
	}
	if status.Usage.CostKnown && status.Usage.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", status.Usage.CostUSD))
	}
	if status.Agent.Isolated {
		parts = append(parts, "isolated")
	}
	if status.Agent.KeyName != "" {
		parts = append(parts, status.Agent.KeyName)
	}
	return strings.Join(parts, "  ")
}

// Context is what the frame shows beside the title.
func (m Model) Context() string {
	needing := 0
	for _, status := range m.statuses {
		if status.State.NeedsAttention() {
			needing++
		}
	}

	summary := fmt.Sprintf("%d agents", len(m.statuses))
	if needing > 0 {
		// Said first, because it is the reason somebody would look at this screen at all.
		summary = fmt.Sprintf("%d need you, %d agents", needing, len(m.statuses))
	}
	return summary + "  " + m.mode.String()
}
