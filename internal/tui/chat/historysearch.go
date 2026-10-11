package chat

// Searching what you have sent, on ctrl+r, the way a shell does: type part of an earlier message,
// ctrl+r again for an older one that has it, enter to put it in the box to edit or send, esc to
// leave the box as it was, since nothing in it changes until enter. It reads only what this
// conversation already holds, so it costs nothing.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

type historySearch struct {
	open  bool
	query string
	// at is the history index of the message shown, -1 when nothing matches.
	at int
}

// openHistorySearch starts a search from the newest message, with nothing typed.
func (m *Model) openHistorySearch() {
	m.sent = historySearch{open: true, at: -1}
	m.menu = menu{}
	m.sent.at = m.findSent(m.sent.query, len(m.input.History())-1)
}

// findSent is the newest sent message at or before index that holds query, ignoring case, or -1.
func (m Model) findSent(query string, from int) int {
	sent := m.input.History()
	query = strings.ToLower(query)
	for i := min(from, len(sent)-1); i >= 0; i-- {
		if strings.Contains(strings.ToLower(sent[i]), query) {
			return i
		}
	}
	return -1
}

// historyKey handles a key while the search is up: it takes every key, as the find bar does.
func (m Model) historyKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+g", "ctrl+c":
		m.sent = historySearch{}
	case "ctrl+r", "up":
		// Older: the next match before the one shown, staying put when there is none.
		if older := m.findSent(m.sent.query, m.sent.at-1); older >= 0 && m.sent.at >= 0 {
			m.sent.at = older
		}
	case "enter", "tab":
		if m.sent.at >= 0 {
			m.input.SetValue(m.input.History()[m.sent.at])
		}
		m.sent = historySearch{}
	case "backspace":
		if runes := []rune(m.sent.query); len(runes) > 0 {
			m.sent.query = string(runes[:len(runes)-1])
			m.sent.at = m.findSent(m.sent.query, len(m.input.History())-1)
		}
	case "space":
		m.sent.query += " "
		m.sent.at = m.findSent(m.sent.query, len(m.input.History())-1)
	default:
		if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			m.sent.query += msg.Text
			m.sent.at = m.findSent(m.sent.query, len(m.input.History())-1)
		}
	}
	return m, nil
}

// historyLine is the search, drawn above the box: what is typed and the message it finds.
func (m Model) historyLine() []string {
	if !m.sent.open {
		return nil
	}
	t := theme.Current()
	sent := m.input.History()
	head := t.Key.Render("  history ") + t.Body.Render(m.sent.query) + t.Muted.Render("▏")
	switch {
	case len(sent) == 0:
		return []string{head + t.Muted.Render("  nothing has been sent here yet; esc closes")}
	case m.sent.at < 0:
		return []string{head + t.Warning.Render("  no earlier message has that") + t.Muted.Render("; esc closes")}
	}
	found := strings.ReplaceAll(sent[m.sent.at], "\n", " ")
	room := max(10, m.width-lipgloss.Width(head)-36)
	if runes := []rune(found); len(runes) > room {
		found = string(runes[:room-1]) + "…"
	}
	return []string{head + t.Muted.Render("  ") + t.Body.Render(found) +
		t.Muted.Render("  ctrl+r older, enter edits it, esc closes")}
}
