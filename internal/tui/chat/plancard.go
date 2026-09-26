package chat

// A plan written in plan mode is something to approve, not only to read. While the box is empty
// under a finished plan, a line says so, and enter carries it out in build mode: one decision about
// the whole plan in place of one per tool call, which is the review a person actually does properly.

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/theme"
)

// approvedPlan is what is sent when a plan is carried out. The same words the engine's own plan
// execution uses, so an approval means the same thing whichever way it was given.
const approvedPlan = `That plan is approved. Go ahead and carry it out.

If you find that the plan will not work, or that the real fix is somewhere you did not mention, ` +
	`stop and say so rather than carrying on. What was approved was the plan above, not whatever ` +
	`turns out to be necessary.`

// planReady reports whether the conversation is sitting on a finished plan with nothing typed and
// nobody else's question waiting on the same key.
func (m Model) planReady() bool {
	if m.Mode() != core.ModePlan || !m.input.Empty() || m.working || m.awaiting || len(m.visitors) > 0 {
		return false
	}
	session, ok := m.engine.Session(m.sessionID)
	if !ok || len(session.Turns) == 0 {
		return false
	}
	last := session.Turns[len(session.Turns)-1]
	return last.State == core.TurnComplete && looksLikeAPlan(last.Text)
}

// looksLikeAPlan is a reply that sets out steps: at least two lines of a numbered or bulleted list.
// An answer to a question asked in plan mode is not a plan, and approving one would spend money
// and change code on the strength of a stray key.
func looksLikeAPlan(text string) bool {
	steps := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			steps++
		case len(line) > 2 && line[0] >= '0' && line[0] <= '9' &&
			(strings.HasPrefix(line[1:], ". ") || strings.HasPrefix(line[1:], ") ") ||
				len(line) > 3 && line[1] >= '0' && line[1] <= '9' && strings.HasPrefix(line[2:], ". ")):
			steps++
		}
	}
	return steps >= 2
}

// planCard is the line offered under a finished plan.
func (m Model) planCard() []string {
	if !m.planReady() {
		return nil
	}
	t := theme.Current()
	if m.planAsked {
		return []string{t.Key.Render("  enter again") + t.Body.Render(" switches to build and carries this plan out") +
			t.Muted.Render("; any other key keeps planning")}
	}
	return []string{t.Key.Render("  enter twice") + t.Body.Render(" carries this plan out in build mode") +
		t.Muted.Render(", or type to change it")}
}

// carryOutPlan switches to build and sends the approval.
func (m Model) carryOutPlan() (Model, tea.Cmd) {
	build, _ := core.ModeByName(core.ModeBuild)
	if err := m.engine.ModeUnusable(m.sessionID, build); err != nil {
		m.err = "the plan cannot be carried out here: " + err.Error()
		return m, nil
	}
	m.setMode(core.ModeBuild)
	if m.err != "" {
		return m, nil
	}
	if _, err := m.engine.Send(m.sessionID, approvedPlan); err != nil {
		m.err = err.Error()
		return m, nil
	}
	m.notice = "carrying out the plan in build mode"
	m.scroll = 0
	m.refresh()
	return m, nil
}
