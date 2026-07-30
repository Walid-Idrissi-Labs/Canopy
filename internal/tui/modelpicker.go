package tui

// The model picker, and why it stands where the message box stands.
//
// It was a whole screen. Opening it took the conversation away and gave back a frame with a list in
// it, which is the wrong shape for the question being asked: which model answers next is a fact
// about the conversation, and choosing it while unable to see the conversation is choosing in the
// dark. The screen was written that way because this repository has no compositing, nothing draws
// over anything else, and inventing z-order for one feature would leave every screen afterwards with
// an opinion about what may sit on top of it.
//
// That argument still holds and this is still not an overlay. The chat screen already gives rows
// away to blocks that are not the conversation: the task pane, the btw panel, another agent's
// question. The picker is one more of those with one difference, which is that it stands in the
// message box's place rather than above it, because while it is up there is nothing to type into.
// Nothing is drawn over anything, the transcript is exactly where it was, and the frame goes on
// doing the arithmetic.
//
// The list is bounded and scrolls, which the full screen version never had to be. That is the cost
// of the change and it is the right one to pay: eight models under one credential is an ordinary
// setup, and a block that grew to fit them would push the conversation it exists to be read against
// off the top of the screen.
//
// What it is for is the other half of D-46. A key holds many models; which one this conversation
// runs on is a fact about the conversation, so choosing here never rewrites the key's recorded
// default. That stays with the credential screen and the CLI, which is where somebody goes when
// they mean "from now on" rather than "for this". The block's own label says as much.
//
// The list itself is assembled by internal/tui/keys, which owns the question "what can this
// credential be pointed at". This screen asks it once per key and arranges the answers.

