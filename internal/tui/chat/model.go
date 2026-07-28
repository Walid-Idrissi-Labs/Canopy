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

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
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

	// Trust is how much the agent in this conversation may do, and SetTrust changes it. This pair is
	// what plan mode is made of: the permission layer decides against the level and the tool list
	// the model is shown is filtered by it, so an agent that is planning cannot edit a file by
	// ignoring the instruction to plan. A mode the model could talk its way out of would be worth
	// less than no mode at all, because it would look like a guarantee.
	Trust(sessionID string) core.TrustLevel
	SetTrust(sessionID string, trust core.TrustLevel)
}

// Commands is the catalog resolved for this chat's project.
//
// Re-exported at the input boundary so the application shell can carry it without depending on the
// configuration package directly.
type Commands = config.CommandSet

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

// markInterval is how often the mark on the opening screen redraws.
//
// Six times slower than the spinner, and deliberately. A spinner is telling you something is
// happening and has to keep up with it; the campfire is telling you nothing at all, and the thing
// that makes a fire across a clearing restful rather than distracting is that it moves at about this
// rate. Three frames at this interval is a cycle of a little over two seconds.
const markInterval = 750 * time.Millisecond

// markTickMsg advances the mark, and says which conversation's ticker sent it.
//
// The generation is what stops two tickers running at once. Starting a new conversation schedules a
// tick, and with no way to tell it from the one already in flight the animation would run at double
// speed after the first new chat and faster after every one after that. A tick from a conversation
// that has been left is dropped and not rescheduled, which is also how the old ticker stops.
type markTickMsg struct{ generation int }

func markTick(generation int) tea.Cmd {
	return tea.Tick(markInterval, func(time.Time) tea.Msg {
		return markTickMsg{generation: generation}
	})
}

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

	// commands is resolved for the project this screen belongs to. Expansion happens here, at the
	// input boundary, so the engine receives an ordinary prompt and no model or tool path gains a
	// second command language.
	commands config.CommandSet

	// markStep is where the mark in the corner of the opening screen has got to, and markGeneration
	// says which conversation its ticker belongs to. See markTickMsg.
	markStep       int
	markGeneration int

	// buildTrust is the level to go back to when this conversation leaves plan mode.
	//
	// Remembered rather than assumed, because an agent running at broad trust that planned and then
	// went back to building would otherwise come out at standard. That is a silent demotion, and the
	// kind nobody notices until a command they expected to run stops to ask permission.
	buildTrust core.TrustLevel
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

// Init subscribes to engine events and starts the spinner and the mark.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.subscribe(), tick(), markTick(m.markGeneration))
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

	case markTickMsg:
		if msg.generation != m.markGeneration {
			// A ticker left behind by a conversation that has been closed. Dropped and not
			// rescheduled, which is what ends it.
			return m, nil
		}
		if !m.blank() {
			// The moment there is a conversation there is no opening screen to animate, and the
			// ticker is not scheduled again, so the mark stops costing anything at all rather than
			// going on redrawing something nobody can see. A new conversation starts a new one.
			return m, nil
		}
		m.markStep++
		return m, markTick(m.markGeneration)

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

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// wheelStep is how many lines one notch of the wheel moves.
//
// Three, which is what terminals themselves use when they translate the wheel into arrow keys, so
// scrolling here feels like scrolling anywhere else. One line per notch reads as the program
// ignoring most of the gesture.
const wheelStep = 3

// handleMouse scrolls the conversation.
//
// The wheel is the conversation's, and only the conversation's. It used to be the message box's by
// accident: in the alternate screen most terminals translate the wheel into arrow key sequences, so
// once up and down recalled what you had sent, scrolling back to reread something replaced what you
// were typing with an old message. Asking for mouse events is what stops the translation happening,
// which is the whole reason this exists.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollBy(wheelStep)
	case tea.MouseButtonWheelDown:
		m.scrollBy(-wheelStep)
	}
	return m, nil
}

