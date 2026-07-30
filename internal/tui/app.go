package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	agentsui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/agents"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// Engine is everything the application needs from the session engine.
//
// One interface combining the screens' own, rather than each screen being handed its own value.
// The agents view was constructed without an engine for exactly as long as this did not exist:
// nothing forced the caller to supply one, its list silently showed nothing, and creating an agent
// dereferenced a nil interface and took the program down. A screen that needs an engine should be
// impossible to build without one.
type Engine interface {
	chat.Engine
	agentsui.Engine

	// Create starts a fresh conversation and returns it. The old one is left alone: it keeps its
	// history, keeps running any turn that is in flight, and is still in the session list.
	Create(keyName, model string) core.Session

	// RenameCredential re-points every conversation and agent naming a credential at its new name,
	// and reports how many conversations moved.
	//
	// Here because a credential's name is what a conversation writes down and what the resolver
	// dereferences on the next message. The credential screen can rename the key and cannot reach a
	// single conversation, so a rename that stopped there would leave every conversation started on
	// it pointing at a name nothing answers to, and the failure would arrive one message later from
	// the far end.
	RenameCredential(from, to string) int
}

// screen identifies which view is in front.
type screen int

const (
	screenChat screen = iota
	screenAgents
	screenDashboard
	screenReview
	screenKeys
	screenHelp
	screenModel
)

// There was a launch screen here, shown for nine hundred milliseconds before the application
// appeared. It is gone, at the supervisors' request, and the request is right: a splash is a delay
// somebody has to sit through to reach the thing they typed a command to reach, and the argument
// for it was recognition, which the opening screen already does while being usable. Every terminal
// tool worth being measured against opens on something you can type into.
//
// The mark and the drawn name did not go with it. They are on the screen a conversation opens on,
// which is where somebody is looking anyway.

// App is the top level model. It owns which screen is showing and routes messages to it.
//
// Chat is the home screen. Running `canopy` in a directory opens a conversation there, and
// everything else is somewhere you go from it. Opening on the dashboard instead would put the
// least common activity first and make Canopy look like something you watch rather than something
// you talk to.
//
// The screens are separate models rather than one growing model, because they share nothing except
// the terminal. Merging them would mean every keystroke handler knowing which mode it was in,
// which is how a TUI turns into one large switch nobody wants to touch.
type App struct {
	screen screen

	// engine is held so the application can start a conversation, which is the one thing no screen
	// can do for itself: the chat screen shows a session and the agents screen makes agents, and a
	// plain new conversation belongs to neither.
	engine Engine

	// confirmingNew is true when a new conversation has been asked for while a reply is still
	// arriving, and the same key pressed again will go through with it.
	confirmingNew bool

	// confirmingQuit is true when ctrl+c has been pressed once in a conversation, and the same key
	// again will actually leave. One press quitting outright was the sharpest edge in the program:
	// ctrl+c is also the interrupt key, so the muscle memory that stops a long reply fired a quit
	// the instant the reply happened to finish first.
	confirmingQuit bool

	// dir is the working directory new agents are given.
	dir string

	// usingKey is the credential last applied from the credential screen, so choosing the same one
	// twice does not re-apply it on every keystroke.
	usingKey string

	// helpScroll is how far down the overlay has been scrolled. Reset when it closes, so opening it
	// again starts at the top rather than wherever it was left last time.
	helpScroll int

	// helpFrom is where the help overlay returns to. Separate from cameFrom, because help is
	// reachable from the credential screen too and one field would send you to the wrong place.
	helpFrom screen

	// keyStore is held so the model picker can be built from what is stored at the moment it is
	// opened. The credential screen has its own copy of the same store; sharing one value rather
	// than asking that screen for its list keeps the picker from depending on where that screen
	// happens to have its cursor.
	keyStore keysui.Store

	// picker is the model picker while it is up, and its zero value the rest of the time. Rebuilt
	// on every opening, so a key added since the last one is there.
	picker modelPicker

	// cameFrom is where escape goes back to. Recorded rather than assumed, because the credential
	// screen is reachable from both chat and the dashboard, and always returning to one of them
	// would throw away where somebody actually was.
	cameFrom screen

	// waiting is who needed a person the last time the engine said anything, held so the bell can
	// ring on the transition into needing somebody rather than once per frame for as long as they
	// go on waiting. See bell.go.
	waiting []string

	chat      chat.Model
	agents    agentsui.Model
	dashboard Model
	review    ReviewModel
	keys      keysui.Model

	dim Dimensions
}

// AppOptions are the capabilities resolved for the project this application is opening.
type AppOptions struct {
	Review   ReviewSource
	Commands chat.Commands
	Costs    CostOutcomeSource

	// Agent names the agent whose conversation is being opened, for the corner of the header.
	//
	// Handed in rather than looked up, because the conversation Canopy opens on belongs to an agent
	// that whoever started the program has just created, and every other way into a conversation
	// carries the name with it already. Empty is a legitimate state and reads as the brand.
	Agent string

	// Session is the conversation to open. Empty starts a new one.
	//
	// Which conversation you land in is a decision for whoever ran Canopy, not for the interface, so
	// it arrives here rather than being worked out here. This field replaces a hardcoded "session-1",
	// which was correct on a machine with no history and wrong on every machine after that: the
	// engine loads saved conversations at startup and numbers new ones from the highest it found, so
	// "session-1" is the oldest chat in the database. Every launch opened it, while the agent that
	// had just been created sat in a conversation nobody could see.
	Session string
}

