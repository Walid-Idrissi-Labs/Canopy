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
	"github.com/charmbracelet/x/ansi"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/clipboard"
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

	// PendingAll is every question waiting on a person anywhere in the project, oldest first.
	//
	// Asked for because a question raised by another agent has to be visible from here: walking to a
	// subagent's screen to find out it was stuck is the attention failure D-47 names. What this
	// screen does with them is deliberately less than what it does with its own, since none of them
	// may take the keyboard from the conversation somebody is actually in.
	PendingAll() []session.Waiting

	// UseCredential points this conversation at a different credential and model.
	UseCredential(sessionID, keyName, model string) error

	// Undo puts the workspace back as it was before a turn, from the checkpoint taken before it ran.
	// The conversation is left alone: undoing the files and deleting the exchange are different
	// things to want, and doing both would throw away the record of what was tried along with the
	// attempt, which is the half worth keeping when something did not work.
	Undo(ctx context.Context, sessionID, turnID string) error

	// Mode is what this conversation's agent is doing, and SetMode changes it. This pair is what a
	// mode is made of: the permission layer decides against the mode's level and the tool list the
	// model is shown is filtered by it, so an agent that is planning cannot edit a file by ignoring
	// the instruction to plan. A mode the model could talk its way out of would be worth less than
	// no mode at all, because it would look like a guarantee.
	//
	// SetMode returns an error rather than nothing, because a mode can lower what an agent may do
	// and can never raise it above what its configuration allows, and a key that silently declines
	// reads as a key that is broken.
	//
	// ModeUnusable is that refusal asked as a question, without making the change. The key offers a
	// mode before it applies one, and a mode offered and then refused two seconds later would be a
	// key that appeared to work.
	Mode(sessionID string) core.Mode
	SetMode(sessionID string, mode core.Mode) error
	ModeUnusable(sessionID string, mode core.Mode) error

	// Fork branches a conversation at a turn, keeping everything said up to it. The original is
	// untouched, which is what makes trying a second approach cheap enough to be worth doing.
	Fork(sessionID, throughTurnID string) (core.Session, error)

	// Trail is the record of every tool call and what was decided about it. Nil where nothing is
	// recording, which is a legitimate state rather than an error: a conversation with no tools
	// attached has nothing to record.
	Trail() *permission.Trail

	// Tools is the registry this conversation was given, used to label a call by what kind of thing
	// it is rather than by guessing from its name.
	//
	// Asked for rather than inferred because the transcript renders calls from tools it has never
	// heard of. A table of known names would label the built in ones and leave everything from an
	// MCP server unlabelled, which is exactly backwards: a tool from somebody else's server is the
	// one where knowing it can run commands matters most. False when no tools are attached.
	Tools() (*core.ToolRegistry, bool)

	// Steer queues a correction for the next turn boundary and never cancels anything. Distinct from
	// Cancel on purpose: correcting an agent by interrupting it throws away the work in progress,
	// which usually means throwing away the reasoning that led to it.
	Steer(sessionID, guidance string) error

	// Steering is the guidance waiting for a session, oldest first. The screen shows it until it is
	// delivered, because a correction that vanishes the moment it is typed reads as one that was
	// swallowed, and somebody who thinks that types it again.
	Steering(sessionID string) []string

	// Aside answers a question from this conversation's context without joining it. No turn is
	// created, nothing joins the conversation's history, and a turn in flight is undisturbed, which
	// is what separates asking something from saying something.
	//
	// Asides is what was asked before, oldest first. The exchange is written down beside the
	// conversation rather than in it: recording an aside and putting it in the model's context are
	// different things, and only the second would change what the agent knows.
	Aside(ctx context.Context, sessionID, question string) (string, error)
	Asides(sessionID string) []session.Aside
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

// modeSettleDelay is how long the mode key waits after the last press before the mode it stopped on
// is applied.
//
// **Cycling past a mode is not choosing it.** The key walks a ladder, so reaching build from cruise
// goes through plan on the way, and a mode takes hold at the next tool call rather than at the next
// message. Applying each one as it went past would hand a working agent the read-only level for a
// fraction of a second, and whichever tool call landed in that fraction would be refused by a mode
// nobody meant to be in. The agent then spends a turn arguing with a boundary that has already gone.
//
// Two seconds, which is longer than the gap between deliberate presses and short enough that the
// mode is in effect by the time somebody has finished reading the box. The wait is never the last
// word: sending a message, naming a mode outright, leaving the conversation and quitting all apply
// the selection at once, because each of those is somebody who has stopped cycling.
//
// A variable only so the test binary can shorten it. Nothing outside a test changes it: how long
// the key waits is a decision about how it feels, not configuration.
var modeSettleDelay = 2 * time.Second

// modeSettleMsg applies the mode the key stopped on, and says which selection it belongs to.
//
// The generation is what makes this a wait rather than a queue. Every press supersedes the one
// before it, so the timer from a mode that was only passed through arrives, finds a newer
// generation, and is dropped. Without it, walking the ladder would apply every rung along it two
// seconds late, which is the bug this exists to prevent with a delay bolted on.
type modeSettleMsg struct{ generation int }

