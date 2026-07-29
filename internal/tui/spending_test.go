package tui_test

// No reflex spends money. D-43's third rule, asserted against the list of keys the program tells
// people to press: the help table is where somebody learns what is safe to try.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
)

// pressable turns a help table entry into the keys a person could actually press from it.
//
// The table is written for people, so a row can say "1 to 8" or "any other key" or "mouse drag".
// Those are descriptions of a class of key rather than a key, and the ones that are keys are the
// tokens with no space in them.
func pressable(entry string) []tea.KeyMsg {
	var keys []tea.KeyMsg
	for _, token := range strings.Split(entry, " / ") {
		token = strings.TrimSpace(token)
		if token == "" || strings.Contains(token, " ") {
			continue
		}
		keys = append(keys, keyFor(token))
	}
	return keys
}

// keyFor is one key by the name the help table prints.
func keyFor(name string) tea.KeyMsg {
	named := map[string]tea.KeyType{
		"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab,
		"space": tea.KeySpace, "up": tea.KeyUp, "down": tea.KeyDown, "pgup": tea.KeyPgUp,
		"pgdown": tea.KeyPgDown, "home": tea.KeyHome, "end": tea.KeyEnd,
		"alt+enter": tea.KeyEnter, "arrows": tea.KeyUp,
		"ctrl+c": tea.KeyCtrlC, "ctrl+d": tea.KeyCtrlD, "ctrl+g": tea.KeyCtrlG,
		"ctrl+k": tea.KeyCtrlK, "ctrl+n": tea.KeyCtrlN, "ctrl+r": tea.KeyCtrlR,
		"ctrl+s": tea.KeyCtrlS, "ctrl+home": tea.KeyCtrlHome, "ctrl+end": tea.KeyCtrlEnd,
	}
	if key, ok := named[name]; ok {
		return tea.KeyMsg{Type: key}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
}

// Every binding the program advertises, pressed on a conversation, none of them reaching a
// provider. ctrl+r is the one this was written for: it fired a compaction, a real request on a real
// key, and it is what half the world's fingers press expecting to search their history.
func TestNoAdvertisedKeyReachesAProviderOnItsOwn(t *testing.T) {
	store := fake.New()
	defer store.Close()

	for _, entry := range tui.HelpKeys() {
		for _, key := range pressable(entry) {
			engine := &stubEngine{session: manyTurns(12)}
			app := launchWith(store, withOneKey(), engine)

			app.(tui.App).Update(key)

			switch {
			case engine.compacted != 0:
				t.Errorf("%q started a compaction on one press", entry)
			case len(engine.sent) != 0:
				t.Errorf("%q sent %+v with nothing typed", entry, engine.sent)
			case len(engine.asked) != 0:
				t.Errorf("%q asked the model %+v", entry, engine.asked)
			}
		}
	}
}

// The other half of the rule: an explicit send still sends, and a confirmed spend still spends. A
// program that asked twice for everything would have replaced one failure with another.
func TestAnExplicitSendStillSendsAndAConfirmedCompactionStillRuns(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: manyTurns(12)}
	app := launchWith(store, withOneKey(), engine)

	typed, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("carry on")})
	sent, _ := typed.(tui.App).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(engine.sent) != 1 {
		t.Fatalf("enter with a message in the box sent %+v", engine.sent)
	}

	offered, _ := sent.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if view := plain(offered.(tui.App).View()); !strings.Contains(view, "ctrl+r again") {
		t.Fatalf("the first press did not offer anything:\n%s", view)
	}

	// The command that goes with the second press runs off the update loop, so the call is made by
	// running it rather than by pressing the key.
	_, cmd := offered.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd == nil {
		t.Fatal("the second press asked for nothing")
	}
	cmd()

	if engine.compacted != 1 {
		t.Errorf("the confirmed compaction ran %d times", engine.compacted)
	}
}

// The confirmation lapses on any other keystroke, like every other confirmation in the program. One
// that outlived a change of mind would eventually be taken up by a key meant for something else.
func TestAnUnansweredCompactionOfferLapses(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: manyTurns(12)}
	app := launchWith(store, withOneKey(), engine)

	offered, _ := app.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	elsewhere, _ := offered.(tui.App).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	again, cmd := elsewhere.(tui.App).Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if cmd != nil {
		cmd()
	}

	if engine.compacted != 0 {
		t.Errorf("a lapsed offer was taken up by the next ctrl+r, which spent %d times",
			engine.compacted)
	}
	if view := plain(again.(tui.App).View()); !strings.Contains(view, "ctrl+r again") {
		t.Errorf("the fresh press did not offer again:\n%s", view)
	}
}

// The resolver tells a first run to press ctrl+k when it has no credential, and a key named in a
// message has to be one the program actually binds.
func TestTheKeyTheEmptyCredentialMessageNamesIsInTheTable(t *testing.T) {
	for _, entry := range tui.HelpKeys() {
		if entry == "ctrl+k" {
			return
		}
	}
	t.Error("ctrl+k is not in the help table, and the resolver tells people to press it")
}
