package chat

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// Input is the message box.
//
// Hand written rather than pulled from a widget library, for one reason that matters: the cursor
// has to sit inside wrapped text, and a single line field that scrolls horizontally is the wrong
// shape for the thing people actually type here, which is several sentences and sometimes a pasted
// stack trace. A box that grows to a few lines and then scrolls is what every comparable tool does,
// and it is not much code.
type Input struct {
	runes  []rune
	cursor int

	// Width is the drawable width, set by the model on resize.
	Width int
	// MaxLines caps how tall the box may grow before it scrolls internally, so a pasted essay
	// cannot push the conversation off the screen.
	MaxLines int
}

// NewInput builds an empty input.
func NewInput() Input { return Input{Width: 80, MaxLines: 6} }

// Value is what has been typed.
func (i Input) Value() string { return string(i.runes) }

// Empty reports whether there is nothing to send.
func (i Input) Empty() bool { return strings.TrimSpace(i.Value()) == "" }

// Clear empties the box, which is what happens once a message is sent.
func (i *Input) Clear() {
	i.runes = nil
	i.cursor = 0
}

// SetValue replaces the contents and puts the cursor at the end.
func (i *Input) SetValue(s string) {
	i.runes = []rune(s)
	i.cursor = len(i.runes)
}

// Update handles a keystroke and reports whether it was consumed.
//
// Returning "not consumed" is what lets the model above decide what an unhandled key means, rather
// than the input silently swallowing every keystroke that reaches it.
func (i *Input) Update(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes:
		i.insert(msg.Runes)
		return true

	case tea.KeySpace:
		i.insert([]rune{' '})
		return true

	case tea.KeyBackspace:
		if i.cursor > 0 {
			i.runes = append(i.runes[:i.cursor-1], i.runes[i.cursor:]...)
			i.cursor--
		}
		return true

	case tea.KeyDelete:
		if i.cursor < len(i.runes) {
			i.runes = append(i.runes[:i.cursor], i.runes[i.cursor+1:]...)
		}
		return true

	case tea.KeyLeft:
		if i.cursor > 0 {
			i.cursor--
		}
		return true

	case tea.KeyRight:
		if i.cursor < len(i.runes) {
			i.cursor++
		}
		return true

	case tea.KeyHome, tea.KeyCtrlA:
		i.cursor = 0
		return true

	case tea.KeyEnd, tea.KeyCtrlE:
		i.cursor = len(i.runes)
		return true

	case tea.KeyCtrlU:
		// Everything before the cursor, which is the shell habit and the one people reach for when
		// they have changed their mind about a whole message.
		i.runes = append([]rune(nil), i.runes[i.cursor:]...)
		i.cursor = 0
		return true

	case tea.KeyCtrlW:
		i.deleteWord()
		return true
	}

	// A literal newline, for a message with a blank line in it. Enter sends, so there has to be
	// some way to type one, and every comparable tool uses this pair.
	if msg.String() == "alt+enter" || msg.String() == "ctrl+j" {
		i.insert([]rune{'\n'})
		return true
	}
	return false
}

func (i *Input) insert(runes []rune) {
	tail := append([]rune(nil), i.runes[i.cursor:]...)
	i.runes = append(i.runes[:i.cursor], runes...)
	i.runes = append(i.runes, tail...)
	i.cursor += len(runes)
}

func (i *Input) deleteWord() {
	if i.cursor == 0 {
		return
	}
	end := i.cursor
	for end > 0 && i.runes[end-1] == ' ' {
		end--
	}
	for end > 0 && i.runes[end-1] != ' ' {
		end--
	}
	i.runes = append(i.runes[:end], i.runes[i.cursor:]...)
	i.cursor = end
}

// cursorBlock is what stands in for a terminal cursor.
//
// Drawn rather than positioned, because the real cursor cannot be placed inside a lipgloss
// composed string without tracking every style's effect on the offset. A reversed cell is what
// every terminal editor falls back to and it survives a theme change.
const cursorBlock = " "

// Lines renders the input as display lines with the cursor drawn in.
//
// Returns at most MaxLines, scrolled so the cursor is always one of them. An input that hid the
// cursor when the text got long would leave people typing blind.
func (i Input) Lines() []string {
	t := theme.Current()
	width := i.Width
	if width < 8 {
		width = 8
	}

	// The cursor is a position in the text, so it is rendered by splitting the text there rather
	// than by counting columns afterwards, which would have to re-derive the wrapping.
	before := string(i.runes[:i.cursor])
	after := string(i.runes[i.cursor:])

	var head string
	var tail string
	if len(after) > 0 && !strings.HasPrefix(after, "\n") {
		runes := []rune(after)
		head = t.Cursor.Render(string(runes[0]))
		tail = string(runes[1:])
	} else {
		head = t.Cursor.Render(cursorBlock)
		tail = after
	}

	lines := wrapWithMarkers(before, head, tail, width)

	if len(lines) <= i.MaxLines {
		return lines
	}
	// Scrolled to keep the last lines, which is where the cursor is while typing. A box that
	// scrolled from the top would show the beginning of a long message and hide what is being
	// written.
	return lines[len(lines)-i.MaxLines:]
}

// Height is how many lines the box will occupy, including its border.
func (i Input) Height() int {
	const border = 2
	return len(i.Lines()) + border
}