// scrollBy moves the view, bounded at both ends.
//
// Bounded above by the length of the conversation, because an unbounded count would keep growing
// while somebody spun the wheel at the top and then take the same number of notches to come back
// down, which reads as the scroll having stopped working.
func (m *Model) scrollBy(lines int) {
	m.scroll += lines
	if limit := len(m.transcript()); m.scroll > limit {
		m.scroll = limit
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
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

	case "tab":
		if m.completeCommand() {
			return m, nil
		}
		return m, nil

	case "shift+tab":
		// Beside tab rather than on a letter, because every printable key belongs to the message
		// box, and next to the key that completes a command because both are about what is going to
		// happen rather than about what has been said.
		m.togglePlanning()
		return m, nil

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
		m.scrollBy(m.transcriptHeight() / 2)
		return m, nil

	case "pgdown":
		m.scrollBy(-m.transcriptHeight() / 2)
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

func (m *Model) completeCommand() bool {
	value := m.input.Value()
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") ||
		strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	prefix := strings.TrimPrefix(value, "/")
	var matches []config.ResolvedCommand
	for _, command := range m.commands.All() {
		if strings.HasPrefix(command.Name, prefix) {
			matches = append(matches, command)
		}
	}
	switch len(matches) {
	case 0:
		m.err = fmt.Sprintf("no command begins with /%s; type /commands to list the commands available here",
			prefix)
	case 1:
		m.input.SetValue("/" + matches[0].Name + " ")
		m.notice = matches[0].Description
		m.err = ""
	default:
		names := make([]string, 0, len(matches))
		for _, command := range matches {
			names = append(names, "/"+command.Name)
		}
		m.notice = strings.Join(names, "  ")
		m.err = ""
	}
	return true
}

func (m Model) send() (Model, tea.Cmd) {
	if m.input.Empty() {
		return m, nil
	}

	typed := m.input.Value()
	trimmed := strings.TrimSpace(typed)
	if trimmed == "/commands" {
		m.notice = commandListing(m.commands)
		m.err = ""
		m.input.Clear()
		return m, nil
	}

	prompt := typed
	if strings.HasPrefix(prompt, "//") {
		// One extra slash is the explicit escape for a literal prompt beginning with slash.
		prompt = strings.TrimPrefix(prompt, "/")
	} else {
		expanded, invocation, err := m.commands.Expand(prompt)
		if err != nil {
			m.err = err.Error()
			return m, nil
		}
		if invocation {
			prompt = expanded
		}
	}

	if _, err := m.engine.Send(m.sessionID, prompt); err != nil {
		// The message stays in the box. Clearing it on a failure would mean somebody has to retype
		// what they just wrote because a provider was busy.
		m.err = err.Error()
		return m, nil
	}

	// Filed only once the engine has accepted it, so a message that was refused is still in the box
	// rather than in the box and in the history, which is one message showing up twice.
	// History remembers what the person typed, not the expanded body. Pressing up should offer
	// `/review auth` again rather than a page of generated prompt text.
	m.input.Remember(typed)
	m.input.Clear()
	// Sending returns to the tail. Someone who scrolled up to read something old and then asked a
	// question is asking about now.
	m.scroll = 0
	m.err = ""
	m.notice = ""
	m.refresh()
	return m, nil
}

func commandListing(commands config.CommandSet) string {
	all := commands.All()
	if len(all) == 0 {
		return "no custom commands are available here"
	}

	var lines []string
	for _, command := range all {
		lines = append(lines, fmt.Sprintf("/%s  %s (%s)",
			command.Name, command.Description, command.Scope))
	}
	return strings.Join(lines, "\n")
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
// Returns the command that restarts the mark, since the conversation being arrived in may well be
// an empty one. A method that changed what is on screen and left the caller to work out that
// something now needs driving is how the animation would be live in some new conversations and not
// in others, depending on which key got you there.
func (m *Model) SetSession(sessionID, label string) tea.Cmd {
	if sessionID == m.sessionID {
		return nil
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

	m.markStep = 0
	m.markGeneration++
	return markTick(m.markGeneration)
}

// SetNotice puts a line above the message box.
//
// For the application around this screen, which sometimes needs to say something about the
// conversation without owning any of the conversation's rendering.
func (m *Model) SetNotice(text string) { m.notice = text }

// SetCommands installs the already resolved global and project command catalog.
func (m *Model) SetCommands(commands config.CommandSet) { m.commands = commands }

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

// blank reports whether this conversation still has nothing in it, and so opens on the composed
// screen rather than on a transcript.
//
// A waiting tool call disqualifies it. In practice a conversation with no turns cannot have one, and
// the opening screen has nowhere to put a question, so relying on that rather than checking it is
// how a permission prompt would one day be drawn on a screen that has no room for it.
func (m Model) blank() bool {
	return (!m.loaded || len(m.session.Turns) == 0) && !m.awaiting
}

func (m Model) transcript() []string {
	var lines []string
	if len(m.session.Turns) > 0 {
		lines = Transcript(m.session, m.width, m.spinnerFrame())
	}
	if m.awaiting {
		// At the bottom of the transcript rather than in a dialogue over it, so the command being
		// approved sits directly under the reasoning that led to it. A modal that covers the
		// conversation asks somebody to decide with the context hidden.
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, m.promptLines()...)
	}
	return lines
}

// Planning reports whether this conversation is in plan mode.
//
// Read from the engine rather than from a flag of its own, so there is one answer to the question
// and the box cannot say "build" over a conversation the permission layer is refusing every write
// in. Two sources of truth for a mode is how an interface comes to lie about a guarantee.
func (m Model) Planning() bool {
	return m.engine.Trust(m.sessionID) == core.TrustReadOnly
}

// Mode is the word the box shows: what this conversation is doing.
func (m Model) Mode() string {
	if m.Planning() {
		return "plan"
	}
	return "build"
}

// togglePlanning moves between planning and building.
func (m *Model) togglePlanning() {
	if !m.Planning() {
		m.buildTrust = m.engine.Trust(m.sessionID)
		m.engine.SetTrust(m.sessionID, core.TrustReadOnly)
		m.notice = ""
		return
	}

	if m.buildTrust == "" || m.buildTrust == core.TrustReadOnly {
		// There is nothing to go back to, because this agent is read-only by its own profile rather
		// than because somebody put it in plan mode. Said out loud, since a key that silently does
		// nothing reads as a key that is broken.
		m.notice = "this agent is read-only, so planning is all it can do"
		return
	}
	m.engine.SetTrust(m.sessionID, m.buildTrust)
	m.notice = ""
}

// contextLines are what the opening screen says along its bottom left: where the agent is working
// and what it is talking to.
func (m Model) contextLines() []string {
	t := theme.Current()

	var lines []string
	if m.dir != "" {
		lines = append(lines, t.Muted.Render("working in ")+t.Body.Render(m.dir))
	}
	if m.keyName != "" {
		lines = append(lines, t.Muted.Render("using ")+t.Body.Render(m.keyName))
	} else {
		// The one thing that makes the rest of the program work, said plainly rather than left to be
		// discovered when the first message fails.
		lines = append(lines, t.Warning.Render("no credential yet")+
			t.Muted.Render(", press ")+t.Key.Render("ctrl+k")+t.Muted.Render(" to add one"))
	}
	return lines
}

// transcriptHeight is how many lines are left for the conversation once the input box and the task
// pane have taken what they need.
//
// The task pane is measured rather than assumed, so a list that grows does not push the message box
// off the bottom of the terminal. That is the failure this arithmetic exists to prevent, and it is
// invisible until somebody with a seven item list cannot see what they are typing.
func (m Model) transcriptHeight() int {
	h := m.height - m.input.Height() - 1 // one line for the status row
	if pane := m.taskPane(); pane != "" {
		h -= strings.Count(pane, "\n") + 1
	}
	if h < 1 {
		return 1
	}
	return h
}

// Body renders the screen.
func (m Model) Body() string {
	if m.blank() {
		return opening{
			width:   m.width,
			height:  m.height,
			box:     strings.Split(m.inputBox(), "\n"),
			status:  m.statusRow(0),
			context: m.contextLines(),
			step:    m.markStep,
		}.render()
	}

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

	if tasks := m.taskPane(); tasks != "" {
		b.WriteString("\n")
		b.WriteString(tasks)
	}
	b.WriteString("\n")
	b.WriteString(m.statusRow(len(lines) - end))
	b.WriteString("\n")
	b.WriteString(m.inputBox())
	return b.String()
}

// maxTaskLines is how tall the task pane may grow before it summarises instead.
//
// Six, which covers most lists. Beyond that the pane would be competing with the conversation for
// the screen, and the conversation wins: somebody who wants the whole list can read it, and
// somebody watching an agent work needs to see what it is saying.
const maxTaskLines = 6

// taskPane is the agent's task list, drawn between the conversation and the message box.
//
// Between them rather than in the transcript, because the transcript scrolls and this must not. A
// task list that scrolls out of view is a task list you have to go looking for, and the entire
// value of it is answering "where is this up to" without going looking for anything.
func (m Model) taskPane() string {
	tasks := m.session.Tasks
	if len(tasks) == 0 {
		return ""
	}
	t := theme.Current()

	// A long list collapses to what is happening now plus the counts, rather than being cut off at
	// an arbitrary item. Truncating would hide the end of the list, and the end is where the
	// unfinished work is.
	if len(tasks) > maxTaskLines {
		return t.Info.Render("  tasks  ") + t.Body.Render(core.TaskSummary(tasks))
	}

	var b strings.Builder
	for i, task := range tasks {
		if i > 0 {
			b.WriteString("\n")
		}

		style := t.Muted
		switch task.State {
		case core.TaskInProgress:
			// The one that is happening now is the line the eye should land on.
			style = t.Body
		case core.TaskDone:
			style = t.Muted
		}

		line := "  [" + task.State.Glyph() + "] " + task.Text
		if task.Outcome != "" {
			// The outcome is what makes a finished list worth reading, so it is on the same line as
			// the item rather than folded away behind a key nobody presses.
			line += ", " + task.Outcome
		}
		b.WriteString(style.Render(truncate(line, m.width-2)))
	}
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
	b.WriteString(" " + m.boxTop(topLeft, topRight, horizontal, inner+2) + "\n")
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

// boxTop draws the top edge of the message box with the mode and the model written into it.
//
// On the edge rather than on a line of its own, which is the whole reason it is here: these two
// facts are worth having on screen at all times and are not worth a row of the terminal each. The
// box already draws a rule across the full width and this spends part of it.
//
// Both matter for the same reason. Which model is answering changes what a reply is worth and what
// it costs, and it is set per conversation and per credential, so it is genuinely easy to lose
// track of. The mode matters more: plan and build differ in whether the agent can change your files,
// and a person who thinks they are planning while the agent is building finds out afterwards.
func (m Model) boxTop(left, right, horizontal string, width int) string {
	t := theme.Current()

	label := m.Mode()
	if model := m.session.Model; model != "" {
		label += "  " + model
	}

	// Two for the spaces either side of the label, and two more so the rule is still visibly a rule
	// on both sides rather than a stub. Below that the label is dropped and the plain edge is drawn,
	// because a truncated model name is worse than no model name: it looks like a different model.
	if lipgloss.Width(label)+4 > width {
		return t.Border.Render(left + strings.Repeat(horizontal, width) + right)
	}

	mode := t.Info
	if m.Planning() {
		// Amber, and the word is what carries it. Plan is the narrower mode and the one somebody
		// needs to notice they are in, since it is the difference between an agent that will change
		// files and one that cannot.
		mode = t.Warning
	}

	written := mode.Render(m.Mode())
	if model := m.session.Model; model != "" {
		written += t.Muted.Render("  " + model)
	}

	rest := width - lipgloss.Width(label) - 3
	return t.Border.Render(left+horizontal) + " " + written + " " +
		t.Border.Render(strings.Repeat(horizontal, rest)+right)
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
