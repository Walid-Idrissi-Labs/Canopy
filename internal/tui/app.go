package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// screen identifies which view is in front.
type screen int

const (
	screenSplash screen = iota
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
// The dashboard and the credential screen are kept as separate models rather than one growing
// model, because they share nothing except the terminal. Merging them would mean every keystroke
// handler needing to know which mode it was in, which is how a TUI turns into one large switch
// nobody wants to touch.
type App struct {
	screen screen

	// afterSplash is the screen to show once the launch screen clears, decided at construction so
	// the splash never has to know anything about credentials.
	afterSplash screen

	dashboard Model
	keys      keysui.Model

	dim Dimensions
}

// NewApp builds the application.
//
// A run with no credentials opens on the credential screen. An empty dashboard would be
// technically correct and useless: the first thing a new user needs is not a list of nothing, it
// is the one action that makes the rest of the program work.
func NewApp(store core.SnapshotStore, keyStore keysui.Store) App {
	app := App{
		screen:      screenSplash,
		afterSplash: screenDashboard,
		dashboard:   New(store),
		keys:        keysui.New(keyStore),
		dim:         Dimensions{Width: 80, Height: 24},
	}
	if app.keys.IsEmpty() {
		app.afterSplash = screenKeys
	}
	return app
}

func (a App) Init() tea.Cmd {
	return tea.Batch(
		a.dashboard.Init(),
		tea.Tick(splashDuration, func(time.Time) tea.Msg { return splashDoneMsg{} }),
	)
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.dim = Dimensions{Width: m.Width, Height: m.Height}
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
		// so typing a credential and pressing esc to cancel the field would exit the program and
		// lose the session. Screens that are not visible do not get keys.
		switch a.screen {
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

	// Everything else goes to both, so the dashboard keeps consuming engine events while another
	// screen is in front. A dashboard that stopped listening whenever you looked elsewhere would
	// be stale the moment you came back.
	updated, cmd := a.dashboard.Update(msg)
	if next, ok := updated.(Model); ok {
		a.dashboard = next
	}
	a.keys, _ = a.keys.Update(msg)
	return a, cmd
}

// routeKey handles screen switching, and reports whether it consumed the key.
func (a App) routeKey(msg tea.KeyMsg) (bool, tea.Model, tea.Cmd) {
	switch a.screen {
	case screenDashboard:
		if msg.String() == "k" {
			// Only when it is unambiguous. "k" is also move-up, so it opens the credential screen
			// only from a dashboard that has nothing to move around in.
			if len(a.dashboard.Snapshot().Workspaces) == 0 {
				a.screen = screenKeys
				return true, a, nil
			}
		}
		if msg.String() == "K" {
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
			a.screen = screenDashboard
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
	case screenKeys:
		return Frame(a.dim, "canopy", "credentials", a.keys.Body(), a.keys.Footer())
	default:
		return Frame(a.dim, "canopy", a.dashboard.Context(), a.dashboard.Body(),
			Keys("j/k", "move", "K", "credentials", "r", "refresh", "q", "quit"))
	}
}

// Screen reports which view is in front. For tests.
func (a App) Screen() string {
	switch a.screen {
	case screenSplash:
		return "splash"
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

// DismissSplash skips the launch screen. For tests, which should not wait on a timer.
func (a App) DismissSplash() App {
	if a.screen == screenSplash {
		a.screen = a.afterSplash
	}
	return a
}

// RunApp starts the full application.
func RunApp(store core.SnapshotStore, keyStore keysui.Store) error {
	program := tea.NewProgram(NewApp(store, keyStore), tea.WithAltScreen())
	_, err := program.Run()
	return err
}