func settleMode(generation int) tea.Cmd {
	return tea.Tick(modeSettleDelay, func(time.Time) tea.Msg {
		return modeSettleMsg{generation: generation}
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

	// workingSince is when this screen first saw the turn in flight, cleared when it lands.
	//
	// The screen's clock rather than the turn's, for the reason callSeen carries below: a Turn has
	// timestamps but reading them means the interface and the engine disagreeing about which of two
	// clocks a number came from. What is shown is how long the screen has been watching, which is
	// what the person in front of it is actually asking.
	workingSince time.Time

	// expanded lifts the caps on diffs, tool output and error text, toggled with ctrl+o.
	//
	// One switch for the whole transcript rather than a state per call. Per-call folding needs a
	// cursor that can land on a call, and the transcript is deliberately a flat list of lines with no
	// such cursor; adding one to fold a diff would be rebuilding the scroll model to answer a
	// question that "show me everything" answers.
	expanded bool

	// callSeen is when this screen first saw a tool call with no result, by call ID.
	//
	// The clock for the "running for" label. Kept here rather than on the call because a ToolCall
	// carries no start time and internal/core is frozen under the P1-01 rule, and kept as first-seen
	// rather than pretending to be a start time, which is what the label says out loud.
	callSeen map[string]time.Time

	// compacting is true while a summary is being produced, which is a model call and takes as long
	// as any other. Without it the interface looks frozen and people press the key again.
	compacting bool

	// prompt is the tool call waiting on an answer, when there is one.
	prompt   session.Prompt
	awaiting bool

	// visitors are the questions other conversations are waiting on, oldest first.
	//
	// Shown here so that a subagent stuck behind a permission prompt is discovered from wherever
	// somebody is sitting rather than by going to look. They are held apart from prompt and awaiting
	// on purpose: those two own the keyboard the moment they exist, and none of these ever may.
	visitors []session.Waiting

	// visitorFocus is the conversation whose question has been handed the keyboard, empty when the
	// keyboard belongs to this conversation.
	//
	// The step D-47 puts between seeing another agent's question and answering it. Without it, the
	// y somebody types into their own conversation would spend a permission in a conversation they
	// are not even looking at, which is the same reflex D-43 forbids at one more remove.
	//
	// A session id rather than a flag, because a flag focuses whatever is at the front of the queue
	// at the moment the key lands, and the front can change between taking the focus and using it:
	// the question somebody walked to can be answered on its own screen or from the agents view, and
	// then the y they press arrives at whoever moved up. Focus is a claim on one question.
	visitorFocus string

	// answeredVisitors are questions answered from this screen that the engine still lists, because
	// the goroutine the answer unblocked has not woken up yet.
	//
	// Held only for that window. Without it the next engine event, and one arrives for every agent
	// in the project, rebuilds the panel from PendingAll and puts the just answered question back on
	// screen with the cursor on it, which is an invitation to answer it twice.
	answeredVisitors []string

	// commands is resolved for the project this screen belongs to. Expansion happens here, at the
	// input boundary, so the engine receives an ordinary prompt and no model or tool path gains a
	// second command language.
	commands config.CommandSet

	// markStep is where the mark in the corner of the opening screen has got to, and markGeneration
	// says which conversation its ticker belongs to. See markTickMsg.
	markStep       int
	markGeneration int
	// True only while a mark tick is outstanding. A completed conversation draws static coals,
	// so keeping a second animation timer alive there would redraw identical pixels forever.
	markRunning bool

	// menu is the command list that drops out of the message box.
	menu menu

	// asides is every btw asked in this conversation, oldest first, and btwOpen is whether the panel
	// showing them is up.
	//
	// It used to leave with the screen, because the engine deliberately recorded nothing about an
	// aside and this slice was the whole of the history there was. Recording it turned out to be a
	// different question from putting it in the context: an aside still never reaches the model, and
	// it is now written down beside the conversation, so opening one tomorrow opens its side
	// questions with it. This is loaded from the engine when the screen moves to a conversation, and
	// appended to as answers arrive.
	asides []asideExchange

	btwOpen bool
	// btwScroll is how many lines up from the bottom of the panel the view is held, zero when
	// following the latest answer.
	btwScroll int

	// sel is the mouse selection over the conversation, and clip is what puts its text on the
	// clipboard. A function rather than a call, so tests can catch the text instead of writing to
	// the machine's actual clipboard from a test run.
	sel  selection
	clip func(string) error

	// copied is whether the "copied" confirmation is up, and copiedGeneration is which copy it
	// belongs to, so the timer from an old copy cannot take down a newer one's message.
	copied           bool
	copiedGeneration int

	// pendingMode is the mode the key has stopped on and has not applied yet, empty when the key is
	// not in the middle of anything, and modeGeneration says which selection the outstanding timer
	// belongs to. See modeSettleMsg.
	pendingMode    core.Mode
	modeGeneration int
}

// asideExchange is one btw question and the answer it got.
type asideExchange struct {
	question string
	answer   string
}

// loadAsides reads what this conversation has been asked on the side.
//
// Replaces rather than merges, because the answer the engine holds is the whole history and anything
// already on screen is part of it. Called wherever the screen changes which conversation it is
// showing, so the panel is about the conversation in front of somebody and never about the last one.
func (m *Model) loadAsides() {
	stored := m.engine.Asides(m.sessionID)

	asides := make([]asideExchange, 0, len(stored))
	for _, aside := range stored {
		asides = append(asides, asideExchange{question: aside.Question, answer: aside.Answer})
	}
	m.asides = asides
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
		clip:      clipboard.Write,
	}
	m.refresh()
	m.markRunning = m.markVisible()
	m.input.LoadHistory(promptsOf(m.session))
	// The conversation Canopy opens on arrives here rather than through SetSession, so its side
	// questions are read here too. Without this the one conversation somebody actually lands in
	// would be the one that had forgotten them.
	m.loadAsides()
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
	commands := []tea.Cmd{m.subscribe(), tick()}
	if m.markRunning {
		commands = append(commands, markTick(m.markGeneration))
	}
	return tea.Batch(commands...)
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
		return m, tea.Batch(tick(), m.ensureMark())

	case markTickMsg:
		if msg.generation != m.markGeneration {
			// A ticker left behind by a conversation that has been closed. Dropped and not
			// rescheduled, which is what ends it.
			return m, nil
		}
		if !m.markVisible() {
			m.markRunning = false
			return m, nil
		}
		m.markStep++
		return m, markTick(m.markGeneration)

	case modeSettleMsg:
		if msg.generation != m.modeGeneration {
			// The timer from a mode the key has already moved past. Dropped, which is the whole
			// point: it was walked through on the way somewhere and never chosen.
			return m, nil
		}
		m.applyPendingMode()
		return m, nil

	case asideMsg:
		if msg.err != nil {
			m.err = msg.err.Error()
			m.notice = ""
			return m, nil
		}
		// Into the panel above the box rather than folded into the transcript, because an aside is
		// not part of the conversation and putting it there would make it look like one. The panel
		// keeps every aside asked here, so an answer half remembered from twenty minutes ago is a
		// bare /btw away rather than gone, and it opens on the newest.
		m.asides = append(m.asides, asideExchange{question: msg.question, answer: msg.answer})
		m.btwOpen = true
		m.btwScroll = 0
		m.notice = ""
		m.err = ""
		return m, nil

	case undoneMsg:
		m.notice = ""
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, nil
		}
		m.notice = "the workspace is back as it was before the last turn, and the conversation is unchanged"
		m.refresh()
		return m, nil

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

	case copiedClearMsg:
		// Only the timer that belongs to the message on screen takes it down. See finishSelection.
		if msg.generation == m.copiedGeneration {
			m.copied = false
		}
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

// handleMouse scrolls the conversation with the wheel, and selects it with a drag.
//
// The wheel is the conversation's, and only the conversation's. It used to be the message box's by
// accident: in the alternate screen most terminals translate the wheel into arrow key sequences, so
// once up and down recalled what you had sent, scrolling back to reread something replaced what you
// were typing with an old message. Asking for mouse events is what stops the translation happening,
// which is the whole reason this exists — and it is also what takes the terminal's own
// drag-to-select away, which the drag handling below gives back. See select.go.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	switch msg.Action {
	case tea.MouseActionPress:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollBy(wheelStep)
		case tea.MouseButtonWheelDown:
			m.scrollBy(-wheelStep)
		case tea.MouseButtonLeft:
			m.beginSelection(msg.X, msg.Y)
		}
		return m, nil

	case tea.MouseActionMotion:
		m.extendSelection(msg.X, msg.Y)
		return m, nil

	case tea.MouseActionRelease:
		if m.sel.dragging {
			return m.finishSelection()
		}
		return m, nil
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

// visitorFocusKey is the one keystroke that hands the keyboard to another agent's question.
//
// ctrl+g because it is free: every printable key belongs to the message box, and ctrl+a, ctrl+e,
// ctrl+u, ctrl+w and ctrl+j are the box's own editing keys, while ctrl+c, ctrl+d, ctrl+k, ctrl+n and
// ctrl+r already mean something on this screen. It is also deliberately not y or a: the two keys
// that answer must be unreachable until somebody has said which question they are answering.
const visitorFocusKey = "ctrl+g"

// answerVisitor answers the question the focus was taken for, on behalf of the agent that asked.
//
// The question, not the front of the queue. They are almost always the same one and the exception is
// the one that matters: a question answered on its own screen while somebody here was reaching for
// the keyboard leaves the queue, whoever was behind it moves up, and a focus that meant "the front"
// would spend that keystroke on an agent nobody had looked at.
func (m Model) answerVisitor(msg tea.KeyMsg) (Model, tea.Cmd) {
	asking, ok := m.focusedVisitor()
	if !ok {
		// The question this focus was taken for is gone. The keyboard goes back to the conversation
		// rather than moving on to the next waiter, because taking a focus is a decision about one
		// agent and inheriting it is not a decision anybody made.
		m.visitorFocus = ""
		return m, nil
	}

	switch msg.String() {
	case "y":
		m.engine.Answer(asking.SessionID, true, false)
	case "a":
		m.engine.Answer(asking.SessionID, true, true)
	case "n":
		m.engine.Answer(asking.SessionID, false, false)
	case "esc":
		m.visitorFocus = ""
		return m, nil
	default:
		return m, nil
	}

	// Dropped here as well as in the engine, so the panel advances on this keystroke. Answering
	// hands the reply to a goroutine that is still parked, and the entry does not leave the engine
	// until that goroutine wakes, so re-reading now would put the answered question back on screen.
	// Remembered as answered for exactly as long as the engine goes on listing it, which is what
	// stops the next event undoing this.
	m.answeredVisitors = append(append([]string(nil), m.answeredVisitors...), asking.SessionID)
	m.visitors = withoutVisitor(m.visitors, asking.SessionID)
	m.visitorFocus = ""
	return m, nil
}

// focusedVisitor is the question the keyboard was handed to, and whether it is still waiting.
func (m Model) focusedVisitor() (session.Waiting, bool) {
	if m.visitorFocus == "" {
		return session.Waiting{}, false
	}
	for _, waiting := range m.visitors {
		if waiting.SessionID == m.visitorFocus {
			return waiting, true
		}
	}
	return session.Waiting{}, false
}

// focusedOn reports whether a question is the one holding the keyboard.
func (m Model) focusedOn(waiting session.Waiting) bool {
	return m.visitorFocus != "" && m.visitorFocus == waiting.SessionID
}

func withoutVisitor(waiting []session.Waiting, sessionID string) []session.Waiting {
	out := make([]session.Waiting, 0, len(waiting))
	for _, one := range waiting {
		if one.SessionID != sessionID {
			out = append(out, one)
		}
	}
	return out
}

// promptLines renders the question.
func (m Model) promptLines() []string {
	t := theme.Current()
	req := m.prompt.Request

	var body []string
	body = append(body, t.Warning.Render("This agent wants to "+describeRequest(req)))
	body = append(body, "")

	// The thing being approved, shown verbatim and in full. A command summarised or truncated is a
	// command somebody approved without having seen it.
	//
	// For a tool whose arguments Canopy did not define, that is the arguments themselves and not the
	// fields below: those are picked out by looking for names like "path", which means nothing on a
	// remote server, and what an approval covers there is the exact call. Offering "always, this tool
	// with exactly these arguments" while showing none of them asks somebody to agree to something
	// they cannot see.
	inner := m.width - promptChrome
	switch {
	case req.Opaque && req.Arguments != "":
		for _, line := range wrap(req.Arguments, inner) {
			body = append(body, t.Body.Render(line))
		}
	default:
		if req.Command != "" {
			for _, line := range wrap(req.Command, inner) {
				body = append(body, t.Body.Render(line))
			}
		}
		for _, path := range req.Paths {
			body = append(body, t.Body.Render(path))
		}
	}

	body = append(body, "")
	body = append(body, t.Muted.Render(m.prompt.Decision.Reason))
	body = append(body, "")
	body = append(body,
		t.Key.Render("y")+t.Muted.Render(" once   ")+
			t.Key.Render("a")+t.Muted.Render(" always, "+m.prompt.Scope().String()+"   ")+
			t.Key.Render("any other key")+t.Muted.Render(" no"))

	return promptPanel(body, inner)
}

