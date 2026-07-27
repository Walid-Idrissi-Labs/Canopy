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

	// history is what has been sent in this conversation, oldest first.
	history []string
	// browsing is the position in history being shown, and equals len(history) when the box holds
	// something being written rather than something recalled. One field rather than a separate
	// bool, because two would let them disagree and there is exactly one state to represent.
	browsing int
	// draft is what was in the box when history browsing started, kept so walking back down
	// returns it rather than an empty box.
	draft string

	// Width is the drawable width, set by the model on resize.
	Width int
	// MaxLines caps how tall the box may grow before it scrolls internally, so a pasted essay
	// cannot push the conversation off the screen.
	MaxLines int
}

// NewInput builds an empty input.
func NewInput() Input { return Input{Width: 80, MaxLines: 6} }

// HistoryLimit is how many sent messages are kept per conversation.
//
// Enough to cover a working session and small enough that it is never the reason memory grows. The
// thing people actually reach for is one of the last few, and anybody hunting further back than
// sixty is scrolling the transcript, not pressing up.
const HistoryLimit = 60

// Value is what has been typed.
func (i Input) Value() string { return string(i.runes) }

// Empty reports whether there is nothing to send.
func (i Input) Empty() bool { return strings.TrimSpace(i.Value()) == "" }

// Clear empties the box, which is what happens once a message is sent.
//
// History survives it. Clearing the box is what happens on every send, and a send that also threw
// away the ability to press up would leave the feature working exactly once.
func (i *Input) Clear() {
	i.runes = nil
	i.cursor = 0
	i.release()
}

// SetValue replaces the contents and puts the cursor at the end.
func (i *Input) SetValue(s string) {
	i.runes = []rune(s)
	i.cursor = len(i.runes)
}

// Remember files a sent message.
func (i *Input) Remember(message string) {
	message = strings.TrimRight(message, "\n")
	if strings.TrimSpace(message) == "" {
		return
	}
	// A message identical to the one before it is not filed twice. Sending the same thing again is
	// usually a retry, and two identical entries mean two presses of up to get past one message,
	// which is the small annoyance that makes people stop using history at all.
	if n := len(i.history); n == 0 || i.history[n-1] != message {
		i.history = append(i.history, message)
	}
	if len(i.history) > HistoryLimit {
		// Re-sliced into a fresh array rather than left as a view on the old one, so the messages
		// that fell off the front can actually be collected.
		i.history = append([]string(nil), i.history[len(i.history)-HistoryLimit:]...)
	}
	i.release()
}

// LoadHistory replaces the history, oldest first.
//
// Called when the screen points at a different conversation, so opening one that was started
// yesterday has the same history as one started a minute ago. Rebuilt from the conversation's own
// messages rather than stored separately, which means there is no second copy to fall out of step
// with the transcript.
func (i *Input) LoadHistory(messages []string) {
	i.history = nil
	i.release()
	for _, message := range messages {
		i.Remember(message)
	}
}

// History is what would be recalled, oldest first. For tests.
func (i Input) History() []string { return append([]string(nil), i.history...) }

// release puts the box back into the state where it holds something being written.
func (i *Input) release() {
	i.browsing = len(i.history)
	i.draft = ""
}

// older walks back through what has been sent.
func (i *Input) older() bool {
	if len(i.history) == 0 {
		return false
	}
	if i.browsing >= len(i.history) {
		// Stepping off the message being written, which is kept so coming back down returns it
		// rather than an empty box. Losing a half typed thought to a keystroke meant for
		// convenience is the thing that makes people stop trusting the arrow keys.
		i.draft = string(i.runes)
		i.browsing = len(i.history)
	}
	if i.browsing == 0 {
		// Already at the oldest. Consumed anyway, so the key does not fall through and mean
		// something else at the far end of the conversation.
		return true
	}
	i.browsing--
	i.SetValue(i.history[i.browsing])
	return true
}

// newer walks forward, and off the end back into the draft.
func (i *Input) newer() bool {
	if i.browsing >= len(i.history) {
		return false
	}
	i.browsing++
	if i.browsing >= len(i.history) {
		draft := i.draft
		i.release()
		i.SetValue(draft)
		return true
	}
	i.SetValue(i.history[i.browsing])
	return true
}

// edited is called by everything that changes the text.
//
// Typing into a recalled message detaches it from history: the box now holds something being
// written, which happens to have started life as an old message. The alternative is an edit that
// silently disappears the next time an arrow key is pressed.
func (i *Input) edited() { i.release() }

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
			i.edited()
		}
		return true

	case tea.KeyDelete:
		if i.cursor < len(i.runes) {
			i.runes = append(i.runes[:i.cursor], i.runes[i.cursor+1:]...)
			i.edited()
		}
		return true

	case tea.KeyUp:
		return i.older()

	case tea.KeyDown:
		return i.newer()

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
		i.edited()
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
	i.edited()
}

func (i *Input) deleteWord() {
	if i.cursor == 0 {
		return
	}
	i.edited()
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
