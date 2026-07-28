package chat_test

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// The command list that drops out of the message box.

func withCommands(session core.Session) chat.Model {
	m := chat.New(&fakeEngine{session: session}, "s1", "canopy", "claude")
	m.SetCommands(config.ResolveCommands(nil, []config.Command{
		{Name: "audit", Description: "check the dependencies", Prompt: "x"},
		{Name: "changelog", Description: "write the changelog entry", Prompt: "x"},
	}))
	m.SetSize(96, 28)
	return m
}

func rowsOf(m chat.Model) []string { return strings.Split(plain(m.Body()), "\n") }

func rowIndex(lines []string, want string) int {
	for i, line := range lines {
		if strings.Contains(line, want) {
			return i
		}
	}
	return -1
}

// A bare slash offers what somebody is most likely to want, which is the built-ins in the order they
// are declared rather than in alphabetical order. Alphabetical is what a filtered list gets, because
// by then the person has said what they are after.
func TestABareSlashOffersTheCommonCommandsFirst(t *testing.T) {
	lines := rowsOf(typeText(withCommands(core.Session{ID: "s1"}), "/"))

	help, commands := rowIndex(lines, "/help"), rowIndex(lines, "/commands")
	if help < 0 || commands < 0 {
		t.Fatalf("the list did not open on a bare slash:\n%s", strings.Join(lines, "\n"))
	}
	if help > commands {
		t.Errorf("/help is below /commands, so the list is not in its declared order")
	}
	// "audit" sorts first alphabetically and is a user command, so it must not lead the bare list.
	if audit := rowIndex(lines, "/audit"); audit >= 0 && audit < help {
		t.Errorf("a user command leads the bare list, so it is sorted rather than ordered")
	}
}

// Typing narrows it. Commands that begin with what was typed lead the list, and commands that
// merely contain it follow them, so spelling a name from its start works the way it always has
// while half remembering the middle of one still finds it.
func TestTypingNarrowsTheListToWhatBeginsWithIt(t *testing.T) {
	lines := rowsOf(typeText(withCommands(core.Session{ID: "s1"}), "/ch"))

	if rowIndex(lines, "/changelog") < 0 {
		t.Errorf("the list lost the command that matches:\n%s", strings.Join(lines, "\n"))
	}
	for _, gone := range []string{"/help", "/commands", "/audit"} {
		if rowIndex(lines, gone) >= 0 {
			t.Errorf("%s is still offered under the prefix \"ch\"", gone)
		}
	}
}

// The half remembered middle of a name still finds it, below any command the same letters begin.
// Somebody who remembers "pact" but not that the command is called "compact" gets the command
// rather than "no command matches", which is what every comparable palette does.
func TestAFragmentFromTheMiddleOfANameStillFindsIt(t *testing.T) {
	lines := rowsOf(typeText(withCommands(core.Session{ID: "s1"}), "/pact"))

	if rowIndex(lines, "/compact") < 0 {
		t.Errorf("\"pact\" does not find /compact:\n%s", strings.Join(lines, "\n"))
	}

	// And a prefix match outranks a substring one, so the ranking is the predictable half of each.
	mixed := rowsOf(typeText(withCommands(core.Session{ID: "s1"}), "/c"))
	commands, compact := rowIndex(mixed, "/commands"), rowIndex(mixed, "/compact")
	if commands < 0 || compact < 0 {
		t.Fatalf("the prefix \"c\" lost a command it selects:\n%s", strings.Join(mixed, "\n"))
	}
}

// Four rows and a count of the rest. Enough to see there is a choice, few enough that the list is a
// suggestion rather than a page, and the count is what stops four reading as the whole list.
func TestOnlyFourAreShownAndTheRestAreCounted(t *testing.T) {
	lines := rowsOf(typeText(withCommands(core.Session{ID: "s1"}), "/"))

	var shown int
	for _, line := range lines {
		if strings.Contains(line, "  /") || strings.HasPrefix(line, "> /") {
			shown++
		}
	}
	if shown != 4 {
		t.Errorf("%d commands are on screen, want four:\n%s", shown, strings.Join(lines, "\n"))
	}
	if rowIndex(lines, "more, up and down to move") < 0 {
		t.Errorf("nothing says the list goes on:\n%s", strings.Join(lines, "\n"))
	}
}

