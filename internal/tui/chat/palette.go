package chat

// The command palette, on ctrl+p: every command, mode, theme and file in one list that narrows as
// you type, for somebody who knows what they want and not where it lives.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/session"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// paletteVisible is how many entries are on screen at once.
const paletteVisible = 8

// paletteAction is what choosing an entry does.
type paletteAction int

const (
	// paletteRun runs the text as if it had been typed and sent: a built-in, which costs nothing.
	paletteRun paletteAction = iota
	// paletteFill puts the text in the box to be finished and sent: a command of the project's
	// own, which sends a prompt, so it is never sent without a person pressing enter.
	paletteFill
	// paletteMention adds the text to what is in the box.
	paletteMention
	// paletteGo opens another conversation, an agent's or one from before.
	paletteGo
)

type paletteItem struct {
	label, detail, text string
	action              paletteAction
	// to is where paletteGo goes.
	to Destination
}

// Destination is somewhere the palette can take a person: an agent, or a conversation.
type Destination struct {
	// Label is what the entry says, Detail what it says beside it.
	Label, Detail string
	// SessionID and AgentName are what opening it asks the application for; see SwitchMsg.
	SessionID, AgentName string
}

// History is the engine's record of conversations: the palette lists this project's from it and
// finds them by what was said in them.
type History interface {
	Sessions() []core.Session
	InThisProject(sessionID string) bool
	SearchHistory(query string, limit int) []session.SearchHit
}

// SetHistory attaches the conversations the palette can reopen; nil lists none.
func (m *Model) SetHistory(history History) { m.history = history }

// conversations are this project's conversations with something in them, the most recently active
// first, leaving out the one on screen and any already listed.
func (m Model) conversations(listed map[string]bool) []paletteItem {
	if m.history == nil {
		return nil
	}
	sessions := m.history.Sessions()
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt) })
	var items []paletteItem
	for _, s := range sessions {
		if listed[s.ID] || s.ID == m.sessionID || len(s.Turns) == 0 || !m.history.InThisProject(s.ID) {
			continue
		}
		to := Destination{Label: "conversation " + conversationTitle(s.Title, s.ID), Detail: conversationDetail(s),
			SessionID: s.ID}
		items = append(items, paletteItem{label: to.Label, detail: to.Detail, action: paletteGo, to: to})
	}
	return items
}

// findSaid searches what was said in this project's other conversations.
func (m Model) findSaid() func(query string) []paletteItem {
	history, current := m.history, m.sessionID
	if history == nil {
		return nil
	}
	return func(query string) []paletteItem {
		var items []paletteItem
		seen := map[string]bool{current: true}
		for _, hit := range history.SearchHistory(query, 40) {
			if seen[hit.SessionID] || !history.InThisProject(hit.SessionID) {
				continue
			}
			seen[hit.SessionID] = true
			excerpt := strings.NewReplacer("<<", "", ">>", "", "\n", " ").Replace(hit.Excerpt)
			to := Destination{Label: "said in " + conversationTitle(hit.SessionTitle, hit.SessionID),
				Detail: excerpt, SessionID: hit.SessionID}
			items = append(items, paletteItem{label: to.Label, detail: to.Detail, action: paletteGo, to: to})
		}
		return items
	}
}

// conversationTitle is what a conversation is called in a list.
func conversationTitle(title, id string) string {
	if title != "" {
		return title
	}
	return "code " + session.Code(id)
}

// conversationDetail is what a list says beside a conversation: its code for `canopy pickup`, how
// long it is, what it cost, when it was last active, and where it was forked from.
func conversationDetail(s core.Session) string {
	parts := []string{"code " + session.Code(s.ID), strconv.Itoa(len(s.Turns)) + " turns"}
	if usage := s.Usage(); usage.CostKnown && usage.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", usage.CostUSD))
	}
	parts = append(parts, s.UpdatedAt.Local().Format("Jan 2 15:04"))
	if s.ForkedFrom != "" {
		parts = append(parts, "forked from "+session.Code(s.ForkedFrom))
	}
	return strings.Join(parts, " · ")
}

// SetDestinations gives the palette the agents and conversations it can open, read each time it
// opens so it lists who is there now. Nil lists none.
func (m *Model) SetDestinations(list func() []Destination) { m.destinations = list }

type palette struct {
	// find searches conversations by what was said in them; see SetHistory. generation counts the
	// queries, so an answer to one typed over is dropped.
	find       func(query string) []paletteItem
	generation int
	open       bool
	query      string
	items      []paletteItem
	matches    []paletteItem
	selected   int
	offset     int
}

// paletteItems is everything the palette offers, in the order an empty query shows it.
func (m Model) paletteItems() []paletteItem {
	var items []paletteItem
	for _, builtin := range config.Builtins() {
		items = append(items, paletteItem{label: "/" + builtin.Name, detail: builtin.Description,
			text: "/" + builtin.Name, action: paletteRun})
	}
	for _, command := range m.commands.All() {
		items = append(items, paletteItem{label: "/" + command.Name, detail: command.Description,
			text: "/" + command.Name + " ", action: paletteFill})
	}
	for _, mode := range core.Modes() {
		items = append(items, paletteItem{label: "mode " + mode.Name, detail: mode.Description,
			text: "/mode " + mode.Name, action: paletteRun})
	}
	for _, name := range theme.Names() {
		items = append(items, paletteItem{label: "theme " + name, detail: "switch the palette",
			text: "/theme " + name, action: paletteRun})
	}
	listed := map[string]bool{}
	if m.destinations != nil {
		for _, to := range m.destinations() {
			listed[to.SessionID] = true
			if to.SessionID == m.sessionID {
				continue
			}
			items = append(items, paletteItem{label: to.Label, detail: to.Detail, action: paletteGo, to: to})
		}
	}
	items = append(items, m.conversations(listed)...)
	if m.files != nil {
		for _, path := range m.files() {
			items = append(items, paletteItem{label: "@" + path, detail: "mention this file",
				text: "@" + path + " ", action: paletteMention})
		}
	}
	return items
}

