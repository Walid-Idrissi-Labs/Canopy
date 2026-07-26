package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// Model is the dashboard.
//
// It reads through core.SnapshotStore and nothing else, so it runs identically against the fake
// and against the real engine. That is the point of the contract: when the engine lands, this
// file should not need to change, and if it does, the contract was wrong.
type Model struct {
	store  core.SnapshotStore
	events <-chan core.Event

	snapshot core.ProjectSnapshot

	// selectedID is a workspace ID rather than a row index.
	//
	// Indices look simpler and are wrong. Workspaces appear and disappear as worktrees are created
	// and removed outside Canopy, so an index quietly starts pointing at a different workspace than
	// the one the user chose. In a tool whose entire job is being trustworthy, acting on the wrong
	// workspace because a row moved is about the worst bug available.
	selectedID string

	width  int
	height int

	streamClosed bool
	lastEvent    core.Event
	eventCount   int
}

// eventMsg carries a store notification into the Bubble Tea update loop.
type eventMsg core.Event

// streamClosedMsg means the store shut down and no further events are coming.
type streamClosedMsg struct{}

// New builds a dashboard reading from the given store.
//
// The snapshot is taken before subscribing, and the subscription starts from that snapshot's
// sequence. Doing it in that order is what makes it impossible for a change to slip through the
// gap between reading the state and starting to listen for changes to it.
func New(store core.SnapshotStore) Model {
	snapshot := store.Snapshot()
	model := Model{
		store:    store,
		events:   store.Events(snapshot.Sequence),
		snapshot: snapshot,
		width:    80,
		height:   24,
	}
	if len(snapshot.Workspaces) > 0 {
		model.selectedID = snapshot.Workspaces[0].ID
	}
	return model
}

func (m Model) Init() tea.Cmd {
	return waitForEvent(m.events)
}

func waitForEvent(events <-chan core.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return streamClosedMsg{}
		}
		return eventMsg(event)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case eventMsg:
		// The event says something moved. It does not say what the new state is, so the snapshot
		// is re-read rather than trusted from the payload. That is the whole reason events carry
		// no state: there is only ever one thing claiming to be the truth.
		m.snapshot = m.store.Snapshot()
		m.lastEvent = core.Event(msg)
		m.eventCount++
		m.ensureSelectionExists()
		return m, waitForEvent(m.events)

	case streamClosedMsg:
		m.streamClosed = true
		return m, nil
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return m, tea.Quit
	case "j", "down":
		m.moveSelection(1)
	case "k", "up":
		m.moveSelection(-1)
	case "g", "home":
		if len(m.snapshot.Workspaces) > 0 {
			m.selectedID = m.snapshot.Workspaces[0].ID
		}
	case "G", "end":
		if n := len(m.snapshot.Workspaces); n > 0 {
			m.selectedID = m.snapshot.Workspaces[n-1].ID
		}
	case "r":
		// A manual re-read. The dashboard is event driven and should never need this, which is
		// exactly why it is worth having: if pressing it ever changes what is on screen, the event
		// path has a bug and this is how it gets noticed.
		m.snapshot = m.store.Snapshot()
		m.ensureSelectionExists()
	}
	return m, nil
}

func (m *Model) moveSelection(delta int) {
	if len(m.snapshot.Workspaces) == 0 {
		return
	}
	index := m.selectedIndex()
	index += delta
	if index < 0 {
		index = 0
	}
	if index > len(m.snapshot.Workspaces)-1 {
		index = len(m.snapshot.Workspaces) - 1
	}
	m.selectedID = m.snapshot.Workspaces[index].ID
}

// selectedIndex resolves the selected workspace ID to a row, returning 0 if it is gone.
func (m Model) selectedIndex() int {
	for i, workspace := range m.snapshot.Workspaces {
		if workspace.ID == m.selectedID {
			return i
		}
	}
	return 0
}

// ensureSelectionExists reseats the cursor when the selected workspace disappears, which happens
// when a worktree is removed outside Canopy.
func (m *Model) ensureSelectionExists() {
	if len(m.snapshot.Workspaces) == 0 {
		m.selectedID = ""
		return
	}
	for _, workspace := range m.snapshot.Workspaces {
		if workspace.ID == m.selectedID {
			return
		}
	}
	m.selectedID = m.snapshot.Workspaces[0].ID
}

// SelectedWorkspace returns the workspace under the cursor.
func (m Model) SelectedWorkspace() (core.WorkspaceSnapshot, bool) {
	if m.selectedID == "" {
		return core.WorkspaceSnapshot{}, false
	}
	return m.snapshot.Workspace(m.selectedID)
}

func (m Model) View() string {
	var b strings.Builder

	b.WriteString(m.renderTitle())
	b.WriteString("\n\n")

	if len(m.snapshot.Workspaces) == 0 {
		b.WriteString(m.renderEmpty())
		b.WriteString("\n\n")
		b.WriteString(m.renderFooter())
		return b.String()
	}

	b.WriteString(m.renderTable())
	b.WriteString("\n")
	b.WriteString(m.renderDetail())
	b.WriteString("\n")
	b.WriteString(m.renderFooter())

	return b.String()
}

