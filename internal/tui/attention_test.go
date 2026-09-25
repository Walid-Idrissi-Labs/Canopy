package tui_test

// Attention is ambient. D-43's second rule: an agent needing a person is visible from every screen,
// no screen is ever locked against leaving, and the question is still there when you come back.

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core/fake"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/permission"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// waitingOn is a question raised by an agent other than the one on screen.
func waitingOn(agent, sessionID string) session.Waiting {
	req := permission.Request{
		AgentID: sessionID, SessionID: sessionID,
		Tool: "run_command", Kind: core.ToolExecute, Command: "npm test",
	}
	return session.Waiting{SessionID: sessionID, Agent: agent, Request: req}
}

// The premise of the product in one assertion. Somebody is talking to one agent while another has
// stopped and cannot start again without them, and no screen they might be standing on may keep
// that from them. Before this the only screen that said so was the agent list.
func TestTheHeaderSaysWhoNeedsYouFromEveryScreen(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{
		session: core.Session{ID: "session-1"},
		waiting: []session.Waiting{waitingOn("worker-1", "s2"), waitingOn("worker-2", "s3")},
	}
	app := launchWith(store, withOneKey(), engine).(tui.App)

	if view := plain(app.View().Content); !strings.Contains(view, "2 need you") {
		t.Errorf("the conversation does not say who is waiting:\n%s", view)
	}

	// And every screen a key reaches from it, in turn.
	for _, run := range []struct {
		screen string
		key    tea.KeyPressMsg
	}{
		{"agents", keyCode('d', tea.ModCtrl)},
		{"keys", keyCode('k', tea.ModCtrl)},
		{"help", keyText("?")},
	} {
		next, _ := app.Update(run.key)
		if next.(tui.App).Screen() != run.screen {
			t.Fatalf("the key for %s landed on %q", run.screen, next.(tui.App).Screen())
		}
		if view := plain(next.(tui.App).View().Content); !strings.Contains(view, "2 need you") {
			t.Errorf("the %s screen does not say who is waiting:\n%s", run.screen, view)
		}
	}
}

// A failed agent has no question outstanding and has still stopped and cannot start again on its
// own, which is what the agent list has always counted. Counting only pending questions would walk
// somebody past it on every screen but that one.
func TestTheCountIsQuestionsAndFailedAgentsWithoutCountingOneTwice(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{
		session: core.Session{ID: "session-1"},
		waiting: []session.Waiting{waitingOn("worker-1", "s2")},
		agents: []session.AgentStatus{
			// The same agent as the question above, which is one agent needing one person.
			{Agent: session.Agent{Name: "worker-1", SessionID: "s2"}, State: core.AgentAwaitingPermission},
			{Agent: session.Agent{Name: "worker-2", SessionID: "s3"}, State: core.AgentFailed},
			{Agent: session.Agent{Name: "worker-3", SessionID: "s4"}, State: core.AgentWorking},
		},
	}
	app := launchWith(store, withOneKey(), engine).(tui.App)

	if view := plain(app.View().Content); !strings.Contains(view, "2 need you") {
		t.Errorf("the count is not the union of the two ways of needing somebody:\n%s", view)
	}
}

// A word and a glyph rather than a colour. The count has to read correctly with colour disabled,
// because NO_COLOR is a promise the whole ecosystem makes and a coloured mark says nothing in a
// monochrome terminal or in a pasted bug report.
func TestTheAttentionCountSurvivesNoColour(t *testing.T) {
	defer theme.Set(theme.Default)
	theme.Set(theme.Monochrome)

	got := plain(tui.Header(tui.Dimensions{Width: 80, Height: 24}, tui.Status{
		Screen: "chat", Mode: "build", Attention: 3,
	}))
	if !strings.Contains(got, "! 3 need you") {
		t.Errorf("the count is not readable without colour:\n%s", got)
	}
}

