package agents

// The mosaic: every agent on screen at once, each in a pane of its own.
//
// This is the screen the program is named for. Several agents working away and somewhere
// comfortable to watch them all from, which means the layout's one job is that no agent is ever
// invisible: what does not fit on the grid is said to be off screen rather than silently absent.
//
// Each pane is a small copy of the conversation frame, deliberately. The name and state ride the
// top border the way the application's own facts ride the header, and the campfire from the chat
// box rides the bottom border: lit and dancing while that agent works, grey coals while it rests.
// Someone across the room can count the fires and know how many agents are busy. The wordmark is
// nowhere in a pane, because a pane is an agent's window and the brand already owns the header.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

const (
	// maxTiles is the most panes a mosaic draws at once.
	//
	// Eight, because the digits that jump to a pane run 1 to 8 and because past eight the panes
	// stop being windows and become a contact sheet. More agents than tiles page rather than
	// shrink.
	maxTiles = 8

	// maxColumns is the most panes across, whatever the width.
	//
	// Four across at two rows is the full grid of eight. A fifth column is possible on a very
	// wide terminal and is not worth having: the eye sweeps a row of four in one pass, and five
	// panes of a code discussion in one row is a wall.
	maxColumns = 4

	// minPaneWidth is the narrowest a mosaic pane may be drawn.
	//
	// The old two way split refused below twenty columns of text; a pane carries chrome as well,
	// so it needs more before its content column is worth reading. Below this the grid drops a
	// column rather than squeezing every pane.
	minPaneWidth = 38

	// minPaneHeight is the shortest a pane may be drawn: two border rows and five of content,
	// which is a waiting line, the request and a three line tail. Below that a pane shows a
	// title and nothing the title is about.
	minPaneHeight = 7

	// minSliceWidth is the narrowest a hero layout's bottom slice may be. Slices are glances,
	// not reading material, so they may be narrower than a mosaic pane.
	minSliceWidth = 24

	// heroSlices is the most agents shown along the bottom of the hero layout.
	heroSlices = 4
)

// grid is the shape of one mosaic page: how many panes across each row.
//
// Rows rather than a single columns count, because the last row of an uneven page is divided
// among fewer panes so the tiling stays perfect: three agents on a two column grid draw as two
// up top and one across the whole bottom, not two panes and a hole.
type grid struct {
	rows []int
}

// tiles is how many panes the grid holds.
func (g grid) tiles() int {
	total := 0
	for _, row := range g.rows {
		total += row
	}
	return total
}

// planGrid decides the page shape for a number of agents in a body of width by height.
//
// Columns are as many as keep every pane readable, capped at four. Rows are as many as keep
// every pane tall enough, capped by how many agents there are to show. The remainder pages.
func planGrid(count, width, height int) grid {
	// No agents means no grid. Callers render the empty state instead, and the guard is here so
	// that a keystroke like ] on an empty screen asks a harmless question rather than dividing
	// by zero.
	if count < 1 {
		return grid{}
	}
	columns := width / minPaneWidth
	if columns < 1 {
		columns = 1
	}
	if columns > maxColumns {
		columns = maxColumns
	}
	if columns > count {
		columns = count
	}

	rows := (count + columns - 1) / columns
	if most := height / minPaneHeight; rows > most {
		rows = most
	}
	if rows < 1 {
		rows = 1
	}
	for rows > 1 && rows*columns > maxTiles {
		rows--
	}

	visible := minInt(count, minInt(rows*columns, maxTiles))
	// The visible panes are dealt across the rows as evenly as they go, fuller rows first, so an
	// uneven last page still tiles the whole body: three agents on a two column grid draw as two
	// up top and one across the whole bottom, not two panes and a hole.
	base, extra := visible/rows, visible%rows
	var shape []int
	for i := 0; i < rows; i++ {
		row := base
		if i < extra {
			row++
		}
		if row > 0 {
			shape = append(shape, row)
		}
	}
	return grid{rows: shape}
}

// mosaicPlan settles the paging question once, so no two answers about it can disagree: how many
// agents fit a page, how many pages that makes, and how tall the panes may be once the paging
// line has taken its row.
//
// The line is reserved only when paging exists, and whether paging exists is judged at the full
// height, so the answer cannot chase its own tail. In the corner case where giving up the line
// pushes a pane off the page, that pane moves to the next page, which is what the line is there
// to say.
func (m Model) mosaicPlan() (per, pages, height int) {
	count := len(m.statuses)
	height = m.height
	if per = planGrid(count, m.width, height).tiles(); count <= per {
		return per, 1, height
	}
	height = maxInt(m.height-1, minPaneHeight)
	per = planGrid(count, m.width, height).tiles()
	return per, (count + per - 1) / per, height
}