// visitorPanel is another agent's question, above the message box.
//
// Deliberately smaller than the conversation's own prompt. It wears the same frame, so it is
// recognisable as the same kind of thing from across the room, and it says four lines at most:
// who is asking, what they want, how many others are behind them, and which key hands it the
// keyboard. The full detail lives on that agent's own screen, which is where somebody who wants to
// read a command in full is one keystroke away from being.
//
// While this conversation has a question of its own the panel shrinks to a single line. Your own
// prompt outranks a visitor, and two heavy boxes stacked over one message box would be two things
// competing to be answered first, but the count still has to be there or the second agent waits
// unseen until the first is dealt with.
func (m Model) visitorPanel() []string {
	if len(m.visitors) == 0 {
		return nil
	}
	t := theme.Current()
	asking := m.visitors[0]
	focused := m.focusedOn(asking)

	if m.awaiting {
		// Shrunk to a count while this conversation has a question of its own, which outranks a
		// visitor. It says whether the panel holds the keyboard either way: a focus nobody can see
		// is a focus that answers for somebody without their knowing, so this line is written so
		// that state cannot exist unsaid. Your own prompt drops the focus, so the second half is
		// belt and braces, and belt and braces is what this particular sentence is for.
		if focused {
			return []string{"  " + t.Warning.Render(othersWaiting(m.visitors)+
				", and your keys still answer it, esc to stop")}
		}
		return []string{"  " + t.Muted.Render(othersWaiting(m.visitors)+", "+
			visitorFocusKey+" after this one")}
	}

	inner := m.width - promptChrome
	if inner < 12 {
		inner = 12
	}

	// Nothing bounds an agent's name or a scope's text, and both are drawn on a line with a wall at
	// the end of it, so both are cut here. The full command and the full scope are on the asking
	// agent's own screen, which is where somebody deciding on the detail should be.
	var body []string
	body = append(body, t.Warning.Render(truncate(asking.Agent, inner/3))+
		t.Body.Render(" wants to "+describeRequest(asking.Request)))

	// One line of what it actually is, truncated rather than wrapped. A command shown in full is
	// what the asking agent's own screen is for; here the job is to say which agent needs a person,
	// and a panel that grows with the command would push the conversation off the screen.
	if what := requestSubject(asking.Request); what != "" {
		body = append(body, t.Muted.Render(truncate(what, inner)))
	}
	if len(m.visitors) > 1 {
		body = append(body, t.Muted.Render(othersWaiting(m.visitors[1:])))
	}

	if focused {
		// Said in words before the keys are offered. The keys alone imply the panel has the
		// keyboard and do not state it, and the thing somebody has to be able to see at a glance is
		// exactly that their next keystroke is going somewhere other than their own conversation.
		body = append(body, t.Warning.Render("your keys answer this one until esc"))

		const fixed = len("y once   a always,    n no   esc leave it")
		body = append(body,
			t.Key.Render("y")+t.Muted.Render(" once   ")+
				t.Key.Render("a")+t.Muted.Render(" always, "+
				truncate(asking.Scope().String(), inner-fixed)+"   ")+
				t.Key.Render("n")+t.Muted.Render(" no   ")+
				t.Key.Render("esc")+t.Muted.Render(" leave it"))
	} else {
		body = append(body, t.Key.Render(visitorFocusKey)+
			t.Muted.Render(" to answer it, and nothing else here will"))
	}
	return promptPanel(body, inner)
}

// othersWaiting is how many agents are queued behind the one being shown, in words.
func othersWaiting(waiting []session.Waiting) string {
	if len(waiting) == 1 {
		return waiting[0].Agent + " is waiting on you"
	}
	return fmt.Sprintf("%d agents are waiting on you", len(waiting))
}

// requestSubject is the one line worth showing about a call somebody else's agent wants to make.
func requestSubject(req permission.Request) string {
	switch {
	case req.Command != "":
		return req.Command
	case len(req.Paths) > 0:
		return strings.Join(req.Paths, " ")
	case req.Opaque && req.Arguments != "":
		return req.Arguments
	default:
		return ""
	}
}

// promptChrome is the columns the question's frame spends on itself: an indent, two walls and a
// space of padding inside each.
const promptChrome = 8

// promptPanel puts the question in a box of its own.
//
// It used to be plain lines at the bottom of the transcript, which put the most consequential thing
// on the screen in the same shape as everything the agent had been saying up to it. A person
// answering this is about to let something run on their machine, and the moment they are asked
// should not look like more conversation.
//
// The frame is drawn in the warning colour rather than the border colour, so the box itself carries
// the signal and the answer does not depend on somebody reading the first line. It sits inside the
// transcript rather than over it, which is the existing decision and the right one: a modal covering
// the conversation asks somebody to decide with the context hidden.
//
// Two things make it unmistakable among the interface's other boxes, because the first version was
// not: every other frame on screen is a thin rounded line, so this one is heavy, and its top edge
// carries "needs you" in reverse video. The heavy border says "different in kind" the way a light
// one cannot, and reverse video is the one emphasis that survives NO_COLOR, a monochrome theme and
// a terminal palette that renders the warning colour dull. Somebody glancing back at a screen they
// left should find this box before they find anything else on it.
func promptPanel(body []string, inner int) []string {
	t := theme.Current()

	const indent = "  "

	// The chip is drawn from the warning style so the reversal shows the warning colour as the
	// background, which is the loudest thing this palette can say.
	chip := t.Warning.Reverse(true).Bold(true).Render(" needs you ")
	top := t.Warning.Render("┏━") + chip
	if rest := inner + 1 - lipgloss.Width(ansi.Strip(chip)); rest > 0 {
		top += t.Warning.Render(strings.Repeat("━", rest) + "┓")
	} else {
		// Too narrow for the label, and the border matters more: a heavy frame with no title is
		// still obviously not conversation.
		top = t.Warning.Render("┏" + strings.Repeat("━", inner+2) + "┓")
	}

	out := make([]string, 0, len(body)+2)
	out = append(out, indent+top)
	for _, line := range body {
		pad := inner - lipgloss.Width(ansi.Strip(line))
		if pad < 0 {
			pad = 0
		}
		out = append(out, indent+t.Warning.Render("┃")+" "+line+strings.Repeat(" ", pad)+
			" "+t.Warning.Render("┃"))
	}
	return append(out, indent+t.Warning.Render("┗"+strings.Repeat("━", inner+2)+"┛"))
}

