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
			{"?", "this screen, except while a message is half typed"},
			{"ctrl+c", "quit, asked twice in a conversation"},
		}},
		{"chat", []binding{
			{"enter", "send"},
			{"shift+tab", "plan or build, which is what the agent may change"},
			{"/", "the command list, which narrows as you type"},
			{"up / down", "move the command list, or otherwise your sent history"},
			{"tab", "take the highlighted command"},
			{"//", "send a literal prompt beginning with slash"},
			{"esc", "interrupt the turn, or clear a half written message"},
			{"ctrl+c", "stop the turn, then twice to quit"},
			{"ctrl+n", "a new conversation, keeping this one"},
			{"ctrl+d", "agents, and it works with a question waiting here"},
			{"ctrl+k", "credentials"},
			{"ctrl+r", "compact the conversation, asked twice because it spends"},
			{"ctrl+o", "show tool output and diffs in full, and fold them back"},
			{"pgup / pgdown", "scroll the conversation, or the btw panel while it is up"},
			{"mouse drag", "select conversation text, copied when you let go"},
			{"ctrl+home / ctrl+end", "the top, and back to following"},
			{"alt+enter", "a line break instead of sending"},
			{"enter / y", "allow a tool call once, while a question is up"},
			{"a", "allow it for the rest of the session"},
			{"arrows / pgup", "read on with a question up, deciding nothing"},
			{"any other key", "refuse it"},
		}},
		{"another agent's question, above the box", []binding{
			{"enter", "approve it once, while the box is empty"},
			{"backspace", "decline it, while the box is empty"},
			{"ctrl+g", "open its conversation and full request"},
			{"a", "always is answered on that full prompt only"},
		}},
		{"agents", []binding{
			{"1 to 8", "jump to that pane, and again to open it"},
			{"enter", "open the selected agent, or approve its question once"},
			{"backspace", "decline the selected agent's question"},
			{"h / j / k / l", "move around the grid, as do the arrows"},
			{"v", "cycle list, mosaic, hero and focus"},
			{"[ / ]", "page, when there are more agents than panes"},
			{"tab", "next agent, or into the mosaic from the list"},
			{"n", "new agent"},
			{"y", "create it, on the direct mode confirmation"},
			{"w", "worktree monitor"},
			{"r", "review"},
			{"K", "credentials"},
			{"esc / q", "back a step, or from the list back to chat"},
		}},
		{"review", []binding{
			{"j / k", "move"},
			{"enter", "open the changes, then a file"},
			{"tab", "cycle the queue, ranking, cost outcome and overlap"},
			{"c", "commit, from the file list"},
			{"K", "credentials"},
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
		{"worktrees", []binding{
			{"j / k", "move"},
			{"g / G", "top and end, as do home and end"},
			{"r", "refresh"},
			{"K", "credentials"},
			{"esc / tab / q", "agents"},
		}},
		{"the model picker, on /model", []binding{
			{"j / k", "move, across credentials as well as within one"},
			{"enter", "run this conversation on it from the next message"},
			{"any other key", "back, with nothing changed"},
		}},
		{"credentials", []binding{
			{"j / k", "move"},
			{"enter", "use this one for the conversation"},
			{"m", "set which model it talks to"},
			{"a / n", "add one"},
			{"d / x", "remove one"},
			{"r", "reload the list"},
			{"esc / tab", "back to where you were"},
			{"q", "quit"},
		}},
	}
}

// Help renders the overlay, scrolled down by the given number of lines.
//
// Two columns when there is room and one when there is not, rather than a fixed layout that either
// wastes half a wide terminal or overflows a narrow one. Even in two columns the list is taller than
// a short terminal, which is why it scrolls: the alternative is dropping bindings to make it fit,
// and an overlay that lists most of the keys is one people stop trusting.
func Help(dim Dimensions) string { return HelpFrom(dim, 0) }