// perPage is how many agents one mosaic page shows at the current size.
func (m Model) perPage() int {
	per, _, _ := m.mosaicPlan()
	return maxInt(per, 1)
}

// pages is how many mosaic pages there are.
func (m Model) pages() int {
	_, pages, _ := m.mosaicPlan()
	return pages
}

// page is which mosaic page the cursor is on. Derived rather than stored, so the selected agent
// can never be on a page that is not showing.
func (m Model) page() int {
	return m.cursor / m.perPage()
}

// mosaic renders the tiled grid of every agent on the current page.
func (m Model) mosaic() string {
	per, pages, height := m.mosaicPlan()
	start := m.page() * per
	visible := m.statuses[start:minInt(start+per, len(m.statuses))]

	var footer string
	if pages > 1 {
		footer = m.pagingLine(len(visible))
	}

	shape := planGrid(len(m.statuses), m.width, height)
	// The shape was planned for a full page; the last page may hold fewer, so it is replanned
	// for what it actually shows and stays a perfect tiling.
	if len(visible) < shape.tiles() {
		shape = planGrid(len(visible), m.width, height)
	}

	heights := divide(height, len(shape.rows))
	var b strings.Builder
	index := start
	for r, count := range shape.rows {
		widths := divide(m.width, count)
		panes := make([][]string, count)
		for c := 0; c < count; c++ {
			panes[c] = m.paneBox(m.statuses[index], widths[c], heights[r],
				index == m.cursor, index-start+1)
			index++
		}
		for line := 0; line < heights[r]; line++ {
			for c := 0; c < count; c++ {
				b.WriteString(lineAt(panes[c], line))
			}
			b.WriteString("\n")
		}
	}
	if footer != "" {
		b.WriteString(footer)
		return b.String()
	}
	return strings.TrimRight(b.String(), "\n")
}

// pagingLine says what the grid could not show, and how to reach it.
//
// A page indicator alone would say the truth quietly; this one names the count, because "3 more
// off screen" is the difference between paging out of curiosity and knowing an agent is waiting
// somewhere you cannot see it.
func (m Model) pagingLine(shown int) string {
	t := theme.Current()
	line := fmt.Sprintf("page %d/%d · %d more off screen",
		m.page()+1, m.pages(), len(m.statuses)-shown)
	return t.Muted.Render("  "+line+"  ") + t.Key.Render("[") + t.Muted.Render(" and ") +
		t.Key.Render("]") + t.Muted.Render(" to page")
}