import (
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

// pickerRows is the most list lines the block draws at once.
//
// Ten, which covers a credential's whole lineup and the row underneath that takes a typed model,
// and stops well short of the block becoming the screen. A smaller terminal gets fewer, because the
// conversation behind it is the thing this is being read against and a block that leaves none of it
// visible has defeated its own point. See pickerHeight.
const pickerRows = 10

// modelRow is one line of the picker: a model, and the credential it would run on.
//
// typed marks the row that takes a model from the keyboard instead of naming one. Every other model
// surface has that escape and this one did not, which made the picker the one place where a list
// Canopy ships could stand between somebody and the model they actually wanted. See D-46 rule 1.
type modelRow struct {
	key   string
	id    string
	label string
	typed bool
}

// modelSection is one credential and everything it can run.
//
// A key with nothing to offer keeps its section and says so, rather than disappearing. A screen that
// silently omits a credential somebody added is a screen that looks like it lost it, and the reason
// the section is empty is exactly the thing they need to read.
type modelSection struct {
	key      string
	provider core.Provider
	host     string
	rows     []modelRow

	// offered is how many of the rows are models rather than the row that takes typing, so a section
	// with nothing to offer can still say so while still being typed into.
	offered int
}

type modelPicker struct {
	sections []modelSection

	// rows is the sections flattened, which is what up and down actually move through. Held rather
	// than recomputed so the cursor cannot drift out of step with what is drawn.
	rows   []modelRow
	cursor int

	// currentKey and currentModel are what the conversation runs on now: marked in the list, and
	// where the cursor opens.
	currentKey   string
	currentModel string

	// typing is whether the keyboard belongs to the free text field, and draft is what has been
	// typed into it. Entered from the row that offers it and left with esc, which is the credential
	// screen's arrangement for the same job.
	typing bool
	draft  string

	problem string

	// top is the first drawn line of the list and visible is how many of them there is room for.
	//
	// Held in lines rather than in rows because the headings are drawn lines too, and a window
	// measured in models would scroll a section's title off while leaving its models under it. The
	// count comes from the application, which is the only thing that knows how tall the terminal is.
	top     int
	visible int
}

// newModelPicker reads the credentials and works out what each one can be asked for.
//
// Built fresh every time it is opened rather than kept on the application, because a key added or a
// model recorded between one opening and the next has to be here, and a cached list is a list that
// is wrong exactly when somebody has just changed something.
func newModelPicker(store keysui.Store, currentKey, currentModel string) modelPicker {
	picker := modelPicker{currentKey: currentKey, currentModel: currentModel, visible: pickerRows}

	stored, err := store.List()
	if err != nil {
		picker.problem = err.Error()
		return picker
	}

	for _, meta := range stored {
		section := modelSection{
			key:      meta.Ref.Name,
			provider: meta.Ref.Provider,
			host:     hostOfURL(meta.BaseURL),
		}

		offered, err := keysui.Offered(store, meta)
		if err != nil {
			picker.problem = err.Error()
		}
		for _, model := range offered {
			section.rows = append(section.rows,
				modelRow{key: meta.Ref.Name, id: model.ID, label: model.Label()})
		}

		// The model this conversation is on, when no list has heard of it. Somebody who typed an id
		// by hand must be able to see where they are and get back to it, and without this the mark
		// that says "you are here" would be missing from the whole screen.
		if meta.Ref.Name == currentKey && currentModel != "" && !offers(section.rows, currentModel) {
			section.rows = append(section.rows, modelRow{key: currentKey, id: currentModel, label: currentModel})
		}

		// Last in every section, so whatever the lists do not know is still one row away from the
		// model they do. Per section rather than one at the end, because a typed model has to belong
		// to a credential and the section it sits under is what says which.
		section.offered = len(section.rows)
		section.rows = append(section.rows, modelRow{
			key: meta.Ref.Name, label: "something else, type it", typed: true,
		})

		picker.sections = append(picker.sections, section)
		picker.rows = append(picker.rows, section.rows...)
	}

	for i, row := range picker.rows {
		if row.key == currentKey && row.id == currentModel {
			picker.cursor = i
			break
		}
	}
	picker.reveal()
	return picker
}

func offers(rows []modelRow, id string) bool {
	for _, row := range rows {
		if row.id == id {
			return true
		}
	}
	return false
}

// pickerHeight is how many list lines a terminal of this size can spare.
//
// Half the body at most, and never more than pickerRows. The half is the part worth stating: the
// whole argument for the picker standing in the box's place rather than replacing the screen is that
// the conversation stays readable behind it, and a block allowed to take four fifths of a short
// terminal would have taken the screen by another route.
func pickerHeight(d Dimensions) int {
	// Three of those rows are not list: the block's two borders, and the line that says which way
	// the rest of it is. That line is only drawn while there is a rest, and the row is reserved
	// either way, because a budget that depended on the scroll position would make the conversation
	// behind the block jump by a line as somebody moved through it.
	const chrome = 3

	rows := d.BodyHeight()/2 - chrome
	if rows > pickerRows {
		rows = pickerRows
	}
	if rows < 3 {
		// Three is the floor: a heading, the row the cursor is on, and one more to show that the list
		// goes on. Below that the block says nothing a footer hint could not, and the terminal is
		// already at the size where the frame refuses to draw.
		rows = 3
	}
	return rows
}

// fit tells the picker how many lines it has, and keeps the cursor visible in the new size.
func (p *modelPicker) fit(rows int) {
	p.visible = rows
	p.reveal()
}

// move walks the flattened list, which crosses section boundaries without stopping at them.
//
// Sections are a way of reading the list, not a thing to navigate between. Making somebody press a
// different key to reach the next credential would be two movement models for one column of rows.
func (p *modelPicker) move(by int) {
	if len(p.rows) == 0 {
		return
	}
	p.cursor += by
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(p.rows) {
		p.cursor = len(p.rows) - 1
	}
	p.reveal()
}

// reveal scrolls the window the least distance that puts the cursor back inside it.
//
// The least distance rather than re-centring, because a list that jumps under the cursor on every
// keypress is a list nobody can keep their place in. Walking down moves the window one line at the
// bottom edge and not at all before it.
func (p *modelPicker) reveal() {
	lines, at := p.laid(0)

	visible := p.visible
	if visible < 1 {
		visible = 1
	}
	if len(lines) <= visible {
		p.top = 0
		return
	}
	if at < p.top {
		p.top = at
	}
	if at >= p.top+visible {
		p.top = at - visible + 1
	}
	if p.top > len(lines)-visible {
		p.top = len(lines) - visible
	}
	if p.top < 0 {
		p.top = 0
	}
}

// Chosen is the row the cursor is on, and whether there is one.
func (p modelPicker) Chosen() (modelRow, bool) {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return modelRow{}, false
	}
	return p.rows[p.cursor], true
}