// HelpFrom is the overlay starting at a given line.
func HelpFrom(dim Dimensions, from int) string {
	lines := helpLines(dim.Width)

	height := dim.BodyHeight()
	if height < 1 || height >= len(lines) {
		return strings.Join(lines, "\n")
	}

	// The footer already says j/k scroll whenever the list overflows, so no body row is spent
	// repeating it: the row it used to cost goes back to the list, which is the thing somebody
	// opened this screen to read.
	if from > len(lines)-height {
		from = len(lines) - height
	}
	if from < 0 {
		from = 0
	}
	return strings.Join(lines[from:from+height], "\n")
}

// HelpHeight is how many lines the overlay needs in full. For tests and for scroll bounds.
func HelpHeight(width int) int { return len(helpLines(width)) }

// helpColumns is how many bindings can sit side by side.
//
// Two needs room for the widest key column and a readable description twice over. Below that the
// descriptions get cut to nothing, and a key with a truncated description is a key nobody presses.
func helpColumns(width int) int {
	if width >= 96 {
		return 2
	}
	return 1
}

func helpLines(width int) []string {
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

	render := func(s section) []string {
		out := []string{styleHeader.Render(s.title)}
		for _, b := range s.bindings {
			pad := column - lipgloss.Width(b.keys)
			if pad < 1 {
				pad = 1
			}
			// The key is styled and the description is not, so the eye lands on the column it is
			// scanning. Padding is applied to the plain text before styling, because a style adds
			// escape sequences that lipgloss.Width does not count but a terminal does not print.
			out = append(out, "  "+styleTitle.Render(b.keys)+strings.Repeat(" ", pad)+
				styleMuted.Render(b.does))
		}
		return out
	}

	single := func() []string {
		var lines []string
		for i, s := range sections {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, render(s)...)
		}
		return lines
	}

	if helpColumns(width) == 1 {
		return single()
	}

	// Split so the two columns come out close to the same height, rather than filling the first and
	// leaving the second short. Sections are kept whole: a section split across columns is one a
	// reader has to reassemble.
	blocks := make([][]string, 0, len(sections))
	total := 0
	for _, s := range sections {
		block := render(s)
		blocks = append(blocks, block)
		total += len(block) + 1
	}

	var left, right []string
	used := 0
	for _, block := range blocks {
		target := &left
		if used*2 >= total {
			target = &right
		}
		if len(*target) > 0 {
			*target = append(*target, "")
		}
		*target = append(*target, block...)
		used += len(block) + 1
	}

	// The gap is measured from the widest left hand line so the right column starts in one place
	// rather than stepping in and out down the page.
	gutter := 0
	for _, line := range left {
		if w := lipgloss.Width(line); w > gutter {
			gutter = w
		}
	}
	gutter += 3

	// Measured rather than assumed. Two columns of unknown text can be wider than the terminal even
	// when the terminal looked wide enough, and a help screen that wraps is worse than a tall one:
	// every second line starts mid description, in the middle of a column, and the whole thing reads
	// as broken. Falling back to one column costs scrolling, which already works.
	widestRight := 0
	for _, line := range right {
		if w := lipgloss.Width(line); w > widestRight {
			widestRight = w
		}
	}
	if gutter+widestRight > width {
		return single()
	}

	tall := len(left)
	if len(right) > tall {
		tall = len(right)
	}
	lines := make([]string, 0, tall)
	for i := range tall {
		var l, r string
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		if r == "" {
			lines = append(lines, l)
			continue
		}
		lines = append(lines, l+strings.Repeat(" ", maxInt(1, gutter-lipgloss.Width(l)))+r)
	}
	return lines
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

// HelpKeys is the key column of every binding, exactly as the overlay prints it.
//
// For the test that walks the table pressing each one and asserts that none of them reaches a
// provider without a confirmation or an explicit send. Exported for the same reason the count is:
// the claim is about the whole table, and a test with its own copy of the list would go on passing
// about the keys somebody added last week.
func HelpKeys() []string {
	var keys []string
	for _, s := range bindings() {
		for _, b := range s.bindings {
			keys = append(keys, b.keys)
		}
	}
	return keys
}