// And the window scrolls with the selection, or the ones past the fourth are unreachable and the
// count is a promise the list does not keep.
func TestTheListScrollsToKeepTheSelectionOnScreen(t *testing.T) {
	m := typeText(withCommands(core.Session{ID: "s1"}), "/")
	for range 5 {
		m = press(m, tea.KeyDown)
	}

	lines := rowsOf(m)
	selected := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimRight(line, " "), "> /") {
			selected = i
		}
	}
	if selected < 0 {
		t.Fatalf("nothing is highlighted after moving down:\n%s", strings.Join(lines, "\n"))
	}
	// The first entry has scrolled away, which is what proves the window moved rather than the
	// highlight simply stopping at the bottom of a fixed list.
	if rowIndex(lines, "/help") >= 0 {
		t.Errorf("the list did not scroll, the first entry is still on screen:\n%s",
			strings.Join(lines, "\n"))
	}
}

// Below the box on an empty conversation and above it on one in progress. The box is in the middle
// of the screen in the first case and on the floor in the second, so there is only one direction
// with any room in it either time.
func TestTheListIsBelowTheBoxWhenEmptyAndAboveItWhenNot(t *testing.T) {
	lines := rowsOf(typeText(withCommands(core.Session{ID: "s1"}), "/"))
	box, list := rowIndex(lines, "╭"), rowIndex(lines, "/help")
	if box < 0 || list < 0 {
		t.Fatalf("box at %d and list at %d:\n%s", box, list, strings.Join(lines, "\n"))
	}
	if list < box {
		t.Errorf("on an empty conversation the list is above the box, at %d against %d", list, box)
	}

	inProgress := withCommands(core.Session{ID: "s1", Turns: []core.Turn{{
		Request: core.Message{Text: "a question"}, Text: "an answer", State: core.TurnComplete,
	}}})
	lines = rowsOf(typeText(inProgress, "/"))
	box, list = rowIndex(lines, "╭"), rowIndex(lines, "/help")
	if box < 0 || list < 0 {
		t.Fatalf("box at %d and list at %d:\n%s", box, list, strings.Join(lines, "\n"))
	}
	if list > box {
		t.Errorf("on a conversation in progress the list is below the box, at %d against %d", list, box)
	}
}

// Opening the list must not move the box. Somebody is typing into it while the list filters, and a
// box that walked up the screen with every keystroke would be unusable.
func TestOpeningTheListDoesNotMoveTheBox(t *testing.T) {
	before := rowIndex(rowsOf(withCommands(core.Session{ID: "s1"})), "╭")
	after := rowIndex(rowsOf(typeText(withCommands(core.Session{ID: "s1"}), "/")), "╭")

	if before != after {
		t.Errorf("the box moved from row %d to row %d when the list opened", before, after)
	}
}

// The list belongs to a command being named. Once there is a space the name is settled and what
// follows is arguments, and two slashes are the escape for a message that genuinely starts with one.
func TestTheListKnowsWhenItIsNotWanted(t *testing.T) {
	for _, typed := range []string{"/help ", "//not a command", "ordinary text"} {
		lines := rowsOf(typeText(withCommands(core.Session{ID: "s1"}), typed))
		if rowIndex(lines, "every key, on one screen") >= 0 {
			t.Errorf("the list opened on %q:\n%s", typed, strings.Join(lines, "\n"))
		}
	}
}

// Escape closes it and leaves what was typed, so dismissing the list is not the same as abandoning
// the command.
func TestEscapeClosesTheListAndKeepsWhatWasTyped(t *testing.T) {
	m := press(typeText(withCommands(core.Session{ID: "s1"}), "/co"), tea.KeyEsc)

	if rowIndex(rowsOf(m), "/commands") >= 0 {
		t.Errorf("the list is still up after escape:\n%s", plain(m.Body()))
	}
	if m.InputValue() != "/co" {
		t.Errorf("escape took the typing with it, the box holds %q", m.InputValue())
	}
}

// The highlight follows the command it was on rather than the row it was in.
//
// Typing another letter usually removes entries above the one somebody is aiming at, and a highlight
// that stayed on its index would slide onto a different command under their fingers, which is how
// somebody runs the wrong one.
func TestTheHighlightFollowsTheCommandNotTheRow(t *testing.T) {
	m := typeText(withCommands(core.Session{ID: "s1"}), "/c")
	// Matches are alphabetical: changelog, commands, compact, cost. Move to compact.
	m = press(press(m, tea.KeyDown), tea.KeyDown)
	m = typeText(m, "o") // "/co" drops changelog, so compact moves up a row

	m = press(m, tea.KeyTab)
	if m.InputValue() != "/compact " {
		t.Errorf("the highlight slid to %q when the list narrowed", m.InputValue())
	}
}