// NewApp builds the application.
//
// A run with no credentials still opens on the credential screen, and leaving it lands on chat. The
// empty state is the one moment where a form beats a conversation: an agent with no key can be
// talked to and cannot answer, and finding that out by typing a message and watching it fail is a
// worse introduction than being asked for the one thing that makes the rest work.
func NewApp(
	store core.SnapshotStore, keyStore keysui.Store, engine Engine, dir, keyName string,
) App {
	return NewAppWithReview(store, keyStore, engine, dir, keyName, nil)
}

// NewAppWithReview is the same with a source for the review screen.
//
// Separate rather than a sixth parameter on NewApp, because most callers, including every existing
// test, run without a repository and would have to pass a nil they do not care about. A nil source
// is a legitimate state here: Canopy runs in directories that are not git repositories, and the
// review screen says so rather than being hidden.
func NewAppWithReview(
	store core.SnapshotStore, keyStore keysui.Store, engine Engine, dir, keyName string,
	review ReviewSource,
) App {
	return NewAppConfigured(store, keyStore, engine, dir, keyName, AppOptions{Review: review})
}

// NewAppConfigured builds the application with project-specific capabilities.
func NewAppConfigured(
	store core.SnapshotStore, keyStore keysui.Store, engine Engine, dir, keyName string,
	options AppOptions,
) App {
	credentials := keysui.New(keyStore)
	model := credentials.ModelFor(keyName)

	// Nothing named means a fresh conversation, made here because there is nothing to show
	// otherwise. Starting a new one is the safe direction to be wrong in: an unwanted new
	// conversation costs a keystroke to leave, and landing in the wrong old one is how somebody
	// appends tonight's question to last week's context without noticing.
	sessionID := options.Session
	if sessionID == "" {
		sessionID = engine.Create(keyName, model).ID
	}

	app := App{
		screen:    screenChat,
		engine:    engine,
		chat:      chat.New(engine, sessionID, dir, keyName),
		agents:    agentsui.New(engine),
		dashboard: New(store),
		review:    NewReview(options.Review),
		keys:      credentials,
		keyStore:  keyStore,
		cameFrom:  screenChat,
		usingKey:  keyName,
		dir:       dir,
		dim:       Dimensions{Width: 80, Height: 24},
	}
	app.chat.SetCommands(options.Commands)
	app.chat.SetAgent(options.Agent)
	app.review.SetCostOutcomes(options.Costs)
	// What a new agent inherits. Without it every agent created from that screen was built with an
	// empty credential and an empty working directory, which fails on its first message.
	app.agents.SetDefaults(keyName, model, dir)
	// The credential screen comes first when there is no credential to run on, which is not the same
	// as having none stored. With several stored and none chosen there is no obvious default, and
	// landing on the chat means typing a message and watching it fail, which is a worse introduction
	// than being asked the one question that makes everything else work.
	if app.keys.IsEmpty() || keyName == "" {
		app.screen = screenKeys
	}

	app.resize(app.dim)
	return app
}

func (a *App) resize(dim Dimensions) {
	a.dim = dim
	a.chat.SetSize(dim.Width, dim.BodyHeight())
	a.agents.SetSize(dim.Width, dim.BodyHeight())
	a.review.SetSize(dim.Width, dim.BodyHeight())
	// The picker's window follows, so a terminal resized while it is up keeps the row somebody was
	// on in view instead of scrolling out from under them.
	a.picker.fit(pickerHeight(dim))
}

