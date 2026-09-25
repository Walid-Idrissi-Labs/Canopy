package tui

// Desktop notifications, the window title and the terminal's progress indicator: the ways to tell
// somebody who is looking at another window that an agent needs them or has finished.
//
// Notifications are off unless asked for, like the bell and for the same reason: how much a program
// interrupts belongs to the person in front of it. The title always says what is going on, since a
// title is only read by somebody who looks.

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// NotifyEnv turns desktop notifications on: present and not "0", as for CANOPY_BELL.
const NotifyEnv = "CANOPY_NOTIFY"

// notifyOut is where notifications are written: standard error, for the reason bellOut gives.
var notifyOut io.Writer = os.Stderr

// getenv is read through a variable so a test can play a terminal.
var getenv = os.Getenv

func notifyWanted() bool {
	value := getenv(NotifyEnv)
	return value != "" && value != "0"
}

// notify posts a desktop notification, when they were asked for.
func notify(body string) {
	if notifyWanted() {
		_, _ = io.WriteString(notifyOut, notification("Canopy", body))
	}
}

// notification is the escape sequence this terminal shows as a notification: kitty's own, the
// one foot and urxvt take, or the one iTerm2, WezTerm, Ghostty and Windows Terminal take. Inside
// tmux it is passed through to the terminal outside, which tmux allows with allow-passthrough.
func notification(title, body string) string {
	title, body = terminalSafe(title), terminalSafe(body)
	var seq string
	term := getenv("TERM")
	switch {
	case getenv("KITTY_WINDOW_ID") != "" || strings.Contains(term, "kitty"):
		// No metadata: one notification, complete in this sequence (kitty's default is done=1).
		seq = "\x1b]99;;" + title + ": " + body + "\x1b\\"
	case strings.HasPrefix(term, "foot") || strings.HasPrefix(term, "rxvt"):
		seq = "\x1b]777;notify;" + title + ";" + body + "\x1b\\"
	default:
		seq = "\x1b]9;" + title + ": " + body + "\x07"
	}
	if getenv("TMUX") != "" {
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return seq
}

// terminalSafe makes text safe inside an escape sequence: no control characters, which could end
// the sequence early and start another, no field separator, and short enough to read at a glance.
func terminalSafe(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		case r == ';':
			return ','
		}
		return r
	}, s)
	const limit = 160
	if utf8.RuneCountInString(s) > limit {
		s = string([]rune(s)[:limit]) + "..."
	}
	return strings.TrimSpace(s)
}

// progressSupported is whether this terminal draws OSC 9;4 as a progress indicator. Asked rather
// than assumed, because an older iTerm2 shows any OSC 9 as a notification, and a progress update on
// every frame would be a notification on every frame.
func progressSupported() bool {
	switch getenv("TERM_PROGRAM") {
	case "ghostty", "WezTerm":
		return true
	}
	return getenv("WT_SESSION") != "" || getenv("ConEmuPID") != ""
}

// windowTitle says what is going on, for a person looking at a list of windows or tabs.
func windowTitle(project string, waiting, working int) string {
	title := "canopy"
	if project != "" {
		title += " " + terminalSafe(project)
	}
	var parts []string
	if waiting > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting on you", waiting))
	}
	if working > 0 {
		parts = append(parts, fmt.Sprintf("%d working", working))
	}
	if len(parts) > 0 {
		title += ": " + strings.Join(parts, ", ")
	}
	return title
}

// progress is the indicator for this moment: a warning while somebody is waited on, moving while
// something works, nothing otherwise.
func progress(waiting, working int) *tea.ProgressBar {
	switch {
	case waiting > 0:
		return &tea.ProgressBar{State: tea.ProgressBarWarning, Value: 100}
	case working > 0:
		return &tea.ProgressBar{State: tea.ProgressBarIndeterminate}
	}
	return &tea.ProgressBar{State: tea.ProgressBarNone}
}
