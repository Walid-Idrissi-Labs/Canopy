package tui_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// M-04. Quitting and restarting to get a clean context is what people do when there is no key for
// this, and it costs them their credential choice every time.
func TestANewConversationStartsEmptyAndKeepsTheCredential(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{
		ID: "session-1",
		Turns: []core.Turn{{
			Request: core.Message{Text: "an earlier question"},
			Text:    "an earlier answer",
			State:   core.TurnComplete,
		}},
	}}
	app := launchWith(store, withOneKey(), engine)

	before := plain(app.(tui.App).View())
	if !strings.Contains(before, "an earlier question") {
		t.Fatalf("the conversation is not on screen to begin with:\n%s", before)
	}

	next, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})

	if engine.created != 1 {
		t.Fatalf("%d conversations created, want one", engine.created)
	}
	// The credential carries over. Somebody starting a new conversation is changing subject, not
	// changing provider, and being asked again every time is the tax that stops the key being used.
	if engine.session.KeyName != "claude" {
		t.Errorf("the new conversation is on credential %q", engine.session.KeyName)
	}

	after := plain(next.(tui.App).View())
	if strings.Contains(after, "an earlier question") {
		t.Errorf("the previous conversation is still on screen:\n%s", after)
	}
	if next.(tui.App).Screen() != "chat" {
		t.Errorf("a new conversation landed on %q", next.(tui.App).Screen())
	}
}

// The empty conversation is a screen with nothing on it, and a blank rectangle is indistinguishable
// from a conversation that failed to load. The mark is what says which one this is.
func TestANewConversationShowsTheMarkAgain(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{
		ID:    "session-1",
		Turns: []core.Turn{{Request: core.Message{Text: "hello"}, State: core.TurnComplete}},
	}}
	app := launchWith(store, withOneKey(), engine)

	next, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})

	view := plain(next.(tui.App).View())
	if !strings.Contains(view, "█") {
		t.Errorf("the new conversation has no mark on it:\n%s", view)
	}
	if !strings.Contains(view, "Canopy") {
		t.Errorf("the new conversation does not name the program:\n%s", view)
	}
}

// Half typed text belongs to the conversation it was being written in. Carrying it into a new one
// means finding words in the box that were meant for somebody else.
func TestANewConversationEmptiesTheBox(t *testing.T) {
	store := fake.New()
	defer store.Close()

	app := launchWith(store, withOneKey(), &stubEngine{})
	next, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("half written")})
	if next.(tui.App).ChatInput() == "" {
		t.Fatal("nothing was typed, so this test is not testing anything")
	}

	next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if got := next.(tui.App).ChatInput(); got != "" {
		t.Errorf("the box carried %q into the new conversation", got)
	}
}

// Asked for while a reply is arriving, the first press explains and the second goes through. The
// point of the pause is that leaving looks like abandoning, and it is not.
func TestANewConversationWhileAReplyIsArrivingAsksFirst(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{
		ID:    "session-1",
		Turns: []core.Turn{{Request: core.Message{Text: "go"}, State: core.TurnStreaming}},
	}}
	app := launchWith(store, withOneKey(), engine)

	next, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if engine.created != 0 {
		t.Fatal("the conversation was replaced without asking, with a reply still arriving")
	}
	view := plain(next.(tui.App).View())
	if !strings.Contains(view, "ctrl+n again") {
		t.Errorf("the screen does not say how to go through with it:\n%s", view)
	}
	// Said explicitly, because the fear on being asked is that the running turn is about to be
	// thrown away, and it is not.
	if !strings.Contains(view, "keeps going") {
		t.Errorf("the screen does not say the old conversation survives:\n%s", view)
	}

	next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if engine.created != 1 {
		t.Errorf("%d conversations created after confirming, want one", engine.created)
	}
}

// A confirmation that outlives the keystroke after it would eventually fire on a key somebody meant
// for something else, which is the worst possible moment to replace what is on screen.
func TestTheConfirmationLapsesOnTheNextKey(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{
		ID:    "session-1",
		Turns: []core.Turn{{Request: core.Message{Text: "go"}, State: core.TurnStreaming}},
	}}
	app := launchWith(store, withOneKey(), engine)

	next, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	next, _ = next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})

	if view := plain(next.(tui.App).View()); strings.Contains(view, "ctrl+n again") {
		t.Errorf("the question is still up after a keystroke answered it:\n%s", view)
	}

	next.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if engine.created != 0 {
		t.Error("a lapsed confirmation was still honoured, so a stray key replaced the conversation")
	}
}

// Every navigation key is refused while a tool call is waiting, and this is one. Leaving the screen
// with a question up hides the thing that is blocking.
func TestANewConversationIsRefusedWhileAQuestionIsUp(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{ID: "session-1"}, asking: true}
	app := launchWith(store, withOneKey(), engine)

	next, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if engine.created != 0 {
		t.Error("a new conversation was started with a tool call waiting on an answer")
	}
	if next.(tui.App).Screen() != "chat" {
		t.Errorf("the screen moved to %q", next.(tui.App).Screen())
	}
}
