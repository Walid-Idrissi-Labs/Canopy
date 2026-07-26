package chat

import (
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// The transcript, rendered as a flat list of display lines.
//
// Flat rather than a tree of components, because scrolling and following the tail are both
// questions about lines, and every alternative ends up converting to lines anyway at the point
// where it has to answer "what is on screen".

// Transcript renders a session.
//
// Nothing here decides what a turn means: it asks the state. `Whole()` is the only thing that lets
// a reply be shown as a finished answer, and every other state gets a label saying what it actually
// is. A refusal, a truncation and an interruption all leave text that reads like an answer, and the
// label is the only thing standing between that text and somebody acting on it.
func Transcript(session core.Session, width int, spinner string) []string {
	if width < 20 {
		width = 20
	}

	var lines []string
	for i, turn := range session.Turns {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, renderTurn(turn, width, spinner)...)
	}
	return lines
}

func renderTurn(turn core.Turn, width int, spinner string) []string {
	t := theme.Current()
	var lines []string

	// The question, marked so it is findable when scrolling back through a long session.
	for i, line := range wrap(turn.Request.Text, width-2) {
		prefix := t.Key.Render("> ")
		if i > 0 {
			prefix = "  "
		}
		lines = append(lines, prefix+t.Body.Render(line))
	}

	if turn.Thinking != "" {
		lines = append(lines, "")
		lines = append(lines, t.Muted.Render("thinking"))
		for _, line := range wrap(turn.Thinking, width) {
			lines = append(lines, t.Muted.Render(line))
		}
	}

	if turn.Text != "" {
		lines = append(lines, "")
		for _, line := range wrap(turn.Text, width) {
			lines = append(lines, t.Body.Render(line))
		}
	}

	for _, call := range turn.ToolCalls {
		lines = append(lines, t.Info.Render(fmt.Sprintf("  [%s]", call.Name)))
	}

	if status := statusLine(turn, spinner); status != "" {
		lines = append(lines, status)
	}
	return lines
}

// statusLine says how a turn ended, or that it has not.
//
// Empty for a turn that finished cleanly, because a completed answer speaks for itself and a line
// under every reply saying "complete" is noise that trains people to stop reading the ones that
// matter.
func statusLine(turn core.Turn, spinner string) string {
	t := theme.Current()

	switch turn.State {
	case core.TurnPending:
		return t.Muted.Render(spinner + " thinking")

	case core.TurnStreaming:
		// No spinner once text is arriving: the text moving is the progress indicator, and a
		// spinner next to it is two things claiming to say the same thing.
		if turn.Text == "" {
			return t.Muted.Render(spinner + " thinking")
		}
		return ""

	case core.TurnAwaitingTools:
		return t.Info.Render(spinner + " running tools")

	case core.TurnComplete:
		return ""

	case core.TurnInterrupted:
		return t.Warning.Render("[stopped, the reply above is partial]")

	case core.TurnRefused:
		return t.Warning.Render("[the provider declined this request]")

	case core.TurnTruncated:
		return t.Warning.Render("[cut off at the output limit, so the reply above is incomplete]")

	case core.TurnFailed:
		return t.Danger.Render("[" + firstLine(turn.Error) + "]")

	default:
		return ""
	}
}

func firstLine(s string) string {
	if s == "" {
		return "the turn failed"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Welcome is what the transcript shows before anything has been said.
//
// A blank screen with a prompt is technically correct and tells a first time user nothing. This is
// the one place in the program where somebody is guaranteed to be looking and has not yet decided
// whether the tool is worth their time.
func Welcome(width int, dir, key string) []string {
	t := theme.Current()

	var lines []string
	lines = append(lines, t.Title.Render("Canopy"))
	lines = append(lines, t.Muted.Render("a terminal coding agent for running several at once"))
	lines = append(lines, "")

	if dir != "" {
		lines = append(lines, t.Muted.Render("working in ")+t.Body.Render(dir))
	}
	if key != "" {
		lines = append(lines, t.Muted.Render("using ")+t.Body.Render(key))
	} else {
		// The one thing that makes the rest of the program work, said plainly rather than left to
		// be discovered when the first message fails.
		lines = append(lines, t.Warning.Render("no credential yet")+
			t.Muted.Render(", press ")+t.Key.Render("K")+t.Muted.Render(" to add one"))
	}

	lines = append(lines, "")
	lines = append(lines, t.Muted.Render("Type a message and press enter."))
	return lines
}
