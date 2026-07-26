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
func (m Model) View() string {
	var b strings.Builder

	b.WriteString(styleTitle.Render("Credentials"))
	b.WriteString(styleMuted.Render("   " + m.store.BackendName()))
	b.WriteString("\n")

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

	b.WriteString("\n\n")
	b.WriteString(styleMuted.Render("  " + m.footer()))
	return b.String()
}

func (m Model) viewList() string {
	if len(m.keys) == 0 {
		return styleMuted.Render(
			"  No credentials yet.\n\n" +
				"  Canopy needs a provider API key before it can run an agent.\n" +
				"  Press a to add one, or run `canopy keys add claude` outside the interface.")
	}

	var b strings.Builder
	b.WriteString(styleMuted.Render(fmt.Sprintf("  %-16s %-20s %-14s %s", "NAME", "PROVIDER", "FINGERPRINT", "ADDED")))
	b.WriteString("\n")

	for i, key := range m.keys {
		marker := "  "
		line := fmt.Sprintf("%-16s %-20s %-14s %s",
			key.Ref.Name, key.Ref.Provider, key.Fingerprint, key.CreatedAt.Format("2006-01-02"))
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
	b.WriteString(styleMuted.Render("  Adding a credential"))
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
			return "a add   esc back"
		}
		return "a add   d remove   j/k move   r refresh   esc back"
	case modeConfirmRemove:
		return "y confirm   n cancel"
	case modeProvider:
		return "j/k choose   enter select   esc cancel"
	default:
		return "enter continue   esc cancel"
	}
}
