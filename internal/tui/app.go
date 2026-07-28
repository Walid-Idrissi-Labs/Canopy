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

	// cameFrom is where escape goes back to. Recorded rather than assumed, because the credential
	// screen is reachable from both chat and the dashboard, and always returning to one of them
	// would throw away where somebody actually was.
	cameFrom screen

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
		cameFrom:  screenChat,
		usingKey:  keyName,
		dir:       dir,
		dim:       Dimensions{Width: 80, Height: 24},
	}
	app.chat.SetCommands(options.Commands)
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
		return a, cmd

	case tea.MouseMsg:
		// Routed to the screen in front rather than broadcast, for the same reason keystrokes are:
		// a wheel notch is aimed at what somebody is looking at. Broadcasting would scroll the
		// conversation behind whatever screen they had actually opened.
		//
		// Only chat answers it today. The other screens navigate with j and k and ignoring the
		// wheel there is what they already did, since before this the wheel was arriving as arrow
		// keys that none of them bound.
		if a.screen == screenChat {
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
		// Not while something is being typed into, or a message containing a question mark could
		// never be written.
		if key.String() == "?" && !a.typing() {
			a.helpFrom, a.screen = a.screen, screenHelp
			return a, nil
		}

		// A pending confirmation lasts exactly one keystroke. Anything other than the same key
		// again is a change of mind, and a confirmation that outlived it would eventually fire on
		// a keystroke somebody meant for something else entirely.
		if a.confirmingNew && key.String() != "ctrl+n" {
			a.confirmingNew = false
			a.chat.SetNotice("")
		}

		if handled, next, cmd := a.routeKey(key); handled {
			return next, cmd
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

			// A credential chosen on that screen is a fact about the conversation, so it is applied
			// here where the conversation lives. The screen states a preference and owns nothing.
			if name, picked := a.keys.Chosen(); picked && name != a.usingKey {
				model := a.keys.ModelFor(name)
				a.usingKey = name
				a.chat.UseCredential(name, model)
				// Agents created after the switch inherit it too, or the next one would quietly go
				// on using the credential somebody had just moved away from.
				a.agents.SetDefaults(name, model, a.dir)
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
	// state it had when you last looked.
	a.agents, _ = a.agents.Update(msg)

	a.keys, _ = a.keys.Update(msg)
	return a, tea.Batch(cmds...)
}

// routeKey handles screen switching, and reports whether it consumed the key.
//
// Chat is the awkward case and it is worth saying why. Every printable key belongs to the message
// box, so navigation away from chat has to be on keys that are not printable. Anything else would
// mean the letter that opens the dashboard could never be typed in a message.
func (a App) routeKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
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
			return true, a, tea.Quit
		case "ctrl+n":
			if a.chat.Awaiting() {
				return false, a, nil
			}
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
			// Navigation is disabled while a question is open, for the same reason: leaving the
			// screen with a tool call waiting would hide the thing that is blocking.
			if a.chat.Awaiting() {
				return false, a, nil
			}
			if msg.String() == "ctrl+k" {
				a.cameFrom = screenChat
				a.screen = screenKeys
			} else {
				a.screen = screenAgents
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
		}

	case screenDashboard:
		switch msg.String() {
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
			return true, a, tea.Quit
		}
	}
	return false, a, nil
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

	switch a.screen {
	case screenHelp:
		footer := Keys(a.dim.Width, "any other key", "back")
		if HelpHeight(a.dim.Width) > a.dim.BodyHeight() {
			footer = Keys(a.dim.Width, "j/k", "scroll", "any other key", "back")
		}
		return Frame(a.dim, "canopy", "keys", HelpFrom(a.dim, a.helpScroll), footer)

	case screenAgents:
		footer := Keys(a.dim.Width, "enter", "open", "n", "new", "j/k", "move", "v", "layout",
			"esc", "chat", "w", "worktrees", "r", "review", "K", "credentials", "?", "help")
		if a.agents.ConfirmingDirect() {
			footer = Keys(a.dim.Width, "y", "create direct agent", "esc", "back")
		} else if a.agents.Naming() {
			footer = Keys(a.dim.Width, "enter", "review", "esc", "cancel")
		}
		return Frame(a.dim, "canopy", a.agents.Context(), a.agents.Body(), footer)

	case screenReview:
		return Frame(a.dim, "canopy", a.review.Context(), a.review.Body(), a.review.Footer())

	case screenChat:
		// The keys mean something different while a question is up, and again depending on whether
		// there is anything in the box, so the footer says which set is in effect rather than
		// listing commands that are not.
		//
		// Hints are dropped from the right when they do not fit, so the order is what somebody
		// needs first rather than what the program does most. At eighty columns only about five
		// survive, and the ones that have to be among them are how to get help and how to reach a
		// credential, because everything else in the program is downstream of those two.
		footer := Keys(a.dim.Width, "enter", "send", "esc", "stop", "shift+tab", a.chat.Mode(),
			"↑", "history", "ctrl+n", "new", "ctrl+k", "keys", "ctrl+d", "agents",
			"ctrl+r", "compact", "ctrl+c", "quit")
		if a.chat.InputEmpty() {
			// The mode is named after the key rather than described, so the footer says which one
			// you are in as well as how to change it, in the space one hint costs.
			footer = Keys(a.dim.Width, "enter", "send", "shift+tab", a.chat.Mode(), "?", "help",
				"ctrl+k", "keys", "ctrl+n", "new", "ctrl+d", "agents", "↑", "history",
				"ctrl+r", "compact", "esc", "stop", "ctrl+c", "quit")
		}
		if a.chat.Awaiting() {
			footer = Keys(a.dim.Width, "y", "allow once", "a", "always", "any other key", "refuse")
		}
		return Frame(a.dim, "canopy", a.chat.Context(), a.chat.Body(), footer)
	case screenKeys:
		return Frame(a.dim, "canopy", "credentials", a.keys.Body(), a.keys.Footer())
	default:
		return Frame(a.dim, "canopy", a.dashboard.Context(), a.dashboard.Body(),
			Keys(a.dim.Width, "j/k", "move", "K", "credentials", "r", "refresh", "esc/q", "agents",
				"?", "help"))
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
