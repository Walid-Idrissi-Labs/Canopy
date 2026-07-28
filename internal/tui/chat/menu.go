package chat

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// The command list that drops out of the message box.
//
// It opens on a bare slash and narrows as you type, which is what every comparable tool does and
// what people now expect a slash to do. The alternative Canopy had was a tab key that completed
// silently when there was exactly one match and printed a row of names when there was more than one,
// which asks somebody to already know what they are looking for.
//
// Below the box on an empty conversation and above it on one in progress. The box is in the middle
// of the screen in the first case and on the floor in the second, so there is only one direction
// with room in it either time, and a menu that always dropped downwards would be drawn off the
// bottom of the terminal for the whole of a real conversation.

// menuVisible is how many commands are on screen at once.
//
// Four. Enough to see that there is a choice and few enough that the list is not the screen; past
// that it stops being a suggestion and starts being a page somebody has to read. The rest are still
// reachable, because the window scrolls with the selection.
const menuVisible = 4

// menuItem is one command as it appears in the list.
type menuItem struct {
	name        string
	description string
	scope       string
}

// menu is the state of the list.
//
// The filter is not stored. It is whatever is in the box, so there is no way for the two to
// disagree, which is the bug this shape exists to prevent: a list that says it is showing matches
// for "co" while the box says "com".
type menu struct {
	open bool

	// matches are the commands the current input selects, in the order they are offered.
	matches []menuItem

	// selected is the index into matches, and offset is the first row on screen. Both are clamped
	// every time the matches change, because they outlive the list they point into.
	selected int
	offset   int
}

// refreshMenu recomputes the list from whatever is in the box.
//
// Called after every keystroke that could change the input rather than only after the ones that
// obviously do, since the cheap wrong version of this is a menu that opens on the slash and then
// does not notice a backspace.
func (m *Model) refreshMenu() {
	prefix, wanted := commandPrefix(m.input.Value())
	if !wanted {
		m.menu = menu{}
		return
	}

	previous := ""
	if m.menu.open && m.menu.selected < len(m.menu.matches) {
		previous = m.menu.matches[m.menu.selected].name
	}

	m.menu.open = true
	m.menu.matches = matching(prefix, m.commands)

	// The selection follows the command it was on where that command is still in the list, rather
	// than following the index. Typing another letter usually removes entries above the one somebody
	// is aiming at, and an index that stayed put would slide the highlight onto a different command
	// under their fingers.
	m.menu.selected = 0
	for i, item := range m.menu.matches {
		if item.name == previous {
			m.menu.selected = i
			break
		}
	}
	m.menu.clamp()
}

// commandPrefix reads the command being typed, and whether one is.
//
// A space ends it: by then the name is settled and what follows is arguments, so the list has
// nothing left to offer. Two slashes are the escape for a literal message beginning with one, and
// offering commands there would be offering to undo the thing somebody just asked for.
func commandPrefix(value string) (string, bool) {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "", false
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return "", false
	}
	return strings.TrimPrefix(value, "/"), true
}

// matching is every command the prefix selects.
//
// With nothing typed the built-ins come first in their own order, which is by how often somebody
// reaches for one, and that is the answer to what a bare slash should show. Once there is a prefix
// the matches are alphabetical, because by then the person has said what they are looking for and
// the useful ordering is the predictable one.
func matching(prefix string, commands config.CommandSet) []menuItem {
	var user []menuItem
	for _, command := range commands.All() {
		user = append(user, menuItem{
			name:        command.Name,
			description: command.Description,
			scope:       string(command.Scope),
		})
	}

	if prefix == "" {
		return append(builtinItems(), user...)
	}

	// Prefix matches first and substring matches after them, each alphabetical. The prefix block is
	// what somebody spelling a name from its start expects; the substring block is what saves the
	// person who remembers "pact" but not that the command is called "compact". Ranking rather than
	// mixing, because a substring hit sorting above a prefix hit reads as the list guessing.
	var starts, contains []menuItem
	for _, item := range append(builtinItems(), user...) {
		switch {
		case strings.HasPrefix(item.name, prefix):
			starts = append(starts, item)
		case strings.Contains(item.name, prefix):
			contains = append(contains, item)
		}
	}
	sort.SliceStable(starts, func(i, j int) bool { return starts[i].name < starts[j].name })
	sort.SliceStable(contains, func(i, j int) bool { return contains[i].name < contains[j].name })
	return append(starts, contains...)
}