// describeRequest says what is being asked for in words rather than in tool names.
//
// "run a command" is something somebody can decide about at two in the morning. "run_command" is a
// symbol from a codebase they have never read.
func describeRequest(req permission.Request) string {
	// The dispatch confirmation arrives as an execute, which is honest about its breadth and wrong
	// as a description: nobody reading "run a command" hears "start agents that will spend money on
	// your account", and that is the one fact this particular question turns on.
	if req.Tool == "spawn_agents" {
		return "start more agents, on your account and at your expense"
	}
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

	// Another agent's question, and the two keystrokes it takes to answer one.
	//
	// While the panel has been focused it holds the keys that answer, and only those: a key that
	// means nothing here does nothing, rather than refusing on somebody else's behalf the way an
	// unrecognised key refuses your own question above. Your own question is one you are looking
	// at; this one you had to walk to, and a stray keystroke should not spend it either way.
	if m.visitorFocus != "" {
		return m.answerVisitor(msg)
	}
	if msg.String() == visitorFocusKey && len(m.visitors) > 0 {
		// The front of the queue, which is the one the panel is showing, remembered by name so the
		// keys that follow answer that agent and not whoever happens to be at the front by then.
		m.visitorFocus = m.visitors[0].SessionID
		return m, nil
	}

	// The list takes the keys that mean something in a list, and only those, and only while it is
	// up. Everything else still reaches the box, so a command can go on being typed without the
	// menu having to hand each character back.
	if m.menu.open {
		switch msg.String() {
		case "up":
			m.menu.move(-1)
			return m, nil
		case "down":
			m.menu.move(1)
			return m, nil
		case "esc":
			// Closes the list and leaves what was typed. Escape also stops a running turn, and that
			// still happens on the next press: dismissing a menu is the more local meaning and gets
			// the first one.
			m.menu = menu{}
			return m, nil
		case "tab":
			// Tab always completes and never sends, which is the split every shell uses.
			if m.acceptFromMenu() {
				return m, nil
			}
		case "enter":
			// Enter completes when there is something left to complete, and sends when there is
			// not. Without the second half, typing a command out in full and pressing enter would
			// put the name you already typed back in the box and do nothing, which reads as the
			// key having stopped working.
			if chosen, ok := m.menu.chosen(); ok && m.input.Value() != "/"+chosen.name {
				m.acceptFromMenu()
				return m, nil
			}
			m.menu = menu{}
		}
	}

	// The btw panel takes the keys that mean something to a panel, and only those, and only while
	// it is up. Everything else still reaches the box, so a message can go on being typed over it.
	if m.btwOpen {
		switch msg.String() {
		case "esc":
			// The more local meaning gets the first press, which is the menu's rule too: stopping
			// a turn or clearing the box is still one more press away.
			m.btwOpen = false
			return m, nil
		case "pgup":
			m.btwScrollBy(btwVisible / 2)
			return m, nil
		case "pgdown":
			m.btwScrollBy(-btwVisible / 2)
			return m, nil
		}
	}

	switch msg.String() {
	case "enter":
		return m.send()

	case "tab":
		return m, nil

	case "shift+tab":
		// Beside tab rather than on a letter, because every printable key belongs to the message
		// box, and next to the key that completes a command because both are about what is going to
		// happen rather than about what has been said.
		return m, m.cycleMode()

	case "esc":
		// Stops the turn rather than leaving the screen. With a reply streaming, escape means stop,
		// and a screen that navigated away instead would abandon a running turn out of sight.
		if m.working {
			m.engine.Cancel(m.sessionID)
			return m, nil
		}
		// With nothing running, escape clears a half written message, which is what it means in
		// every comparable tool. Before this the only way to abandon a draft was ctrl+u, a key
		// the footer never mentions.
		if !m.input.Empty() {
			m.input.Clear()
			m.refreshMenu()
			return m, nil
		}
		return m, nil

	case "ctrl+r":
		return m.compact()

	case "ctrl+o":
		// The reading view and the checking view, on one key. Scroll is held rather than reset,
		// because the reason somebody expands is that they are looking at something particular and
		// want more of it, and moving the screen under them would lose exactly that.
		m.expanded = !m.expanded
		if m.expanded {
			m.notice = "showing full output, ctrl+o to fold it back"
		} else {
			m.notice = ""
		}
		return m, nil

	case "pgup":
		m.scrollBy(m.transcriptHeight() / 2)
		return m, nil

	case "pgdown":
		m.scrollBy(-m.transcriptHeight() / 2)
		return m, nil

	case "ctrl+home":
		m.scroll = len(m.transcript())
		return m, nil

	case "ctrl+end", "ctrl+down":
		// Two keys for one thing, because ctrl+end is the one a terminal veteran reaches for and
		// ctrl+down is the one somebody guesses from the arrow they were already scrolling with. The
		// jump-to-bottom pill names ctrl+down for that reason: it is the one you can work out.
		m.scroll = 0
		return m, nil
	}

	if m.input.Update(msg) {
		m.err = ""
		m.refreshMenu()
		return m, nil
	}
	return m, nil
}

// acceptFromMenu puts the highlighted command in the box, and reports whether there was one.
//
// The name and a trailing space rather than sending it outright. Half of these take arguments, and a
// menu that sent on enter would make the ones that do unreachable from the menu at all. It is also
// what happens elsewhere: the tab key completed rather than submitted before this list existed.
func (m *Model) acceptFromMenu() bool {
	chosen, ok := m.menu.chosen()
	if !ok {
		return false
	}
	m.input.SetValue("/" + chosen.name + " ")
	m.menu = menu{}
	m.notice = chosen.description
	m.err = ""
	return true
}

