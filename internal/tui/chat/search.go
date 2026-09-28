package chat

// Finding something in the conversation, on ctrl+f: a query typed above the box, the view moved to
// each place it occurs, newest first, and the words themselves marked the way a selection is.

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

type hit struct{ line, col, length int }

type search struct {
	open  bool
	query string
	hits  []hit
	at    int
}

// openSearch starts a search with nothing typed.
func (m *Model) openSearch() {
	m.search = search{open: true}
	m.menu = menu{}
}

// findAll is every place the query occurs in the transcript, top to bottom, ignoring case.
func (m Model) findAll(query string) []hit {
	if query == "" {
		return nil
	}
	// Compared rune for rune without changing case first, since lowering can change how many runes
	// a string has, and the columns have to be the screen's.
	needle := []rune(query)
	var hits []hit
	for i, line := range m.transcript() {
		hay := []rune(ansi.Strip(line))
		for col := 0; col+len(needle) <= len(hay); col++ {
			if strings.EqualFold(string(hay[col:col+len(needle)]), query) {
				hits = append(hits, hit{i, col, len(needle)})
				col += len(needle) - 1
			}
		}
	}
	return hits
}

// refreshSearch finds the query again and shows the newest place it occurs.
func (m *Model) refreshSearch() {
	m.search.hits = m.findAll(m.search.query)
	m.search.at = len(m.search.hits) - 1
	m.showHit()
}

// showHit scrolls so the current hit is on screen, a little above the middle, and marks it.
func (m *Model) showHit() {
	if m.search.at < 0 || m.search.at >= len(m.search.hits) {
		m.sel = selection{}
		return
	}
	h := m.search.hits[m.search.at]
	total := len(m.transcript())
	end := h.line + m.transcriptHeight()/2 + 1
	m.scroll = max(0, min(total, total-end))
	m.sel = selection{active: true, anchor: point{line: h.line, col: h.col},
		head: point{line: h.line, col: h.col + h.length - 1}}
}

// searchKey handles a key while the search is open.
func (m Model) searchKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+f":
		// The view stays where the search took it, which is usually why somebody searched.
		m.search = search{}
		m.sel = selection{}
	case "enter", "up":
		if len(m.search.hits) > 0 {
			m.search.at = (m.search.at - 1 + len(m.search.hits)) % len(m.search.hits)
			m.showHit()
		}
	case "down":
		if len(m.search.hits) > 0 {
			m.search.at = (m.search.at + 1) % len(m.search.hits)
			m.showHit()
		}
	case "backspace":
		if runes := []rune(m.search.query); len(runes) > 0 {
			m.search.query = string(runes[:len(runes)-1])
			m.refreshSearch()
		}
	case "space":
		m.search.query += " "
		m.refreshSearch()
	default:
		if msg.Text != "" && msg.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			m.search.query += msg.Text
			m.refreshSearch()
		}
	}
	return m, nil
}

func (s search) height() int {
	if !s.open {
		return 0
	}
	return 1
}

// line is the search bar: the query, where it is among the matches, and the keys.
func (s search) line() []string {
	if !s.open {
		return nil
	}
	t := theme.Current()
	where := "no match"
	switch {
	case s.query == "":
		where = "type to find"
	case len(s.hits) > 0:
		where = itoa(s.at+1) + " of " + itoa(len(s.hits))
	}
	return []string{t.Key.Render("  find ") + t.Body.Render(s.query) + t.Cursor.Render(" ") +
		t.Muted.Render("  "+where+", enter older, down newer, esc closes")}
}

// lastReplyToCopy is what ctrl+y copies: the last code block of the latest reply that has any text,
// or the whole reply when it has no code, and which of the two it is.
func (m Model) lastReplyToCopy() (string, string) {
	session, ok := m.engine.Session(m.sessionID)
	if !ok {
		return "", ""
	}
	for i := len(session.Turns) - 1; i >= 0; i-- {
		text := strings.TrimSpace(session.Turns[i].Text)
		if text == "" {
			continue
		}
		if block := lastCodeBlock(text); block != "" {
			return block, "the last code block"
		}
		return text, "the last reply"
	}
	return "", ""
}

// lastCodeBlock is the body of the last fenced block in text, or "".
func lastCodeBlock(text string) string {
	var block []string
	var last string
	inside := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inside {
				last = strings.Join(block, "\n")
			}
			inside, block = !inside, nil
			continue
		}
		if inside {
			block = append(block, line)
		}
	}
	return last
}

// copyReply puts the latest reply's last code block, or the reply, on the clipboard.
func (m Model) copyReply() (Model, tea.Cmd) {
	text, what := m.lastReplyToCopy()
	if text == "" {
		m.notice = "there is no reply to copy yet"
		return m, nil
	}
	if err := m.clip(text); err != nil {
		m.err = "could not copy: " + err.Error()
		return m, nil
	}
	m.notice = "copied " + what
	return m, nil
}
