package tui

// The keybinding overlay.
//
// Every binding, in one place, reachable from everywhere. The footer of each screen lists the four
// or five keys that matter there, which is right for working and wrong for learning: the key you
// need is always the one that did not fit. So this exists, and the thing that makes it worth having
// is that it is exhaustive. A help screen that lists most of the bindings teaches people that the
// help screen cannot be trusted, and then they stop opening it.
//
// Generated from one table rather than written per screen, so a binding that is added and not
// listed here is a table nobody edited rather than a screen somebody forgot.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// binding is one key and what it does.
type binding struct {
	keys string
	does string
}

// section is a group of bindings under a heading.
type section struct {
	title    string
	bindings []binding
}

// bindings is every key Canopy responds to.
//
// Grouped by where you are rather than by what the key is, because "what can I press right now" is
// the question somebody opens this with, and an alphabetical list of every key in the program
// answers a question nobody has.
func bindings() []section {
	return []section{
		{"anywhere", []binding{
			{"?", "this screen"},
			{"ctrl+c", "stop the turn, or quit when nothing is running"},
		}},
		{"chat", []binding{
			{"enter", "send"},
			{"esc", "interrupt the turn, keeping what has arrived"},
			{"up / down", "walk back through what you have sent here"},
			{"ctrl+n", "a new conversation, keeping this one"},
			{"ctrl+d", "agents"},
			{"ctrl+k", "credentials"},
			{"ctrl+r", "compact the conversation"},
			{"pgup / pgdown", "scroll the conversation, as does the wheel"},
			{"ctrl+home / ctrl+end", "the top, and back to following"},
			{"alt+enter", "a line break instead of sending"},
			{"y", "allow a tool call once, while a question is up"},
			{"a", "allow it for the rest of the session"},
			{"any other key", "refuse it"},
		}},
		{"agents", []binding{
			{"j / k", "move"},
			{"enter", "open that agent's conversation"},
			{"n", "new agent"},
			{"v", "cycle tabbed, split and list"},
			{"w", "worktree monitor"},
			{"r", "review"},
			{"esc / q", "back to chat"},
		}},
		{"review", []binding{
			{"j / k", "move"},
			{"enter", "open the changes, then a file"},
			{"tab", "cycle the queue, the ranking and the overlap"},
			{"c", "commit, from the file list"},
			{"esc", "back one level"},
		}},
		{"a diff", []binding{
			{"j / k", "scroll a line"},
			{"space", "scroll a page"},
			{"g / G", "top and end"},
			{"esc", "back to the file list"},
		}},
		{"writing a commit", []binding{
			{"ctrl+s", "commit. nothing is staged until then"},
			{"esc", "cancel, throwing the message away"},
		}},
		{"worktree monitor", []binding{
			{"j / k", "move"},
			{"r", "refresh"},
			{"K", "credentials"},
			{"esc / tab", "agents"},
		}},
		{"credentials", []binding{
			{"esc / tab", "back to where you were"},
			{"q", "quit"},
		}},
	}
}

// Help renders the overlay.
//
// Two columns when there is room and one when there is not, rather than a fixed layout that either
// wastes half a wide terminal or overflows a narrow one. The break point is measured from the
// widest key column rather than guessed, so it holds if a binding is renamed.
func Help(dim Dimensions) string {
	sections := bindings()

	widest := 0
	for _, s := range sections {
		for _, b := range s.bindings {
			if w := lipgloss.Width(b.keys); w > widest {
				widest = w
			}
		}
	}

	column := widest + 2
	var lines []string
	for i, s := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, styleHeader.Render(s.title))
		for _, b := range s.bindings {
			pad := column - lipgloss.Width(b.keys)
			if pad < 1 {
				pad = 1
			}
			// The key is styled and the description is not, so the eye lands on the column it is
			// scanning. Padding is applied to the plain text before styling, because a style adds
			// escape sequences that lipgloss.Width does not count but a terminal does not print.
			lines = append(lines, "  "+styleTitle.Render(b.keys)+strings.Repeat(" ", pad)+
				styleMuted.Render(b.does))
		}
	}
	return strings.Join(lines, "\n")
}

// HelpBindingCount is how many bindings the overlay lists. For tests, which assert that every key
// the application handles appears here.
func HelpBindingCount() int {
	total := 0
	for _, s := range bindings() {
		total += len(s.bindings)
	}
	return total
}
