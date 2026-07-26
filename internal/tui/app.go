package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// screen identifies which view is in front.
type screen int

const (
	screenDashboard screen = iota
	screenKeys
)

// App is the top level model. It owns which screen is showing and routes messages to it.
//
// The dashboard and the credential screen are kept as separate models rather than one growing
// model, because they share nothing except the terminal. Merging them would mean every keystroke
// handler needing to know which mode it was in, which is how a TUI turns into one large switch
// nobody wants to touch.
type App struct {
	screen    screen
	dashboard Model
	keys      keysui.Model
}

// NewApp builds the application.
//
// A run with no credentials opens on the credential screen. An empty dashboard would be
// technically correct and useless: the first thing a new user needs is not a list of nothing, it
// is the one action that makes the rest of the program work.
func NewApp(store core.SnapshotStore, keyStore keysui.Store) App {
	app := App{
		dashboard: New(store),
		keys:      keysui.New(keyStore),
	}
	if app.keys.IsEmpty() {
		app.screen = screenKeys
	}
	return app
}

func (a App) Init() tea.Cmd {
	return a.dashboard.Init()
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
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
	switch a.screen {
	case screenKeys:
		return a.keys.View()
	default:
		return a.dashboard.View() + "\n" + styleFooter.Render("  K credentials")
	}
}

// Screen reports which view is in front. For tests.
func (a App) Screen() string {
	if a.screen == screenKeys {
		return "keys"
	}
	return "dashboard"
}

// RunApp starts the full application.
func RunApp(store core.SnapshotStore, keyStore keysui.Store) error {
	program := tea.NewProgram(NewApp(store, keyStore), tea.WithAltScreen())
	_, err := program.Run()
	return err
}