// startTyping hands the keyboard to the field, and reports whether the cursor was on the row that
// offers it.
func (p *modelPicker) startTyping() bool {
	row, ok := p.Chosen()
	if !ok || !row.typed {
		return false
	}
	p.typing = true
	p.draft = ""
	return true
}

// typeKey edits the field, and returns the row to apply once there is something to apply.
//
// Esc cancels the typing rather than leaving the screen, which is the more local meaning and the
// rule the command menu and the btw panel already follow: the way out of the picker is still one
// more press away.
func (p *modelPicker) typeKey(msg tea.KeyMsg) (modelRow, bool) {
	switch msg.Type {
	case tea.KeyEnter:
		id := strings.TrimSpace(p.draft)
		if id == "" {
			// Nothing typed is nothing chosen. Applying an empty model would leave the conversation
			// pointed at nothing and fail on its next message, at the far end.
			return modelRow{}, false
		}
		row, _ := p.Chosen()
		p.typing, p.draft = false, ""
		return modelRow{key: row.key, id: id, label: id}, true

	case tea.KeyEsc:
		p.typing, p.draft = false, ""

	case tea.KeyBackspace:
		if runes := []rune(p.draft); len(runes) > 0 {
			p.draft = string(runes[:len(runes)-1])
		}

	case tea.KeyRunes:
		p.draft += string(msg.Runes)

	case tea.KeySpace:
		p.draft += " "
	}
	return modelRow{}, false
}

// Block draws the picker as the block that stands where the message box goes.
//
// The current model is marked with a glyph and the cursor is a character in the margin, so the list
// reads with no colour at all. That is D-10, and it is why neither is a highlight: a highlight is an
// accelerant and never the fact itself. What the mark means is said in the footer rather than in a
// line of the block, because a line of the block is a line of somebody's conversation.
func (p modelPicker) Block(width int) []string {
	inner := chat.BlockWidth(width)

	// Named for what a choice made here is worth. Which model this conversation runs on and what the
	// credential defaults to are different facts, and the label is the cheapest place to say which of
	// the two is being changed.
	const label = "model, for this conversation"

	if p.problem != "" {
		return chat.Block(label, []string{
			styleCaveat.Render(clip("the credentials could not be read: "+p.problem, inner)),
		}, width)
	}
	if len(p.sections) == 0 {
		return chat.Block(label, []string{
			styleMuted.Render(clip("no credentials yet, so there is nothing to choose between", inner)),
			styleMuted.Render(clip("press ctrl+k to add one", inner)),
		}, width)
	}

	lines, _ := p.laid(inner)

	visible := p.visible
	if visible < 1 {
		visible = 1
	}
	start := p.top
	if start > len(lines)-visible {
		start = len(lines) - visible
	}
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > len(lines) {
		end = len(lines)
	}

	shown := append([]string(nil), lines[start:end]...)
	// How much list is out of sight, said on the block's own last line rather than left to be
	// discovered by pressing down. A list that silently continues past its edge is a list people stop
	// at the first screen of.
	if above, below := start, len(lines)-end; above > 0 || below > 0 {
		shown = append(shown, styleMuted.Render(clip(more(above, below), inner)))
	}
	return chat.Block(label, shown, width)
}

// more says which way the list goes on.
func more(above, below int) string {
	switch {
	case above > 0 && below > 0:
		return "↑ " + itoa(above) + " more   ↓ " + itoa(below) + " more"
	case above > 0:
		return "↑ " + itoa(above) + " more"
	default:
		return "↓ " + itoa(below) + " more"
	}
}

