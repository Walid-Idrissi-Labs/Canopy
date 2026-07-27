package tui

import (
	"time"

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
	screenSplash screen = iota
	screenChat
	screenAgents
	screenDashboard
	screenReview
	screenKeys
	screenHelp
)

// splashDuration is how long the launch screen stays before the application appears.
//
// Short on purpose. A splash is worth a moment of recognition and nothing more, and any key
// dismisses it, so nobody who is in a hurry ever waits.
const splashDuration = 900 * time.Millisecond

// splashDoneMsg ends the splash.
type splashDoneMsg struct{}

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

	// afterSplash is the screen to show once the launch screen clears, decided at construction so
	// the splash never has to know anything about credentials.
	afterSplash screen

	// confirmingNew is true when a new conversation has been asked for while a reply is still
	// arriving, and the same key pressed again will go through with it.
	confirmingNew bool

	// dir is the working directory new agents are given.
	dir string

	// usingKey is the credential last applied from the credential screen, so choosing the same one
	// twice does not re-apply it on every keystroke.
	usingKey string

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
	app := App{
		screen:      screenSplash,
		engine:      engine,
		afterSplash: screenChat,
		chat:        chat.New(engine, "session-1", dir, keyName),
		agents:      agentsui.New(engine),
		dashboard:   New(store),
		review:      NewReview(review),
		keys:        keysui.New(keyStore),
		cameFrom:    screenChat,
		usingKey:    keyName,
		dir:         dir,
		dim:         Dimensions{Width: 80, Height: 24},
	}
	// What a new agent inherits. Without it every agent created from that screen was built with an
	// empty credential and an empty working directory, which fails on its first message.
	app.agents.SetDefaults(keyName, keysui.New(keyStore).ModelFor(keyName), dir)
	// The credential screen comes first when there is no credential to run on, which is not the same
	// as having none stored. With several stored and none chosen there is no obvious default, and
	// landing on the chat means typing a message and watching it fail, which is a worse introduction
	// than being asked the one question that makes everything else work.
	if app.keys.IsEmpty() || keyName == "" {
		app.afterSplash = screenKeys
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
	return tea.Batch(
		a.dashboard.Init(),
		a.chat.Init(),
		tea.Tick(splashDuration, func(time.Time) tea.Msg { return splashDoneMsg{} }),
	)
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case agentsui.SwitchMsg:
		// The agents view asks and the application decides, which is what keeps "which screen is
		// showing" in one place.
		a.chat.SetSession(m.SessionID, m.AgentName)
		a.screen = screenChat
		return a, nil

	case tea.WindowSizeMsg:
		a.resize(Dimensions{Width: m.Width, Height: m.Height})
	case splashDoneMsg:
		if a.screen == screenSplash {
			a.screen = a.afterSplash
		}
		return a, nil
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		// Any key dismisses the splash, and is then not passed on. Swallowing it is deliberate:
		// the first keystroke after launch is usually impatience rather than a command, and acting
		// on it would mean landing somewhere you did not ask for.
		if a.screen == screenSplash {
			a.screen = a.afterSplash
			return a, nil
		}

		// Help is answered before anything else, and any key leaves it. Somebody who opened it by
		// accident should not have to find the one key that closes it.
		if a.screen == screenHelp {
			a.screen = a.helpFrom
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
			return true, a.newConversation(), nil

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
func (a App) newConversation() App {
	created := a.engine.Create(a.usingKey, a.keys.ModelFor(a.usingKey))
	a.chat.SetSession(created.ID, "")
	a.chat.SetNotice("")
	a.confirmingNew = false
	a.screen = screenChat
	return a
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
	if a.screen == screenSplash {
		return Splash(a.dim, "a terminal coding agent for running several at once")
	}
	if !a.dim.Usable() {
		return TooSmall(a.dim)
	}

	switch a.screen {
	case screenHelp:
		return Frame(a.dim, "canopy", "keys", Help(a.dim), Keys(a.dim.Width, "any key", "back"))

	case screenAgents:
		footer := Keys(a.dim.Width, "enter", "open", "n", "new", "j/k", "move", "v", "layout",
			"esc", "chat", "w", "worktrees", "r", "review", "?", "keys")
		if a.agents.Naming() {
			footer = Keys(a.dim.Width, "enter", "create", "esc", "cancel")
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
		footer := Keys(a.dim.Width, "enter", "send", "esc", "stop", "↑", "history",
			"ctrl+n", "new", "ctrl+k", "keys", "ctrl+d", "agents", "ctrl+r", "compact",
			"ctrl+c", "quit")
		if a.chat.InputEmpty() {
			footer = Keys(a.dim.Width, "enter", "send", "?", "help", "ctrl+k", "keys",
				"ctrl+n", "new", "ctrl+d", "agents", "↑", "history", "ctrl+r", "compact",
				"esc", "stop", "ctrl+c", "quit")
		}
		if a.chat.Awaiting() {
			footer = Keys(a.dim.Width, "y", "allow once", "a", "always", "any other key", "refuse")
		}
		return Frame(a.dim, "canopy", a.chat.Context(), a.chat.Body(), footer)
	case screenKeys:
		return Frame(a.dim, "canopy", "credentials", a.keys.Body(), a.keys.Footer())
	default:
		return Frame(a.dim, "canopy", a.dashboard.Context(), a.dashboard.Body(),
			Keys(a.dim.Width, "j/k", "move", "K", "credentials", "r", "refresh", "esc", "agents"))
	}
}

// Screen reports which view is in front. For tests.
func (a App) Screen() string {
	switch a.screen {
	case screenSplash:
		return "splash"
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
// Init batches this with the splash timer, and a batched command yields a tea.BatchMsg rather than
// the event itself, so a test driving the event path needs the subscription on its own. Exported
// for that reason and no other.
func (a App) SubscribeCmd() tea.Cmd { return a.dashboard.Init() }

// ChatInput exposes what has been typed into the message box. For tests.
func (a App) ChatInput() string { return a.chat.InputValue() }

// ChatSubscribeCmd is the same, for the chat screen's own event stream.
func (a App) ChatSubscribeCmd() tea.Cmd { return a.chat.SubscribeCmd() }

// DismissSplash skips the launch screen. For tests, which should not wait on a timer.
func (a App) DismissSplash() App {
	if a.screen == screenSplash {
		a.screen = a.afterSplash
	}
	return a
}

// RunApp starts the full application.
func RunApp(
	store core.SnapshotStore, keyStore keysui.Store, engine Engine, dir, keyName string,
) error {
	return RunAppWithReview(store, keyStore, engine, dir, keyName, nil)
}

// RunAppWithReview starts the application with a source for the review screen.
func RunAppWithReview(
	store core.SnapshotStore, keyStore keysui.Store, engine Engine, dir, keyName string,
	review ReviewSource,
) error {
	program := tea.NewProgram(
		NewAppWithReview(store, keyStore, engine, dir, keyName, review), tea.WithAltScreen())
	_, err := program.Run()
	return err
}
