package chat_test

// Reading a permission prompt is not answering it. D-43's first rule: navigation moves the
// conversation and decides nothing, and every other key on a question still means no.

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// keyMsg turns a key's name into the message the program receives for it.
//
// Written from the names rather than from tea.KeyType values so a test can drive the same strings
// the handler switches on and the help table prints, which is what makes an enumeration of the
// bindings an enumeration of the behaviour rather than of a parallel list.
func keyMsg(name string) tea.KeyMsg {
	named := map[string]tea.KeyType{
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown,
		"ctrl+home": tea.KeyCtrlHome, "ctrl+end": tea.KeyCtrlEnd, "ctrl+down": tea.KeyCtrlDown,
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"space": tea.KeySpace, "ctrl+c": tea.KeyCtrlC, "ctrl+d": tea.KeyCtrlD,
		"ctrl+g": tea.KeyCtrlG, "ctrl+k": tea.KeyCtrlK, "ctrl+n": tea.KeyCtrlN,
		"ctrl+r": tea.KeyCtrlR, "ctrl+s": tea.KeyCtrlS,
	}
	if key, ok := named[name]; ok {
		return tea.KeyMsg{Type: key}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// asking is a long conversation with a question waiting on it, which is the situation the whole of
// U-01 is about: the command being approved is at the bottom and the reasoning for it is above.
func asking() (*fakeEngine, chat.Model) {
	engine := &fakeEngine{
		session: longConversation(40),
		prompt:  pendingPrompt("rm -rf ./build"),
	}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	return engine, m
}

// The set is written out here as well as in the code, deliberately. The property being protected is
// that a key cannot join the refusal path by accident, and a test that only walked whatever the
// program happened to declare would pass just as happily on a set somebody had emptied.
func TestTheNavigationSetIsExactlyTheKeysThatOnlyMove(t *testing.T) {
	want := []string{
		"ctrl+down", "ctrl+end", "ctrl+home", "down", "left", "pgdown", "pgup", "right", "up",
	}
	if got := chat.NavigationKeys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the navigation set is %v, want %v. A key added here stops refusing a permission "+
			"prompt, so it is a decision to make on purpose", got, want)
	}

	// And the keys that answer are not in it, which is the other half of the same claim.
	for _, decides := range []string{"y", "a", "enter", "esc"} {
		for _, key := range chat.NavigationKeys() {
			if key == decides {
				t.Errorf("%q is in the navigation set, so the key that answers no longer answers", key)
			}
		}
	}
}

// Every key in the set, on a live prompt, deciding nothing.
func TestNavigationKeysMoveTheConversationAndAnswerNothing(t *testing.T) {
	for _, key := range chat.NavigationKeys() {
		engine, m := asking()

		m, _ = m.Update(keyMsg(key))

		if len(engine.answers) != 0 {
			t.Errorf("%s answered the question: %+v", key, engine.answers)
		}
		if !m.Awaiting() {
			t.Errorf("%s left the question answered, so reading it decided it", key)
		}
	}
}

// The keys move the view rather than being accepted and dropped, which is the difference between
// navigation working and navigation being silently swallowed by the prompt.
func TestPagingUnderAQuestionScrollsAndComesBack(t *testing.T) {
	_, m := asking()

	m, _ = m.Update(keyMsg("pgup"))
	if body := plain(m.Body()); !strings.Contains(body, "more below") {
		t.Fatalf("pgup did not move the conversation while a question was up:\n%s", body)
	}

	m, _ = m.Update(keyMsg("ctrl+end"))
	body := plain(m.Body())
	if strings.Contains(body, "more below") {
		t.Errorf("ctrl+end did not return to the tail while a question was up:\n%s", body)
	}
	// And the question is where it was, waiting, which is what makes reading first worth doing.
	if !strings.Contains(body, "rm -rf ./build") {
		t.Errorf("the question did not come back with the tail:\n%s", body)
	}

	// ctrl+home is the other end of the same journey, and it is the one somebody presses to read
	// how a turn started before approving what it ended up wanting to do.
	m, _ = m.Update(keyMsg("ctrl+home"))
	if body := plain(m.Body()); !strings.Contains(body, "more below") {
		t.Errorf("ctrl+home did not reach the top of the conversation:\n%s", body)
	}
}

// The safety property U-01 keeps rather than removes. The reflex key on an unread prompt is enter,
// and enter meaning no is the difference between a misread prompt costing a retry and costing a
// repository.
func TestEveryKeyThatIsNotNavigationStillRefuses(t *testing.T) {
	for _, key := range []string{"enter", "esc", "tab", "n", "q", "space", "ctrl+g", "ctrl+r", "?"} {
		engine, m := asking()

		_, _ = m.Update(keyMsg(key))

		if len(engine.answers) != 1 {
			t.Errorf("%s gave %d answers, want one refusal", key, len(engine.answers))
			continue
		}
		if engine.answers[0][0] {
			t.Errorf("%s approved the call rather than refusing it", key)
		}
	}

	// And the two that decide still decide.
	for _, run := range []struct {
		key      string
		remember bool
	}{{"y", false}, {"a", true}} {
		engine, m := asking()
		_, _ = m.Update(keyMsg(run.key))

		if len(engine.answers) != 1 {
			t.Fatalf("%s gave %d answers, want one", run.key, len(engine.answers))
		}
		if !engine.answers[0][0] || engine.answers[0][1] != run.remember {
			t.Errorf("%s gave %+v", run.key, engine.answers[0])
		}
	}
}

// The panel is the only thing on screen saying what may be pressed, because the footer goes quiet
// while a question is up. A key that is safe and unmentioned is a key nobody risks.
func TestTheQuestionSaysWhichKeysOnlyRead(t *testing.T) {
	_, m := asking()

	body := plain(m.Body())
	if !strings.Contains(body, "pgup") {
		t.Errorf("the question does not say that scrolling is safe:\n%s", body)
	}
}