func (m Model) renderTitle() string {
	title := styleTitle.Render("canopy")

	context := m.snapshot.RepoRoot
	if context == "" {
		context = "no repository"
	}

	status := ""
	switch {
	case m.snapshot.ConfigState == core.ConfigInvalid:
		reason := m.snapshot.ConfigError
		if reason == "" {
			reason = "configuration is invalid"
		}
		status = testStatus(core.TestError).style.Render("config invalid: " + reason)
	case m.snapshot.ConfigState == core.ConfigMissing:
		status = styleMuted.Render("no configuration, nothing to run")
	case !m.snapshot.TrustState.AllowsExecution():
		status = testStatus(core.TestStale).style.Render(
			"not approved to run commands (" + m.snapshot.TrustState.String() + ")")
	}

	line := title + styleMuted.Render("  "+context)
	if status != "" {
		line += styleMuted.Render("  |  ") + status
	}
	return line
}

func (m Model) renderEmpty() string {
	return styleMuted.Render("No worktrees found.\n\n" +
		"Canopy watches worktrees that already exist. Create one with\n" +
		"  git worktree add ../my-branch -b my-branch\n" +
		"and it will appear here.")
}

// columns is the table layout. Widths are fixed so a state change never shifts a column, which
// would otherwise make the whole table twitch every time one workspace went stale.
type column struct {
	title string
	width int
}

// Widths are budgeted against an 80 column terminal: 2 for the selection marker plus 76 here,
// leaving a couple of columns spare. Every header has to fit with a space to spare after it, or
// pad truncates the heading itself, which is how "VERIFIED" first appeared on screen as "VERI...".
var columns = []column{
	{"WORKSPACE", 15},
	{"BRANCH", 16},
	{"REVISION", 14},
	{"TESTS", 11},
	{"SERVICES", 11},
	{"VERIFIED", 9},
}

func (m Model) renderTable() string {
	var b strings.Builder

	header := "  "
	for _, col := range columns {
		header += pad(col.title, col.width)
	}
	b.WriteString(styleHeader.Render(strings.TrimRight(header, " ")))
	b.WriteString("\n")

	selected := m.selectedIndex()
	for i, workspace := range m.snapshot.Workspaces {
		b.WriteString(m.renderRow(workspace, i == selected))
		if i < len(m.snapshot.Workspaces)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m Model) renderRow(workspace core.WorkspaceSnapshot, selected bool) string {
	rollup := core.RollUp(workspace)

	marker := "  "
	nameStyle := lipgloss.NewStyle().Foreground(colorText)
	if selected {
		marker = "> "
		nameStyle = styleSelected
	}

	branch := workspace.Branch
	if workspace.Detached {
		branch = "(detached)"
	}
	if branch == "" {
		branch = "(none)"
	}

	revision := workspace.Revision.Short()
	if workspace.Dirty.IsDirty() {
		revision += "*"
	}

	tests := testStatus(rollup.Tests)
	services := serviceStatus(rollup.Services)
	verified := verifiedStatus(rollup)

	row := marker
	row += nameStyle.Render(pad(workspace.Name, columns[0].width))
	row += styleMuted.Render(pad(branch, columns[1].width))
	row += styleMuted.Render(pad(revision, columns[2].width))
	row += padRendered(tests, columns[3].width)
	row += padRendered(services, columns[4].width)
	row += padRendered(verified, columns[5].width)

	return strings.TrimRight(row, " ")
}

// renderDetail explains the selected row.
//
// This panel is the product promise made visible. A dashboard that shows a state without being
// able to say what produced it is asking to be trusted on authority, and the entire argument for
// this tool is that it never does that.
func (m Model) renderDetail() string {
	workspace, ok := m.SelectedWorkspace()
	if !ok {
		return ""
	}
	rollup := core.RollUp(workspace)

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(styleReason.Render("  " + truncate(rollup.Reason, m.width-4)))

	if rollup.Caveat != "" {
		b.WriteString("\n")
		b.WriteString(styleCaveat.Render("  also: " + truncate(rollup.Caveat, m.width-10)))
	}

	if workspace.RevisionError != "" {
		b.WriteString("\n")
		b.WriteString(testStatus(core.TestUnknown).style.Render(
			"  " + truncate(workspace.RevisionError, m.width-4)))
	}

	return b.String()
}

func (m Model) renderFooter() string {
	keys := "j/k move   r refresh   q quit"

	activity := ""
	switch {
	case m.streamClosed:
		activity = "event stream closed"
	case m.eventCount > 0:
		activity = fmt.Sprintf("%d events, last %s", m.eventCount, m.lastEvent.Kind)
	default:
		activity = "waiting for changes"
	}

	return "\n" + styleFooter.Render("  "+keys+"   |   "+activity)
}

// pad fits text to an exact display width, truncating when it does not fit.
func pad(text string, width int) string {
	text = truncate(text, width-1)
	gap := width - lipgloss.Width(text)
	if gap < 0 {
		gap = 0
	}
	return text + strings.Repeat(" ", gap)
}

// padRendered pads a status by its plain width, then applies styling.
//
// Padding has to be measured before the colour escapes are added. Measuring afterwards counts the
// escape sequences as characters and every styled column ends up short.
func padRendered(status statusText, width int) string {
	plain := status.plain()
	gap := width - lipgloss.Width(plain)
	if gap < 0 {
		gap = 0
	}
	return status.render() + strings.Repeat(" ", gap)
}

func truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	if width <= 3 {
		return text[:width]
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	return string(runes[:width-3]) + "..."
}

// Snapshot exposes the current state, for tests and for the harness.
func (m Model) Snapshot() core.ProjectSnapshot { return m.snapshot }

// Run starts the dashboard as a full screen program.
func Run(store core.SnapshotStore) error {
	program := tea.NewProgram(New(store), tea.WithAltScreen())
	_, err := program.Run()
	return err
}
