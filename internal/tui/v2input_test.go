package tui_test

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// A paste reaches whatever text field is in front, not only the message box: a model id pasted into
// the picker's own field is the model applied.
func TestAPasteFillsThePickersField(t *testing.T) {
	engine := onOpus()
	app := openPicker(t, twoKeys(), engine)
	next := tea.Model(app)
	for range 7 {
		next, _ = next.(tui.App).Update(keyText("j"))
	}
	next, _ = next.(tui.App).Update(keyCode(tea.KeyEnter))
	next, _ = next.(tui.App).Update(tea.PasteMsg{Content: "claude-pasted\x1b[2J-model\n"})
	_, _ = next.(tui.App).Update(keyCode(tea.KeyEnter))
	if engine.using[1] != "claude-pasted[2J-model" {
		t.Fatalf("the pasted model reached the conversation as %v", engine.using)
	}
}

// The agent list's name field takes a paste too, as one line.
func TestAPasteNamesANewAgent(t *testing.T) {
	store := fake.New()
	defer store.Close()
	var model tea.Model = tui.NewAppConfigured(store, withOneKey(), &stubEngine{}, "myproject", "claude",
		tui.AppOptions{Session: "session-1"})
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model, _ = model.Update(keyCode('d', tea.ModCtrl))
	model, _ = model.Update(keyText("n"))
	model, _ = model.Update(tea.PasteMsg{Content: "parser-fix\r\n"})
	if view := plain(model.View().Content); !strings.Contains(view, "parser-fix") {
		t.Fatalf("the pasted name is not in the field:\n%s", view)
	}
	if model.(tui.App).Screen() != "agents" {
		t.Fatalf("the newline in the paste acted as enter and left for %q", model.(tui.App).Screen())
	}
}

// A paste is a change of mind about quitting, as any keystroke is: ctrl+c, a paste, ctrl+c is not
// the double press that quits.
func TestAPasteCancelsAPendingQuit(t *testing.T) {
	store := fake.New()
	defer store.Close()
	var model tea.Model = tui.NewAppConfigured(store, withOneKey(), &stubEngine{}, "myproject", "claude",
		tui.AppOptions{Session: "session-1"})
	model, _ = model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	model, _ = model.Update(keyCode('c', tea.ModCtrl))
	model, _ = model.Update(tea.PasteMsg{Content: "more text"})
	_, cmd := model.Update(keyCode('c', tea.ModCtrl))
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("ctrl+c after a paste quit, as if the paste had not happened")
		}
	}
}

// The chat is drawn below the header, so a mouse row has to be moved up by the header's height
// before the chat sees it: a drag over words on screen copies those words, not the line above them.
func TestAMouseDragCopiesWhatIsUnderIt(t *testing.T) {
	store := fake.New()
	defer store.Close()
	engine := &stubEngine{session: core.Session{ID: "session-1", Turns: []core.Turn{{
		ID: "t1", State: core.TurnComplete, Request: core.Message{Text: "where is the parser"},
		Text: "The parser lives in internal/config.",
	}}}}
	app := tui.NewAppConfigured(store, withOneKey(), engine, "myproject", "claude",
		tui.AppOptions{Session: "session-1"})
	var copied []string
	app.SetClipboard(func(text string) error {
		copied = append(copied, text)
		return nil
	})
	var model tea.Model = app
	model, _ = model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	row, col := -1, -1
	for r, line := range strings.Split(plain(model.View().Content), "\n") {
		if c := strings.Index(line, "parser lives"); c >= 0 {
			row, col = r, len([]rune(line[:c]))
			break
		}
	}
	if row < 0 {
		t.Fatalf("the reply is not on screen:\n%s", plain(model.View().Content))
	}
	model, _ = model.Update(tea.MouseClickMsg{Button: tea.MouseLeft, X: col, Y: row})
	model, _ = model.Update(tea.MouseMotionMsg{Button: tea.MouseLeft, X: col + 11, Y: row})
	_, _ = model.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: col + 11, Y: row})
	if len(copied) != 1 || copied[0] != "parser lives" {
		t.Fatalf("dragging over %q copied %q", "parser lives", copied)
	}
}

// The terminal's answer about its background reaches the theme, so adaptive colours follow it.
func TestTheTerminalsBackgroundReachesTheTheme(t *testing.T) {
	defer theme.SetDark(true)
	store := fake.New()
	defer store.Close()
	var model tea.Model = tui.NewAppConfigured(store, withOneKey(), &stubEngine{}, "myproject", "claude",
		tui.AppOptions{Session: "session-1"})
	model.Update(tea.BackgroundColorMsg{Color: color.White})
	if theme.Dark() {
		t.Fatal("a white background left the theme drawing for a dark one")
	}
	model.Update(tea.BackgroundColorMsg{Color: color.Black})
	if !theme.Dark() {
		t.Fatal("a black background left the theme drawing for a light one")
	}
}
