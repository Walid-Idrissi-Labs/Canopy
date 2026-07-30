// Package agents is the view over several agents at once.
//
// Four ways of looking at the same thing, because they answer different questions and no single
// layout answers all four:
//
//   - **List** is "what is everyone doing", one line each, ordered by what needs you.
//   - **Mosaic** is everyone at once: a tiled grid of panes, up to eight, each one a window on a
//     live conversation with the agent's own campfire on its bottom edge. This is the screen the
//     product exists for.
//   - **Hero** is "read one, keep an eye on the rest": the selected agent across the whole top
//     half, the next few in slices along the bottom.
//   - **Focus** is one agent, full frame, for when a pane is not enough.
//
// Switching is one keystroke, because the question changes faster than anybody wants to
// renavigate, and the digits jump straight to a pane so no agent is more than two keys away.
package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// Mode is which of the four layouts is showing.
type Mode int

const (
	// ModeList is one line per agent.
	ModeList Mode = iota
	// ModeMosaic tiles every agent into a grid of panes.
	ModeMosaic
	// ModeHero is the selected agent large, the others along the bottom.
	ModeHero
	// ModeFocus shows one agent full width.
	ModeFocus

	// modeCount is how many layouts v cycles through.
	modeCount
)

func (m Mode) String() string {
	switch m {
	case ModeMosaic:
		return "mosaic"
	case ModeHero:
		return "hero"
	case ModeFocus:
		return "focus"
	default:
		return "list"
	}
}

// flameTickMsg advances the pane fires, and says which start of the animation it belongs to, so a
// tick left over from an earlier run is dropped rather than doubling the speed.
type flameTickMsg struct{ generation int }

// flameInterval matches the campfire on the chat box. The two fires are the same fire, and a pane
// that flickered at a different rate from the conversation it opens into would read as two
// programs.
const flameInterval = 750 * time.Millisecond

func flameTick(generation int) tea.Cmd {
	return tea.Tick(flameInterval, func(time.Time) tea.Msg {
		return flameTickMsg{generation: generation}
	})
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

	// step is where the pane fires have got to, and the three fields after it are the machinery
	// that keeps them moving only while there is something to move for. The animation runs while
	// the screen is showing, a pane layout is up and at least one agent is working; otherwise no
	// tick is scheduled at all, which is the same discipline the chat's own campfire keeps.
	step       int
	ticking    bool
	generation int
	visible    bool

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

// Update handles a keystroke, and keeps the fires burning between them.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if tick, ok := msg.(flameTickMsg); ok {
		// A tick from an animation that has been restarted since is dropped and not rescheduled,
		// which is also how the old ticker stops.
		if tick.generation != m.generation {
			return m, nil
		}
		m.refresh()
		if !m.flickering() {
			m.ticking = false
			return m, nil
		}
		m.step++
		return m, flameTick(m.generation)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		m.refresh()
		// Work may have started since the last look, and the ticker only exists while there is
		// work, so every refresh is a chance to relight the fires.
		return m, m.ensureFlame()
	}

	// The creation flow takes the keyboard while it is happening, or the letters of the name would
	// be read as layout commands and typing "vim" would change the layout twice.
	if m.confirmingDirect {
		return m.confirmDirect(key)
	}
	if m.naming {
		return m.typeName(key)
	}

	switch pressed := key.String(); pressed {
	case "enter":
		return m, m.open()
	case "n":
		m.naming = true
		m.draft = ""
		m.err = ""
		return m, nil

	case "j", "down":
		// Down a row in the mosaic, down one agent everywhere else.
		m.move(m.stride())
	case "k", "up":
		m.move(-m.stride())
	case "h", "left":
		if m.mode != ModeList {
			m.move(-1)
		}
	case "l", "right":
		if m.mode != ModeList {
			m.move(1)
		}
	case "tab":
		// Tab walks the agents in every pane layout, and from the list it opens the mosaic, which
		// is the layout the list is a summary of.
		if m.mode == ModeList {
			m.mode = ModeMosaic
		} else {
			m.move(1)
		}
	case "shift+tab":
		if m.mode != ModeList {
			m.move(-1)
		}
	case "[":
		m.move(-m.perPage())
	case "]":
		m.move(m.perPage())
	case "1", "2", "3", "4", "5", "6", "7", "8":
		// The digit drawn in a pane's border jumps to that pane, and the digit of the pane you
		// are already on opens it, so any visible agent is two presses from a conversation.
		return m.jump(int(pressed[0] - '0'))
	case "v":
		// One key that cycles, for people who would rather not remember four.
		m.mode = (m.mode + 1) % modeCount
	}
	return m, m.ensureFlame()
}

// open asks the application to show the selected agent's conversation.
//
// A message rather than a direct act, since which screen is showing is not this view's to decide.
func (m Model) open() tea.Cmd {
	status, selected := m.Selected()
	if !selected {
		return nil
	}
	name, id := status.Agent.Name, status.Agent.SessionID
	return func() tea.Msg { return SwitchMsg{SessionID: id, AgentName: name} }
}

// jump moves to the pane wearing a digit, or opens it when it is the pane you are on.
func (m Model) jump(digit int) (Model, tea.Cmd) {
	target, ok := m.paneAt(digit)
	if !ok {
		return m, nil
	}
	if target == m.cursor {
		return m, m.open()
	}
	m.cursor = target
	m.anchored = m.statuses[target].Agent.Name
	return m, m.ensureFlame()
}

