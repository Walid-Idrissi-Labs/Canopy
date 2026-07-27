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
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
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

	// Pending is the tool call waiting on a person, and Answer replies to it. The interface never
	// blocks: it notices the question through the ordinary event stream and the answer travels back
	// through the engine.
	Pending(sessionID string) (session.Prompt, bool)
	Answer(sessionID string, approved, remember bool) bool

	// UseCredential points this conversation at a different credential and model.
	UseCredential(sessionID, keyName, model string) error
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

	// dir and keyName are context for the welcome screen and the header, and agentName says which
	// agent's conversation this is once there is more than one.
	dir       string
	keyName   string
	agentName string

	// err is the last thing that went wrong at this layer, such as a refused send. Failures inside
	// a turn live on the turn instead, where they stay attached to what they describe.
	err string

	// notice is something the screen around this one wants said, such as a question waiting on a
	// second keystroke. Kept apart from err because it is not a failure and should not be red.
	notice string

	spinner int
	working bool

	// compacting is true while a summary is being produced, which is a model call and takes as long
	// as any other. Without it the interface looks frozen and people press the key again.
	compacting bool

	// prompt is the tool call waiting on an answer, when there is one.
	prompt   session.Prompt
	awaiting bool
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
	m.input.LoadHistory(promptsOf(m.session))
	return m
}

// promptsOf is every message the user sent in a conversation, oldest first.
func promptsOf(s core.Session) []string {
	out := make([]string, 0, len(s.Turns))
	for _, turn := range s.Turns {
		out = append(out, turn.Request.Text)
	}
	return out
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

// answerPrompt handles the keys that reply to a permission question.
//
// Deliberately few, and deliberately not a single key for the widest option. Approving once is `y`,
// approving everything like this for the rest of the session is `a`, and refusing is anything else
// including escape and enter. That last part matters: the reflex key on a prompt somebody has not
// read is enter, and enter meaning no is the difference between a misread prompt costing a retry
// and costing a repository.
func (m Model) answerPrompt(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.engine.Answer(m.sessionID, true, false)
	case "a":
		m.engine.Answer(m.sessionID, true, true)
	default:
		m.engine.Answer(m.sessionID, false, false)
	}
	m.refresh()
	return m, nil
}

// promptLines renders the question.
func (m Model) promptLines() []string {
	t := theme.Current()
	req := m.prompt.Request

	var lines []string
	lines = append(lines, t.Warning.Render("This agent wants to "+describeRequest(req)))
	lines = append(lines, "")

	// The thing being approved, shown verbatim and in full. A command summarised or truncated is a
	// command somebody approved without having seen it.
	if req.Command != "" {
		for _, line := range wrap(req.Command, m.width-4) {
			lines = append(lines, "  "+t.Body.Render(line))
		}
	}
	for _, path := range req.Paths {
		lines = append(lines, "  "+t.Body.Render(path))
	}

	lines = append(lines, "")
	lines = append(lines, t.Muted.Render("  "+m.prompt.Decision.Reason))
	lines = append(lines, "")
	lines = append(lines,
		"  "+t.Key.Render("y")+t.Muted.Render(" once   ")+
			t.Key.Render("a")+t.Muted.Render(" always, "+m.prompt.Scope().String()+"   ")+
			t.Key.Render("any other key")+t.Muted.Render(" no"))
	return lines
}

// describeRequest says what is being asked for in words rather than in tool names.
//
// "run a command" is something somebody can decide about at two in the morning. "run_command" is a
// symbol from a codebase they have never read.
func describeRequest(req permission.Request) string {
	switch req.Kind {
	case core.ToolExecute:
		return "run a command"
	case core.ToolWrite:
		return "change a file"
	case core.ToolNetwork:
		return "fetch something from the internet"
	case core.ToolGit:
		return "run a git operation that can destroy work"
	default:
		return "use " + req.Tool
	}
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
	// A question takes the keyboard while it is up. Everything else is a keystroke that would go
	// into the message box, and typing an answer to a yes or no question into a text field and
	// wondering why nothing happens is a bad minute to give somebody.
	if m.awaiting {
		return m.answerPrompt(msg)
	}

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

	// Filed only once the engine has accepted it, so a message that was refused is still in the box
	// rather than in the box and in the history, which is one message showing up twice.
	m.input.Remember(prompt)
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
	current, ok := m.engine.Session(m.sessionID)
	if !ok {
		return
	}
	m.session = current
	m.loaded = true
	_, m.working = current.Active()
	m.prompt, m.awaiting = m.engine.Pending(m.sessionID)
}

