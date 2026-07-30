package tui_test

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// M-08, and the bug it was written from.
//
// The application opened on a conversation called "session-1" whatever it had been handed. On a
// machine with no history that is the conversation the caller had just created and everything
// looked fine. On every machine after the first run it is the oldest chat in the database, because
// the engine loads what is saved at startup and numbers new ones from the highest ID it found. So
// launching Canopy reopened the first conversation you ever had, and the agent that had just been
// started was talking into a session with no screen attached to it.
func TestTheApplicationOpensTheConversationItWasGiven(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{sessions: map[string]core.Session{
		"session-1": {ID: "session-1", Turns: []core.Turn{{
			Request: core.Message{Text: "something asked last week"},
			Text:    "and answered last week",
			State:   core.TurnComplete,
		}}},
		"session-9": {ID: "session-9"},
	}}

	view := plain(launchSession(store, withOneKey(), engine, "session-9").(tui.App).View())
	if strings.Contains(view, "something asked last week") {
		t.Errorf("opening session-9 landed in an older conversation:\n%s", view)
	}
}

// And with nothing named it starts one, rather than picking a conversation for you.
//
// This is the property that makes `canopy` with no arguments mean a new chat. The safe direction to
// be wrong in is a new conversation nobody wanted, which costs a keystroke, rather than an old one
// somebody is now unknowingly adding to.
func TestTheApplicationWithNothingNamedStartsANewConversation(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{}
	tui.NewAppConfigured(store, withOneKey(), engine, "myproject", "claude", tui.AppOptions{})

	if engine.created != 1 {
		t.Errorf("%d conversations started with none named, want one", engine.created)
	}
}

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

// This test used to assert the opposite, and D-43 reverses it. Every navigation key was refused
// while a tool call waited, on the argument that leaving hides the thing that is blocking. What it
// actually hid was every other agent: no screen is ever locked, and a conversation you walk away
// from keeps its question for when you come back.
func TestANewConversationIsAllowedWhileAQuestionIsUp(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{ID: "session-1"}, asking: true}
	app := launchWith(store, withOneKey(), engine)

	next, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlN})
	if engine.created != 1 {
		t.Errorf("%d conversations were started, want the one that was asked for", engine.created)
	}
	if next.(tui.App).Screen() != "chat" {
		t.Errorf("the screen moved to %q", next.(tui.App).Screen())
	}
}

// A wheel notch is aimed at whatever somebody is looking at. Broadcast to every screen it would
// scroll the conversation sitting behind the one they actually opened, and they would come back to
// a view held somewhere they never put it, with no way to tell what moved it.
func TestTheWheelOnlyReachesTheScreenInFront(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: manyTurns(40)}
	app := launchWith(store, withOneKey(), engine)

	if strings.Contains(plain(app.(tui.App).View()), "more below") {
		t.Fatal("the conversation does not start at the tail, so this test proves nothing")
	}

	agents, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if agents.(tui.App).Screen() != "agents" {
		t.Fatalf("ctrl+d landed on %q", agents.(tui.App).Screen())
	}
	for range 5 {
		agents, _ = agents.(tui.App).Update(
			tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	}

	back, _ := agents.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if view := plain(back.(tui.App).View()); strings.Contains(view, "more below") {
		t.Errorf("scrolling on the agents view moved the conversation behind it:\n%s", view)
	}
}

// And on the screen in front it has to actually work, or the routing above is just a way of
// ignoring the wheel everywhere.
func TestTheWheelScrollsTheConversationInFront(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: manyTurns(40)}
	app := launchWith(store, withOneKey(), engine)

	scrolled, _ := app.(tui.App).Update(
		tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if view := plain(scrolled.(tui.App).View()); !strings.Contains(view, "more below") {
		t.Errorf("the wheel did not scroll the conversation:\n%s", view)
	}
}

func manyTurns(n int) core.Session {
	s := core.Session{ID: "session-1"}
	for i := range n {
		s.Turns = append(s.Turns, core.Turn{
			Request: core.Message{Text: fmt.Sprintf("question %d", i)},
			Text:    fmt.Sprintf("answer %d", i),
			State:   core.TurnComplete,
		})
	}
	return s
}