func (m Model) send() (Model, tea.Cmd) {
	if m.input.Empty() {
		return m, nil
	}

	typed := m.input.Value()
	trimmed := strings.TrimSpace(typed)

	// What Canopy answers itself, before anything is expanded or sent. These never reach a provider
	// and never cost anything, so they are decided before the path that does either.
	if name, arguments, ok := builtinInvocation(trimmed); ok {
		if handled, cmd := m.runBuiltin(name, arguments); handled {
			m.input.Remember(typed)
			m.input.Clear()
			m.menu = menu{}
			return m, cmd
		}
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

	// A mode the key stopped on governs the message about to be sent, rather than arriving two
	// seconds into it. Pressing shift+tab and then enter is somebody who has chosen.
	m.applyPendingMode()

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

// The listing moved to builtin.go, so that what Canopy ships and what a project defines are printed
// by one function rather than by two that can come to disagree about the format.

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
	wasWorking := m.working
	_, m.working = current.Active()
	switch {
	case m.working && !wasWorking:
		m.workingSince = time.Now()
	case !m.working:
		m.workingSince = time.Time{}
	}
	m.prompt, m.awaiting = m.engine.Pending(m.sessionID)
	m.noteCalls(current)

	// Every other conversation's questions, read the same way and at the same moment, so the panel
	// cannot disagree with the conversation it is drawn above. Events arrive here for every session
	// rather than only this one, which is what makes this current without any extra plumbing.
	// A fresh slice rather than the old one truncated, because this model is copied by value
	// everywhere and two copies sharing a backing array would let one of them rewrite the other's
	// panel from under it.
	var visitors []session.Waiting
	var stillListed []string
	for _, waiting := range m.engine.PendingAll() {
		if waiting.SessionID == m.sessionID {
			continue
		}
		if answeredHere(m.answeredVisitors, waiting.SessionID) {
			// Answered from this screen a moment ago and still on the engine's list, because the
			// goroutine holding it has not woken. Kept out of the panel and kept in the note, so it
			// stays out until the engine itself stops listing it.
			stillListed = append(stillListed, waiting.SessionID)
			continue
		}
		visitors = append(visitors, waiting)
	}
	m.visitors = visitors
	m.answeredVisitors = stillListed

	// Focus is a claim on one question that is still waiting, and it survives neither that question
	// leaving nor this conversation being asked something of its own.
	//
	// The second is the half that mattered. Your own prompt takes the keyboard the moment it exists,
	// so a focus taken before it arrived would sit there invisibly through the whole exchange, and
	// the y that answered your own question would be followed by whatever you typed next landing on
	// somebody else's. Cleared in the same statement that sets awaiting, so no ordering of events
	// can slip between the two.
	if _, waiting := m.focusedVisitor(); !waiting || m.awaiting {
		m.visitorFocus = ""
	}
}

// noteCalls remembers when each unanswered tool call was first seen, and forgets the answered ones.
//
// The map is rebuilt from the session on every refresh rather than added to, so it cannot outgrow the
// conversation: a call that has come back is not in it, and a turn that was compacted away takes its
// calls with it. Timestamps for calls already being watched are carried over, because a first-seen
// time that resets on every frame would render as a counter stuck at zero.
func (m *Model) noteCalls(current core.Session) {
	var seen map[string]time.Time
	now := time.Now()
	for _, turn := range current.Turns {
		for _, call := range turn.ToolCalls {
			if resultFor(turn, call) != nil {
				continue
			}
			if seen == nil {
				seen = make(map[string]time.Time, len(turn.ToolCalls))
			}
			if started, ok := m.callSeen[call.ID]; ok {
				seen[call.ID] = started
				continue
			}
			seen[call.ID] = now
		}
	}
	m.callSeen = seen
}

// answeredHere reports whether a question was answered from this screen and is still being listed.
func answeredHere(answered []string, sessionID string) bool {
	for _, id := range answered {
		if id == sessionID {
			return true
		}
	}
	return false
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
	// A selection belongs to the conversation it was made in, so it is applied before leaving rather
	// than carried across or thrown away. Somebody who pressed the key and then walked away still
	// pressed the key.
	m.applyPendingMode()
	m.sessionID = sessionID
	m.agentName = label
	m.scroll = 0
	m.err = ""
	m.notice = ""
	// The asides go with the conversation they were asked about. Carrying them across would show
	// answers about one agent's work over another agent's conversation, so the ones on screen are
	// dropped and that conversation's own are read in their place. The selection goes too, since it
	// is a place in a transcript that is no longer on screen.
	m.loadAsides()
	m.btwOpen = false
	m.btwScroll = 0
	m.sel = selection{}
	m.copied = false
	m.input.Clear()
	m.refresh()
	// History belongs to the conversation, not to the box. Carrying it across would offer you, on
	// the first press of up, the message you sent to a different agent.
	m.input.LoadHistory(promptsOf(m.session))

	m.markStep = 0
	m.markGeneration++
	m.markRunning = m.markVisible()
	if !m.markRunning {
		return nil
	}
	return markTick(m.markGeneration)
}

func (m Model) markVisible() bool {
	return m.blank() || m.working || m.compacting
}

// ensureMark starts the mark ticker on the transition from static to animated. Both the engine event
// and spinner refresh paths call it; markRunning makes those observations converge on one timer.
func (m *Model) ensureMark() tea.Cmd {
	if m.markRunning || !m.markVisible() {
		return nil
	}
	m.markRunning = true
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

// UseCredential switches this conversation to a different credential and model, and reports whether
// the engine accepted.
//
// A refusal is shown rather than swallowed. Choosing a credential and having nothing visibly happen
// is how somebody concludes the screen does not work, which is exactly what it looked like before
// there was any way to choose at all.
//
// It is also returned rather than only shown. The engine refuses mid answer, and the screen around
// this one keeps its own note of which credential is in use for the conversations it starts next; a
// void return let that note move to a credential this conversation had just failed to switch to, so
// the next new conversation opened on a key nobody had successfully chosen, with no model.
func (m *Model) UseCredential(keyName, model string) bool {
	if err := m.engine.UseCredential(m.sessionID, keyName, model); err != nil {
		m.err = err.Error()
		return false
	}
	m.keyName = keyName
	m.err = ""
	m.refresh()
	return true
}

// SessionID is the conversation being shown.
func (m Model) SessionID() string { return m.sessionID }

// AgentName is whose conversation this is, empty where no agent owns it.
//
// Read by the frame around this screen, which writes it in the corner. It is not repeated in the
// facts row: the same word twice on one header is one of them wasted.
func (m Model) AgentName() string { return m.agentName }

// SetAgent names the agent whose conversation this is.
//
// Needed because the conversation Canopy opens on is handed to this screen at construction, before
// anything has switched into it, and the agent that owns it is named by whoever started it. Every
// later switch carries the name through SetSession instead.
func (m *Model) SetAgent(name string) { m.agentName = name }

// KeyName is the credential this conversation runs on.
func (m Model) KeyName() string { return m.keyName }

// ModelName is the model this conversation runs on.
//
// Read off the session rather than held here, because the session is the thing the engine actually
// sends on and a second copy in this screen would be a second copy to keep in step.
func (m Model) ModelName() string { return m.session.Model }

// Awaiting reports whether a question is on screen. The frame uses it to change the footer, since
// the keys mean something different while one is up.
func (m Model) Awaiting() bool { return m.awaiting }

// Input exposes the message box. For tests.
func (m Model) InputValue() string { return m.input.Value() }

// SetClipboard replaces what a finished selection writes to. For tests, which want to catch the
// text rather than write to the machine's actual clipboard from a test run.
func (m *Model) SetClipboard(write func(string) error) { m.clip = write }

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

// Blank reports whether this conversation is still on its opening screen.
//
// Exported for the shell, which draws the name in the header only once the opening screen has gone.
// Two copies of the name on one screen, one in the middle and one in the corner, is one too many.
func (m Model) Blank() bool { return m.blank() }

func (m Model) transcript() []string {
	var lines []string
	if len(m.session.Turns) > 0 {
		lines = TranscriptWith(m.session, m.width, m.spinnerFrame(), m.toolKind, Detail{
			Expanded: m.expanded,
			Now:      time.Now(),
			Started:  m.callSeen,
		})
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
func (m Model) Planning() bool { return m.Mode() == core.ModePlan }

// Mode is the word the box shows: what this conversation is doing.
//
// Read from the engine rather than from a flag of its own, so there is one answer to the question
// and the box cannot say "build" over a conversation the permission layer is refusing every write
// in. Two sources of truth for a mode is how an interface comes to lie about a guarantee.
func (m Model) Mode() string { return m.engine.Mode(m.sessionID).Name }

// Selecting is the mode the key has stopped on that is not in effect yet, and whether there is one.
//
// Kept apart from Mode because they are different facts and an interface that ran them together
// would be lying about the more important of the two. The mode in effect is a guarantee about what
// the agent can do to your files. A selection is where a key has got to on the way somewhere, and
// showing it under the same name would put "plan" over a conversation the permission layer was
// still letting write, which is exactly the failure Mode's own comment exists to prevent.
func (m Model) Selecting() (string, bool) {
	if m.pendingMode.Name == "" {
		return "", false
	}
	return m.pendingMode.Name, true
}

// cycleMode moves the selection to the next mode in the ladder and starts the wait before it takes
// effect. See modeSettleDelay for why the change is not made here.
//
// Skips past any the agent cannot be put in rather than stopping on them, so the key always does
// something on an agent that has a ceiling below the top of the ladder. Stopping would mean a
// confined agent whose key appeared to have jammed, and refusing outright would mean it could not
// reach plan mode either, which it certainly can.
//
// The engine is asked which modes are reachable rather than told to enter one and allowed to
// refuse, now that entering one happens later. A key that offered a mode and then had it rejected
// after the fact would report the refusal over a conversation that had moved on.
func (m *Model) cycleMode() tea.Cmd {
	// From where the key is, which is the selection while there is one. Otherwise a second press
	// would set off from the mode still in effect and land on the same rung again.
	current := m.Mode()
	if m.pendingMode.Name != "" {
		current = m.pendingMode.Name
	}

	for range core.Modes() {
		next := core.NextMode(current)
		if err := m.engine.ModeUnusable(m.sessionID, next); err == nil {
			m.pendingMode = next
			m.modeGeneration++
			m.notice = next.Name + ", " + next.Description
			m.err = ""
			return settleMode(m.modeGeneration)
		}
		current = next.Name
	}
	// Every mode was refused, which means the agent's ceiling admits none of them. Not reachable
	// while plan mode sits at read-only, and said plainly rather than left as a key that does
	// nothing if that ever changes.
	m.err = "this agent cannot be put into any of the modes"
	return nil
}

// applyPendingMode puts the conversation in the mode the key stopped on.
//
// Called by the timer, and directly from everywhere that waiting any longer would be wrong: sending
// a message, naming a mode outright, leaving the conversation, and quitting. The delay exists so
// that cycling past a mode does not select it, and somebody who presses the key and then does
// something has stopped cycling.
//
// Applied on the way out rather than dropped, wherever it is called early. Dropping it would be
// worse than having no delay at all, since the key would have shown a mode, said what it does, and
// then not done it.
func (m *Model) applyPendingMode() {
	mode := m.pendingMode
	m.pendingMode = core.Mode{}
	// Whatever timer is still in flight belongs to a selection that has now been dealt with.
	m.modeGeneration++

	if mode.Name == "" || mode.Name == m.Mode() {
		// Nothing chosen, or the ladder was walked all the way round back to where it started.
		return
	}
	if err := m.engine.SetMode(m.sessionID, mode); err != nil {
		// Reachable if the safety net a mode needs went away while the selection was settling, such
		// as the checkpoints runway and cruise are refused without.
		m.err = err.Error()
	}
}

// SettleMode applies a mode the key stopped on without waiting the delay out.
//
// For the shell, which calls it on the way out of the program. A mode is written down and restored
// with the conversation, so a selection abandoned by quitting would be a conversation that reopened
// in the mode somebody had just moved away from.
func (m *Model) SettleMode() { m.applyPendingMode() }

// setMode puts this conversation in a named mode, for the slash commands.
func (m *Model) setMode(name string) {
	mode, ok := core.ModeByName(name)
	if !ok {
		// The selection is left alone. A typo is not a change of mind about the mode being chosen.
		m.err = "there is no mode called " + name + ", try one of: " +
			strings.Join(core.ModeNames(), ", ")
		return
	}
	// Naming a mode is not cycling through one, so there is nothing to wait for, and it supersedes
	// whatever the key was in the middle of choosing.
	m.pendingMode = core.Mode{}
	m.modeGeneration++

	if err := m.engine.SetMode(m.sessionID, mode); err != nil {
		m.err = err.Error()
		return
	}
	m.notice = mode.Name + ", " + mode.Description
	m.err = ""
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
	// The status row is measured rather than assumed to be one line. Several of the slash commands
	// answer with a listing many lines tall, and budgeting one line for it pushed the box and the
	// footer off the bottom of the frame the first time somebody ran /commands on a small terminal.
	h := m.height - m.input.Height() - m.statusHeight()
	h -= len(m.taskPane())
	// The command list takes its rows from the conversation rather than from the box. Taking them
	// from the box would shrink what somebody is typing into at the exact moment they are typing.
	h -= m.menu.height()

	// The btw panel and the queued steering take their rows from the conversation too, for the
	// same reason, and so does another agent's question.
	h -= len(m.btwPanel())
	h -= len(m.steeringPane())
	h -= len(m.visitorPanel())

	// The jump pill takes its rows from the conversation too, and only while it is on screen. It is
	// keyed on the scroll position rather than on the rendered pill because the pill's height is
	// needed here to decide how much transcript to render, and asking the renderer would be a loop.
	if m.scroll > 0 {
		h -= pillHeight
	}
	if h < 1 {
		return 1
	}
	return h
}

// pillHeight is how many rows the jump marker costs: two borders and its label.
const pillHeight = 3

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
			// Below the box here, because the box is in the middle of the screen and below is where
			// the room is. On a conversation in progress the box is on the floor and the list goes
			// above it instead. The btw panel goes with it, for the same reason, and so does another
			// agent's question: a fresh conversation is exactly where somebody sits while agents they
			// started are working, so it is the last screen that should hide one asking for a hand.
			panel: append(m.visitorPanel(), m.btwPanel()...),
			menu:  m.menu.lines(m.width, m.menuFilter()),
		}.render()
	}

	// The tail is what matters, unless the user has deliberately scrolled away from it. A view that
	// jumped to the top on every token would be unusable, and one that always pinned the bottom
	// would make it impossible to read anything while an agent was talking. The windowing itself
	// lives in window(), which the mouse selection shares: the two have to agree exactly or the
	// highlight lands one row off the pointer.
	lines, start, end := m.window()
	visible := m.transcriptHeight()

	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, m.highlighted(lines[i], i))
	}

	// Pad so the input box stays at the bottom rather than floating under however much has been
	// said so far.
	for pad := visible - len(rows); pad > 0; pad-- {
		rows = append(rows, "")
	}

	rows = append(rows, m.taskPane()...)
	// Guidance waiting for the agent stays on screen until it is delivered.
	rows = append(rows, m.steeringPane()...)
	// The btw panel sits above the box, where the answers to questions about the conversation are
	// close to the conversation they are about without being in it.
	rows = append(rows, m.btwPanel()...)
	// Another agent's question goes closest to the box, because it is the thing on this screen most
	// likely to be why somebody came back to it.
	rows = append(rows, m.visitorPanel()...)
	// Above the box, because on a conversation in progress the box is already on the floor of the
	// screen and there is nothing below it to drop into.
	rows = append(rows, m.menu.lines(m.width, m.menuFilter())...)
	// Last before the status row and the box, which puts it directly on top of the thing somebody
	// is about to type into. See jumpPill.
	rows = append(rows, m.jumpPill(len(lines)-end)...)

	// The smoke drifts up from the fire into whatever air these rows have spare.
	m.driftSmoke(rows)

	var b strings.Builder
	b.WriteString(strings.Join(rows, "\n"))
	b.WriteString("\n")
	b.WriteString(m.flameOver(m.statusRow(len(lines) - end)))
	b.WriteString("\n")
	b.WriteString(m.inputBox())
	return b.String()
}