// Session exposes the current session. For tests and for the screen around this one.
func (m Model) Session() core.Session { return m.session }

// Working reports whether a turn is in flight.
func (m Model) Working() bool { return m.working }

// SetSession points this screen at a different conversation.
//
// The scroll position and the half typed message are cleared with it. Carrying either across would
// mean arriving in one agent's conversation scrolled to a position from another's, and finding text
// in the box that was meant for somebody else.
func (m *Model) SetSession(sessionID, label string) {
	if sessionID == m.sessionID {
		return
	}
	m.sessionID = sessionID
	m.agentName = label
	m.scroll = 0
	m.err = ""
	m.notice = ""
	m.input.Clear()
	m.refresh()
	// History belongs to the conversation, not to the box. Carrying it across would offer you, on
	// the first press of up, the message you sent to a different agent.
	m.input.LoadHistory(promptsOf(m.session))
}

// SetNotice puts a line above the message box.
//
// For the application around this screen, which sometimes needs to say something about the
// conversation without owning any of the conversation's rendering.
func (m *Model) SetNotice(text string) { m.notice = text }

// Notice is what is currently being said. For tests.
func (m Model) Notice() string { return m.notice }

// UseCredential switches this conversation to a different credential and model.
//
// A refusal is shown rather than swallowed. Choosing a credential and having nothing visibly happen
// is how somebody concludes the screen does not work, which is exactly what it looked like before
// there was any way to choose at all.
func (m *Model) UseCredential(keyName, model string) {
	if err := m.engine.UseCredential(m.sessionID, keyName, model); err != nil {
		m.err = err.Error()
		return
	}
	m.keyName = keyName
	m.err = ""
	m.refresh()
}

// SessionID is the conversation being shown.
func (m Model) SessionID() string { return m.sessionID }

// Awaiting reports whether a question is on screen. The frame uses it to change the footer, since
// the keys mean something different while one is up.
func (m Model) Awaiting() bool { return m.awaiting }

// Input exposes the message box. For tests.
func (m Model) InputValue() string { return m.input.Value() }

// InputEmpty reports whether there is nothing in the box.
//
// The screen around this one uses it to decide what a printable key means. With nothing typed there
// is no message for a keystroke to be part of, which is what makes it safe to give some of them
// another meaning.
func (m Model) InputEmpty() bool { return m.input.Empty() }

func (m Model) spinnerFrame() string { return spinnerFrames[m.spinner] }

func (m Model) transcript() []string {
	if !m.loaded || len(m.session.Turns) == 0 {
		return Welcome(m.width, m.dir, m.keyName)
	}

	lines := Transcript(m.session, m.width, m.spinnerFrame())
	if m.awaiting {
		// At the bottom of the transcript rather than in a dialogue over it, so the command being
		// approved sits directly under the reasoning that led to it. A modal that covers the
		// conversation asks somebody to decide with the context hidden.
		lines = append(lines, "")
		lines = append(lines, m.promptLines()...)
	}
	return lines
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
	// Above the working line on purpose. A notice is usually a question waiting on the next
	// keystroke, and a spinner saying "working" is not the thing to answer.
	if m.notice != "" {
		return t.Warning.Render("  " + m.notice)
	}
	if below > 0 {
		// Said explicitly, because a view that has silently stopped following the tail looks
		// identical to one where nothing is happening.
		return t.Warning.Render(fmt.Sprintf("  %d more lines below, ctrl+end to follow", below))
	}
	if m.awaiting {
		return t.Warning.Render("  waiting for you")
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
	if m.agentName != "" {
		// First, because with several agents the question "whose conversation am I in" comes before
		// every other thing this line says.
		parts = append(parts, m.agentName)
	}
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