// hero is one agent large across the top half, and the next few along the bottom.
//
// The top pane is the one you are reading; the bottom row is the corner of your eye. The slices
// are the agents after the selected one in order, wrapping, so tab walks the hero through the
// whole set and the row below always shows who is on deck.
func (m Model) hero() string {
	if len(m.statuses) < 2 || m.height < 2*minPaneHeight || m.width < minSliceWidth {
		return m.focus()
	}

	slices := minInt(len(m.statuses)-1, minInt(heroSlices, m.width/minSliceWidth))
	bottom := maxInt(minPaneHeight, m.height/3)
	top := m.height - bottom

	var b strings.Builder
	for _, line := range m.paneBox(m.statuses[m.cursor], m.width, top, true, 1) {
		b.WriteString(line)
		b.WriteString("\n")
	}

	widths := divide(m.width, slices)
	panes := make([][]string, slices)
	for i := 0; i < slices; i++ {
		panes[i] = m.paneBox(m.statuses[(m.cursor+1+i)%len(m.statuses)], widths[i], bottom,
			false, i+2)
	}
	for line := 0; line < bottom; line++ {
		for i := 0; i < slices; i++ {
			b.WriteString(lineAt(panes[i], line))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// focus is one agent in one pane, using everything the screen has.
func (m Model) focus() string {
	status, ok := m.Selected()
	if !ok {
		return m.empty()
	}
	return strings.Join(m.paneBox(status, m.width, m.height, true, 1), "\n")
}

// paneBox draws one agent as a bordered pane: name and state on the top edge, the tail of its
// conversation inside, and its own campfire on the bottom edge.
//
// The digit is the key that jumps here, drawn into the border so the shortcut is taught by the
// thing it points at. Every line is exactly width cells, or the grid built from these would tear.
func (m Model) paneBox(
	status session.AgentStatus, width, height int, focused bool, digit int,
) []string {
	t := theme.Current()

	border := t.Border
	if focused {
		// The selected pane's frame takes the brand blue, which is how you find your place on a
		// grid of eight without reading eight names.
		border = t.Info
	}

	inner := maxInt(width-2, 1)
	content := maxInt(width-4, 4)
	lines := make([]string, 0, height)

	lines = append(lines, m.paneTop(status, border, inner, focused, digit))

	body := m.paneBody(status, content, maxInt(height-2, 1), focused)
	working := status.State == core.AgentWorking
	if working && len(body) > 0 {
		// The tip of the flame dances on the last row of the pane, directly over the ember on the
		// border below it, exactly as it dances over the chat box. Only where that row has room:
		// the flame goes behind words, never over them.
		last := body[len(body)-1]
		if column := content - brand.EmberWidth; lipgloss.Width(last) < column {
			body[len(body)-1] = last + strings.Repeat(" ", column-lipgloss.Width(last)) +
				t.Flame.Render(brand.EmberTip(m.step))
		}
	}
	for _, line := range body {
		pad := ""
		if gap := content - lipgloss.Width(line); gap > 0 {
			pad = strings.Repeat(" ", gap)
		}
		lines = append(lines, border.Render("│")+" "+line+pad+" "+border.Render("│"))
	}

	lines = append(lines, m.paneBottom(status, border, inner))
	return lines
}

// paneTop is a pane's top border: the jump digit, the name, and the state, on the rule.
func (m Model) paneTop(
	status session.AgentStatus, border lipgloss.Style, inner int, focused bool, digit int,
) string {
	t := theme.Current()

	name := t.Body
	if focused {
		name = t.Selected
	}

	// The name gives way before the rule disappears, because a label flush against both corners
	// reads as a bug. State stays whole: it is the fact this screen exists for.
	state := paneState(status.State)
	fixed := 5 + 3 + lipgloss.Width(state) // corners, rule stubs, digit, separators
	title := truncate(status.Agent.Name, maxInt(inner-fixed, 4))

	label := " " + t.Key.Render(fmt.Sprint(digit)) + " " + name.Render(title) +
		t.Muted.Render(" · ") + state + " "
	rest := inner - lipgloss.Width(label) - 1
	if rest < 0 {
		rest = 0
	}
	return border.Render("╭─") + label + border.Render(strings.Repeat("─", rest)+"╮")
}

// paneBottom is a pane's bottom border, with the agent's own campfire riding it.
//
// Lit while this agent works and coals while it does not, judged from the pane's agent and never
// from the cursor: eight panes are eight fires, and each one answers for its own agent.
func (m Model) paneBottom(status session.AgentStatus, border lipgloss.Style, inner int) string {
	if inner < brand.EmberWidth+4 {
		return border.Render("╰" + strings.Repeat("─", inner) + "╯")
	}

	fire := emberOut()
	if status.State == core.AgentWorking {
		fire = emberLit()
	}
	rule := strings.Repeat("─", inner-brand.EmberWidth-2)
	return border.Render("╰"+rule) + " " + fire + " " + border.Render("╯")
}

// paneBody is what a pane shows: the agent's conversation, drawn by the same renderer the chat
// screen uses, so a pane is a split screen of the view you would get by opening the agent rather
// than a summary of it. What is blocked stays pinned above the transcript, and the tail wins the
// space, because what an agent is doing now is at the bottom of its conversation.
func (m Model) paneBody(status session.AgentStatus, width, height int, focused bool) []string {
	t := theme.Current()

	var top []string
	if status.State == core.AgentAwaitingPermission {
		top = m.panePrompt(status, width, focused)
	} else if status.Waiting != "" {
		top = append(top, t.Warning.Render(truncate("waiting: "+status.Waiting, width)))
	}

	var conversation []string
	if m.engine == nil {
		conversation = []string{t.Muted.Render("no engine")}
	} else if s, ok := m.engine.Session(status.Agent.SessionID); !ok || len(s.Turns) == 0 {
		conversation = []string{t.Muted.Render(m.summary(status)), "",
			t.Muted.Render("nothing said yet")}
	} else {
		// Only the turns whose tail can fit are rendered, because rendering a long transcript to
		// throw most of it away is markdown work done per pane per frame. The tool kinds are not
		// threaded through here, so a pane draws tool calls without their kind labels; the full
		// labels are one keystroke away in the view this pane is a miniature of.
		if keep := 3; len(s.Turns) > keep {
			s.Turns = s.Turns[len(s.Turns)-keep:]
		}
		conversation = chat.Transcript(s, width, spinnerFrames[m.step%len(spinnerFrames)], nil)
	}

	lines := append(top, conversation...)
	if len(lines) > height {
		keep := append([]string{}, lines[:len(top)]...)
		rest := lines[len(top):]
		lines = append(keep, rest[len(rest)-(height-len(top)):]...)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	// A transcript line can be wider than the pane when the renderer was given more room than a
	// word needed, so every line is cut to fit rather than trusted; a torn grid is worse than a
	// clipped word.
	for i, line := range lines {
		if lipgloss.Width(line) > width {
			lines[i] = truncate(line, width)
		}
	}
	return lines
}

// panePrompt is the small popup a waiting pane pins above its conversation: what the agent wants,
// and, on the selected pane, the keys that answer it from right here (D-50).
//
// The same heavy needs-you frame the chat's prompt and the direct-mode confirmation wear, because
// it is the same kind of moment at a smaller size. Every pane that waits shows its request, so the
// grid says who is stuck without being walked; only the selected pane names enter and backspace,
// because those keys answer for the selection and a grid of eight identical key hints would read
// as eight live buttons.
func (m Model) panePrompt(status session.AgentStatus, width int, focused bool) []string {
	t := theme.Current()

	// The frame spends six columns on itself: the shared indent and the walls with their padding.
	inner := maxInt(width-6, 8)

	body := []string{t.Warning.Render(truncate("waiting: "+status.Waiting, inner))}
	if focused {
		keys := t.Key.Render("enter") + t.Muted.Render(" approve once   ") +
			t.Key.Render("backspace") + t.Muted.Render(" decline")
		if lipgloss.Width(keys) > inner {
			// The short form for a narrow pane, chosen by measure rather than truncated, because
			// cutting styled text tears its escape codes before it tears the frame.
			keys = t.Key.Render("enter") + t.Muted.Render(" yes   ") +
				t.Key.Render("backspace") + t.Muted.Render(" no")
		}
		body = append(body, keys)
	}
	return needsYouPanel(body, inner)
}

// spinnerFrames matches the chat's working indicator, so a turn in flight looks the same at every
// size. It advances on the fire's own ticker here, which is slower and deliberately so: a pane is
// watched from a distance.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// paneState is the one word of an agent's state, coloured but never padded, for a border label.
func paneState(state core.AgentState) string {
	t := theme.Current()
	switch state {
	case core.AgentAwaitingPermission:
		return t.Warning.Render("needs you")
	case core.AgentFailed:
		return t.Danger.Render("failed")
	case core.AgentWorking:
		return t.Info.Render("working")
	case core.AgentStopped:
		return t.Muted.Render("stopped")
	default:
		return t.Muted.Render("idle")
	}
}

// emberLit is the fire burning: the same bed the chat box wears, its heart a step brighter than
// its ends. The split columns come from the brand package so the two cannot drift apart.
func emberLit() string {
	t := theme.Current()
	return emberHeart(brand.EmberBase, t.Flame, t.FlameCore)
}

// emberOut is the fire gone cold: grey at the edges, the last warmth in the middle, which is how
// a real fire dies, from the outside in.
func emberOut() string {
	t := theme.Current()
	return emberHeart(brand.EmberOut, t.SmokeFaint, t.Smoke)
}

// emberHeart styles the seven cell drawing with one style at its ends and another at its middle,
// the split coming from the brand package so the two cannot drift apart.
func emberHeart(drawing string, ends, middle lipgloss.Style) string {
	runes := []rune(drawing)
	from, to := brand.EmberCoreColumn, brand.EmberCoreColumn+brand.EmberCoreWidth
	return ends.Render(string(runes[:from])) +
		middle.Render(string(runes[from:to])) +
		ends.Render(string(runes[to:]))
}

// divide splits a span into parts that differ by at most one cell, earlier parts taking the
// remainder, so a row of panes always fills its row exactly.
func divide(total, parts int) []int {
	if parts < 1 {
		parts = 1
	}
	out := make([]int, parts)
	base, extra := total/parts, total%parts
	for i := range out {
		out[i] = base
		if i < extra {
			out[i]++
		}
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
