package chat

import (
	"github.com/charmbracelet/lipgloss"

	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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
// KindOf says what kind of thing a tool is, by name.
//
// A function rather than a table in this file, because the transcript renders calls from tools it has
// never heard of. A table would label the built in ones and leave everything from an MCP server
// blank, which is exactly backwards: a tool from somebody else's server is the one where knowing it
// can run commands matters most. False for a name nothing knows about, which draws no label rather
// than a wrong one.
type KindOf func(name string) (core.ToolKind, bool)

// Detail is how much of a tool call the transcript shows.
//
// Zero value is the reading view: every call names what it touched and how it ended, and the bulk of
// what came back is summarised rather than printed. Expanded is the same transcript with the caps
// lifted, which is what ctrl+o toggles. The distinction exists because these are two different jobs.
// Following an agent is a glance, and the answer is what you are waiting for, so a thousand line file
// read must not bury it. Checking an agent is a read, and then the caps are in the way.
type Detail struct {
	// Expanded lifts the caps on diffs, output previews and error text.
	Expanded bool

	// Now and Started say how long a call that has not come back yet has been going.
	//
	// Started is keyed by call ID and filled in by the screen, not by the engine, because a call
	// carries no start time and internal/core is frozen. It is therefore how long the call has been
	// on screen rather than how long it has been running, which differs by at most one frame and is
	// the honest thing to render: the label says "running for", not "took".
	Now     time.Time
	Started map[string]time.Time
}

func Transcript(session core.Session, width int, spinner string, kinds KindOf) []string {
	return TranscriptWith(session, width, spinner, kinds, Detail{})
}

// TranscriptWith renders a session at a chosen level of detail.
func TranscriptWith(session core.Session, width int, spinner string, kinds KindOf, detail Detail) []string {
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
		lines = append(lines, renderTurn(turn, width, spinner, kinds, detail)...)
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

func renderTurn(turn core.Turn, width int, spinner string, kinds KindOf, detail Detail) []string {
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
		lines = append(lines, renderToolCall(call, resultFor(turn, call), width, kinds, detail)...)
	}

	lines = append(lines, statusLines(turn, spinner, width)...)
	return lines
}

// Tool calls, rendered so somebody can follow what the agent actually did.
//
// A line saying `[run_command]` and nothing else is the same information as a spinner: it says
// something happened and refuses to say what. Somebody watching an agent edit their repository
// needs three things, and they are the three below: which file or command, whether it worked, and
// how long it took. Without the first they cannot tell an agent reading a file from an agent
// rewriting it. Without the second a failed call looks exactly like a successful one, which is how
// an agent ends up looking productive while getting nowhere.

// resultFor pairs a call with its answer.
//
// By call ID rather than by position, because a turn can have several calls in flight and the
// results come back in whatever order the tools finish. Matching by index would attribute one
// tool's failure to another tool's call, which is worse than showing nothing.
func resultFor(turn core.Turn, call core.ToolCall) *core.ToolResult {
	for i := range turn.ToolResults {
		if turn.ToolResults[i].CallID == call.ID {
			return &turn.ToolResults[i]
		}
	}
	return nil
}

func renderToolCall(call core.ToolCall, result *core.ToolResult, width int, kinds KindOf, detail Detail) []string {
	t := theme.Current()

	// The label is what makes a call readable at a glance rather than at a read. A wall of tool
	// names all in one colour is a wall; the same wall with "run" against the one that shells out is
	// something an eye can skim for the calls worth looking at.
	label, labelStyle := kindLabel(call.Name, kinds)

	head := "  " + labelStyle.Render(label) + " " + t.Info.Render(call.Name)
	if subject := summariseArguments(call.Input); subject != "" {
		// Truncated to the width rather than wrapped. The argument line is a label, and a file path
		// spilling onto three lines turns a glance into a paragraph.
		spent := 2 + lipgloss.Width(label) + 1 + lipgloss.Width(call.Name) + 2
		head += "  " + t.Muted.Render(truncate(terminalSafe(subject), width-spent))
	}
	lines := []string{head}

	// Aligned under the tool name rather than under the label, so the outcome reads as belonging to
	// the call above it and the labels stay in one column down the left.
	const indent = "      "

	// No result yet means it is still running, or waiting on a person. Said in words and, once the
	// screen has watched it for a second, with the count: a call that has been sitting there for a
	// minute and a call that started a moment ago are the same line otherwise, and telling them
	// apart is the whole question somebody watching a stuck agent is asking.
	if result == nil {
		return append(lines, t.Muted.Render(indent+runningFor(call.ID, detail)))
	}

	timing := formatDuration(result.Duration)
	content := terminalSafe(result.Content)
	if result.IsError {
		lines = append(lines, t.Danger.Render(indent+"✗ failed after "+timing))
		// The reason, in the agent's own words, and as many of those words as fit. Showing only the
		// first line was a quiet loss of exactly the thing a person needs: a compiler error, a stack
		// trace and a refused permission all start with a line that does not say which one it is.
		lines = append(lines, renderOutput(content, width, errorLines, detail, t.Muted)...)
		return lines
	}

	summary := indent + "✓ " + timing
	if extra := summariseResult(content); extra != "" {
		summary += ", " + extra
	}
	lines = append(lines, t.Muted.Render(summary))

	// What the call did to the repository, where it did something. A write is the one kind of call
	// whose consequences outlive the conversation, so it is the one kind that gets shown rather than
	// counted.
	if change := renderChange(call, width, detail); len(change) > 0 {
		return append(lines, change...)
	}
	return append(lines, renderOutput(content, width, outputLines, detail, t.Muted)...)
}

// How much of what came back is worth putting on screen, before ctrl+o.
//
// Six lines of output and ten of an error, because they answer different questions. Output is
// usually confirmation that a thing worked and the first few lines carry it; an error is the whole
// reason you are reading, and a Go stack or a compiler's second sentence is routinely past line six.
const (
	outputLines = 6
	errorLines  = 10
	diffLines   = 14
)

// runningFor is the label on a call that has not come back.
func runningFor(id string, detail Detail) string {
	started, ok := detail.Started[id]
	if !ok || detail.Now.IsZero() || !detail.Now.After(started) {
		return "running"
	}
	elapsed := detail.Now.Sub(started)
	if elapsed < time.Second {
		return "running"
	}
	return "running for " + formatDuration(elapsed)
}

// renderChange draws what a writing call did, as a diff.
//
// Only the two tools whose arguments carry the change: `edit_file` sends the old and the new text,
// and `write_file` sends the whole content, which against nothing is every line an addition. A tool
// from an MCP server that happens to write files is not guessed at, because a diff drawn from
// arguments this function does not understand would be a confident lie about somebody's repository.
func renderChange(call core.ToolCall, width int, detail Detail) []string {
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(call.Input, &args); err != nil {
		return nil
	}

	var lines []DiffLine
	switch call.Name {
	case "edit_file":
		if args.OldText == "" && args.NewText == "" {
			return nil
		}
		lines = trimToChanges(Diff(args.OldText, args.NewText))
	case "write_file":
		if args.Content == "" {
			return nil
		}
		lines = Diff("", args.Content)
	default:
		return nil
	}
	if len(lines) == 0 {
		return nil
	}

	limit := diffLines
	if detail.Expanded {
		limit = 0
	}
	out := renderDiff(lines, languageFor(args.Path), width, limit)

	added, removed := DiffCounts(lines)
	t := theme.Current()
	tally := t.Success.Render("+"+strconv.Itoa(added)) + " " + t.Danger.Render("-"+strconv.Itoa(removed))
	return append([]string{"      " + tally}, out...)
}

// renderOutput shows the head of what a call returned.
//
// Bounded on purpose, and the bound is stated rather than silent. The rule this replaces was that
// output belongs to the model and not to the screen, and the half of it worth keeping is still here:
// the reply is what somebody is waiting for and a thousand line read must not bury it. What was
// wrong was the other half, that the screen therefore shows none of it, which left a `run_command`
// printing a test failure rendering as a tick and a duration.
func renderOutput(content string, width int, limit int, detail Detail, style lipgloss.Style) []string {
	content = terminalSafe(content)
	content = strings.TrimRight(content, "\n")
	if strings.TrimSpace(content) == "" {
		return nil
	}

	const indent = "        "
	body := width - len(indent)
	if body < 8 {
		body = 8
	}

	all := strings.Split(content, "\n")
	if detail.Expanded {
		limit = len(all)
	}

	var out []string
	for i, line := range all {
		if i >= limit {
			out = append(out, style.Render(indent+plural(len(all)-i, "more line", "more lines")+", ctrl+o for all of it"))
			break
		}
		for _, fragment := range wrapLine(expandTabs(line), body) {
			out = append(out, style.Render(indent+fragment))
		}
	}
	return out
}

// kindLabels are the four character tags, one per kind.
//
// Four characters and padded, so the tool names line up in a column no matter which kinds are on
// screen. A ragged left edge is what makes a list of calls read as noise.
//
// Words rather than glyphs. A pencil and a globe are charming in the two fonts that have them and are
// missing glyph boxes everywhere else, and this is the part of the screen somebody stares at while an
// agent works. It also means the distinction survives NO_COLOR, which colour alone would not.
var kindLabels = map[core.ToolKind]string{
	core.ToolRead:    "read",
	core.ToolWrite:   "edit",
	core.ToolExecute: "run ",
	core.ToolNetwork: "net ",
	core.ToolGit:     "git ",
}

// kindLabel is the tag and the colour for one call.
func kindLabel(name string, kinds KindOf) (string, lipgloss.Style) {
	t := theme.Current()
	if kinds == nil {
		return "    ", t.Muted
	}

	kind, known := kinds(name)
	if !known {
		// A tool nothing can identify gets space rather than a guess, so the column still lines up.
		return "    ", t.Muted
	}

	label, ok := kindLabels[kind]
	if !ok {
		return "    ", t.Muted
	}

	// Coloured by how much the kind can do, which is the same ordering the permission model uses.
	// Running a command is the broadest thing there is and reaching the network is the one whose
	// results come back untrusted, so those two are the ones that do not read as quiet.
	switch kind {
	case core.ToolExecute:
		return label, t.Warning
	case core.ToolNetwork:
		return label, t.Info
	case core.ToolWrite, core.ToolGit:
		return label, t.Success
	default:
		return label, t.Muted
	}
}

// argumentOrder is which argument to show when a tool takes several.
//
// The one that says what the call is about. A `read_file` call is about its path and an
// `edit_file` call is also about its path, not about the two blocks of text being swapped, which
// would fill the screen and say less.
var argumentOrder = []string{"path", "command", "pattern", "query", "url", "name", "old_path"}

// summariseArguments picks the one argument worth putting on the call line.
//
// Generic rather than a table of tools, because the interface renders calls from tools it has never
// heard of. MCP servers and anything added later go through this same path, and a table would
// render those as bare names while the built in ones got labels.
func summariseArguments(input []byte) string {
	var args map[string]any
	if err := json.Unmarshal(input, &args); err != nil || len(args) == 0 {
		return ""
	}

	for _, key := range argumentOrder {
		if value, ok := args[key]; ok {
			if text := scalar(value); text != "" {
				return text
			}
		}
	}

	// Nothing recognised, so show the keys in a stable order rather than whichever one the map
	// happened to yield first. An argument line that changes on every redraw reads as flicker.
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if text := scalar(args[key]); text != "" {
			return key + " " + text
		}
	}
	return ""
}

