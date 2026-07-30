package keys

import (
	"fmt"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/catalog"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// View renders the credential screen.
//
// The rule this whole file is written under: the secret being typed is never printed. Its length
// is shown as dots so the user can see that typing is registering, which is the only feedback a
// masked field can honestly give.
// View renders the screen standalone, with its own chrome.
func (m Model) View() string {
	return styleTitle.Render("Credentials") +
		styleMuted.Render("   "+m.store.BackendName()) + "\n" +
		m.Body() + "\n\n" + styleMuted.Render("  "+m.Footer())
}

// Body is the screen's content, without chrome. The application frame supplies the rest.
func (m Model) Body() string {
	var b strings.Builder

	if m.store.UsingInsecureBackend() {
		b.WriteString(styleWarn.Render(
			"  credentials are stored unencrypted on disk, unset CANOPY_KEY_BACKEND for the keychain"))
		b.WriteString("\n")
	}
	b.WriteString("\n")

	switch m.mode {
	case modeList, modeConfirmRemove:
		b.WriteString(m.viewList())
	case modeModelPick:
		b.WriteString(m.viewModelPick())
	case modeRename:
		b.WriteString(m.viewRename())
	default:
		b.WriteString(m.viewAdd())
	}

	if m.err != nil {
		b.WriteString("\n\n")
		b.WriteString(styleErr.Render("  " + m.err.Error()))
	} else if m.status != "" {
		b.WriteString("\n\n")
		b.WriteString(styleOK.Render("  " + m.status))
	}

	return b.String()
}

// Footer is the key hint line for the current mode.
func (m Model) Footer() string { return m.footer() }

func (m Model) viewList() string {
	if len(m.keys) == 0 {
		return styleMuted.Render(
			"  No credentials yet.\n\n" +
				"  Canopy needs a provider API key before it can run an agent.\n" +
				"  Press a to add one, or run `canopy keys add claude` outside the interface.")
	}

	var b strings.Builder
	b.WriteString(styleMuted.Render(fmt.Sprintf("  %-2s %-14s %-18s %-34s %s",
		"", "NAME", "PROVIDER", "MODEL", "FINGERPRINT")))
	b.WriteString("\n")

	for i, key := range m.keys {
		marker := "  "

		// The model column exists because a credential with none, on a provider that has no default,
		// is one that cannot answer a single message. Blank here is the visible form of that, so it
		// can be noticed before a conversation fails rather than after.
		model := key.Model
		if model == "" && key.Ref.Provider == core.ProviderAnthropic {
			model = styleMuted.Render("provider default")
		} else if model == "" {
			model = styleWarn.Render("none set, press m")
		}

		// The chosen credential is marked rather than reordered, so the list somebody learned the
		// shape of does not rearrange itself under them.
		chosen := " "
		if key.Ref.Name == m.chosen {
			chosen = "*"
		}

		line := fmt.Sprintf("%-1s %-14s %-18s %-34s %s",
			chosen, key.Ref.Name, key.Ref.Provider, model, key.Fingerprint)
		if i == m.cursor {
			marker = "> "
			line = styleSelect.Render(line)
		}
		b.WriteString(marker + line + "\n")
	}

	if m.mode == modeConfirmRemove && m.cursor < len(m.keys) {
		b.WriteString("\n")
		b.WriteString(styleWarn.Render(fmt.Sprintf(
			"  Remove %q? Any profile using it will stop working. y/n",
			m.keys[m.cursor].Ref.Name)))
	}
	return b.String()
}

// viewModelPick is the list of models a credential can be pointed at.
//
// The current one is marked with a character rather than only with the selection colour, for the
// same reason the credential list marks the chosen key: on a terminal with no colour, or for
// somebody who cannot separate two of them, a highlight is not a mark at all. See D-10.
func (m Model) viewModelPick() string {
	var b strings.Builder
	b.WriteString(styleMuted.Render("  Which model should " + m.draftName + " talk to"))
	b.WriteString("\n\n")

	if len(m.modelChoices) == 0 {
		// The honest empty state for an endpoint nobody here has a lineup for. Said out loud,
		// because a list with one row in it otherwise reads as a program that has lost the rest.
		b.WriteString(styleWarn.Render(
			"  Canopy knows no models for this endpoint, so name the one this key should use."))
		b.WriteString("\n\n")
	}

	for i, choice := range m.modelChoices {
		marker := "      "
		label := choice.Label()
		if choice.Named() {
			label += "  " + choice.ID
		}

		current := " "
		if choice.ID == m.draftModel {
			current = "*"
		}
		line := current + " " + label
		if i == m.modelCursor {
			marker = "    > "
			line = styleSelect.Render(line)
		}
		b.WriteString(marker + line + "\n")
	}

	// Always last and always there. The day the list above is wrong is the day it would stand
	// between somebody and the one model they actually want.
	typeIt := "  something else, type it"
	if m.modelCursor >= len(m.modelChoices) {
		b.WriteString("    > " + styleSelect.Render(typeIt) + "\n")
	} else {
		b.WriteString("      " + styleMuted.Render(typeIt) + "\n")
	}

	if len(m.modelChoices) > 0 {
		b.WriteString("\n")
		b.WriteString(styleMuted.Render("  the list was last checked on " +
			catalog.AsOf.Format("2006-01-02") + ", and anything not on it can still be typed"))
		b.WriteString("\n")

		// In the warning style rather than the muted one once it has gone stale, because a list that
		// is out of date is the case where the row underneath, the one that takes anything typed,
		// is the row that matters.
		if note := catalog.StalenessNote(time.Now()); note != "" {
			b.WriteString(styleWarn.Render("  " + note))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// viewRename is the name field on a credential that already exists.
//
// Its own view rather than the add form with one field lit, because the add form is a wizard: it
// shows the fields ahead as well as the one being filled, and here there are none. What it shows
// instead is what is not changing, since the question somebody has before pressing enter on this is
// whether they are about to have to find their API key again.
func (m Model) viewRename() string {
	var b strings.Builder
	b.WriteString(styleMuted.Render("  Renaming " + m.renamingFrom))
	b.WriteString("\n\n")

	// The same masking the add form uses, for the same reason: a value that has stopped looking like
	// a name is most likely a credential pasted into the wrong field.
	shown := m.draftName
	if core.LooksLikeCredential(shown) {
		shown = strings.Repeat("*", len([]rune(shown)))
	}
	b.WriteString(m.field("name", shown, true))
	b.WriteString("\n")
	b.WriteString(m.field("provider", string(m.draftProvider), false))
	b.WriteString("\n")
	if m.draftBaseURL != "" {
		b.WriteString(m.field("base url", m.draftBaseURL, false))
		b.WriteString("\n")
	}
	if m.draftModel != "" {
		b.WriteString(m.field("model", m.draftModel, false))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styleMuted.Render(
		"  the value is not asked for again, and every conversation on this credential follows the\n" +
			"  new name"))
	b.WriteString("\n")
	return b.String()
}

func (m Model) viewAdd() string {
	var b strings.Builder
	header := "  Adding a credential"
	if m.editing {
		header = "  Changing which model " + m.draftName + " talks to"
	}
	b.WriteString(styleMuted.Render(header))
	b.WriteString("\n\n")

	// A name is not a secret, so it is shown as typed. Unless it stops looking like a name: the
	// moment the input resembles a credential it is masked, because the most likely explanation is
	// that somebody pasted a key into the wrong field, and it should not sit on screen while they
	// work that out.
	nameShown := m.draftName
	if core.LooksLikeCredential(nameShown) {
		nameShown = strings.Repeat("*", len([]rune(nameShown)))
	}
	b.WriteString(m.field("name", nameShown, m.mode == modeName))
	b.WriteString("\n")

	if m.mode == modeProvider {
		b.WriteString("  provider\n")
		for i, provider := range core.AllProviders() {
			marker := "      "
			label := string(provider)
			if i == m.providerCursor {
				marker = "    > "
				label = styleSelect.Render(label)
			}
			b.WriteString(marker + label + "\n")
		}
	} else if m.draftName != "" && m.mode != modeName {
		b.WriteString(m.field("provider", string(m.draftProvider), false))
		b.WriteString("\n")
	}

	if m.draftProvider.RequiresBaseURL() && (m.mode == modeBaseURL || m.draftBaseURL != "") {
		b.WriteString(m.field("base url", m.draftBaseURL, m.mode == modeBaseURL))
		b.WriteString("\n")
	}

	if m.mode == modeModel {
		b.WriteString(m.field("model", m.draftModel, true))
		b.WriteString("\n")
		b.WriteString(styleMuted.Render(
			"           the model id this key talks to, exactly as the provider spells it"))
		b.WriteString("\n")
	} else if m.draftModel != "" && m.mode == modeSecret {
		b.WriteString(m.field("model", m.draftModel, false))
		b.WriteString("\n")
	}

	if m.mode == modeSecret {
		// Only the length is rendered. The value is never turned into display text anywhere in
		// this package, which is why there is no code path here that could accidentally do it.
		b.WriteString(m.field("value", strings.Repeat("*", len([]rune(m.draftSecret))), true))
		b.WriteString("\n")
		b.WriteString(styleMuted.Render("           the value is not shown and is not stored in this screen"))
		b.WriteString("\n")
	}

	return b.String()
}

func (m Model) field(label, value string, active bool) string {
	cursor := ""
	if active {
		cursor = "_"
	}
	rendered := fmt.Sprintf("  %-10s %s%s", label, value, cursor)
	if active {
		return styleSelect.Render(rendered)
	}
	return styleMuted.Render(rendered)
}

func (m Model) footer() string {
	switch m.mode {
	case modeList:
		if len(m.keys) == 0 {
			// This is the screen a first run lands on, so the way out of the whole program is named
			// here too rather than discovered.
			return "a add   esc back   q quit"
		}
		return "enter use   m model   e rename   a add   d remove   j/k move   esc back"
	case modeConfirmRemove:
		// The confirmation line in the body already says y or n, beside the name of the thing being
		// removed. A footer repeating it is a second list to keep agreeing with the first.
		return ""
	case modeProvider, modeModelPick:
		return "j/k choose   enter select   esc cancel"
	case modeModel, modeRename:
		return "enter save   esc cancel"
	default:
		return "enter continue   esc cancel"
	}
}
