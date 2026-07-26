// Package chat is the home screen: a conversation in a directory.
//
// It renders a snapshot and nothing else. The session engine owns every message, every partial
// reply and every provider connection, and this asks it what things look like now. That is what
// makes streaming survivable at terminal refresh rates: notifications coalesce because they carry
// nothing, and the growing reply is read from the snapshot rather than assembled here from events
// that might have been dropped.
package chat

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

// Engine is what the chat view needs from the session engine.
//
// Narrow on purpose. Declared here rather than imported so this package depends on a description of
// what it uses rather than on the engine's whole surface, which is also what lets it be driven in
// tests by twenty lines of fake.
type Engine interface {
	Session(id string) (core.Session, bool)
	Send(sessionID, prompt string) (turnID string, err error)
	Cancel(sessionID string)
	Events(afterSequence uint64) <-chan core.Event

	// Compact summarises the older part of a conversation, and Apply is what puts that summary to
	// use. Two calls rather than one, because producing a summary and deciding to rely on it are
	// different decisions and a single call would quietly change what the agent knows.
	Compact(ctx context.Context, sessionID string) (session.CompactionResult, error)
	Apply(sessionID string, result session.CompactionResult) error
}

// EventMsg carries an engine notification into the update loop.
type EventMsg struct{ Event core.Event }

// tickMsg advances the spinner.
type tickMsg struct{}

// compactedMsg carries the outcome of a compaction back into the update loop.
type compactedMsg struct {
	result session.CompactionResult
	err    error
}

// spinnerInterval is how often the working indicator advances.
//
// Slow enough not to be the reason a terminal redraws. A spinner is the least important thing on
// screen and should never be what wakes the program up more often than the work does.
const spinnerInterval = 120 * time.Millisecond

// spinnerFrames are braille dots rather than the rotating slash.
//
// The slash spends a quarter of its time as a vertical bar, which sits directly above the message
// box and reads as part of its border. These occupy one cell, never collide with the box drawing,
// and are what every comparable tool uses.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Model is the chat screen.
type Model struct {
	engine    Engine
	sessionID string

	input Input
	width int
	// height is the number of lines the body may use, set by the frame around it.
	height int

	session core.Session
	loaded  bool

	// scroll is how many lines up from the bottom the view is held. Zero means following the tail,
	// which is the state it returns to whenever a new message is sent.
	scroll int

	// dir and keyName are context for the welcome screen and the header.
	dir     string
	keyName string

	// err is the last thing that went wrong at this layer, such as a refused send. Failures inside
	// a turn live on the turn instead, where they stay attached to what they describe.
	err string

	spinner int
	working bool

	// compacting is true while a summary is being produced, which is a model call and takes as long
	// as any other. Without it the interface looks frozen and people press the key again.
	compacting bool
}

// New builds a chat model over an engine and a session.
//
// Reads the session immediately rather than waiting for the first event. Resuming a conversation
// would otherwise open on the welcome screen and only show its history once something moved, which
// for a session nobody is talking to is never.
func New(engine Engine, sessionID, dir, keyName string) Model {
	m := Model{
		engine:    engine,
		sessionID: sessionID,
		input:     NewInput(),
		width:     80,
		height:    20,
		dir:       dir,
		keyName:   keyName,
	}
	m.refresh()
	return m
}

// Init subscribes to engine events and starts the spinner.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.subscribe(), tick())
}

// SubscribeCmd returns the event subscription on its own.
//
// Init batches it with the spinner, and a batched command yields a tea.BatchMsg rather than the
// event, so a test driving the event path needs the subscription alone. Exported for that and
// nothing else.
func (m Model) SubscribeCmd() tea.Cmd { return m.subscribe() }

func (m Model) subscribe() tea.Cmd {
	events := m.engine.Events(0)
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return nil
		}
		return EventMsg{Event: ev}
	}
}

func tick() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// SetSize tells the model how much room it has.
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.refresh()

	m.input.Width = width - boxChrome
	if m.input.Width < 8 {
		m.input.Width = 8
	}
}