// paneAt is which agent the digit points to in the current layout.
func (m Model) paneAt(digit int) (int, bool) {
	if len(m.statuses) == 0 {
		return 0, false
	}
	switch m.mode {
	case ModeHero:
		// One is the hero and the rest count along the bottom row, which is the order the panes
		// are drawn in.
		target := (m.cursor + digit - 1) % len(m.statuses)
		return target, digit <= len(m.statuses)
	case ModeMosaic:
		target := m.page()*m.perPage() + digit - 1
		return target, digit <= m.perPage() && target < len(m.statuses)
	default:
		// The list and focus have no grid, so the digits count from the top of the ordering.
		return digit - 1, digit <= len(m.statuses)
	}
}

// stride is how far j and k travel: a row of the grid in the mosaic, one agent elsewhere.
func (m Model) stride() int {
	if m.mode != ModeMosaic || len(m.statuses) == 0 {
		return 1
	}
	_, _, height := m.mosaicPlan()
	shape := planGrid(len(m.statuses), m.width, height)
	if len(shape.rows) == 0 {
		return 1
	}
	return shape.rows[0]
}

// flickering reports whether the fires have anything to animate: the screen is showing, a pane
// layout is up, and at least one agent is actually working.
func (m Model) flickering() bool {
	if !m.visible || m.mode == ModeList {
		return false
	}
	for _, status := range m.statuses {
		if status.State == core.AgentWorking {
			return true
		}
	}
	return false
}

// ensureFlame starts the animation when it is needed and not already running.
//
// The generation is what makes restarting safe: a tick from before the restart no longer matches
// and dies, so there is never more than one live ticker however many times the screen is entered
// and left.
func (m *Model) ensureFlame() tea.Cmd {
	if m.ticking || !m.flickering() {
		return nil
	}
	m.ticking = true
	m.generation++
	return flameTick(m.generation)
}

// SetVisible tells the view whether it is the screen in front.
//
// The view cannot know on its own, and it matters here because the pane fires animate: a ticker
// running behind another screen would be waking the program for frames nobody can see.
func (m *Model) SetVisible(visible bool) tea.Cmd {
	m.visible = visible
	if !visible {
		return nil
	}
	m.refresh()
	return m.ensureFlame()
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
	case ModeMosaic:
		return m.mosaic()
	case ModeHero:
		return m.hero()
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
//
// It wears the same heavy needs-you frame the chat's permission question does, because it is the
// same kind of moment: the one question on this screen that creates something with access to the
// checkout, drawn so it cannot be mistaken for information.
func (m Model) directPrompt() string {
	t := theme.Current()
	workspace := m.dir
	if strings.TrimSpace(workspace) == "" {
		workspace = "(current directory)"
	}

	body := []string{
		t.Title.Render("Create direct agent?"),
		"",
		t.Warning.Render("Direct mode") + t.Body.Render("  in "+workspace),
		"",
		t.Danger.Render("This agent works directly in this checkout, which may be the primary checkout."),
		t.Muted.Render("Structured tools may modify it when trust permits."),
		t.Muted.Render("An enabled shell is not contained here."),
		"",
		t.Key.Render("y") + t.Body.Render(" create direct agent   ") +
			t.Key.Render("esc") + t.Body.Render(" go back"),
	}
	return strings.Join(needsYouPanel(body, m.width-8), "\n")
}

// needsYouPanel is the heavy warning frame around a question that creates or spends something.
//
// The same drawing the chat uses around a permission prompt, kept in step by eye and by the shared
// reverse video chip: every other frame in the interface is a thin rounded line, so the heavy one
// means "this is not information, this is a question", and reverse video is the one emphasis that
// survives NO_COLOR and a dull palette.
func needsYouPanel(body []string, inner int) []string {
	t := theme.Current()

	const indent = "  "
	for i, line := range body {
		body[i] = truncate(line, inner)
	}

	chip := t.Warning.Reverse(true).Bold(true).Render(" needs you ")
	top := t.Warning.Render("┏━") + chip
	if rest := inner + 1 - lipgloss.Width(chip); rest > 0 {
		top += t.Warning.Render(strings.Repeat("━", rest) + "┓")
	} else {
		top = t.Warning.Render("┏" + strings.Repeat("━", maxInt(inner, 1)+2) + "┓")
	}

	out := make([]string, 0, len(body)+2)
	out = append(out, indent+top)
	for _, line := range body {
		out = append(out, indent+t.Warning.Render("┃")+" "+pad(line, inner)+" "+
			t.Warning.Render("┃"))
	}
	return append(out, indent+t.Warning.Render("┗"+strings.Repeat("━", maxInt(inner, 1)+2)+"┛"))
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
	case len(status.Tasks) > 0:
		// Ahead of the title, because a task list is what the agent says it is doing now and a
		// title is what the conversation was about when it started. With eight agents running, the
		// first is the question and the second is trivia.
		return truncate(core.TaskSummary(status.Tasks), m.width-40)
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
//
// The count of who needs a person used to be here, first, because it is the reason somebody would
// look at this screen at all. It is in the header itself now, on every screen rather than on this
// one, which is what D-43 asks for: an indicator that lives only on the agent list is a smoke alarm
// installed inside the fire. Kept in one place rather than both, because the header counts every
// conversation waiting on somebody and this could only ever count agents, and two numbers a row
// apart disagreeing about the same question is worse than either of them alone.
func (m Model) Context() string {
	summary := fmt.Sprintf("%d agents", len(m.statuses))
	summary += "  " + m.mode.String()
	if m.mode == ModeMosaic && m.pages() > 1 {
		summary += fmt.Sprintf(" %d/%d", m.page()+1, m.pages())
	}
	return summary
}
