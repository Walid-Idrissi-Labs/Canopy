package chat

import (
	"fmt"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
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

	compaction, compacted := session.Compacted()

	var lines []string
	for i, turn := range session.Turns {
		// The marker sits where the compaction happened, between the turns it summarised and the
		// ones it kept, so somebody scrolling back can see exactly where the agent's memory of the
		// conversation stops being the conversation.
		if compacted && i == compaction.Through {
			lines = append(lines, "")
			lines = append(lines, compactionMarker(compaction, width)...)
		}
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, renderTurn(turn, width, spinner)...)
	}
	return lines
}

// compactionMarker is the line in the transcript saying the agent no longer has the turns above it.
//
// Always drawn, never optional. An agent that quietly forgets half of what it was told and carries
// on answering is the same class of problem as a test result that says passing about code it never
// ran: confident, wrong, and undetectable from outside. This is the thing that makes it detectable.
func compactionMarker(compaction core.Compaction, width int) []string {
	t := theme.Current()

	saved := ""
	if compaction.TokensBefore > compaction.TokensAfter {
		saved = fmt.Sprintf(", about %s down to %s",
			shortCount(compaction.TokensBefore), shortCount(compaction.TokensAfter))
	}
	headline := fmt.Sprintf("--- summarised the %d turns above%s ---",
		compaction.Through, saved)

	lines := []string{t.Warning.Render(headline)}
	for _, line := range wrap(compaction.Summary, width-2) {
		lines = append(lines, t.Muted.Render("  "+line))
	}
	// Said explicitly, because the obvious fear on reading the line above is that the conversation
	// has been thrown away, and it has not.
	lines = append(lines, t.Muted.Render(
		"  The turns above are still here and still searchable, they are just no longer sent."))
	return lines
}

func shortCount(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
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
		// The reply goes through the markdown renderer; the question above does not. What somebody
		// typed is what they typed, and rendering their asterisks as emphasis would change their
		// own words back at them.
		lines = append(lines, RenderMarkdown(turn.Text, width)...)
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
// It is also what a new conversation opens on, which is the other reason the mark is here. Pressing
// the key for a fresh chat and getting a blank rectangle makes it impossible to tell a new
// conversation from one that failed to load.
func Welcome(width int, dir, key string) []string {
	t := theme.Current()

	var lines []string
	for _, line := range brand.Mark(width) {
		lines = append(lines, t.Logo.Render(line))
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}

	lines = append(lines, t.Title.Render("Canopy"))
	lines = append(lines, t.Muted.Render(brand.Tagline))
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
			t.Muted.Render(", press ")+t.Key.Render("ctrl+k")+t.Muted.Render(" to add one"))
	}

	lines = append(lines, "")
	lines = append(lines, t.Muted.Render("Type a message and press enter."))
	return lines
}
