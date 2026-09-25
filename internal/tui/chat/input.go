package chat

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/paste"
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
	// MinLines is how tall the box is with nothing in it.
	//
	// Three rather than one. A one line box reads as a search field, and what people write here is
	// several sentences or a pasted stack trace, so the box that fits it should be the box they are
	// looking at rather than one that grows on the second word and jogs the whole screen up. It also
	// gives the mode and the model written into the top edge something to sit above, instead of
	// leaving them as a caption on a bare rule.
	MinLines int
}

// NewInput builds an empty input.
func NewInput() Input { return Input{Width: 80, MaxLines: 6, MinLines: 3} }

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
func (i *Input) Update(msg tea.KeyPressMsg) bool {
	switch msg.String() {
	case "space":
		i.insert([]rune{' '})
		return true

	// Option on a Mac keyboard arrives as alt: option+delete removes a word, as it does in every
	// other text field there, and option+arrows move by word.
	case "alt+backspace", "ctrl+w":
		i.deleteWord()
		return true

	case "alt+left", "ctrl+left", "alt+b":
		i.cursor = i.wordStart()
		return true

	case "alt+right", "ctrl+right", "alt+f":
		i.cursor = i.wordEnd()
		return true

	case "backspace":
		if i.cursor > 0 {
			i.runes = append(i.runes[:i.cursor-1], i.runes[i.cursor:]...)
			i.cursor--
			i.edited()
		}
		return true

	case "delete", "alt+delete":
		if i.cursor < len(i.runes) {
			i.runes = append(i.runes[:i.cursor], i.runes[i.cursor+1:]...)
			i.edited()
		}
		return true

	case "up", "alt+up":
		return i.older()

	case "down", "alt+down":
		return i.newer()

	case "left":
		if i.cursor > 0 {
			i.cursor--
		}
		return true

	case "right":
		if i.cursor < len(i.runes) {
			i.cursor++
		}
		return true

	case "home", "ctrl+a":
		i.cursor = 0
		return true

	case "end", "ctrl+e":
		i.cursor = len(i.runes)
		return true

	case "ctrl+u":
		// Everything before the cursor, which is the shell habit and the one people reach for when
		// they have changed their mind about a whole message.
		i.runes = append([]rune(nil), i.runes[i.cursor:]...)
		i.cursor = 0
		i.edited()
		return true
	}

	// Printable text, a character or several at once from an input method.
	if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		i.insert([]rune(msg.Text))
		return true
	}

	// A literal newline, for a message with a blank line in it. Enter sends, so there has to be
	// some way to type one, and every comparable tool uses this pair.
	if msg.String() == "alt+enter" || msg.String() == "ctrl+j" || msg.String() == "shift+enter" {
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
	end := i.wordStart()
	i.runes = append(i.runes[:end], i.runes[i.cursor:]...)
	i.cursor = end
}

// wordStart is where the word before the cursor begins, spaces before the cursor skipped.
func (i *Input) wordStart() int {
	at := i.cursor
	for at > 0 && isSpace(i.runes[at-1]) {
		at--
	}
	for at > 0 && !isSpace(i.runes[at-1]) {
		at--
	}
	return at
}

// wordEnd is where the word after the cursor ends, spaces after the cursor skipped.
func (i *Input) wordEnd() int {
	at := i.cursor
	for at < len(i.runes) && isSpace(i.runes[at]) {
		at++
	}
	for at < len(i.runes) && !isSpace(i.runes[at]) {
		at++
	}
	return at
}

func isSpace(r rune) bool { return r == ' ' || r == '\n' }

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

	if len(lines) > i.MaxLines {
		// Scrolled to keep the last lines, which is where the cursor is while typing. A box that
		// scrolled from the top would show the beginning of a long message and hide what is being
		// written.
		return lines[len(lines)-i.MaxLines:]
	}
	if len(lines) < i.MinLines {
		// Padded below rather than above, so the first line of what is being typed stays on the
		// first line of the box and the cursor does not start halfway down it.
		lines = append(lines, make([]string, i.MinLines-len(lines))...)
	}
	return lines
}

// Height is how many lines the box will occupy, including its border.
func (i Input) Height() int {
	const border = 2
	return len(i.Lines()) + border
}

// Paste inserts pasted text as typed, all at once. A terminal that brackets its pastes sends them as
// their own message rather than as keystrokes, so an enter inside a paste is a newline, not a send.
func (i *Input) Paste(text string) {
	// Terminals send a pasted line break as a carriage return, which drawn raw would send the cursor
	// back over the start of the line; paste.Lines makes it a newline and drops the other controls.
	i.insert([]rune(paste.Lines(text)))
}
