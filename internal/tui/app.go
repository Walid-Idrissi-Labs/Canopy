package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// screen identifies which view is in front.
type screen int

const (
	screenSplash screen = iota
	screenChat
	screenDashboard
	screenKeys
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

	// afterSplash is the screen to show once the launch screen clears, decided at construction so
	// the splash never has to know anything about credentials.
	afterSplash screen

	// cameFrom is where escape goes back to. Recorded rather than assumed, because the credential
	// screen is reachable from both chat and the dashboard, and always returning to one of them
	// would throw away where somebody actually was.
	cameFrom screen

	chat      chat.Model
	dashboard Model
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
	store core.SnapshotStore, keyStore keysui.Store, engine chat.Engine, dir, keyName string,
) App {
	app := App{
		screen:      screenSplash,
		afterSplash: screenChat,
		chat:        chat.New(engine, "session-1", dir, keyName),
		dashboard:   New(store),
		keys:        keysui.New(keyStore),
		cameFrom:    screenChat,
		dim:         Dimensions{Width: 80, Height: 24},
	}
	if app.keys.IsEmpty() {
		app.afterSplash = screenKeys
	}

	app.resize(app.dim)
	return app
}

func (a *App) resize(dim Dimensions) {
	a.dim = dim
	a.chat.SetSize(dim.Width, dim.BodyHeight())
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
		case screenKeys:
			var cmd tea.Cmd
			a.keys, cmd = a.keys.Update(key)
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
			// Quits only when nothing is running. With a turn in flight the first press stops the
			// turn, because that is what somebody hitting it during a long reply almost always
			// means, and quitting instead would throw the conversation away.
			if a.chat.Working() {
				a.chat, _ = a.chat.Update(tea.KeyMsg{Type: tea.KeyEsc})
				return true, a, nil
			}
			return true, a, tea.Quit
		case "ctrl+k":
			a.cameFrom = screenChat
			a.screen = screenKeys
			return true, a, nil
		case "ctrl+d":
			a.screen = screenDashboard
			return true, a, nil
		}

	case screenDashboard:
		switch msg.String() {
		case "K":
			a.cameFrom = screenDashboard
			a.screen = screenKeys
			return true, a, nil
		case "esc", "tab":
			a.screen = screenChat
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

func (a App) View() string {
	if a.screen == screenSplash {
		return Splash(a.dim, "a terminal coding agent for running several at once")
	}
	if !a.dim.Usable() {
		return TooSmall(a.dim)
	}

	switch a.screen {
	case screenChat:
		return Frame(a.dim, "canopy", a.chat.Context(), a.chat.Body(),
			Keys("enter", "send", "esc", "stop", "ctrl+d", "agents", "ctrl+k", "keys",
				"ctrl+c", "quit"))
	case screenKeys:
		return Frame(a.dim, "canopy", "credentials", a.keys.Body(), a.keys.Footer())
	default:
		return Frame(a.dim, "canopy", a.dashboard.Context(), a.dashboard.Body(),
			Keys("j/k", "move", "K", "credentials", "r", "refresh", "esc", "back to chat"))
	}
}

// Screen reports which view is in front. For tests.
func (a App) Screen() string {
	switch a.screen {
	case screenSplash:
		return "splash"
	case screenChat:
		return "chat"
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
	store core.SnapshotStore, keyStore keysui.Store, engine chat.Engine, dir, keyName string,
) error {
	program := tea.NewProgram(
		NewApp(store, keyStore, engine, dir, keyName), tea.WithAltScreen())
	_, err := program.Run()
	return err
}