// openPalette shows the palette with nothing typed.
func (m *Model) openPalette() {
	m.palette = palette{open: true, items: m.paletteItems(), find: m.findSaid()}
	m.menu = menu{}
	m.palette.refresh()
}

// refresh narrows the entries to the query: the query in the label, then its letters in order, and
// shorter labels first within each, so "thn" finds "theme nord" without typing it out.
func (p *palette) refresh() {
	query := strings.ToLower(p.query)
	type ranked struct {
		item paletteItem
		rank int
	}
	var found []ranked
	for i, item := range p.items {
		label := strings.ToLower(item.label)
		switch {
		case query == "":
			found = append(found, ranked{item, i})
		case strings.HasPrefix(label, query), strings.HasPrefix(strings.TrimLeft(label, "/@"), query):
			found = append(found, ranked{item, 0})
		case strings.Contains(label, query):
			found = append(found, ranked{item, 1})
		case inOrder(label, query):
			found = append(found, ranked{item, 2})
		}
	}
	if query != "" {
		sort.SliceStable(found, func(i, j int) bool {
			if found[i].rank != found[j].rank {
				return found[i].rank < found[j].rank
			}
			return len(found[i].item.label) < len(found[j].item.label)
		})
	}
	p.matches = p.matches[:0]
	for _, f := range found {
		p.matches = append(p.matches, f.item)
	}
	// What was said is searched separately, off the update loop; see searchSaid.
	p.generation++
	p.selected, p.offset = 0, 0
}

func (p *palette) move(by int) {
	if len(p.matches) == 0 {
		return
	}
	p.selected = max(0, min(len(p.matches)-1, p.selected+by))
	if p.selected < p.offset {
		p.offset = p.selected
	}
	if p.selected >= p.offset+paletteVisible {
		p.offset = p.selected - paletteVisible + 1
	}
}

func (p palette) height() int {
	if !p.open {
		return 0
	}
	return len(p.lines(80))
}

// lines draws the query and the entries around the selection.
func (p palette) lines(width int) []string {
	if !p.open {
		return nil
	}
	t := theme.Current()
	out := []string{t.Key.Render("  ctrl+p ") + t.Body.Render(p.query) + t.Cursor.Render(" ") +
		t.Muted.Render("  enter runs, esc closes")}
	if len(p.matches) == 0 {
		return append(out, t.Muted.Render("  nothing matches"))
	}
	end := min(len(p.matches), p.offset+paletteVisible)
	for i := p.offset; i < end; i++ {
		item := p.matches[i]
		marker, style := "  ", t.Body
		if i == p.selected {
			marker, style = t.Key.Render("> "), t.Selected
		}
		line := marker + style.Render(item.label) + t.Muted.Render("  "+item.detail)
		if lipgloss.Width(line) > width {
			room := width - lipgloss.Width(marker+item.label) - 4
			line = marker + style.Render(item.label)
			if room >= 4 {
				line += t.Muted.Render("  " + truncate(item.detail, room))
			}
		}
		out = append(out, line)
	}
	if hidden := len(p.matches) - (end - p.offset); hidden > 0 {
		out = append(out, t.Muted.Render("  "+itoa(hidden)+" more, up and down to move"))
	}
	return out
}

// paletteSearchDelay is how long typing pauses before what was said is searched, so a word typed a
// letter at a time is one search rather than one per letter.
const paletteSearchDelay = 150 * time.Millisecond

// paletteSearchMsg asks for the search once typing has paused; paletteFoundMsg brings its answer.
type paletteSearchMsg struct{ generation int }

type paletteFoundMsg struct {
	generation int
	items      []paletteItem
}

// searchSaid starts the wait before a search of what was said, for a query of three letters or more.
func (m Model) searchSaid() tea.Cmd {
	if !m.palette.open || m.palette.find == nil || len([]rune(strings.TrimSpace(m.palette.query))) < 3 {
		return nil
	}
	generation := m.palette.generation
	return tea.Tick(paletteSearchDelay, func(time.Time) tea.Msg { return paletteSearchMsg{generation: generation} })
}

// paletteSearch runs the search, off the update loop, if nothing has been typed since it was asked.
func (m Model) paletteSearch(msg paletteSearchMsg) tea.Cmd {
	if !m.palette.open || msg.generation != m.palette.generation || m.palette.find == nil {
		return nil
	}
	find, query, generation := m.palette.find, strings.TrimSpace(m.palette.query), m.palette.generation
	return func() tea.Msg { return paletteFoundMsg{generation: generation, items: find(query)} }
}

// paletteFound adds what the search found after everything found by name, if it is still wanted.
func (m Model) paletteFound(msg paletteFoundMsg) Model {
	if !m.palette.open || msg.generation != m.palette.generation {
		return m
	}
	m.palette.matches = append(m.palette.matches, msg.items...)
	return m
}