func (a App) Init() tea.Cmd {
	return tea.Batch(a.dashboard.Init(), a.chat.Init())
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case agentsui.SwitchMsg:
		// The agents view asks and the application decides, which is what keeps "which screen is
		// showing" in one place.
		cmd := a.chat.SetSession(m.SessionID, m.AgentName)
		a.screen = screenChat
		a.agents.SetVisible(false)
		return a, cmd

	case chat.SwitchMsg:
		// A surfaced permission summary is intentionally not an approval surface. Opening the
		// conversation that owns it puts the full canonical request on screen before y or a can
		// spend anything, preserving the same ownership boundary as a switch from the agents view.
		cmd := a.chat.SetSession(m.SessionID, m.AgentName)
		a.screen = screenChat
		a.agents.SetVisible(false)
		return a, cmd

	case chat.ActionMsg:
		// A slash command that named something only the application can do. The chat says what was
		// asked for and owns none of it, which is what keeps "which screen is showing" in one place.
		return a.runAction(m.Action)

	case tea.MouseMsg:
		// Routed to the screen in front rather than broadcast, for the same reason keystrokes are:
		// a wheel notch is aimed at what somebody is looking at. Broadcasting would scroll the
		// conversation behind whatever screen they had actually opened.
		//
		// Only chat answers it today. The other screens navigate with j and k and ignoring the
		// wheel there is what they already did, since before this the wheel was arriving as arrow
		// keys that none of them bound.
		if a.screen == screenChat {
			// Translated into the body's own coordinates before it is forwarded, because the chat
			// draws below the header and knows nothing about how tall the header is. Without this a
			// drag would select the row a header's height above the pointer, and a press on the
			// header itself would read as a press on the first line of the conversation.
			m.Y -= a.dim.HeaderHeight()
			var cmd tea.Cmd
			a.chat, cmd = a.chat.Update(m)
			return a, cmd
		}
		return a, nil

	case tea.WindowSizeMsg:
		a.resize(Dimensions{Width: m.Width, Height: m.Height})
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		// Help is answered before anything else, and any key leaves it except the ones that scroll
		// it. Somebody who opened it by accident should not have to find the one key that closes it,
		// and somebody reading it should not be thrown out for trying to see the rest.
		if a.screen == screenHelp {
			switch key.String() {
			case "j", "down":
				a.helpScroll++
			case "k", "up":
				a.helpScroll--
			case "pgdown", " ":
				a.helpScroll += a.dim.BodyHeight() / 2
			case "pgup":
				a.helpScroll -= a.dim.BodyHeight() / 2
			default:
				a.screen = a.helpFrom
				a.helpScroll = 0
				return a, nil
			}
			if a.helpScroll < 0 {
				a.helpScroll = 0
			}
			return a, nil
		}
		// The picker answers before anything else too, for the same reason help does: while it is up
		// every key belongs to it, and the ones it does not use put it away rather than reaching a
		// conversation that cannot be typed into. Escape changes nothing, which is what makes it safe
		// to open.
		if a.screen == screenModel {
			// While a model is being typed every key belongs to the field, including the ones that
			// would otherwise move or leave, or a model id containing j could never be written.
			if a.picker.typing {
				if row, ok := a.picker.typeKey(key); ok {
					return a.applyModelRow(row)
				}
				return a, nil
			}
			switch key.String() {
			case "j", "down":
				a.picker.move(1)
			case "k", "up":
				a.picker.move(-1)
			case "enter":
				// The row that takes typing opens the field instead of applying, since there is
				// nothing on it yet to apply.
				if a.picker.startTyping() {
					return a, nil
				}
				return a.applyPickedModel()
			case "ctrl+k":
				// A credential with nothing to offer says "press ctrl+k" and this is what makes that
				// true. Without it the hint named a key that closed the picker and went nowhere, which
				// is worse than no hint: somebody reads it, presses it, and concludes the program
				// ignored them.
				a.cameFrom = screenChat
				a.screen = screenKeys
			default:
				a.screen = a.cameFrom
			}
			return a, nil
		}

		// Not while something is being typed into, or a message containing a question mark could
		// never be written.
		if key.String() == "?" && !a.typing() {
			a.helpFrom = a.screen
			if a.screen == screenChat {
				a.leaveChat(screenHelp)
			} else {
				a.screen = screenHelp
			}
			return a, nil
		}

		// A pending confirmation lasts exactly one keystroke. Anything other than the same key
		// again is a change of mind, and a confirmation that outlived it would eventually fire on
		// a keystroke somebody meant for something else entirely.
		if a.confirmingNew && key.String() != "ctrl+n" {
			a.confirmingNew = false
			a.chat.SetNotice("")
		}
		if a.confirmingQuit && key.String() != "ctrl+c" {
			a.confirmingQuit = false
			a.chat.SetNotice("")
		}

		if handled, next, cmd := a.routeKey(key); handled {
			// The agents view is told whether it ended up in front, because its pane fires
			// animate and an animation running behind another screen would be waking the program
			// for frames nobody can see. Told here, at the one place every screen switch passes
			// through, rather than at each switch.
			visibility := next.agents.SetVisible(next.screen == screenAgents)
			return next, tea.Batch(cmd, visibility)
		}

		// A keystroke belongs to the screen in front and to nothing else.
		//
		// Forwarding keys to every screen looks harmless and is not: the dashboard quits on esc,
		// so typing a message and pressing esc to stop a turn would exit the program and lose the
		// session. Screens that are not visible do not get keys.
		switch a.screen {
		case screenChat:
			var cmd tea.Cmd
			a.chat, cmd = a.chat.Update(key)
			return a, cmd
		case screenAgents:
			var cmd tea.Cmd
			a.agents, cmd = a.agents.Update(key)
			return a, cmd
		case screenReview:
			var cmd tea.Cmd
			a.review, cmd = a.review.Update(key)
			return a, cmd
		case screenKeys:
			var cmd tea.Cmd
			a.keys, cmd = a.keys.Update(key)

			// A credential renamed on that screen is followed here, before anything else this
			// keystroke produced is read: what a conversation runs on is named, and the name it holds
			// has just stopped resolving. Before the selection below, because a selection made on the
			// same keystroke would otherwise be applied under whichever of the two names got there
			// first.
			if renamed, ok := a.keys.TakeRename(); ok {
				a.followRename(renamed)
			}

			// A credential chosen on that screen is a fact about the conversation, so it is applied
			// here where the conversation lives. The screen states a preference and owns nothing.
			if name, picked := a.keys.Chosen(); picked {
				if name == a.usingKey {
					// Already true, but still acknowledged: the child deliberately does not claim
					// a switch until its parent confirms the session state.
					a.keys.SelectionApplied(name)
				} else {
					model := a.keys.ModelFor(name)
					// Only once the engine has taken it. It refuses mid answer, and moving the
					// application's own note of the credential in use on a refusal would point the
					// next new conversation at a key this one never managed to switch to.
					if a.chat.UseCredential(name, model) {
						a.usingKey = name
						a.keys.SelectionApplied(name)
						// Agents created after the switch inherit it too, or the next one would
						// quietly go on using the credential somebody had just moved away from.
						a.agents.SetDefaults(name, model, a.dir)
					} else {
						a.keys.SelectionRefused(name, a.chat.Error())
					}
				}
			}
			return a, cmd
		default:
			updated, cmd := a.dashboard.Update(key)
			if next, ok := updated.(Model); ok {
				a.dashboard = next
			}
			return a, cmd
		}
	}

	// Everything else goes to every screen, so each keeps consuming its own events while another
	// is in front. A dashboard that stopped listening whenever you looked elsewhere would be stale
	// the moment you came back, and a chat that stopped listening would lose a reply that arrived
	// while you were reading the credential list.
	var cmds []tea.Cmd

	updated, cmd := a.dashboard.Update(msg)
	if next, ok := updated.(Model); ok {
		a.dashboard = next
	}
	cmds = append(cmds, cmd)

	var chatCmd tea.Cmd
	a.chat, chatCmd = a.chat.Update(msg)
	cmds = append(cmds, chatCmd)

	// The agents view keeps up too, so switching to it shows the current state rather than the
	// state it had when you last looked. Its command is kept, not dropped: the pane fires run on
	// a ticker of their own, and a ticker whose reschedule is discarded stops on the first engine
	// event that arrives.
	var agentsCmd tea.Cmd
	a.agents, agentsCmd = a.agents.Update(msg)
	cmds = append(cmds, agentsCmd)

	a.keys, _ = a.keys.Update(msg)

	// Who needs a person is worked out here rather than on the key path, because a keystroke of
	// yours cannot make another agent ask a question. Everything that starts one arrives as an
	// engine event, and this is the branch every engine event passes through.
	a.noticeAttention()

	return a, tea.Batch(cmds...)
}

