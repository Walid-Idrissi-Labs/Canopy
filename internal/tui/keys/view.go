package keys

import (
	"fmt"
	"strings"

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
		return "enter use   m model   a add   d remove   j/k move   esc back"
	case modeConfirmRemove:
		// The confirmation line in the body already says y or n, beside the name of the thing being
		// removed. A footer repeating it is a second list to keep agreeing with the first.
		return ""
	case modeProvider:
		return "j/k choose   enter select   esc cancel"
	case modeModel:
		return "enter save   esc cancel"
	default:
		return "enter continue   esc cancel"
	}
}
