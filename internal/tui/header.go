package tui

// The bar at the top of every screen.
//
// It replaced a title and a rule, and the reason is that the title and the rule were the only things
// that stayed on screen once a conversation started, so everything a person might want at a glance
// had to be asked for somewhere else. What model is this, which mode am I in, how much has this cost,
// how full is the context: all of it was either in a footer that changes, on a screen you have to
// leave the conversation to reach, or nowhere.
//
// It is exactly three lines, which is what the title, the rule and the blank line under them used to
// cost together. That is not a coincidence and it is load bearing: Dimensions.BodyHeight subtracts a
// fixed amount of chrome, and a header that grew by a line would push the footer off the bottom of
// every screen at once.

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// badge is the mark at one cell.
//
// brand.go argues at length that a plain triangle reads as a mountain rather than as a tent, and it
// is right about the large mark, where there is room for a doorway to make the difference. There is
// no room here. At one cell every candidate is a triangle or a box, and the honest options are this
// or nothing.
//
// It is drawn from the same block characters as the mark itself rather than from the geometric or
// dingbat ranges, for the reason brand.go gives: this appears on every screen, and a header that
// opens with a missing glyph box on somebody's terminal is worse than one with no picture in it.
const badge = "▲"

// separator divides one fact from the next.
//
// A middle dot rather than a pipe or two spaces. Two spaces stop reading as a division once the line
// is full, and a pipe competes with the box it is sitting inside.
const separator = " · "

// Status is what the header says about where you are and what is happening.
//
// Parts are ordered by what somebody wants first, because they are dropped from the right when the
// terminal is narrow, exactly as footer hints are. The screen name and the mode are never dropped:
// one says where you are and the other says what an agent is allowed to do without asking, and
// losing either to a narrow window would be losing the two facts most worth having.
type Status struct {
	// Screen is where you are: chat, agents, review, credentials.
	Screen string

	// Parts are the details, most important first.
	Parts []string

	// Mode is the trust level this conversation is running at, empty on screens that have none.
	Mode string
}

// modeStyle colours a mode by how much it is allowed to do without asking.
//
// The colour is the point rather than decoration. A mode is a permission level the engine enforces,
// and the two that can act without a person in the loop are the two worth noticing from across a
// desk. Somebody who left an agent in cruise and walked away should be able to tell from the shape
// of the screen rather than by reading a word.
func modeStyle(mode string) lipgloss.Style {
	t := theme.Current()
	switch mode {
	case core.ModeCruise:
		return t.Danger
	case core.ModeRunway:
		return t.Warning
	case core.ModePlan:
		return t.Info
	default:
		return t.Success
	}
}

// Header draws the top bar, always exactly three lines.
func Header(d Dimensions, s Status) string {
	t := theme.Current()

	width := minInt(maxInt(d.Width, minWidth), 200)

	// Two for the walls and one of padding inside each of them.
	inner := width - 4
	if inner < 1 {
		inner = 1
	}

	// Measured in display cells rather than bytes throughout. The separator, the badge and the
	// context meter are all multi byte, so len would over count every one of them and the header
	// would drop facts it had room for.
	left := t.Logo.Render(badge) + " " + t.Title.Render("canopy")
	used := lipgloss.Width(badge) + 1 + len("canopy")

	if s.Screen != "" {
		left += "  " + t.Heading.Render(s.Screen)
		used += 2 + lipgloss.Width(s.Screen)
	}
	if s.Mode != "" {
		left += "  " + modeStyle(s.Mode).Render(s.Mode)
		used += 2 + lipgloss.Width(s.Mode)
	}

	// Details, dropped from the right rather than wrapped. A header that wraps is a header that is
	// four lines tall on somebody's terminal and three on everybody else's, which breaks the footer
	// for exactly the people with the narrowest screens.
	var details []string
	spent := 0
	for _, part := range s.Parts {
		if part == "" {
			continue
		}
		cost := lipgloss.Width(part)
		if len(details) > 0 {
			cost += lipgloss.Width(separator)
		}
		// Two columns of gap between the name and the details, so they do not run together.
		if used+2+spent+cost > inner {
			break
		}
		details = append(details, part)
		spent += cost
	}

	line := left
	if len(details) > 0 {
		line += "  " + t.Muted.Render(strings.Join(details, separator))
		used += 2 + spent
	}
	if pad := inner - used; pad > 0 {
		line += strings.Repeat(" ", pad)
	}

	rule := strings.Repeat("─", inner+2)
	return t.Border.Render("╭"+rule+"╮") + "\n" +
		t.Border.Render("│") + " " + line + " " + t.Border.Render("│") + "\n" +
		t.Border.Render("╰"+rule+"╯")
}
