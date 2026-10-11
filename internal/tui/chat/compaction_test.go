package chat_test

// No reflex spends money. D-43's third rule: ctrl+r fired a real request to a real provider on one
// press, and it is the key half the world's fingers press expecting to search their history. Since
// Q-21 (D-67) it does search history, and compacting is /compact, typed twice.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// worthCompacting is a conversation long enough that summarising it would do something.
func worthCompacting() *fakeEngine {
	turns := make([]core.Turn, 16)
	for i := range turns {
		turns[i] = turn("t", strings.Repeat("question ", 40), strings.Repeat("answer ", 40),
			core.TurnComplete)
	}
	return &fakeEngine{session: core.Session{ID: "s1", Model: "claude-opus-5", Turns: turns}}
}

// The whole of U-09 in one press.
func TestOnePressOfControlRSpendsNothing(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)

	_, cmd := m.Update(keyCode('r', tea.ModCtrl))

	if engine.compacted != 0 {
		t.Errorf("one press reached the provider %d times", engine.compacted)
	}
	if cmd != nil {
		// Running it would be the same failure a frame later.
		if msg := cmd(); msg != nil {
			t.Errorf("one press asked for work: %T", msg)
		}
	}
}

// What the offer has to say. A confirmation that leaves any of these out is one people press
// through, and the bound is the part that makes it safe to agree to: the recent turns are where the
// actual work is and summarising those is how an agent loses the thread mid task.
func TestTheOfferNamesTheTurnsTheCostTheBoundAndTheKey(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)

	m, _ = run(m, "/compact")

	view := plain(m.Body())
	for what, want := range map[string]string{
		"how much of the conversation goes": "12 of 16 turns",
		"roughly what it costs":             "about 1.9k tokens",
		"what is kept whatever happens":     "last 4 kept",
		"what goes ahead with it":           "/compact again",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the offer does not say %s (%q):\n%s", what, want, view)
		}
	}

	// And it stays on one line at eighty columns, or it pushes the message box down the screen and
	// gets answered without being read.
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "/compact again") && len([]rune(line)) > 80 {
			t.Errorf("the offer is %d columns wide: %q", len([]rune(line)), line)
		}
	}
}

// The second /compact is the one that pays, and it pays once.
func TestTheSecondPressCompactsAndTheOfferGoesWithIt(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)

	m, _ = run(m, "/compact")
	m, cmd := run(m, "/compact")
	if cmd == nil {
		t.Fatal("the confirmed press asked for nothing")
	}
	m, _ = m.Update(cmd())

	if engine.compacted != 1 {
		t.Errorf("the engine was asked to compact %d times, want once", engine.compacted)
	}
	if view := plain(m.Body()); strings.Contains(view, "/compact again") {
		t.Errorf("the offer is still on screen after it was taken up:\n%s", view)
	}
}

// The offer survives the typing that takes it up, and lapses on a change of mind: a message sent,
// another command, or esc. One that outlived that would be taken up by a later /compact meant as a
// fresh question.
func TestAnOfferLapsesOnAChangeOfMind(t *testing.T) {
	for name, change := range map[string]func(chat.Model) chat.Model{
		"a message": func(m chat.Model) chat.Model { m, _ = run(m, "no thanks"); return m },
		"a command": func(m chat.Model) chat.Model { m, _ = run(m, "/cost"); return m },
		"esc":       func(m chat.Model) chat.Model { m, _ = m.Update(keyCode(tea.KeyEscape)); return m },
	} {
		engine := worthCompacting()
		m := model(engine)
		m, _ = run(m, "/compact")
		m = change(m)
		m, cmd := run(m, "/compact")
		if cmd != nil {
			cmd()
		}
		if engine.compacted != 0 {
			t.Errorf("%s: a lapsed offer was taken up by a later /compact: %d calls", name, engine.compacted)
		}
		if view := plain(m.Body()); !strings.Contains(view, "/compact again") {
			t.Errorf("%s: the later /compact did not offer again:\n%s", name, view)
		}
	}
}