// flameOver puts the tip of the fire above its base, at the right hand end of the status row.
//
// The base rides the box's top edge and holds still; this is the part that dances. It is drawn only
// while the fire is lit, so a finished turn leaves coals on the rule and nothing above them.
//
// Right aligned to the same columns the base occupies, computed from the box rather than guessed, so
// the flame sits on the fire instead of near it. The status row's own text is measured with its
// styling stripped, because a row measured with escape sequences in it comes out far too wide and the
// padding disappears.
func (m Model) flameOver(status string) string {
	if m.blank() || (!m.working && !m.compacting) {
		return status
	}

	// One space of padding inside the box wall, then the fire, then the wall itself.
	column := m.width - 2 - brand.EmberWidth
	used := lipgloss.Width(ansi.Strip(status))
	if column <= used {
		return status
	}
	return status + strings.Repeat(" ", column-used) +
		theme.Current().Flame.Render(brand.EmberTip(m.markStep))
}

// driftSmoke lets a wisp or two rise from the fire into the rows above the status row, in place.
//
// A little way into the conversation and no further: the near wisp one row above the flame's tip and
// a fainter, sparser one a row above that, so the smoke visibly thins and fades rather than climbing
// through somebody's transcript. Each wisp is drawn only where its row has nothing else in those
// columns — smoke goes behind words, not over them — and not at all while the view is scrolled away
// from the tail, where the rows above the box are the middle of something being read.
func (m Model) driftSmoke(rows []string) {
	if m.blank() || (!m.working && !m.compacting) || m.scroll > 0 {
		return
	}
	t := theme.Current()
	fades := []lipgloss.Style{t.Smoke, t.SmokeFaint}

	column := m.width - 2 - brand.EmberWidth
	for rise := 1; rise <= len(fades); rise++ {
		i := len(rows) - rise
		if i < 0 {
			return
		}
		if used := lipgloss.Width(ansi.Strip(rows[i])); used <= column {
			wisp := strings.TrimRight(brand.EmberWisp(m.markStep, rise), " ")
			rows[i] += strings.Repeat(" ", column-used+leading(wisp)) +
				fades[rise-1].Render(strings.TrimLeft(wisp, " "))
		}
	}
}