// move walks the selection, and scrolls the window to keep it on screen.
func (m *menu) move(by int) {
	if len(m.matches) == 0 {
		return
	}
	m.selected += by
	// Bounded rather than wrapping. A list that jumps from the last entry to the first is
	// disorienting at four rows, where the whole list is nearly on screen anyway.
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.matches) {
		m.selected = len(m.matches) - 1
	}
	m.clamp()
}

func (m *menu) clamp() {
	if len(m.matches) == 0 {
		m.selected, m.offset = 0, 0
		return
	}
	if m.selected >= len(m.matches) {
		m.selected = len(m.matches) - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+menuVisible {
		m.offset = m.selected - menuVisible + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// chosen is the command under the selection.
func (m menu) chosen() (menuItem, bool) {
	if !m.open || m.selected >= len(m.matches) {
		return menuItem{}, false
	}
	return m.matches[m.selected], true
}

// height is how many rows the list occupies, including the row that counts what is off screen.
func (m menu) height() int {
	if !m.open {
		return 0
	}
	if len(m.matches) == 0 {
		// The row that says nothing matched. Said rather than closing the list, because a menu that
		// vanished on a typo looks like the feature broke.
		return 1
	}
	rows := len(m.matches)
	if rows > menuVisible {
		rows = menuVisible + 1
	}
	return rows
}

// lines renders the list. The filter is whatever command is in the box, passed in rather than
// stored so the two cannot disagree, and it is what lets each row light up the part of its name
// that earned it a place in the list.
func (m menu) lines(width int, filter string) []string {
	if !m.open {
		return nil
	}
	t := theme.Current()

	if len(m.matches) == 0 {
		return []string{t.Muted.Render("  no command matches")}
	}

	end := m.offset + menuVisible
	if end > len(m.matches) {
		end = len(m.matches)
	}

	out := make([]string, 0, menuVisible+1)
	for i := m.offset; i < end; i++ {
		out = append(out, m.row(m.matches[i], i == m.selected, width, filter))
	}

	// The count of what is off screen, so four rows do not read as the whole list.
	if len(m.matches) > menuVisible {
		out = append(out, t.Muted.Render(
			"  "+itoa(len(m.matches)-menuVisible)+" more, up and down to move"))
	}
	return out
}

// row draws one command: the marker, the name with the matched letters lit, and what it does.
func (m menu) row(item menuItem, selected bool, width int, filter string) string {
	t := theme.Current()

	marker := "  "
	base := t.Body
	if selected {
		// A marker as well as a colour. The selected row has to be identifiable with the colour
		// stripped, which is the rule the whole interface is built on and the reason the monochrome
		// theme exists.
		marker, base = t.Key.Render("> "), t.Selected
	}

	// The letters the filter matched are drawn in the secondary colour, so the list shows why each
	// row is in it: type "pa" and the pa in compact lights up. The slash stays in the row's own
	// style, because it is punctuation rather than part of what matched.
	name := base.Render("/")
	if at := strings.Index(item.name, filter); filter != "" && at >= 0 {
		name += base.Render(item.name[:at]) +
			t.Success.Render(item.name[at:at+len(filter)]) +
			base.Render(item.name[at+len(filter):])
	} else {
		name += base.Render(item.name)
	}

	line := marker + name + t.Muted.Render("  "+item.description)
	if lipgloss.Width(line) <= width {
		return line
	}
	// The description is what gets cut, never the name. A truncated command name is a command
	// somebody cannot type.
	room := width - 2 - lipgloss.Width("/"+item.name) - 4
	if room < 4 {
		return marker + name
	}
	return marker + name + t.Muted.Render("  "+truncate(item.description, room))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
