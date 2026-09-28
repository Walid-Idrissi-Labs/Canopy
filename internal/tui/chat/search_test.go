package chat_test

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

func conversationWithNeedles() *fakeEngine {
	var turns []core.Turn
	for i := 0; i < 40; i++ {
		text := fmt.Sprintf("answer number %d", i)
		switch i {
		case 3:
			text = "the Needle is in the parser"
		case 30:
			text = "another needle, in the lexer"
		}
		turns = append(turns, turn(fmt.Sprintf("t%d", i), fmt.Sprintf("question %d", i), text, core.TurnComplete))
	}
	return &fakeEngine{session: core.Session{ID: "s1", Turns: turns}}
}

// ctrl+f finds the newest place first and moves the view to it; enter walks to older ones.
func TestFindMovesTheViewToEachMatch(t *testing.T) {
	m := chat.New(conversationWithNeedles(), "s1", "canopy", "claude")
	m.SetSize(100, 20)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m, _ = m.Update(keyCode('f', tea.ModCtrl))
	m = typeFind(m, "needle")
	body := plain(m.Body())
	if !strings.Contains(body, "in the lexer") || !strings.Contains(body, "2 of 2") {
		t.Fatalf("the newest match is not on screen:\n%s", body)
	}
	if m.InputValue() != "" {
		t.Fatalf("typing into the find bar reached the box: %q", m.InputValue())
	}
	m, _ = m.Update(keyCode(tea.KeyEnter))
	body = plain(m.Body())
	if !strings.Contains(body, "in the parser") || !strings.Contains(body, "1 of 2") {
		t.Fatalf("enter did not move to the older match:\n%s", body)
	}
	m, _ = m.Update(keyCode(tea.KeyDown))
	if !strings.Contains(plain(m.Body()), "2 of 2") {
		t.Fatal("down did not move to the newer match")
	}
	m, _ = m.Update(keyCode(tea.KeyEsc))
	if strings.Contains(plain(m.Body()), "esc closes") {
		t.Fatal("esc left the find bar open")
	}
	if !strings.Contains(plain(m.Body()), "in the lexer") {
		t.Fatal("closing the find bar moved the view away from what was found")
	}
}

func TestFindSaysWhenNothingMatches(t *testing.T) {
	m := chat.New(conversationWithNeedles(), "s1", "canopy", "claude")
	m.SetSize(100, 20)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m, _ = m.Update(keyCode('f', tea.ModCtrl))
	m = typeFind(m, "zebra")
	if !strings.Contains(plain(m.Body()), "no match") {
		t.Fatalf("%s", plain(m.Body()))
	}
	// A conversation with nothing in it has nothing to find.
	empty := chat.New(&fakeEngine{session: core.Session{ID: "s1"}}, "s1", "canopy", "claude")
	empty.SetSize(100, 20)
	empty, _ = empty.Update(keyCode('f', tea.ModCtrl))
	if strings.Contains(plain(empty.Body()), "type to find") {
		t.Fatal("the find bar opened on an empty conversation")
	}
}

func typeFind(m chat.Model, text string) chat.Model {
	for _, r := range text {
		m, _ = m.Update(keyText(string(r)))
	}
	return m
}

// ctrl+y copies the last code block of the latest reply, or the reply when it has none.
func TestCtrlYCopiesTheLastCodeBlock(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		turn("t1", "fix it", "Here:\n```go\nfirst()\n```\nand\n```go\nsecond()\n```\ndone", core.TurnComplete),
	}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 20)
	var copied []string
	m.SetClipboard(func(text string) error { copied = append(copied, text); return nil })
	m, _ = m.Update(keyCode('y', tea.ModCtrl))
	if len(copied) != 1 || copied[0] != "second()" || !strings.Contains(m.Notice(), "code block") {
		t.Fatalf("copied %q, notice %q", copied, m.Notice())
	}
	engine.session.Turns = append(engine.session.Turns, turn("t2", "and?", "Nothing else to change.", core.TurnComplete))
	m, _ = m.Update(keyCode('y', tea.ModCtrl))
	if len(copied) != 2 || copied[1] != "Nothing else to change." {
		t.Fatalf("copied %q", copied)
	}
}

// Matches do not overlap, and a question arriving takes the keyboard back from the find bar.
func TestFindCountsWholeMatchesAndGivesWayToAQuestion(t *testing.T) {
	engine := &fakeEngine{session: core.Session{ID: "s1", Turns: []core.Turn{
		turn("t1", "q", "aaaa", core.TurnComplete), turn("t2", "go", "", core.TurnStreaming)}}}
	m := chat.New(engine, "s1", "canopy", "claude")
	m.SetSize(100, 20)
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m, _ = m.Update(keyCode('f', tea.ModCtrl))
	m = typeFind(m, "aa")
	if !strings.Contains(plain(m.Body()), "2 of 2") {
		t.Fatalf("overlapping matches were counted:\n%s", plain(m.Body()))
	}
	engine.session.Turns[1].State = core.TurnAwaitingTools
	engine.prompt = pendingPrompt("make clean")
	m, _ = m.Update(chat.EventMsg{Event: core.Event{}})
	m, _ = m.Update(keyText("x"))
	if strings.Contains(plain(m.Body()), "esc closes") {
		t.Fatal("the find bar kept the keyboard from a question")
	}
}