// Update handles one message.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case EventMsg:
		m.refresh()
		// Re-subscribing after each event rather than holding the channel open in the model keeps
		// every read inside the update loop, which is what makes the model safe to copy.
		return m, m.subscribe()

	case tickMsg:
		m.spinner = (m.spinner + 1) % len(spinnerFrames)
		// The refresh matters more than the frame. Coalescing means several tokens can arrive as
		// one notification or, under load, as none at all for a moment, and this is the beat that
		// guarantees the screen catches up regardless.
		m.refresh()
		return m, tick()

	case compactedMsg:
		m.compacting = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		if err := m.engine.Apply(m.sessionID, msg.result); err != nil {
			m.err = err.Error()
			return m, nil
		}
		m.refresh()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// compact asks for a summary of the older half of the conversation.
//
// Manual, on a key, rather than only automatic at the limit. Somebody who knows they are about to
// paste a large file has a reason to compact before it, and a tool that only acts at the threshold
// makes them wait for the failure first.
func (m Model) compact() (Model, tea.Cmd) {
	if m.compacting || m.working {
		return m, nil
	}
	m.compacting = true
	m.err = ""

	engine, sessionID := m.engine, m.sessionID
	return m, func() tea.Msg {
		result, err := engine.Compact(context.Background(), sessionID)
		return compactedMsg{result: result, err: err}
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		return m.send()

	case "esc":
		// Stops the turn rather than leaving the screen. With a reply streaming, escape means stop,
		// and a screen that navigated away instead would abandon a running turn out of sight.
		if m.working {
			m.engine.Cancel(m.sessionID)
			return m, nil
		}
		return m, nil

	case "ctrl+r":
		return m.compact()

	case "pgup":
		m.scroll += m.transcriptHeight() / 2
		return m, nil

	case "pgdown":
		m.scroll -= m.transcriptHeight() / 2
		if m.scroll < 0 {
			m.scroll = 0
		}
		return m, nil

	case "ctrl+home":
		m.scroll = len(m.transcript())
		return m, nil

	case "ctrl+end":
		m.scroll = 0
		return m, nil
	}

	if m.input.Update(msg) {
		m.err = ""
		return m, nil
	}
	return m, nil
}

func (m Model) send() (Model, tea.Cmd) {
	if m.input.Empty() {
		return m, nil
	}

	prompt := m.input.Value()
	if _, err := m.engine.Send(m.sessionID, prompt); err != nil {
		// The message stays in the box. Clearing it on a failure would mean somebody has to retype
		// what they just wrote because a provider was busy.
		m.err = err.Error()
		return m, nil
	}

	m.input.Clear()
	// Sending returns to the tail. Someone who scrolled up to read something old and then asked a
	// question is asking about now.
	m.scroll = 0
	m.err = ""
	m.refresh()
	return m, nil
}

// refresh re-reads the session from the engine.
//
// Everything the screen shows comes from here. There is no local copy of the conversation being
// appended to as events arrive, which is exactly why a coalesced or dropped notification cannot
// lose a token: the next refresh reads whatever is there now.
func (m *Model) refresh() {
	session, ok := m.engine.Session(m.sessionID)
	if !ok {
		return
	}
	m.session = session
	m.loaded = true
	_, m.working = session.Active()
}

// Session exposes the current session. For tests and for the screen around this one.
func (m Model) Session() core.Session { return m.session }

// Working reports whether a turn is in flight.
func (m Model) Working() bool { return m.working }

// Input exposes the message box. For tests.
func (m Model) InputValue() string { return m.input.Value() }

func (m Model) spinnerFrame() string { return spinnerFrames[m.spinner] }

func (m Model) transcript() []string {
	if !m.loaded || len(m.session.Turns) == 0 {
		return Welcome(m.width, m.dir, m.keyName)
	}
	return Transcript(m.session, m.width, m.spinnerFrame())
}

// transcriptHeight is how many lines are left for the conversation once the input box has taken
// what it needs.
func (m Model) transcriptHeight() int {
	h := m.height - m.input.Height() - 1 // one line for the status row
	if h < 1 {
		return 1
	}
	return h
}