// attention is every conversation waiting on a person anywhere in the project, named once each.
//
// Two sources, because there are two ways to need somebody and neither contains the other. A
// pending question can belong to any conversation, including one that is nobody's agent, and it is
// gone the moment it is answered. A failed agent has no question outstanding and has still stopped
// and cannot start again on its own, which is what the agent list has always counted. Asking only
// the first would have walked a person past a failed agent on every screen but one; asking only the
// second would miss the conversation they started themselves.
//
// Named rather than counted so the bell can tell a second agent arriving from the first one still
// waiting. The name is the session, or the agent where there is no session to name, since two
// unstarted agents sharing an empty id would collapse into one.
func (a App) attention() []string {
	seen := map[string]bool{}
	var who []string
	note := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		who = append(who, id)
	}

	for _, waiting := range a.engine.PendingAll() {
		note(waiting.SessionID)
	}
	for _, status := range a.engine.AgentStatuses() {
		if !status.State.NeedsAttention() {
			continue
		}
		if status.Agent.SessionID != "" {
			note(status.Agent.SessionID)
			continue
		}
		note(status.Agent.Name)
	}
	return who
}

// noticeAttention records who is waiting and rings for anybody who was not waiting before.
//
// Once per agent that starts needing somebody, and never once per frame. A bell that repeated while
// a question sat there would not be an alert, it would be a noise somebody silences at the terminal,
// taking every later one with it. A second agent joining the queue is a second transition and rings
// again, because the first bell was about the first agent and was answered or ignored on its own
// terms.
func (a *App) noticeAttention() {
	waiting := a.attention()

	fresh := false
	for _, id := range waiting {
		if !named(a.waiting, id) {
			fresh = true
			break
		}
	}
	a.waiting = waiting

	if fresh {
		ring()
	}
}

func named(who []string, id string) bool {
	for _, one := range who {
		if one == id {
			return true
		}
	}
	return false
}

