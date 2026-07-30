package tui

// The bar at the top of every screen.
//
// It replaced a title and a rule, and the reason is that the title and the rule were the only things
// that stayed on screen once a conversation started, so everything a person might want at a glance
// had to be asked for somewhere else. What model is this, which mode am I in, how much has this cost,
// how full is the context: all of it was either in a footer that changes, on a screen you have to
// leave the conversation to reach, or nowhere.
//
// Its height is not fixed and it is not free to choose either: it draws exactly what
// Dimensions.HeaderHeight declares, because BodyHeight is computed from the same number. A header
// that drew one line more than it declared would push every screen's footer off the bottom at once,
// which is why the height lives in layout.go with the arithmetic that depends on it rather than here
// with the drawing.
//
// Three lines ordinarily, which is what the title, the rule and the blank line under them used to
// cost together, and five where there is room for the drawn name in the corner.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/brand"
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

	// Agent is whose conversation is on screen, and it takes the brand's place in the corner when
	// it is set.
	//
	// The brand does not vanish, it stops squatting on the one line that could say where you are.
	// "canopy" was on every screen at once, which is a fact nobody needs twice a second, while the
	// question somebody with six agents running actually has is which one they are talking to.
	// Empty on the screens that are nobody's conversation, and they keep the name.
	Agent string

	// Attention is how many conversations are waiting on a person anywhere in the project.
	//
	// On every screen, never dropped, and next to the name rather than among the details, because
	// the details are what a narrow terminal throws away and this is the one fact whose whole value
	// is that it reaches somebody who is looking at something else. An indicator that lived only on
	// the screen listing agents would be a smoke alarm installed inside the fire.
	//
	// Zero draws nothing at all. A count that is always there is chrome, and chrome is what people
	// stop seeing.
	Attention int

	// Parts are the details, most important first.
	Parts []string

	// Mode is the trust level this conversation is running at, empty on screens that have none.
	Mode string

	// Wordmark asks for the drawn name in the corner.
	//
	// False on a conversation that has not started yet, because the opening screen draws the name in
	// the middle of the screen at four times this size. Two copies of it at once is one too many, and
	// the one in the corner is the one that is redundant there.
	Wordmark bool
}

// title is what the header writes beside the mark: who you are with, or the brand.
//
// Bounded, because nothing stops an agent being called something long and this name sits on a row
// whose whole purpose is to carry facts to the right of it. A name that pushed the screen and the
// mode off the row would have made the header worse at the exact moment it had most to say.
func (s Status) title(width int) string {
	if s.Agent == "" {
		return "canopy"
	}
	return shorten(s.Agent, titleBudget(width))
}

// attentionLabel is what the count says, before anything is done to it.
//
// A word and a glyph, and never colour alone. The whole thing has to read correctly with colour
// disabled, for the same reason the agent list spells out "needs you" beside its badge: a coloured
// mark is meaningless under NO_COLOR, in a monochrome palette, and in a pasted bug report.
//
// The exclamation mark rather than a drawn symbol, because this appears on every screen and a
// header that opens with a missing glyph box on somebody's terminal is worse than one with no
// picture in it, which is brand.go's argument about the mark and is right here too.
func attentionLabel(n int) string {
	if n == 1 {
		return " ! 1 needs you "
	}
	return fmt.Sprintf(" ! %d need you ", n)
}

// attentionChip is that label, drawn to be found from across a room.
//
// Reverse video, which is what the permission panel uses and for the same reason: it is the one
// emphasis that survives NO_COLOR, a monochrome theme and a terminal palette that renders the
// warning colour dull. Somebody glancing at a screen they left should find this before anything
// else on it.
func attentionChip(n int) string {
	return theme.Current().Warning.Reverse(true).Bold(true).Render(attentionLabel(n))
}

// titleBudget is how many cells the name may take.
//
// A quarter of the row, floored so a very narrow terminal still shows something recognisable and
// capped so a wide one does not hand a quarter of a large screen to one word. At eighty columns
// this is nineteen cells, which is more than any name anybody types and less than the row.
func titleBudget(width int) int {
	budget := width / 4
	if budget < 8 {
		budget = 8
	}
	if budget > 24 {
		budget = 24
	}
	return budget
}