// leading is how many columns of air position a wisp inside its block.
func leading(wisp string) int {
	return len([]rune(wisp)) - len([]rune(strings.TrimLeft(wisp, " ")))
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
//
// A block with a frame around it rather than loose lines, wearing the same chrome as the btw panel,
// because the two are the same kind of thing: a standing note above the box that is not part of the
// conversation. Loose lines directly under the transcript read as more of what the agent was
// saying, which is exactly what a status that must never be mistaken for prose should not do.
//
// The btw panel stands in its place while it is up rather than stacking under it. Both at once is
// two framed blocks over one message box on a screen whose whole layout argument is that the
// conversation wins ties, and the tasks come back the moment the btw is closed.
func (m Model) taskPane() []string {
	tasks := m.session.Tasks
	if len(tasks) == 0 || m.btwUp() {
		return nil
	}
	t := theme.Current()

	inner := m.width - boxChrome
	if inner < 6 {
		inner = 6
	}

	// A long list collapses to what is happening now plus the counts, rather than being cut off at
	// an arbitrary item. Truncating would hide the end of the list, and the end is where the
	// unfinished work is.
	if len(tasks) > maxTaskLines {
		return borderedBlock("tasks", []string{t.Body.Render(
			truncate(core.TaskSummary(tasks), inner))}, inner)
	}

	rows := make([]string, 0, len(tasks))
	for _, task := range tasks {
		line := "[" + task.State.Glyph() + "] " + task.Text
		if task.Outcome != "" {
			// The outcome is what makes a finished list worth reading, so it is on the same line as
			// the item rather than folded away behind a key nobody presses.
			line += ", " + task.Outcome
		}
		rows = append(rows, taskStyle(t, task.State).Render(truncate(line, inner)))
	}
	return borderedBlock("tasks", rows, inner)
}

// taskStyle colours a row by what is happening to it.
//
// Three states, three colours, all from the theme and none built here, which is the rule at the top
// of internal/tui/theme. The colour is an accelerant and never the fact: every row still carries its
// glyph, so the list reads the same with the palette turned off, which is D-10 and the reason the
// second theme exists at all.
//
// In progress takes the informational colour and done takes the success colour, which is the pairing
// the rest of the interface already uses for "this is happening" and "this worked". Pending is muted
// because a list is mostly pending and a screen where most rows shout has no emphasis left to spend.
func taskStyle(t theme.Theme, state core.TaskState) lipgloss.Style {
	switch state {
	case core.TaskInProgress:
		return t.Info
	case core.TaskDone:
		return t.Success
	default:
		return t.Muted
	}
}

// btwVisible is how many content rows the panel shows at once.
//
// Eight. Enough for a question and a real answer with a previous exchange peeking above it, few
// enough that the panel is a margin note rather than a second transcript competing with the first.
const btwVisible = 8

// btwContent is every aside laid out for the panel, oldest first, wrapped to its width.
func (m Model) btwContent(inner int) []string {
	t := theme.Current()

	var lines []string
	for i, exchange := range m.asides {
		if i > 0 {
			lines = append(lines, "")
		}
		// The question carries a marker and the answer sits under it in the muted colour, so a
		// stack of exchanges reads as questions with answers rather than as one run of text.
		for j, line := range wrap(exchange.question, inner-2) {
			prefix := t.Key.Render("? ")
			if j > 0 {
				prefix = "  "
			}
			lines = append(lines, prefix+t.Body.Render(line))
		}
		for _, line := range wrap(exchange.answer, inner) {
			lines = append(lines, t.Muted.Render(line))
		}
	}
	return lines
}

// btwScrollBy moves the panel's view, bounded at both ends for the reason the transcript's is.
func (m *Model) btwScrollBy(lines int) {
	m.btwScroll += lines
	inner := m.width - boxChrome
	if inner < 6 {
		inner = 6
	}
	if limit := len(m.btwContent(inner)) - btwVisible; m.btwScroll > limit {
		m.btwScroll = limit
	}
	if m.btwScroll < 0 {
		m.btwScroll = 0
	}
}

// btwUp reports whether the asides panel is showing.
//
// Asked in one place because two blocks depend on the answer: this one, and the task list it stands
// in front of while it is up. Two readings of the same condition is how the height budget and the
// rendering come to disagree, and that disagreement is a message box pushed off the bottom of the
// screen.
func (m Model) btwUp() bool { return m.btwOpen && len(m.asides) > 0 }

// btwPanel is the asides in a box of their own, above the message box and exactly as wide.
//
// A bordered panel rather than a line in the status row, which is where the answer used to go and
// where it lasted until the next keystroke. An aside somebody asked twenty minutes ago is still
// here, scrolled to with pgup, and the whole thing folds away on esc and comes back on a bare /btw.
// It is deliberately in the border colour rather than a signal colour: nothing in it is part of the
// conversation, and the frame should say so.
func (m Model) btwPanel() []string {
	if !m.btwUp() {
		return nil
	}

	inner := m.width - boxChrome
	if inner < 6 {
		inner = 6
	}

	content := m.btwContent(inner)
	end := len(content) - m.btwScroll
	if end > len(content) {
		end = len(content)
	}
	if end < 1 {
		end = 1
	}
	start := end - btwVisible
	if start < 0 {
		start = 0
	}

	// The edge names the panel and says how to work it, in the space the rule was spending anyway.
	// Dropped whole on a terminal too narrow for it, because a label that wraps the edge breaks the
	// frame it is written on.
	label := "btw"
	if len(content) > btwVisible {
		label = "btw · pgup to scroll"
	}
	label += " · esc to close"

	return borderedBlock(label, content[start:end], inner)
}

// borderedBlock is the frame the panels above the message box share.
//
// One function rather than one per panel, because "the same chrome as the btw panel" is a claim two
// copies would stop being true of the first time somebody adjusted one of them. The label rides the
// top edge in the space the rule was spending anyway, and is dropped whole on a terminal too narrow
// for it: a label that wraps the edge breaks the frame it is written on.
func borderedBlock(label string, content []string, inner int) []string {
	t := theme.Current()

	top := " " + t.Border.Render("╭"+strings.Repeat("─", inner+2)+"╮")
	if rest := inner - lipgloss.Width(label) - 1; rest >= 0 {
		top = " " + t.Border.Render("╭─") + " " + t.Muted.Render(label) + " " +
			t.Border.Render(strings.Repeat("─", rest)+"╮")
	}

	out := make([]string, 0, len(content)+2)
	out = append(out, top)
	for _, line := range content {
		pad := inner - lipgloss.Width(ansi.Strip(line))
		if pad < 0 {
			pad = 0
		}
		out = append(out, " "+t.Border.Render("│")+" "+line+strings.Repeat(" ", pad)+
			" "+t.Border.Render("│"))
	}
	out = append(out, " "+t.Border.Render("╰"+strings.Repeat("─", inner+2)+"╯"))
	return out
}

// steeringChip is the label a queued correction sits behind, sized once so continuation lines can
// line their text up under the first.
const steeringChip = "  steering  "

// steeringPane is the guidance waiting for the agent, shown from the keystroke that queued it until
// the moment it is delivered.
//
// It shows the correction itself rather than a sentence about it, which is the difference between
// feedback and reassurance: "queued" tells you something happened, the text tells you the right
// thing happened, and it staying on screen tells you it has not been forgotten. It disappears on
// its own when the turn finishes, because that is when the guidance becomes an ordinary message in
// the transcript and there is nothing left to wait for.
func (m Model) steeringPane() []string {
	if m.blank() {
		return nil
	}
	queued := m.engine.Steering(m.sessionID)
	if len(queued) == 0 {
		return nil
	}
	t := theme.Current()

	// The arrival note rides the first line, and is dropped whole on a terminal too narrow to give
	// the guidance most of the row: the guidance is the content, the note is a caption.
	const note = "  · delivered when this turn finishes"
	suffix := note
	room := m.width - len(steeringChip) - len(note) - 2
	if room < 16 {
		suffix = ""
		room = m.width - len(steeringChip) - 2
	}

	out := make([]string, 0, len(queued))
	for i, guidance := range queued {
		if i == 0 {
			out = append(out, t.Info.Render(steeringChip)+
				t.Body.Render(truncate(guidance, room))+t.Muted.Render(suffix))
			continue
		}
		out = append(out, strings.Repeat(" ", len(steeringChip))+
			t.Body.Render(truncate(guidance, m.width-len(steeringChip)-2)))
	}
	return out
}

// jumpPill is the marker that appears when the view has stopped following the tail.
//
// It exists because scrolling up to reread something and an agent having gone quiet look identical
// from the outside: in both cases the bottom of the transcript stops moving. Somebody who has
// forgotten they scrolled will sit and wait for a reply that arrived four screens ago.
//
// A bordered marker rather than the line of text this used to be, and it sits directly on top of the
// message box rather than in the status row. That position is the whole idea: it is between the
// conversation and the place your eyes already are when you go back to typing, so it is in the way
// in the one sense that helps and no other. The status row it vacated is now free for the thing that
// belongs there, which is what the agent is doing.
//
// Returns nothing when the view is at the tail, which is most of the time.
func (m Model) jumpPill(below int) []string {
	if below <= 0 {
		return nil
	}
	t := theme.Current()

	label := fmt.Sprintf(" ↓ %d more below   ctrl+↓ to jump ", below)
	if lipgloss.Width(label) > m.width-4 {
		// Narrow terminals lose the count rather than the key. The number is interesting and the
		// way out is the part somebody actually needs.
		label = " ↓ ctrl+↓ "
	}

	inner := lipgloss.Width(label)
	top := "╭" + strings.Repeat("─", inner) + "╮"
	bottom := "╰" + strings.Repeat("─", inner) + "╯"

	// Indented to sit above the message box rather than flush against the edge, so it reads as
	// belonging to the box it is pointing at.
	const indent = "  "
	return []string{
		indent + t.Warning.Render(top),
		indent + t.Warning.Render("│") + t.Warning.Render(label) + t.Warning.Render("│"),
		indent + t.Warning.Render(bottom),
	}
}

// statusHeight is how many rows the status will occupy.
func (m Model) statusHeight() int { return 1 + strings.Count(m.statusRow(0), "\n") }

// statusRow is the line between the conversation and the box.
func (m Model) statusRow(below int) string {
	t := theme.Current()

	// The copy confirmation outranks everything, because it is the acknowledgement of the thing
	// that happened most recently and it takes itself away in a moment either way.
	if m.copied {
		return t.Success.Render("  ✓ copied to clipboard")
	}
	if m.err != "" {
		return m.statusText(m.err, t.Danger)
	}
	// Above the working line on purpose. A notice is usually a question waiting on the next
	// keystroke, and a spinner saying "working" is not the thing to answer.
	if m.notice != "" {
		return m.statusText(m.notice, t.Warning)
	}
	if m.awaiting {
		return t.Warning.Render("  waiting for you")
	}
	if m.compacting {
		return t.Muted.Render("  " + m.spinnerFrame() + " summarising the conversation so far")
	}
	if m.working {
		// The count is what tells a slow turn from a stuck one. Without it, a request that has been
		// out for four minutes and one that left a moment ago are the same spinner, and the spinner
		// is the only thing on screen saying anything is happening at all.
		return t.Muted.Render("  " + m.spinnerFrame() + " working" + m.workingFor() + ", esc to stop")
	}
	return ""
}

// statusText lays a message out across as many rows as it needs.
//
// Wrapped here rather than left to the terminal. statusHeight budgets this row by counting the
// newlines in it, so a message long enough for the terminal to wrap on its own occupied more rows
// than the frame had reserved and pushed the footer off the bottom of the screen. The messages that
// do this are the long ones, which is to say the errors, which is to say exactly the times when the
// screen going wrong is least welcome.
func (m Model) statusText(text string, style lipgloss.Style) string {
	width := m.width - 2
	if width < 8 {
		width = 8
	}
	lines := wrap(text, width)

	// Bounded, because a provider can return one very long line and the status row is not a
	// transcript. The bound is a share of the screen rather than a fixed number of rows: several of
	// these messages are deliberately multi-line, the mode ladder above all, and a fixed cap of a
	// few rows silently ate the last two rungs of it. Never below five, so the ladder fits on a
	// short terminal too.
	most := m.height / 3
	if most < 5 {
		most = 5
	}
	if len(lines) > most {
		lines = append(lines[:most-1], "…")
	}
	for i, line := range lines {
		lines[i] = style.Render("  " + line)
	}
	return strings.Join(lines, "\n")
}

// workingFor is how long the turn in flight has been going, once that is worth saying.
func (m Model) workingFor() string {
	if m.workingSince.IsZero() {
		return ""
	}
	elapsed := time.Since(m.workingSince)
	if elapsed < time.Second {
		return ""
	}
	return ", " + formatDuration(elapsed)
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
	for _, line := range m.commandLit(m.input.Lines()) {
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

// menuFilter is the command fragment being typed, for the list to light its matches by.
func (m Model) menuFilter() string {
	prefix, ok := commandPrefix(m.input.Value())
	if !ok {
		return ""
	}
	return prefix
}

// commandLit colours a command at the head of the box in the secondary colour, once it names one
// that actually exists.
//
// The colour is confirmation, not decoration: the moment /new turns green you know it will run as a
// command rather than be sent as a message, which is otherwise only discoverable by sending it. A
// name that matches nothing stays plain, which is the same signal in the other direction.
func (m Model) commandLit(lines []string) []string {
	name, ok := m.typedCommand()
	if !ok || len(lines) == 0 {
		return lines
	}
	// Matched on the rendered line rather than assumed, because the drawn cursor is escape
	// sequences in the middle of the text: with the cursor inside the name the prefix will not
	// match, and skipping the highlight there is right anyway — the menu is open and doing it.
	token := "/" + name
	if !strings.HasPrefix(lines[0], token) {
		return lines
	}
	lines[0] = theme.Current().Success.Render(token) + lines[0][len(token):]
	return lines
}

// typedCommand is the command the box currently begins with, when it names one that exists.
func (m Model) typedCommand() (string, bool) {
	value := m.input.Value()
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "", false
	}
	name := strings.TrimPrefix(value, "/")
	if at := strings.IndexAny(name, " \t\r\n"); at >= 0 {
		name = name[:at]
	}
	if name == "" {
		return "", false
	}
	for _, item := range builtinItems() {
		if item.name == name {
			return name, true
		}
	}
	for _, command := range m.commands.All() {
		if command.Name == name {
			return name, true
		}
	}
	return "", false
}

// modeArrow separates the mode in effect from the one the key has stopped on.
//
// A direction rather than a slash or a bracket, because what it is saying is that the second one is
// on its way. It carries the meaning without colour, which the footer's own arrow already relies on.
const modeArrow = " → "

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

	// While a selection is settling the edge says both, in the order they happen: the mode in effect,
	// then the one the key has stopped on. Only the first is a guarantee, so only the first is
	// written in the mode's own colour, and the arrow is what says the second has not landed yet.
	// Showing the selection alone would be the box claiming a level the permission layer is not
	// enforcing for another second and a half.
	label := m.Mode()
	if selecting, ok := m.Selecting(); ok {
		label += modeArrow + selecting
	}
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
	if selecting, ok := m.Selecting(); ok {
		written += t.Muted.Render(modeArrow + selecting)
	}
	if model := m.session.Model; model != "" {
		written += t.Muted.Render("  " + model)
	}

	rest := width - lipgloss.Width(label) - 3

	// The campfire rides on the right hand end of the rule, once the opening screen has gone.
	//
	// The mark in the corner of the opening screen is the same fire, and it disappears with that
	// screen. Losing it entirely the moment somebody says something makes the program feel like two
	// programs, so it moves here: five cells at the far end of a rule that was empty anyway.
	//
	// **It is lit while the agent is working and out when it is not**, which is the part that earns
	// it the space. A spinner already says something is happening and says it in the status row,
	// where somebody has to look. This says the same thing in the corner of the box they are already
	// looking at, and it says the opposite just as clearly: a fire that has gone out is a turn that
	// has finished. The shape changes as well as the colour, so it still reads under NO_COLOR.
	fire := ""
	if !m.blank() && rest >= brand.EmberWidth+2 {
		if m.working || m.compacting {
			fire = " " + emberBase() + " "
		} else {
			fire = " " + emberCoals() + " "
		}
		rest -= brand.EmberWidth + 2
	}

	return t.Border.Render(left+horizontal) + " " + written + " " +
		t.Border.Render(strings.Repeat(horizontal, rest)) + fire + t.Border.Render(right)
}

// emberBase draws the bed of the fire, its heart a step brighter than its ends.
//
// Two shades of the same green rather than one, because a fire is brightest in the middle, and that
// small difference is what makes seven cells on a border rule read as burning rather than as a row
// of green marks. The split columns come from the brand package so the two cannot drift apart.
func emberBase() string {
	t := theme.Current()
	return heartOf(brand.EmberBase, t.Flame, t.FlameCore)
}

// emberCoals draws the fire gone out: cold grey at the edges, the last of the warmth in the middle.
//
// The same split as the lit base and the opposite direction of contrast — the ends fade towards the
// background while the centre keeps the plainer grey — which is what a real fire does as it dies:
// it goes out from the outside in.
func emberCoals() string {
	t := theme.Current()
	return heartOf(brand.EmberOut, t.SmokeFaint, t.Smoke)
}

// heartOf styles a seven cell drawing with one style at its ends and another over its middle, the
// split coming from the brand package so the two cannot drift apart.
func heartOf(drawing string, ends, middle lipgloss.Style) string {
	runes := []rune(drawing)
	core, end := brand.EmberCoreColumn, brand.EmberCoreColumn+brand.EmberCoreWidth
	return ends.Render(string(runes[:core])) +
		middle.Render(string(runes[core:end])) +
		ends.Render(string(runes[end:]))
}

// Context is what the frame shows beside the title.
// Context is the detail line, joined. Kept for callers that want one string.
func (m Model) Context() string { return strings.Join(m.ContextParts(), "  ") }

// ContextParts is the same detail, in the order it should survive a narrow terminal.
//
// Separate from Context because the header drops these from the right rather than truncating the
// joined string. Truncating a joined string cuts a fact in half, and half of "12.3k tokens" is a
// number with no unit on it.
func (m Model) ContextParts() []string {
	// The agent's name used to be first here, and is not here at all any more: it moved into the
	// header's title, beside the mark, where it answers "whose conversation am I in" without
	// spending a fact slot. Written in both places it would be the same word twice on one row, and
	// the facts row is the half that gets dropped from the right on a narrow terminal.
	parts := []string{}
	// The opening screen already says where the agent is working and what it is talking to, along
	// its bottom left, so while that screen is up the header does not say the same two things three
	// rows above it. They move up here the moment the conversation starts and takes the floor back.
	if !m.blank() {
		if m.dir != "" {
			parts = append(parts, m.dir)
		}
		if m.keyName != "" {
			parts = append(parts, m.keyName)
		}
		// The model beside the credential, because one key now runs many and the credential's name
		// no longer answers "what am I talking to". Its own part rather than joined to the key, so a
		// narrow terminal drops the model and keeps the key rather than cutting one string in half.
		if m.session.Model != "" {
			parts = append(parts, m.session.Model)
		}
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
	return parts
}

// contextMeter is the "how full is this conversation" figure in the header.
//
// A drawn bar as well as the number, because a bar is read at a glance from across a desk and a
// percentage has to be found and parsed. The number stays beside it for the person who wants the
// exact figure, and the bar is built from the same two characters as every rule in the interface,
// so it degrades to nothing stranger than a line on a font that has trouble.
//
// The fill goes through the traffic light as it grows: the signature green while there is plenty of
// room, amber past the halfway-and-some mark, red when compaction is due. The number takes the same
// colour, so the two halves of the meter cannot tell different stories, and the empty track stays in
// the border grey, which is what makes the filled part read as filled.
func (m Model) contextMeter() string {
	t := theme.Current()
	use := m.session.ContextUse()
	fraction := use.Fraction()

	tone := t.Success
	switch {
	case use.NeedsCompaction() || fraction >= 0.85:
		tone = t.Danger
	case fraction >= 0.6:
		tone = t.Warning
	}

	filled := int(fraction*float64(meterWidth) + 0.5)
	if filled > meterWidth {
		filled = meterWidth
	}
	// Anything at all shows one cell. A conversation that has started using context showing an
	// empty bar reads as a meter that is broken rather than one that is barely used.
	if filled <= 0 {
		filled = 0
		if fraction > 0 {
			filled = 1
		}
	}

	out := t.Muted.Render("context ") +
		tone.Render(strings.Repeat("█", filled)) +
		t.Border.Render(strings.Repeat("─", meterWidth-filled)) +
		" " + tone.Render(use.String())
	if use.NeedsCompaction() {
		out += t.Warning.Render(", ctrl+r to compact")
	}
	return out
}

// meterWidth is how many cells the header bar spends. Ten: enough steps that the colour change
// lands mid bar rather than at its ends, few enough that the bar is a detail rather than a banner.
const meterWidth = 10

// toolKind answers what kind of thing a tool is, for the transcript's labels.
//
// Asked of the registry this conversation was actually given rather than of a list of known names,
// so a tool from an MCP server is labelled by the same rule as a built in one. That matters more for
// the remote ones: every MCP tool is an execute tool whatever its server calls it, and "run" against
// a name somebody has never seen is the most useful thing the label says all day.
func (m Model) toolKind(name string) (core.ToolKind, bool) {
	registry, ok := m.engine.Tools()
	if !ok || registry == nil {
		return "", false
	}
	tool, found := registry.Get(name)
	if !found {
		return "", false
	}
	return tool.Kind(), true
}