// One waiting is the common case and the one a plural gets wrong.
func TestOneAgentWaitingIsSaidInTheSingular(t *testing.T) {
	got := plain(tui.Header(tui.Dimensions{Width: 80, Height: 24}, tui.Status{
		Screen: "chat", Attention: 1,
	}))
	if !strings.Contains(got, "1 needs you") {
		t.Errorf("the header says %q", got)
	}

	// And nobody waiting says nothing at all. A count that is always there is chrome, and chrome is
	// what people stop seeing.
	quiet := plain(tui.Header(tui.Dimensions{Width: 80, Height: 24}, tui.Status{Screen: "chat"}))
	if strings.Contains(quiet, "need") {
		t.Errorf("the header talks about attention with nobody waiting:\n%s", quiet)
	}
}

// The header draws a declared height at an exact width, and a count added to a row that was already
// full would wrap it. A wrapped header pushes every screen's footer off the bottom at once.
func TestTheAttentionCountFitsTheHeaderItIsDrawnOn(t *testing.T) {
	for _, width := range []int{60, 72, 80, 100, 120, 200} {
		for _, height := range []int{14, 24, 30, 50} {
			d := tui.Dimensions{Width: width, Height: height}
			got := tui.Header(d, tui.Status{
				Screen: "chat", Mode: "cruise", Attention: 12,
				Parts: []string{"~/dev/canopy", "claude", "claude-opus-5", "128.4k tokens, $12.3456"},
			})
			if lines := strings.Count(got, "\n") + 1; lines != d.HeaderHeight() {
				t.Errorf("%dx%d: the header drew %d lines, declared %d",
					width, height, lines, d.HeaderHeight())
			}
			for i, line := range strings.Split(got, "\n") {
				if w := len([]rune(plain(line))); w != width {
					t.Errorf("%dx%d: line %d is %d cells, want %d", width, height, i, w, width)
				}
			}
		}
	}
}

// The lock D-43 removes. Both of these used to hand the key back unhandled while a question was up,
// which left the screen that says who else needs you unreachable exactly when somebody did.
func TestNavigationLeavesAConversationThatHasAQuestionWaiting(t *testing.T) {
	store := fake.New()
	defer store.Close()

	for _, run := range []struct {
		key    tea.KeyPressMsg
		screen string
	}{
		{keyCode('d', tea.ModCtrl), "agents"},
		{keyCode('k', tea.ModCtrl), "keys"},
	} {
		engine := &stubEngine{session: core.Session{ID: "session-1"}, asking: true}
		app := launchWith(store, withOneKey(), engine).(tui.App)

		if !app.ChatAwaiting() {
			t.Fatal("the conversation has no question up, so this test proves nothing")
		}

		next, _ := app.Update(run.key)
		if got := next.(tui.App).Screen(); got != run.screen {
			t.Errorf("%v with a question up landed on %q, want %q", run.key, got, run.screen)
		}
	}
}

// Leaving is not answering, and coming back finds the question where it was.
func TestAQuestionIsStillWaitingWhenYouComeBack(t *testing.T) {
	store := fake.New()
	defer store.Close()

	engine := &stubEngine{session: core.Session{ID: "session-1"}, asking: true}
	app := launchWith(store, withOneKey(), engine).(tui.App)

	away, _ := app.Update(keyCode('d', tea.ModCtrl))
	if away.(tui.App).Screen() != "agents" {
		t.Fatalf("ctrl+d landed on %q with a question up", away.(tui.App).Screen())
	}
	back, _ := away.(tui.App).Update(keyCode(tea.KeyEsc))

	if !back.(tui.App).ChatAwaiting() {
		t.Error("the question was answered by walking away from it")
	}
	if view := plain(back.(tui.App).View().Content); !strings.Contains(view, "needs you") {
		t.Errorf("the question did not survive the trip:\n%s", view)
	}
}