// shorten cuts a name to a number of cells, saying that it was cut.
//
// Measured in cells rather than runes, because a name can hold anything a person can type and a
// double width character counts twice on the row while counting once in a slice.
func shorten(name string, cells int) string {
	if lipgloss.Width(name) <= cells {
		return name
	}
	const cut = "..."
	runes := []rune(name)
	for len(runes) > 0 && lipgloss.Width(string(runes)+cut) > cells {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + cut
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

// Header draws the top bar at exactly the height Dimensions.HeaderHeight declares.
//
// The two have to agree or every screen's footer moves, so the height is asked for rather than
// decided here.
func Header(d Dimensions, s Status) string {
	width := minInt(maxInt(d.Width, minWidth), 200)

	// Two for the walls and one of padding inside each of them.
	inner := width - 4
	if inner < 1 {
		inner = 1
	}

	if d.HeaderHeight() >= 5 {
		return tallHeader(inner, s)
	}
	return shortHeader(inner, s)
}

// shortHeader puts everything on one row, for a terminal with no room for the drawn name.
func shortHeader(inner int, s Status) string {
	t := theme.Current()

	// Measured in display cells rather than bytes throughout. The separator, the badge and the
	// context meter are all multi byte, so len would over count every one of them and the header
	// would drop facts it had room for.
	name := s.title(inner)
	left := t.Logo.Render(badge) + " " + t.Title.Render(name)
	used := lipgloss.Width(badge) + 1 + lipgloss.Width(name)

	// Immediately after the name, ahead of the screen and the mode. On the row that says where you
	// are, who is stuck waiting for you comes before either of them.
	if label := attentionLabel(s.Attention); s.Attention > 0 && used+1+lipgloss.Width(label) <= inner {
		left += " " + attentionChip(s.Attention)
		used += 1 + lipgloss.Width(label)
	}

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

// tallHeader is the three row version, with the drawn name in the corner.
//
// The name goes top right and the facts go left, which is the arrangement asked for and is also the
// one that survives a resize: the name is a fixed width block, so giving it the right hand edge means
// the side that has to shrink is the side made of text that can be dropped.
//
// Three rows of facts rather than one is the reason this is worth two extra lines of chrome. The one
// row version drops most of what it is given on an eighty column terminal; here the same details fit
// with room over, so the branch, the model, the spend and the context meter are all visible at once
// rather than in rotation as the window changes size.
func tallHeader(inner int, s Status) string {
	t := theme.Current()

	var drawn []string
	markWidth := 0
	if s.Wordmark {
		drawn, markWidth = brand.Wordmark(brand.WordmarkWidth), brand.WordmarkWidth
	}
	if inner < markWidth+headerGap+minimumFactsWidth {
		// No room for both, and the facts win. A header that is mostly logo is an advertisement.
		drawn, markWidth = nil, 0
	}

	left := inner
	if markWidth > 0 {
		left = inner - markWidth - headerGap
	}

	rows := headerFacts(s, left)

	var b strings.Builder
	rule := strings.Repeat("─", inner+2)
	b.WriteString(t.Border.Render("╭" + rule + "╮"))

	for i := 0; i < 3; i++ {
		b.WriteString("\n")
		b.WriteString(t.Border.Render("│") + " ")

		row := rows[i]
		b.WriteString(row)
		if pad := left - lipgloss.Width(ansi.Strip(row)); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}

		if markWidth > 0 {
			b.WriteString(strings.Repeat(" ", headerGap))
			mark := ""
			if i < len(drawn) {
				mark = drawn[i]
			}
			b.WriteString(t.Logo.Render(mark))
			if pad := markWidth - lipgloss.Width(mark); pad > 0 {
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		b.WriteString(" " + t.Border.Render("│"))
	}

	b.WriteString("\n")
	b.WriteString(t.Border.Render("╰" + rule + "╯"))
	return b.String()
}

// headerGap is the air between the facts and the drawn name.
const headerGap = 3

// minimumFactsWidth is the narrowest the left hand side may be squeezed before the name is dropped.
//
// Enough for the program's own name, the screen and the mode, which are the three things the short
// header refuses to drop either.
const minimumFactsWidth = 26

// headerFacts lays the status out over three rows, most important first.
//
// Rows rather than one line, so a detail that does not fit moves down instead of disappearing. Only
// what does not fit on any of the three is dropped, which on an ordinary terminal is nothing.
func headerFacts(s Status, width int) [3]string {
	t := theme.Current()

	var rows [3]string
	name := s.title(width)
	rows[0] = t.Logo.Render(badge) + " " + t.Title.Render(name)
	used := lipgloss.Width(badge) + 1 + lipgloss.Width(name)

	// Ahead of the screen and the mode here too, so the two headers put it in the same place and
	// somebody resizing a window does not have to look for it again.
	if label := attentionLabel(s.Attention); s.Attention > 0 && used+1+lipgloss.Width(label) <= width {
		rows[0] += " " + attentionChip(s.Attention)
		used += 1 + lipgloss.Width(label)
	}

	if s.Screen != "" && used+2+lipgloss.Width(s.Screen) <= width {
		rows[0] += "  " + t.Heading.Render(s.Screen)
		used += 2 + lipgloss.Width(s.Screen)
	}
	// Checked rather than assumed. A long screen name next to a long mode overflows the narrowest
	// left hand side this draws, and an overflowing row pushes the drawn name out of alignment on
	// the row below it, which looks like the frame is broken rather than like a name that did not
	// fit.
	if s.Mode != "" && used+2+lipgloss.Width(s.Mode) <= width {
		rows[0] += "  " + modeStyle(s.Mode).Render(s.Mode)
	}

	// Filled from row two onwards, wrapping to row three when row two is full.
	row, spent := 1, 0
	for _, part := range s.Parts {
		if part == "" {
			continue
		}
		cost := lipgloss.Width(part)
		if spent > 0 {
			cost += lipgloss.Width(separator)
		}
		if spent+cost > width {
			if row == 2 {
				break
			}
			row, spent = 2, 0
			cost = lipgloss.Width(part)
		}
		if spent > 0 {
			rows[row] += t.Muted.Render(separator)
		}
		rows[row] += t.Muted.Render(part)
		spent += cost
	}
	return rows
}