// routeKey handles screen switching, and reports whether it consumed the key.
//
// Chat is the awkward case and it is worth saying why. Every printable key belongs to the message
// box, so navigation away from chat has to be on keys that are not printable. Anything else would
// mean the letter that opens the dashboard could never be typed in a message.
func (a App) routeKey(msg tea.KeyMsg) (bool, App, tea.Cmd) {
	switch a.screen {
	case screenChat:
		switch msg.String() {
		case "ctrl+c":
			// While a question is open, every key belongs to the question, including this one.
			// Answering it is refusing, which is the safe reading.
			if a.chat.Awaiting() {
				return false, a, nil
			}
			// Quits only when nothing is running. With a turn in flight the first press stops the
			// turn, because that is what somebody hitting it during a long reply almost always
			// means, and quitting instead would throw the conversation away.
			if a.chat.Working() {
				a.chat, _ = a.chat.Update(tea.KeyMsg{Type: tea.KeyEsc})
				return true, a, nil
			}
			// And even then it asks to be pressed again. The same finger that just stopped a turn
			// is often still coming down when the turn ends on its own, and a quit that fires on
			// that press throws somebody out of the program mid thought. Every comparable tool
			// asks twice, and the confirmation costs nothing: it clears on any other key.
			if !a.confirmingQuit {
				a.confirmingQuit = true
				a.chat.SetNotice("ctrl+c again to quit, the conversation is kept either way")
				return true, a, nil
			}
			// A mode the key had stopped on is applied on the way out rather than left to a timer
			// that will never fire. The mode is written down with the conversation, so abandoning it
			// here would reopen tomorrow in the one somebody had just moved away from.
			return a.quit()
		case "ctrl+n":
			// This refused while a question was open, along with the two keys below, and no longer
			// does. The argument is with them.
			//
			// Asked for while a reply is arriving, the first press explains and the second goes
			// ahead. A confirmation rather than a refusal, because leaving is allowed: the old
			// conversation keeps its turn and finishes it whether or not anybody is watching.
			if a.chat.Working() && !a.confirmingNew {
				a.confirmingNew = true
				a.chat.SetNotice(
					"a reply is still arriving, ctrl+n again for a new conversation and this one keeps going")
				return true, a, nil
			}
			next, cmd := a.newConversation()
			return true, next, cmd

		case "ctrl+k", "ctrl+d":
			// These two, and ctrl+n above, used to refuse while a question was open, on the argument
			// that leaving the screen with a tool call waiting would hide the thing that is
			// blocking. D-43 reverses that argument and this code is the reason it was written down:
			// the screen the lock kept you from is the agent list, which is the one place that says
			// who else has stopped and cannot start again without you. Keeping one blocked agent in
			// view by hiding every other one is the worst trade in the program, and it was made on
			// the screen the product's premise depends on.
			//
			// Leaving is not answering. The question is still pending when you come back, the header
			// counts it from wherever you went, and nothing about the reflex-safety rule changes:
			// every key that reaches the prompt still means no.
			if msg.String() == "ctrl+k" {
				a.cameFrom = screenChat
				a.leaveChat(screenKeys)
			} else {
				a.leaveChat(screenAgents)
			}
			return true, a, nil
		}

	case screenAgents:
		// While a name is being typed every key belongs to the field, including the ones that would
		// otherwise navigate, or an agent called "wesc" could never be typed.
		if a.agents.Naming() {
			return false, a, nil
		}
		switch msg.String() {
		case "esc", "q":
			a.screen = screenChat
			return true, a, nil
		case "w":
			// The worktree monitor, which is a different question from the agent list: one is about
			// what the agents are doing and the other about what state the code is in.
			a.screen = screenDashboard
			return true, a, nil
		case "r":
			// Review is reached from the agent list because that is where you are when you notice one
			// has finished. It is a third question again: not what they are doing and not what state
			// the code is in, but which of them is worth reading.
			a.screen = screenReview
			return true, a, nil
		case "K":
			a.cameFrom = screenAgents
			a.screen = screenKeys
			return true, a, nil
		case "ctrl+c":
			// Quit works from every screen, not only the ones somebody happens to leave from. Before
			// this it did nothing here, which on the one screen with no text field to blame looked
			// like the program refusing to close.
			return a.quit()
		}

	case screenDashboard:
		switch msg.String() {
		case "ctrl+c":
			// The embedded dashboard's own handler also quits on ctrl+c, but routing it through the
			// application first preserves any mode selection made in the conversation.
			return a.quit()
		case "q":
			// Back, not quit. The dashboard's own key handler quits on `q`, which is right for the
			// standalone monitor it was written for and wrong here: inside the application `q` means
			// back everywhere else, and this screen is two keystrokes from a conversation somebody
			// has been having for an hour. Intercepted rather than changed at the far end, because
			// the standalone entry point still exists and its `q` is correct.
			a.screen = screenAgents
			return true, a, nil
		case "K":
			a.cameFrom = screenDashboard
			a.screen = screenKeys
			return true, a, nil
		case "esc", "tab":
			a.screen = screenAgents
			return true, a, nil
		case "k":
			// Only when it is unambiguous. "k" is also move-up, so it opens the credential screen
			// only from a dashboard that has nothing to move around in.
			if len(a.dashboard.Snapshot().Workspaces) == 0 {
				a.cameFrom = screenDashboard
				a.screen = screenKeys
				return true, a, nil
			}
		}

	case screenReview:
		// Escape unwinds the review screen's own panes first, so it only leaves once there is
		// nothing left to come back from. Handled inside the model, which is why this only sees the
		// case where it is already at the top.
		if msg.String() == "esc" && a.review.Pane() == "queue" {
			a.screen = screenAgents
			return true, a, nil
		}
		if msg.String() == "K" {
			a.cameFrom = screenReview
			a.screen = screenKeys
			return true, a, nil
		}
		if msg.String() == "ctrl+c" {
			// Quit works here too, whatever pane the review is in: ctrl+c is not typable into the
			// commit subject, so there is no field for it to belong to instead.
			return a.quit()
		}

	case screenKeys:
		// While a field is being edited every keystroke belongs to that field, including "q" and
		// "esc", or a credential containing them could never be typed.
		if a.keys.Adding() {
			return false, a, nil
		}
		switch msg.String() {
		case "esc", "tab":
			a.screen = a.cameFrom
			return true, a, nil
		case "q", "ctrl+c":
			return a.quit()
		}
	}
	return false, a, nil
}

// leaveChat shows another screen only after applying the mode the key stopped on.
//
// The settle timer continues while another screen is visible, but that is not enough: somebody can
// leave Chat and quit before the timer fires. Applying here keeps navigation from turning a visible
// safety choice into a setting that silently disappears when the program closes.
func (a *App) leaveChat(next screen) {
	a.chat.SettleMode()
	a.screen = next
}

// quit is the one application exit path.
//
// A pending mode belongs to the conversation even when another screen is in front. Every route to
// tea.Quit goes through here so quitting from Agents, Review, Keys or Worktrees cannot reopen the
// conversation later in the mode somebody had just moved away from.
func (a App) quit() (bool, App, tea.Cmd) {
	a.chat.SettleMode()
	return true, a, tea.Quit
}