// Typed once, /compact only offers: typing a command is no confirmation that it should spend.
func TestSlashCompactTypedOnceSpendsNothing(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)
	m, cmd := run(m, "/compact")
	if cmd != nil {
		cmd()
	}
	if engine.compacted != 0 {
		t.Errorf("/compact spent on being typed once: %d calls", engine.compacted)
	}
	if !strings.Contains(plain(m.Body()), "/compact again") {
		t.Errorf("/compact did not offer anything:\n%s", plain(m.Body()))
	}
}

// A conversation with nothing to summarise says so rather than offering to summarise none of it and
// then declining.
func TestAShortConversationIsToldThereIsNothingToSummarise(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Model: "claude-opus-5", Turns: []core.Turn{
		turn("t1", "hello", "hi", core.TurnComplete),
	}}}
	m := model(engine)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})

	m, _ = run(m, "/compact")

	view := plain(m.Body())
	if !strings.Contains(view, "not enough of this conversation") {
		t.Errorf("a conversation with nothing to compact was not told so:\n%s", view)
	}
	if strings.Contains(view, "ctrl+r again") {
		t.Errorf("an empty compaction was offered anyway:\n%s", view)
	}
	if engine.compacted != 0 {
		t.Errorf("it was sent anyway: %d calls", engine.compacted)
	}
}

// ctrl+r searches what was sent, newest first: typed letters narrow it, ctrl+r again goes older,
// enter puts the message in the box without sending it, and esc puts back what was there.
func TestControlRSearchesWhatWasSent(t *testing.T) {
	engine := worthCompacting()
	m := model(engine)
	for _, line := range []string{"fix the parser", "run the tests", "fix the lexer"} {
		m, _ = run(m, line)
	}
	sent := len(engine.sent)
	m = typeText(m, "draft")
	m, _ = m.Update(keyCode('r', tea.ModCtrl))
	m = typeText(m, "fix")
	if !strings.Contains(plain(m.Body()), "fix the lexer") {
		t.Fatalf("the newest match is not shown:\n%s", plain(m.Body()))
	}
	m, _ = m.Update(keyCode('r', tea.ModCtrl))
	if !strings.Contains(plain(m.Body()), "fix the parser") {
		t.Fatalf("ctrl+r again did not go older:\n%s", plain(m.Body()))
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	if m.InputValue() != "fix the parser" || len(engine.sent) != sent || engine.compacted != 0 {
		t.Fatalf("box %q, sent %d more, compacted %d", m.InputValue(), len(engine.sent)-sent, engine.compacted)
	}

	m, _ = m.Update(keyCode('r', tea.ModCtrl))
	m = typeText(m, "zzz")
	if !strings.Contains(plain(m.Body()), "no earlier message has that") {
		t.Fatalf("a query with no match does not say so:\n%s", plain(m.Body()))
	}
	m, _ = m.Update(keyCode(tea.KeyEscape))
	if m.InputValue() != "fix the parser" {
		t.Fatalf("esc did not put the box back: %q", m.InputValue())
	}
}

// An offer pays only in the conversation it was made in, while that conversation is as it was and
// the offer is still on screen: switching away, a new turn, or another notice replacing it each
// make the next /compact a fresh offer.
func TestAnOfferPaysOnlyWhereAndWhileItWasMade(t *testing.T) {
	for name, change := range map[string]func(chat.Model, *fakeEngine) chat.Model{
		"another conversation": func(m chat.Model, _ *fakeEngine) chat.Model {
			m.SetSession("s2", "other")
			m.SetSession("s1", "main")
			return m
		},
		"a new turn": func(m chat.Model, e *fakeEngine) chat.Model {
			e.session.Turns = append(e.session.Turns, turn("t", "more", "more", core.TurnComplete))
			m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
			return m
		},
		"another notice": func(m chat.Model, _ *fakeEngine) chat.Model {
			m.SetNotice("something else happened")
			return m
		},
	} {
		engine := worthCompacting()
		m := model(engine)
		m, _ = run(m, "/compact")
		m = change(m, engine)
		_, cmd := run(m, "/compact")
		if cmd != nil {
			cmd()
		}
		if engine.compacted != 0 {
			t.Errorf("%s: one visible /compact paid for an offer made elsewhere: %d calls", name, engine.compacted)
		}
	}
}