// scalar renders an argument value if it is the kind of thing that fits on a line.
func scalar(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(strings.ReplaceAll(v, "\n", " "))
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		return ""
	}
}

// summariseResult says how much came back, without showing it.
//
// The output itself belongs to the model, not to the screen. A read of a thousand line file printed
// into the conversation buries the reply that follows it, and the reply is the part a person is
// waiting for. The size is what they actually want to know.
func summariseResult(content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if lines := strings.Count(content, "\n") + 1; lines > 1 {
		return fmt.Sprintf("%d lines", lines)
	}
	return ""
}

// formatDuration is a length of time at the precision somebody reading it cares about.
//
// Milliseconds below a second, one decimal below a minute, and minutes and seconds above it.
// Nanoseconds on a call that took four minutes is a number nobody can read at a glance, and the
// glance is the entire point of putting it there.
func formatDuration(d time.Duration) string {
	switch {
	case d <= 0:
		return "no time"
	case d < time.Millisecond:
		// "0ms" reads as a measurement that failed rather than as a call that was fast, and the
		// fast ones are most of them: reading a small file takes a few hundred microseconds.
		return "under a ms"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// truncate shortens with an ellipsis, so a long path ends in something rather than simply stopping.
func truncate(s string, width int) string {
	if width < 8 {
		width = 8
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}

// statusLines say how a turn ended, or that it has not.
//
// Nothing for a turn that finished cleanly, because a completed answer speaks for itself and a line
// under every reply saying "complete" is noise that trains people to stop reading the ones that
// matter.
func statusLines(turn core.Turn, spinner string, width int) []string {
	t := theme.Current()
	one := func(s string) []string { return []string{s} }

	switch turn.State {
	case core.TurnPending:
		return one(t.Muted.Render(spinner + " thinking"))

	case core.TurnStreaming:
		// No spinner once text is arriving: the text moving is the progress indicator, and a
		// spinner next to it is two things claiming to say the same thing.
		if turn.Text == "" {
			return one(t.Muted.Render(spinner + " thinking"))
		}
		return nil

	case core.TurnAwaitingTools:
		return one(t.Info.Render(spinner + " running tools"))

	case core.TurnComplete:
		if turn.RolledBack != "" {
			// Amber rather than red, and on a turn that is otherwise complete, because the turn did
			// not fail: the model answered and the tools ran, and then the workspace did not verify
			// and the whole thing was put back. Showing it as a failure would lose the difference
			// between "this did not work" and "this worked and was not kept".
			return one(t.Warning.Render("[" + firstLine(turn.RolledBack) + "]"))
		}
		return nil

	case core.TurnInterrupted:
		return one(t.Warning.Render("[stopped, the reply above is partial]"))

	case core.TurnRefused:
		return one(t.Warning.Render("[the provider declined this request]"))

	case core.TurnTruncated:
		return one(t.Warning.Render("[cut off at the output limit, so the reply above is incomplete]"))

	case core.TurnFailed:
		// The whole error, wrapped, rather than its first line in brackets. The classifier goes to
		// some trouble to keep the provider's own words on the message, and a renderer that cut
		// everything past the first line would throw that detail away at the last step. The mark is
		// the same one a failed tool call carries, so red failures look alike wherever they appear.
		reason := turn.Error
		if reason == "" {
			reason = "the turn failed"
		}
		out := make([]string, 0, 2)
		for i, line := range wrap(reason, width-2) {
			prefix := "✗ "
			if i > 0 {
				prefix = "  "
			}
			out = append(out, t.Danger.Render(prefix+line))
		}
		return out

	default:
		return nil
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

// What an empty conversation shows used to live here, as a welcome block that flowed from the top
// of the transcript with the message box pinned to the floor below it. It is a composed screen now
// and lives in opening.go, because where the box sits is the whole point of it.