// runAction answers a slash command that named something the chat screen cannot do for itself.
//
// The same destinations the keys reach, deliberately. A slash command that went somewhere a key
// could not, or landed somewhere different from the key with the same name, would be a second
// navigation model rather than a way of discovering the first.
func (a App) runAction(action string) (tea.Model, tea.Cmd) {
	switch action {
	case chat.ActionHelp:
		a.helpFrom = screenChat
		a.leaveChat(screenHelp)
		a.helpScroll = 0
	case chat.ActionNew:
		return a.newConversationModel()
	case chat.ActionAgents:
		a.leaveChat(screenAgents)
	case chat.ActionGreen:
		// The worktree monitor, which is the screen that answers "is this verified, and what has
		// gone stale". Canopy exists for that question, so it gets a word rather than a route.
		a.leaveChat(screenDashboard)
	case chat.ActionKeys:
		a.cameFrom = screenChat
		a.leaveChat(screenKeys)
	case chat.ActionModel:
		a.openModelPicker(screenChat)
	}
	// The same visibility bookkeeping the key routing does, for the same reason: the agents
	// screen animates only while it is in front.
	return a, a.agents.SetVisible(a.screen == screenAgents)
}

// followRename moves everything that names a credential onto its new name.
//
// Four things hold one, and all four are here rather than in the screen that did the renaming,
// because that screen owns credentials and none of these. The engine's conversations and agent
// records, which is what the next message actually looks up. The chat screen's own copy, which is
// what the header shows and what the model picker opens on. What a new conversation and a new agent
// inherit. Miss any one and the rename is half done in a way that only shows up later: a header
// naming a credential that is gone, or a ctrl+n opening on one.
//
// Nothing is refused and nothing can be. The old name stopped resolving when the store wrote, so
// this is keeping up rather than deciding.
func (a *App) followRename(renamed keysui.Rename) {
	a.engine.RenameCredential(renamed.From, renamed.To)
	a.chat.FollowCredentialRename(renamed.From, renamed.To)
	if a.usingKey == renamed.From {
		a.usingKey = renamed.To
		a.agents.SetDefaults(renamed.To, a.keys.ModelFor(renamed.To), a.dir)
	}
}

// openModelPicker builds the picker from what is stored right now and shows it.
func (a *App) openModelPicker(from screen) {
	a.cameFrom = from
	a.picker = newModelPicker(a.keyStore, a.chat.KeyName(), a.chat.ModelName())
	a.picker.fit(pickerHeight(a.dim))
	// Through leaveChat like every other way out of the conversation, so a mode the key had stopped
	// on is applied rather than left to a timer that a quit from this screen would outrun. The
	// conversation stays on screen behind the picker; what it stops doing is taking keystrokes.
	a.leaveChat(screenModel)
}

// applyPickedModel points this conversation at the row the cursor is on.
//
// One call, through the chat screen, which is where a conversation's credential has always been
// changed from: the credential screen does exactly this when somebody picks a key there. The engine
// refuses mid answer and the refusal is shown rather than swallowed, because a choice that appears
// to do nothing is how somebody concludes the screen is broken.
//
// Store.SetModel is deliberately not called. What this conversation runs on and what the key
// defaults to are different facts, and rewriting the second from here would move every future
// conversation on that credential because somebody tried something once.
func (a App) applyPickedModel() (tea.Model, tea.Cmd) {
	choice, ok := a.picker.Chosen()
	if !ok {
		a.screen = a.cameFrom
		return a, nil
	}
	return a.applyModelRow(choice)
}

// applyModelRow is applyPickedModel for a row that did not come from the list, which is the one
// somebody typed. The same path deliberately: a model Canopy has never heard of is applied exactly
// the way a listed one is, or the escape hatch would be a second, weaker way of choosing.
func (a App) applyModelRow(choice modelRow) (tea.Model, tea.Cmd) {
	if a.chat.UseCredential(choice.key, choice.id) {
		// New conversations and new agents follow the credential, but on that key's own default
		// model rather than this choice, which belongs to this conversation alone.
		//
		// Only on acceptance. The engine refuses while a turn is in flight, and this used to move
		// regardless: the conversation correctly stayed where it was, and the next ctrl+n opened on
		// the credential the refusal had just declined, with no model at all.
		a.usingKey = choice.key
	}
	a.screen = a.cameFrom
	return a, nil
}

// newConversationModel is newConversation in the shape Update wants back.
func (a App) newConversationModel() (tea.Model, tea.Cmd) {
	next, cmd := a.newConversation()
	return next, cmd
}

// newConversation starts a fresh session and shows it.
//
// The credential and model carry over. Somebody starting a new conversation is changing subject,
// not changing provider, and making them pick again every time is the kind of small tax that adds
// up to people not using the key at all.
//
// Nothing is deleted. The previous conversation is still in the session list with its history and
// its running turn, which is what makes this safe to press without thinking. A key that silently
// destroyed an hour of work is one nobody presses twice.
func (a App) newConversation() (App, tea.Cmd) {
	created := a.engine.Create(a.usingKey, a.keys.ModelFor(a.usingKey))
	// The command returned is what restarts the mark on the opening screen. A new conversation is
	// the one place it always has something to animate, so dropping it here would mean the corner
	// was alive on the conversation Canopy launched into and still on every one after it.
	cmd := a.chat.SetSession(created.ID, "")
	a.chat.SetNotice("")
	a.confirmingNew = false
	a.screen = screenChat
	return a, cmd
}

