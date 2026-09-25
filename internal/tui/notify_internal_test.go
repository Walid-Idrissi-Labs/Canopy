package tui

import (
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// NotificationsHeard sends notifications to w for a test, as BellHeard does for the bell.
func NotificationsHeard(w io.Writer) func() {
	previous := notifyOut
	notifyOut = w
	return func() { notifyOut = previous }
}

// PlayTerminal makes the environment read as env for a test.
func PlayTerminal(env map[string]string) func() {
	previous := getenv
	getenv = func(name string) string { return env[name] }
	return func() { getenv = previous }
}

func TestANotificationSuitsTheTerminal(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"iTerm2, WezTerm, Ghostty", map[string]string{"TERM": "xterm-256color"}, "\x1b]9;Canopy: done\x07"},
		{"kitty", map[string]string{"TERM": "xterm-kitty"}, "\x1b]99;i=1:d=0;Canopy: done\x1b\\"},
		{"foot", map[string]string{"TERM": "foot"}, "\x1b]777;notify;Canopy;done\x1b\\"},
		{"inside tmux", map[string]string{"TERM": "screen", "TMUX": "/tmp/t"},
			"\x1bPtmux;\x1b\x1b]9;Canopy: done\x07\x1b\\"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer PlayTerminal(tc.env)()
			if got := notification("Canopy", "done"); got != tc.want {
				t.Fatalf("%q, want %q", got, tc.want)
			}
		})
	}
}

// What a notification says comes from agent names and commands a model wrote, so none of it may
// end the sequence early and start another.
func TestANotificationCannotCarryAnEscape(t *testing.T) {
	defer PlayTerminal(map[string]string{"TERM": "foot"})()
	got := notification("Canopy", "worker asks to run \x1b]52;c;cHduZWQ=\x07; rm -rf /\x9b2J\n")
	if strings.Count(got, "\x1b") != 2 || strings.Contains(got, "\x07") || strings.Contains(got, "\x9b") ||
		strings.Count(got, ";") != 3 {
		t.Fatalf("%q", got)
	}
	if long := terminalSafe(strings.Repeat("x", 500)); len(long) > 170 {
		t.Fatalf("a notification of %d bytes", len(long))
	}
}

func TestTheWindowTitleSaysWhatIsGoingOn(t *testing.T) {
	for _, tc := range []struct {
		waiting, working int
		want             string
	}{
		{0, 0, "canopy proj"},
		{0, 2, "canopy proj: 2 working"},
		{1, 3, "canopy proj: 1 waiting on you, 3 working"},
	} {
		if got := windowTitle("proj", tc.waiting, tc.working); got != tc.want {
			t.Errorf("%q, want %q", got, tc.want)
		}
	}
	if got := windowTitle("a\x1b]0;evil\x07b", 0, 0); strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("a directory name put a control in the title: %q", got)
	}
}

// OSC 9;4 is drawn as progress only where it is known to be; elsewhere an older iTerm2 would show it
// as a notification on every frame.
func TestProgressIsShownOnlyWhereItIsUnderstood(t *testing.T) {
	for env, want := range map[string]bool{"ghostty": true, "WezTerm": true, "iTerm.app": false, "": false} {
		restore := PlayTerminal(map[string]string{"TERM_PROGRAM": env})
		if got := progressSupported(); got != want {
			t.Errorf("TERM_PROGRAM=%q: %v", env, got)
		}
		restore()
	}
	restore := PlayTerminal(map[string]string{"WT_SESSION": "x"})
	defer restore()
	if !progressSupported() {
		t.Error("Windows Terminal is not given progress")
	}
	if p := progress(1, 2); p.State != tea.ProgressBarWarning {
		t.Errorf("waiting shows %v", p.State)
	}
	if p := progress(0, 2); p.State != tea.ProgressBarIndeterminate {
		t.Errorf("working shows %v", p.State)
	}
	if p := progress(0, 0); p.State != tea.ProgressBarNone {
		t.Errorf("idle shows %v", p.State)
	}
}