// Off unless it is asked for, once per agent that starts needing somebody, and never per frame. A
// bell that repeated while a question sat there would be a noise somebody silences at the terminal,
// taking every later one with it.
func TestTheBellRingsOnceForAnAgentThatStartsNeedingYou(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var heard bytes.Buffer
	defer tui.BellHeard(&heard)()
	t.Setenv(tui.BellEnv, "1")

	engine := &stubEngine{session: core.Session{ID: "session-1"}}
	app := launchWith(store, withOneKey(), engine).(tui.App)

	app = event(app)
	if heard.Len() != 0 {
		t.Fatalf("the bell rang with nobody waiting: %q", heard.String())
	}

	engine.waiting = []session.Waiting{waitingOn("worker-1", "s2")}
	app = event(app)
	if heard.String() != "\a" {
		t.Fatalf("the transition into needing somebody rang %q, want one bell", heard.String())
	}

	// Four more frames with the same agent still waiting, and silence through all of them.
	for range 4 {
		app = event(app)
	}
	if heard.String() != "\a" {
		t.Errorf("the bell rang more than once for one agent: %q", heard.String())
	}

	// A second agent is a second transition, and rings again. The first bell was about the first
	// agent and was answered or ignored on its own terms.
	engine.waiting = append(engine.waiting, waitingOn("worker-2", "s3"))
	event(app)
	if heard.String() != "\a\a" {
		t.Errorf("a second agent needing somebody rang %q", heard.String())
	}
}

// Off by default, which is the half a terminal user cares about most. A program that makes a noise
// nobody asked for is one people disable the bell for and never hear from again.
func TestTheBellIsSilentUnlessItIsAskedFor(t *testing.T) {
	store := fake.New()
	defer store.Close()

	var heard bytes.Buffer
	defer tui.BellHeard(&heard)()
	t.Setenv(tui.BellEnv, "")

	engine := &stubEngine{session: core.Session{ID: "session-1"}}
	app := launchWith(store, withOneKey(), engine).(tui.App)

	engine.waiting = []session.Waiting{waitingOn("worker-1", "s2")}
	event(app)

	if heard.Len() != 0 {
		t.Errorf("the bell rang without being asked for: %q", heard.String())
	}
}

// event puts one engine notification through the application, which is how every screen finds out
// that anything happened anywhere.
func event(app tui.App) tui.App {
	next, _ := app.Update(chat.EventMsg{Event: core.Event{}})
	return next.(tui.App)
}

// With notifications asked for, an agent that starts needing you is announced with what it wants,
// once; one that finishes is announced only while the terminal is not the window in front.
func TestNotificationsSayWhoNeedsYouAndWhoFinished(t *testing.T) {
	store := fake.New()
	defer store.Close()
	var heard bytes.Buffer
	defer tui.NotificationsHeard(&heard)()
	defer tui.PlayTerminal(map[string]string{tui.NotifyEnv: "1", "TERM": "xterm-256color"})()

	engine := &stubEngine{session: core.Session{ID: "session-1"}}
	app := launchWith(store, withOneKey(), engine).(tui.App)
	engine.waiting = []session.Waiting{waitingOn("worker-1", "s2")}
	app = event(app)
	app = event(app)
	if strings.Count(heard.String(), "\x1b]9;") != 1 || !strings.Contains(heard.String(), "asks to run npm test") {
		t.Fatalf("heard %q", heard.String())
	}

	heard.Reset()
	engine.waiting = nil
	engine.agents = []session.AgentStatus{{Agent: session.Agent{Name: "refactor", SessionID: "s3"}, State: core.AgentWorking}}
	app = event(app)
	engine.agents[0].State = core.AgentIdle
	app = event(app)
	if heard.Len() != 0 {
		t.Fatalf("a finish was announced to somebody looking at it: %q", heard.String())
	}

	next, _ := app.Update(tea.BlurMsg{})
	app = next.(tui.App)
	engine.agents[0].State = core.AgentWorking
	app = event(app)
	engine.agents[0].State = core.AgentIdle
	app = event(app)
	if !strings.Contains(heard.String(), "refactor finished") {
		t.Fatalf("a finish while away was not announced: %q", heard.String())
	}
	// Focus is reported, or the terminal never says it is behind another window and nothing is
	// announced.
	if view := app.View(); !view.ReportFocus || !strings.HasPrefix(view.WindowTitle, "canopy") {
		t.Fatalf("focus reported %v, title %q", view.ReportFocus, view.WindowTitle)
	}
}