// typing reports whether a keystroke currently belongs to a text field.
//
// Asked in one place, because the answer is the same for every global key and getting it wrong
// means a character somebody typed opened a screen instead.
func (a App) typing() bool {
	switch a.screen {
	case screenChat:
		// An empty box is the exception, and it exists because chat is the home screen. While this
		// returned true unconditionally, the one key that lists every other key could not be pressed
		// from the screen the program opens on, which left the help overlay reachable only by
		// navigating away from home first and then wondering what to press there.
		//
		// With nothing typed there is no message for a question mark to be part of, so it means the
		// question it looks like. The moment anything is in the box it goes back to being a
		// character, and the footer says which of the two is in effect.
		return !a.chat.InputEmpty()
	case screenAgents:
		return a.agents.Naming()
	case screenKeys:
		return a.keys.Adding()
	case screenReview:
		return a.review.Pane() == "commit"
	default:
		return false
	}
}

func (a App) View() string {
	if !a.dim.Usable() {
		return TooSmall(a.dim)
	}

	// Asked of the engine on the way to drawing rather than read from what the last event left
	// behind, so the count is right on the frame after a question is answered from this screen as
	// well as on the frame after one is raised somewhere else. Every screen is handed it, because a
	// screen that did not show it would be a place somebody could stand and not know.
	needing := len(a.attention())

	switch a.screen {
	case screenHelp:
		footer := Keys(a.dim.Width, "any other key", "back")
		if HelpHeight(a.dim.Width) > a.dim.BodyHeight() {
			footer = Keys(a.dim.Width, "j/k", "scroll", "any other key", "back")
		}
		return Frame(a.dim, Status{Screen: "help", Attention: needing},
			HelpFrom(a.dim, a.helpScroll), footer)

	case screenAgents:
		// Credentials and help stay ahead of the movement keys, because they are the two hints
		// somebody stuck actually needs and the footer drops from the right on a narrow window.
		footer := Keys(a.dim.Width, "enter", "open", "1-8", "jump", "v", "layout", "n", "new",
			"esc", "chat", "K", "credentials", "?", "help", "hjkl", "move", "[ ]", "page",
			"w", "worktrees", "r", "review")
		if a.agents.SelectedAwaiting() {
			// While the selection is a waiting agent, enter answers rather than opens, so a footer
			// still saying "open" would name the one key whose meaning just changed and lie about
			// it. The answer keys lead, and opening moves to the digit, which is what it costs.
			footer = Keys(a.dim.Width, "enter", "approve once", "backspace", "decline",
				"1-8", "jump, again to open", "v", "layout", "esc", "chat", "?", "help",
				"hjkl", "move", "[ ]", "page")
		}
		if notice := a.agents.Notice(); notice != "" {
			// A stale compact answer is an outcome, not a key hint. It gets the whole footer for one
			// keystroke so the screen cannot imply an approval succeeded when the request was gone.
			footer = notice
		} else if a.agents.ConfirmingDirect() {
			// The confirmation panel in the body already names its keys, with more room to say what
			// they mean. A footer repeating them is two lists to keep agreeing, so the footer goes
			// quiet for the one keystroke the panel is up.
			footer = ""
		} else if a.agents.Naming() {
			// "continue", not "review": enter advances the form, and this screen has an actual
			// review on r. Two different things called by one name is how somebody presses the
			// wrong one.
			footer = Keys(a.dim.Width, "enter", "continue", "esc", "cancel")
		}
		return Frame(a.dim, Status{Screen: "agents", Attention: needing,
			Parts: []string{a.agents.Context()}}, a.agents.Body(), footer)

	case screenReview:
		return Frame(a.dim, Status{Screen: "review", Attention: needing,
			Parts: []string{a.review.Context()}}, a.review.Body(), a.review.Footer())

	case screenChat:
		// The keys mean something different while a question is up, and again depending on whether
		// there is anything in the box, so the footer says which set is in effect rather than
		// listing commands that are not.
		//
		// Hints are dropped from the right when they do not fit, so the order is what somebody
		// needs first rather than what the program does most. At eighty columns only about five
		// survive, and the ones that have to be among them are how to get help and how to reach a
		// credential, because everything else in the program is downstream of those two.
		// The mode is not in the footer at all: it is already written into the box's top edge and
		// the header, and the eighteen columns "shift+tab plan" cost at eighty columns were exactly
		// the columns esc and ctrl+c needed to stay on screen. shift+tab keeps a hint under the
		// name "mode", which is what it changes.
		escMeans := "clear"
		if a.chat.Working() {
			escMeans = "stop"
		}
		// Ordered by what is lost if it falls off the end, because Keys drops from the right and
		// eighty columns is the width most terminals actually open at. `ctrl+d agents` used to sit
		// behind `ctrl+n new` in both lists and was therefore the first thing cut at eighty, which
		// meant the key to the several-agents view was invisible on a default terminal in the
		// product named for it.
		footer := Keys(a.dim.Width, "enter", "send", "esc", escMeans, "ctrl+d", "agents",
			"shift+tab", "mode", "ctrl+c", "quit", "↑", "history", "ctrl+k", "keys",
			"ctrl+n", "new", "ctrl+o", "full output", "ctrl+r", "compact")
		if a.chat.InputEmpty() {
			footer = Keys(a.dim.Width, "enter", "send", "?", "help", "ctrl+d", "agents",
				"shift+tab", "mode", "ctrl+k", "keys", "ctrl+n", "new", "↑", "history",
				"ctrl+o", "full output", "ctrl+r", "compact", "ctrl+c", "quit")
		}
		if a.chat.Awaiting() {
			// The question's own panel names the keys, with the scope of "always" spelled out,
			// which is more than a footer can say. Repeating a shorter version here was two lists
			// disagreeing about the same three keys.
			footer = ""
		}
		return Frame(a.dim, a.chatStatus(needing), a.chat.Body(), footer)

	case screenModel:
		// The conversation's own header and its own body, with the picker where the message box
		// goes. Not a screen of its own, deliberately: what is being chosen is which model answers
		// next in this conversation, and the last thing that should leave the screen while it is
		// being chosen is the conversation.
		//
		// What the footer loses is the message box's keys, which are the ones that do nothing while
		// there is nothing to type into. What it gains is the mark's meaning, which used to cost a
		// line of the list and now costs none.
		footer := Keys(a.dim.Width, "j/k", "move", "enter", "use it here", "esc", "back, unchanged",
			"*", "in use now", "ctrl+k", "credentials")
		if a.picker.typing {
			footer = Keys(a.dim.Width, "enter", "use it here", "esc", "back to the list")
		}
		return Frame(a.dim, a.chatStatus(needing),
			a.chat.InPlaceOfBox(a.picker.Block(a.dim.Width)).Body(), footer)
	case screenKeys:
		return Frame(a.dim, Status{Screen: "credentials", Attention: needing},
			a.keys.Body(), a.keys.Footer())
	default:
		return Frame(a.dim, Status{Screen: "worktrees", Attention: needing,
			Parts: []string{a.dashboard.Context()}},
			a.dashboard.Body(),
			Keys(a.dim.Width, "j/k", "move", "K", "credentials", "r", "refresh", "esc/q", "agents",
				"?", "help"))
	}
}

