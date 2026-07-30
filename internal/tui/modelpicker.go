package tui

// The model picker, and why it is a screen rather than an overlay.
//
// It was asked for as an overlay. This repository has no compositing: nothing draws over anything
// else, and inventing z-order for one feature would mean every screen afterwards having an opinion
// about what may sit on top of it. Help already answered the same question the same way, so this
// follows it: a full frame that arrives on a keystroke, leaves without a trace, and changes nothing
// on the way out unless enter was pressed.
//
// What it is for is the other half of D-46. A key holds many models; which one this conversation
// runs on is a fact about the conversation, so choosing here never rewrites the key's recorded
// default. That stays with the credential screen and the CLI, which is where somebody goes when
// they mean "from now on" rather than "for this".
//
// The list itself is assembled by internal/tui/keys, which owns the question "what can this
// credential be pointed at". This screen asks it once per key and arranges the answers.

import (
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	keysui "github.com/Walid-Idrissi-Labs/Canopy/internal/tui/keys"
)

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
}

// newModelPicker reads the credentials and works out what each one can be asked for.
//
// Built fresh every time it is opened rather than kept on the application, because a key added or a
// model recorded between one opening and the next has to be here, and a cached list is a list that
// is wrong exactly when somebody has just changed something.
func newModelPicker(store keysui.Store, currentKey, currentModel string) modelPicker {
	picker := modelPicker{currentKey: currentKey, currentModel: currentModel}

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

// Body draws the picker.
//
// The current model is marked with a glyph and the line above says what the mark means, so the
// screen reads with no colour at all. That is D-10, and it is why the selection is a character in
// the margin rather than a highlight: a highlight is an accelerant and never the fact itself.
func (p modelPicker) Body() string {
	if p.problem != "" {
		return styleCaveat.Render("  the credentials could not be read: " + p.problem)
	}
	if len(p.sections) == 0 {
		return styleMuted.Render("  No credentials yet, so there is nothing to choose between.\n\n" +
			"  Press ctrl+k to add one.")
	}

	var b strings.Builder
	b.WriteString(styleMuted.Render(
		"  * is what this conversation runs on now, and enter moves it, for this conversation only."))
	b.WriteString("\n")

	row := 0
	for _, section := range p.sections {
		b.WriteString("\n")
		b.WriteString(styleHeader.Render("  " + section.title()))
		b.WriteString("\n")

		if section.offered == 0 {
			// The same words the credential screen uses for the same state, so the two screens agree
			// about what an empty list means rather than each inventing its own phrasing. The row
			// underneath still takes a model, which is what keeps an unknown endpoint usable.
			b.WriteString(styleCaveat.Render("      none set, press ctrl+k"))
			b.WriteString("\n")
		}

		for _, entry := range section.rows {
			selected := row == p.cursor

			if entry.typed && selected && p.typing {
				b.WriteString("    > " + styleSelected.Render("  "+p.draft+"_") + "\n")
				row++
				continue
			}

			mark := " "
			if entry.key == p.currentKey && entry.id == p.currentModel {
				mark = "*"
			}

			line := mark + " " + entry.label
			if selected {
				b.WriteString("    > " + styleSelected.Render(line))
			} else if entry.typed {
				b.WriteString("      " + styleMuted.Render(line))
			} else {
				b.WriteString("      " + line)
			}
			// The id after the name and dimmed, for the entries where the two differ. It has to be
			// on screen, since it is what goes on the wire and what somebody types into the CLI,
			// and it must not compete with the name for the eye scanning the column.
			if entry.id != "" && entry.id != entry.label {
				b.WriteString("  " + styleMuted.Render(entry.id))
			}
			b.WriteString("\n")
			row++
		}
	}

	b.WriteString("\n")
	b.WriteString(styleMuted.Render(
		"  a credential's own default is changed on the credential screen, ctrl+k"))
	return b.String()
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