func TestNotificationsAreOffUnlessAskedFor(t *testing.T) {
	store := fake.New()
	defer store.Close()
	var heard bytes.Buffer
	defer tui.NotificationsHeard(&heard)()
	defer tui.PlayTerminal(map[string]string{"TERM": "xterm-256color"})()
	engine := &stubEngine{session: core.Session{ID: "session-1"}}
	app := launchWith(store, withOneKey(), engine).(tui.App)
	engine.waiting = []session.Waiting{waitingOn("worker-1", "s2")}
	event(app)
	if heard.Len() != 0 {
		t.Fatalf("a notification nobody asked for: %q", heard.String())
	}
}

// A question in the conversation on screen asks in place; it is announced only once the terminal
// is behind another window. Coming back to it ends that, and a turn that stops to wait is waiting,
// not finished.
func TestNotificationsAreForWhatCannotBeSeen(t *testing.T) {
	store := fake.New()
	defer store.Close()
	var heard bytes.Buffer
	defer tui.NotificationsHeard(&heard)()
	defer tui.PlayTerminal(map[string]string{tui.NotifyEnv: "1", "TERM": "xterm-256color"})()

	engine := &stubEngine{session: core.Session{ID: "session-1"}}
	app := launchWith(store, withOneKey(), engine).(tui.App)
	engine.waiting = []session.Waiting{waitingOn("", "session-1")}
	app = event(app)
	if heard.Len() != 0 {
		t.Fatalf("the question on screen was announced: %q", heard.String())
	}
	engine.waiting = nil
	app = event(app)
	next, _ := app.Update(tea.BlurMsg{})
	app = next.(tui.App)
	engine.waiting = []session.Waiting{waitingOn("", "session-1")}
	app = event(app)
	if !strings.Contains(heard.String(), "your conversation asks to run npm test") {
		t.Fatalf("a question while away was not announced: %q", heard.String())
	}

	// Back in front: a finish is seen, so it is not announced.
	heard.Reset()
	engine.waiting = nil
	next, _ = app.Update(tea.FocusMsg{})
	app = next.(tui.App)
	engine.agents = []session.AgentStatus{{Agent: session.Agent{Name: "refactor", SessionID: "s3"}, State: core.AgentWorking}}
	app = event(app)
	engine.agents[0].State = core.AgentIdle
	app = event(app)
	if heard.Len() != 0 {
		t.Fatalf("a finish in front of the person was announced: %q", heard.String())
	}

	// Away again: an agent that stops to ask is announced as asking, not as finished.
	next, _ = app.Update(tea.BlurMsg{})
	app = next.(tui.App)
	engine.agents[0].State = core.AgentWorking
	app = event(app)
	engine.agents[0].State = core.AgentAwaitingPermission
	engine.waiting = []session.Waiting{waitingOn("refactor", "s3")}
	event(app)
	if strings.Contains(heard.String(), "finished") || !strings.Contains(heard.String(), "refactor asks to run") {
		t.Fatalf("heard %q", heard.String())
	}
}

// Progress is drawn only where the terminal is known to show it.
func TestProgressIsLeftOutWhereItIsNotUnderstood(t *testing.T) {
	store := fake.New()
	defer store.Close()
	defer tui.PlayTerminal(map[string]string{"TERM_PROGRAM": "iTerm.app"})()
	app := launchWith(store, withOneKey(), &stubEngine{session: core.Session{ID: "session-1"}}).(tui.App)
	if app.View().ProgressBar != nil {
		t.Fatal("progress drawn on a terminal that shows OSC 9 as a notification")
	}
	defer tui.PlayTerminal(map[string]string{"TERM_PROGRAM": "ghostty"})()
	if app.View().ProgressBar == nil {
		t.Fatal("no progress on a terminal that draws it")
	}
}
