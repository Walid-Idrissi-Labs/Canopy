package chat

// The command palette, on ctrl+p: every command, mode, theme and file in one list that narrows as
// you type, for somebody who knows what they want and not where it lives.

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/config"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
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
)

type paletteItem struct {
	label, detail, text string
	action              paletteAction
}

type palette struct {
	open     bool
	query    string
	items    []paletteItem
	matches  []paletteItem
	selected int
	offset   int
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
	m.palette = palette{open: true, items: m.paletteItems()}
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