// chatStatus is the header the conversation wears.
//
// One function rather than one per screen that shows the conversation, because the model picker
// shows it too and a header that changed while a block was open over the box would say the picker
// had replaced the conversation, which is exactly what it does not do.
func (a App) chatStatus(needing int) Status {
	return Status{
		Screen: "chat",
		// Whose conversation this is, in the corner where the brand used to be. Empty on a
		// conversation no agent owns, a fresh one started with ctrl+n among them, and the brand
		// comes back there rather than the corner naming something that does not exist.
		Agent:     a.chat.AgentName(),
		Attention: needing,
		Parts:     a.chat.ContextParts(),
		Mode:      a.chat.Mode(),
		// Only once the opening screen has gone, which is drawing the name itself.
		Wordmark: !a.chat.Blank(),
	}
}

// Screen reports which view is in front. For tests.
func (a App) Screen() string {
	switch a.screen {
	case screenChat:
		return "chat"
	case screenAgents:
		return "agents"
	case screenReview:
		return "review"
	case screenHelp:
		return "help"
	case screenKeys:
		return "keys"
	case screenModel:
		return "model"
	default:
		return "dashboard"
	}
}

// SubscribeCmd returns the command that waits on the next engine event.
//
// Init batches this with the chat's own commands, and a batched command yields a tea.BatchMsg rather
// than the event itself, so a test driving the event path needs the subscription on its own.
// Exported for that reason and no other.
func (a App) SubscribeCmd() tea.Cmd { return a.dashboard.Init() }

// ChatInput exposes what has been typed into the message box. For tests.
func (a App) ChatInput() string { return a.chat.InputValue() }

// ChatSubscribeCmd is the same, for the chat screen's own event stream.
func (a App) ChatSubscribeCmd() tea.Cmd { return a.chat.SubscribeCmd() }

// ChatAwaiting reports whether the conversation has a question waiting on a person. For tests,
// which assert that walking away from one leaves it waiting rather than answering it.
func (a App) ChatAwaiting() bool { return a.chat.Awaiting() }

// ChatSession is the conversation the chat screen is on. For the caller, which prints the code to
// come back to it, and for tests.
func (a App) ChatSession() string { return a.chat.SessionID() }

// RunAppConfigured starts the application and returns the conversation it was left in.
//
// The conversation comes back because whoever started Canopy is the one that prints how to return
// to it, and by the time the program exits the answer is not the one that was passed in: somebody
// who pressed ctrl+n four times is in a different conversation from the one they opened.
func RunAppConfigured(
	store core.SnapshotStore, keyStore keysui.Store, engine Engine, dir, keyName string,
	options AppOptions,
) (string, error) {
	// Mouse reporting is asked for so the wheel arrives as a wheel.
	//
	// Without it, a terminal in the alternate screen translates the wheel into arrow key sequences,
	// which is how `less` scrolls and is fine right up until the arrow keys mean something. Once up
	// recalled the last message, scrolling back to reread an answer replaced what was in the
	// message box with an old prompt, and there is no way to tell the two apart after the fact
	// because by then they are the same bytes.
	//
	// It costs something and the cost is worth stating. With mouse reporting on, dragging to select
	// text no longer reaches the terminal, so copying out of Canopy means holding a modifier while
	// dragging: option on macOS terminals, shift on most others. That is the standard price every
	// full screen program pays for the wheel, and the trade is worth making in the direction that
	// does not silently eat what somebody was typing.
	program := tea.NewProgram(
		NewAppConfigured(store, keyStore, engine, dir, keyName, options),
		tea.WithAltScreen(), tea.WithMouseCellMotion())

	final, err := program.Run()
	if app, ok := final.(App); ok {
		return app.ChatSession(), err
	}
	return "", err
}