// Body renders the screen.
func (m Model) Body() string {
	lines := m.transcript()
	visible := m.transcriptHeight()

	// The tail is what matters, unless the user has deliberately scrolled away from it. A view that
	// jumped to the top on every token would be unusable, and one that always pinned the bottom
	// would make it impossible to read anything while an agent was talking.
	end := len(lines) - m.scroll
	if end > len(lines) {
		end = len(lines)
	}
	if end < 1 {
		end = 1
	}
	start := end - visible
	if start < 0 {
		start = 0
	}

	var b strings.Builder
	shown := lines[start:end]
	b.WriteString(strings.Join(shown, "\n"))

	// Pad so the input box stays at the bottom rather than floating under however much has been
	// said so far.
	if pad := visible - len(shown); pad > 0 {
		b.WriteString(strings.Repeat("\n", pad))
	}

	b.WriteString("\n")
	b.WriteString(m.statusRow(len(lines) - end))
	b.WriteString("\n")
	b.WriteString(m.inputBox())
	return b.String()
}

// statusRow is the line between the conversation and the box.
func (m Model) statusRow(below int) string {
	t := theme.Current()

	if m.err != "" {
		return t.Danger.Render("  " + m.err)
	}
	if below > 0 {
		// Said explicitly, because a view that has silently stopped following the tail looks
		// identical to one where nothing is happening.
		return t.Warning.Render(fmt.Sprintf("  %d more lines below, ctrl+end to follow", below))
	}
	if m.compacting {
		return t.Muted.Render("  " + m.spinnerFrame() + " summarising the conversation so far")
	}
	if m.working {
		return t.Muted.Render("  " + m.spinnerFrame() + " working, esc to stop")
	}
	return ""
}

// boxChrome is how many columns the message box spends on itself: a leading space, two corner or
// wall characters, and a space of padding inside each of them.
//
// One constant rather than two, because the box and the text inside it have to agree. When they did
// not, the box came out one column wider than the terminal and the whole frame wrapped.
const boxChrome = 5

// inputBox draws the message box.
func (m Model) inputBox() string {
	t := theme.Current()

	// Box drawing rather than dashes and pipes. A message box is the one piece of chrome somebody
	// looks at the whole time they are using this, and dashes make it read as terminal output
	// rather than as part of an application.
	const (
		topLeft     = "╭"
		topRight    = "╮"
		bottomLeft  = "╰"
		bottomRight = "╯"
		horizontal  = "─"
		vertical    = "│"
	)

	inner := m.width - boxChrome
	if inner < 6 {
		inner = 6
	}
	rule := strings.Repeat(horizontal, inner+2)

	var b strings.Builder
	b.WriteString(" " + t.Border.Render(topLeft+rule+topRight) + "\n")
	for _, line := range m.input.Lines() {
		// Padded to the full inner width so the right hand border stays in one column whatever is
		// typed. A border that moves with the text reads as a rendering fault.
		pad := inner - lipgloss.Width(line)
		if pad < 0 {
			pad = 0
		}
		b.WriteString(" " + t.Border.Render(vertical) + " " + line + strings.Repeat(" ", pad) +
			" " + t.Border.Render(vertical) + "\n")
	}
	b.WriteString(" " + t.Border.Render(bottomLeft+rule+bottomRight))
	return b.String()
}

// Context is what the frame shows beside the title.
func (m Model) Context() string {
	parts := []string{}
	if m.dir != "" {
		parts = append(parts, m.dir)
	}
	if m.keyName != "" {
		parts = append(parts, m.keyName)
	}

	usage := m.session.Usage()
	if usage.TotalTokens() > 0 {
		spent := "cost unknown"
		if usage.CostKnown {
			spent = fmt.Sprintf("$%.4f", usage.CostUSD)
		}
		parts = append(parts, fmt.Sprintf("%d tokens, %s", usage.TotalTokens(), spent))
	}

	// The context meter is always here, not only when it is nearly full.
	//
	// A meter that appears at eighty percent is one nobody has learned to read by the time it
	// matters, and its appearance is itself alarming. Always on, and it changes colour rather than
	// materialising.
	if len(m.session.Turns) > 0 {
		parts = append(parts, m.contextMeter())
	}
	return strings.Join(parts, "  ")
}

// contextMeter is the "how full is this conversation" figure in the header.
func (m Model) contextMeter() string {
	t := theme.Current()
	use := m.session.ContextUse()

	text := "context " + use.String()
	switch {
	case use.NeedsCompaction():
		return t.Warning.Render(text + ", ctrl+r to compact")
	case use.Fraction() > 0.5:
		return t.Info.Render(text)
	default:
		return t.Muted.Render(text)
	}
}
