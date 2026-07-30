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
	case modeSignIn:
		b.WriteString(m.viewSignIn())
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
		identity := m.identityOf(key.Ref.Name)
		model := key.Model
		switch {
		case model != "":
		case identity.Kind == KindDelegated:
			// Not a gap to fill in. The vendor's own agent is running the turn and picks the model
			// inside it, so "none set, press m" would be telling somebody to fix something that is
			// not broken and cannot be changed from here.
			model = styleMuted.Render("the vendor chooses")
		case key.Ref.Provider == core.ProviderAnthropic:
			model = styleMuted.Render("provider default")
		default:
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

		// A signed-in credential has no fingerprint to show and two facts a pasted one does not: whose
		// account it is and when the grant stops working. They go on a line of their own rather than
		// into a sixth column, because at eighty columns a sixth column is a fifth column nobody can
		// read, and this line is only there for the rows that have something to put on it.
		if note := signInNote(identity); note != "" {
			b.WriteString("      " + note + "\n")
		}
	}

	if m.mode == modeConfirmRemove && m.cursor < len(m.keys) {
		b.WriteString("\n")
		b.WriteString(styleWarn.Render(fmt.Sprintf(
			"  Remove %q? Any profile using it will stop working. y/n",
			m.keys[m.cursor].Ref.Name)))
	}
	return b.String()
}

// signInNote is the second line of a signed-in credential's row, and empty for a pasted one.
//
// Absolute times rather than "in 58 minutes". A list is redrawn on every keystroke and a countdown
// in it is a number that moves while somebody reads it, which is worse than a timestamp they can
// compare against a clock. Lapsed says lapsed, in the warning colour and in the word as well, so the
// row still reads on a terminal with no colour in it. D-10.
func signInNote(identity Identity) string {
	if !identity.Kind.IsSignIn() {
		return ""
	}

	who := "signed in"
	if identity.Account != "" {
		who = "signed in as " + identity.Account
	}
	if identity.Kind == KindDelegated {
		// The fact that makes this route permitted at all, said on the row rather than only in
		// LIMITATIONS: Canopy is driving something the user signed in to and is holding nothing of
		// theirs. D-51.
		return styleMuted.Render(who + " through the vendor's own agent, which holds the credential")
	}

	if identity.ExpiresAt == nil {
		return styleMuted.Render(who + ", with no expiry given")
	}
	when := identity.ExpiresAt.Local().Format("2006-01-02 15:04")
	if identity.ExpiresAt.Before(time.Now()) {
		return styleWarn.Render(who + ", lapsed " + when + ", press a to sign in again")
	}
	return styleMuted.Render(who + ", expires " + when)
}

// viewSignIn is the step that asks for nothing.
//
// Everything here is text a person can read off a screen and type somewhere else, because a coding
// agent is routinely run over ssh on a machine with no browser at all. A flow that depended on one
// opening would work on a laptop and fail on exactly the machines this program is for.
func (m Model) viewSignIn() string {
	var b strings.Builder
	b.WriteString(styleMuted.Render("  Signing " + m.draftName + " in through " + m.draftRoute.Label))
	b.WriteString("\n\n")
	b.WriteString(styleOK.Render("  Nothing is typed here and nothing is pasted."))
	b.WriteString("\n\n")

	switch {
	case m.prompt.URL != "" || m.prompt.Code != "":
		if m.prompt.URL != "" {
			b.WriteString(m.field("open", m.prompt.URL, false))
			b.WriteString("\n")
		}
		if m.prompt.Code != "" {
			b.WriteString(m.field("code", m.prompt.Code, false))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(styleMuted.Render("  Waiting for " + m.draftRoute.Label + " to confirm."))
		b.WriteString("\n")

	case m.prompt.Doing != "":
		b.WriteString(styleMuted.Render("  " + m.prompt.Doing))
		b.WriteString("\n")

	case m.signingIn:
		// The gap between asking the vendor and the vendor answering. Short, usually, and a blank
		// screen during it is indistinguishable from a program that has stopped.
		b.WriteString(styleMuted.Render("  Asking " + m.draftRoute.Label + " what to do next."))
		b.WriteString("\n")
	}

	// Before anything is stored, which is the only moment it is worth reading. A caveat that arrives
	// after the credential exists is a caveat about a decision somebody has already made.
	if m.draftRoute.Caveat != "" {
		b.WriteString("\n")
		b.WriteString(styleWarn.Render("  " + m.draftRoute.Caveat))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styleMuted.Render("  esc stops this and stores nothing."))
	b.WriteString("\n")
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
		for i, row := range m.providerRows() {
			marker := "      "
			label := string(row.provider)
			if row.signIn {
				label = row.route.Label
			}
			if i == m.providerCursor {
				marker = "    > "
				label = styleSelect.Render(label)
			}
			b.WriteString(marker + label + "\n")

			// What the route needs to already be true, under the row rather than beside it. The
			// question somebody is answering here is which of these they already have, and the
			// answer does not fit on a line that also has to hold the vendor's name.
			if row.signIn && row.route.Detail != "" {
				b.WriteString("        " + styleMuted.Render(row.route.Detail) + "\n")
			}
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
	case modeProvider, modeModelPick:
		return "j/k choose   enter select   esc cancel"
	case modeSignIn:
		// One key, because there is one thing to do. A footer offering enter here would be offering
		// to hurry a vendor that is not listening.
		return "esc cancel"
	case modeModel:
		return "enter save   esc cancel"
	default:
		return "enter continue   esc cancel"
	}
}