// laid is every line of the list as it will be drawn, and which of them the cursor is on.
//
// One function producing both, because the window that scrolls and the lines that are drawn have to
// be counting the same things. When they were counted separately the headings belonged to one and
// not the other, and the cursor could sit a section's worth of lines away from where the scroll
// thought it was.
//
// An inner width of zero asks for the structure rather than the drawing, which is what the scrolling
// wants: it needs to know how many lines there are and where the cursor landed, and nothing about
// how wide they are.
func (p modelPicker) laid(inner int) ([]string, int) {
	var lines []string
	at := 0

	// The id column starts past the longest name, so the ids read as a column rather than as ragged
	// text after the names. Bounded, so one very long name cannot push every id off the right hand
	// edge of the block.
	names := 0
	for _, row := range p.rows {
		if row.id == "" || row.id == row.label {
			continue
		}
		if w := lipgloss.Width(row.label); w > names {
			names = w
		}
	}
	if limit := inner / 2; names > limit {
		names = limit
	}

	row := 0
	for _, section := range p.sections {
		lines = append(lines, styleHeader.Render(clip(section.title(), inner)))

		if section.offered == 0 {
			// The same words the credential screen uses for the same state, so the two screens agree
			// about what an empty list means rather than each inventing its own phrasing. The row
			// underneath still takes a model, which is what keeps an unknown endpoint usable.
			lines = append(lines, styleCaveat.Render(clip("    none set, press ctrl+k", inner)))
		}

		for _, entry := range section.rows {
			selected := row == p.cursor
			if selected {
				at = len(lines)
			}

			if entry.typed && selected && p.typing {
				lines = append(lines, "> "+styleSelected.Render(clip(p.draft+"_", inner-2)))
				row++
				continue
			}

			prefix := "  "
			if selected {
				prefix = "> "
			}
			lines = append(lines, prefix+p.line(entry, selected, names, inner-2))
			row++
		}
	}
	return lines, at
}

// line is one model as it is drawn: the mark, the name, and the id in a column after it.
//
// The id has to be on screen, since it is what goes on the wire and what somebody types into the
// CLI, and it must not compete with the name for the eye scanning the column, so it is dimmed. It is
// the first thing dropped when the block is too narrow to carry both, because a name with no id is
// still a model somebody can recognise and an id with no name is not.
func (p modelPicker) line(entry modelRow, selected bool, names, width int) string {
	body := func(text string) string {
		switch {
		case selected:
			return styleSelected.Render(text)
		case entry.typed:
			return styleMuted.Render(text)
		default:
			return text
		}
	}

	head := p.mark(entry) + " " + entry.label
	if entry.id == "" || entry.id == entry.label {
		return body(clip(head, width))
	}

	padded := head
	if pad := names - lipgloss.Width(entry.label); pad > 0 {
		padded += strings.Repeat(" ", pad)
	}
	if lipgloss.Width(padded)+2+lipgloss.Width(entry.id) > width {
		return body(clip(head, width))
	}
	return body(padded) + "  " + styleMuted.Render(entry.id)
}

// mark is the glyph that says a row is what the conversation runs on now.
func (p modelPicker) mark(row modelRow) string {
	if row.key == p.currentKey && row.id == p.currentModel {
		return "*"
	}
	return " "
}

// clip cuts a line to the width it has, with an ellipsis where something was lost.
//
// Applied to plain text before any styling, which is the only order that works: measuring a string
// with escape sequences in it charges the line for colours nobody can see, and cutting one in the
// middle of an escape sequence leaves the rest of the terminal wearing it.
func clip(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

// title is the section heading: the credential, and enough about it to tell two apart.
func (s modelSection) title() string {
	title := s.key + " (" + string(s.provider) + ")"
	if s.host != "" {
		// The endpoint for the openai-compatible family, because the provider word alone says
		// nothing about which gateway is being paid.
		title += "  " + s.host
	}
	return title
}

func hostOfURL(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}
