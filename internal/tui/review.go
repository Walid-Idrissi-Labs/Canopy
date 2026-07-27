package tui

// The review screen: which agent won, what is worth looking at next, and what it changed.
//
// Three questions that are really one workflow, so they are one screen with three panes rather than
// three screens somebody has to remember the keys for. With six agents running the question is
// never "what is agent four doing", it is "which of these should I read now", and answering that
// requires the ranking, the queue and the diff in the same place.
//
// Everything shown here is derived on each render from the verifier. Nothing is cached in the
// model. That is what makes an agent leave the queue the instant its result goes stale: there is no
// membership to invalidate, because there is no membership.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// ReviewSource is what the review screen reads. Implemented by verify.Verifier.
type ReviewSource interface {
	Rank() core.Ranking
	ReadyToReview() []core.ReadyForReview
	Changes(agent string) ([]core.FileChange, error)
	Patch(agent, path string) (string, error)
}

type reviewPane int

const (
	// paneQueue leads, because "which of these should I look at next" is the question somebody
	// arrives with. The ranking answers a narrower one, and only when several agents were given the
	// same task.
	paneQueue reviewPane = iota
	paneRanking
	paneFiles
	panePatch
)

// ReviewModel is the review screen.
type ReviewModel struct {
	source ReviewSource

	pane     reviewPane
	cursor   int
	agent    string
	files    []core.FileChange
	file     string
	patch    []string
	offset   int
	failure  string
	width    int
	height   int
	unranked bool
}

// NewReview builds the review screen. A nil source is allowed and renders an explanation, because
// Canopy runs in directories that are not repositories and the screen still has to say something.
func NewReview(source ReviewSource) ReviewModel {
	return ReviewModel{source: source, width: 80, height: 20}
}

func (m *ReviewModel) SetSize(width, height int) {
	m.width, m.height = width, height
}

// Pane reports which pane is in front. For tests.
func (m ReviewModel) Pane() string {
	switch m.pane {
	case paneRanking:
		return "ranking"
	case paneFiles:
		return "files"
	case panePatch:
		return "patch"
	default:
		return "queue"
	}
}

// Agent is the agent whose changes are open, if any. For tests.
func (m ReviewModel) Agent() string { return m.agent }

func (m ReviewModel) Update(msg tea.Msg) (ReviewModel, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "tab":
		// Only between the two list panes. Tab out of a diff would lose your place in it for a
		// keystroke people press by habit.
		if m.pane == paneQueue {
			m.pane, m.cursor = paneRanking, 0
		} else if m.pane == paneRanking {
			m.pane, m.cursor = paneQueue, 0
		}
		return m, nil

	case "esc", "backspace":
		switch m.pane {
		case panePatch:
			m.pane, m.patch, m.offset = paneFiles, nil, 0
		case paneFiles:
			m.pane, m.agent, m.files = paneQueue, "", nil
		}
		return m, nil

	case "j", "down":
		m.move(1)
		return m, nil
	case "k", "up":
		m.move(-1)
		return m, nil

	case "pgdown", " ":
		if m.pane == panePatch {
			m.offset += m.bodyHeight()
			m.clampOffset()
		}
		return m, nil
	case "pgup":
		if m.pane == panePatch {
			m.offset -= m.bodyHeight()
			m.clampOffset()
		}
		return m, nil
	case "g":
		if m.pane == panePatch {
			m.offset = 0
		}
		return m, nil
	case "G":
		if m.pane == panePatch {
			m.offset = len(m.patch)
			m.clampOffset()
		}
		return m, nil

	case "enter":
		return m.open(), nil
	}
	return m, nil
}

func (m *ReviewModel) move(delta int) {
	if m.pane == panePatch {
		m.offset += delta
		m.clampOffset()
		return
	}

	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if limit := m.rows() - 1; m.cursor > limit {
		m.cursor = limit
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *ReviewModel) clampOffset() {
	last := len(m.patch) - m.bodyHeight()
	if m.offset > last {
		m.offset = last
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m ReviewModel) bodyHeight() int {
	// Two lines are spent on the file header and the blank line under it.
	height := m.height - 2
	if height < 1 {
		return 1
	}
	return height
}

// rows is how many entries the current list pane has.
func (m ReviewModel) rows() int {
	if m.source == nil {
		return 0
	}
	switch m.pane {
	case paneQueue:
		return len(m.source.ReadyToReview())
	case paneRanking:
		ranking := m.source.Rank()
		return len(ranking.Ranked) + len(ranking.Unranked)
	case paneFiles:
		return len(m.files)
	default:
		return 0
	}
}

// open descends one level: an agent into its files, a file into its patch.
func (m ReviewModel) open() ReviewModel {
	if m.source == nil {
		return m
	}
	m.failure = ""

	switch m.pane {
	case paneQueue:
		queue := m.source.ReadyToReview()
		if m.cursor >= len(queue) {
			return m
		}
		return m.openAgent(queue[m.cursor].Agent)

	case paneRanking:
		all := m.source.Rank().All()
		if m.cursor >= len(all) {
			return m
		}
		// An unranked agent opens too. Refusing to place it is a statement about its evidence, not
		// about its code, and reading the diff is often exactly how somebody decides whether to
		// bother re-running the tests.
		return m.openAgent(all[m.cursor].Agent)

	case paneFiles:
		if m.cursor >= len(m.files) {
			return m
		}
		file := m.files[m.cursor]
		patch, err := m.source.Patch(m.agent, file.Path)
		if err != nil {
			m.failure = err.Error()
			return m
		}
		m.file, m.patch, m.offset, m.pane = file.Path, strings.Split(patch, "\n"), 0, panePatch
		return m
	}
	return m
}

func (m ReviewModel) openAgent(agent string) ReviewModel {
	files, err := m.source.Changes(agent)
	if err != nil {
		m.failure = err.Error()
		return m
	}
	m.agent, m.files, m.cursor, m.pane = agent, files, 0, paneFiles
	return m
}

// Context is the one line describing what is on screen.
func (m ReviewModel) Context() string {
	switch m.pane {
	case paneRanking:
		return "ranking"
	case paneFiles:
		return "changes by " + m.agent
	case panePatch:
		return m.agent + " " + m.file
	default:
		return "ready to review"
	}
}

// Body renders the screen.
func (m ReviewModel) Body() string {
	if m.source == nil {
		return styleMuted.Render("this directory is not a git repository, so there is nothing to review")
	}

	var lines []string
	switch m.pane {
	case paneQueue:
		lines = m.queueLines()
	case paneRanking:
		lines = m.rankingLines()
	case paneFiles:
		lines = m.fileLines()
	case panePatch:
		lines = m.patchLines()
	}

	if m.failure != "" {
		lines = append(lines, "", styleCaveat.Render(m.failure))
	}
	return strings.Join(lines, "\n")
}

func (m ReviewModel) queueLines() []string {
	queue := m.source.ReadyToReview()
	if len(queue) == 0 {
		return []string{
			styleMuted.Render("nothing is ready to review."),
			"",
			styleMuted.Render("an agent appears here once its tests pass for the code that is in its"),
			styleMuted.Render("worktree right now and it has something to show for it."),
		}
	}

	lines := []string{styleHeader.Render("easiest review first")}
	for i, ready := range queue {
		marker := "  "
		name := ready.Agent
		if i == m.cursor {
			marker, name = "> ", styleSelected.Render(ready.Agent)
		}

		// The size goes on the name line and the reason underneath, not the other way round. Both
		// used to be one sentence, and the size was the part that got truncated on a narrow
		// terminal, which is precisely the part somebody is choosing between agents on.
		lines = append(lines,
			fmt.Sprintf("%s%s %s %s", marker, testStatus(core.TestPassing).render(), name,
				styleMuted.Render(ready.Diff.Summary())),
			"    "+styleReason.Render(truncate(ready.Why, m.width-4)))
	}
	return lines
}

func (m ReviewModel) rankingLines() []string {
	all := m.source.Rank().All()
	if len(all) == 0 {
		return []string{styleMuted.Render("no agents are being verified")}
	}

	lines := []string{styleHeader.Render("ranked on evidence, then on how much there is to read")}
	for i, placement := range all {
		marker := "  "
		name := placement.Agent
		if i == m.cursor {
			marker, name = "> ", styleSelected.Render(placement.Agent)
		}

		// An unranked agent gets a dash where its position would be, rather than being pushed to the
		// bottom of a numbered list where it reads as having come last.
		position := "  -"
		if placement.Rank > 0 {
			position = fmt.Sprintf("%3d", placement.Rank)
		}
		lines = append(lines,
			fmt.Sprintf("%s%s %s %s %s", marker, styleMuted.Render(position),
				testStatus(placement.Tests).render(), name, styleMuted.Render(placement.Diff.Summary())),
			"      "+styleReason.Render(truncate(placement.Reason, m.width-6)))
	}
	return lines
}

func (m ReviewModel) fileLines() []string {
	if len(m.files) == 0 {
		return []string{styleMuted.Render(m.agent + " has not changed anything")}
	}

	lines := []string{styleHeader.Render(fmt.Sprintf("%d changed", len(m.files)))}
	for i, file := range m.files {
		marker := "  "
		name := file.Path
		if i == m.cursor {
			marker, name = "> ", styleSelected.Render(file.Path)
		}
		if file.Old != "" {
			name += styleMuted.Render(" was " + file.Old)
		}

		size := fmt.Sprintf("+%d -%d", file.Insertions, file.Deletions)
		if file.Binary {
			// Not "+0 -0", which would read as an empty change rather than an unmeasurable one.
			size = "binary"
		}
		lines = append(lines, fmt.Sprintf("%s%s %s %s",
			marker, styleMuted.Render(string(file.Status)), name, styleMuted.Render(size)))
	}
	return lines
}

// patchLines renders the visible window of a diff and nothing else.
//
// Windowed rather than rendered whole because a two thousand line diff styled in full on every
// keystroke is a screen that stutters when you hold down a movement key, which is the acceptance
// criterion for A7-01 stated the other way round.
func (m ReviewModel) patchLines() []string {
	if len(m.patch) == 0 {
		return []string{styleMuted.Render("no diff for this file")}
	}

	height := m.bodyHeight()
	end := min(m.offset+height, len(m.patch))

	position := fmt.Sprintf("%d-%d of %d", m.offset+1, end, len(m.patch))
	lines := []string{styleHeader.Render(m.file + "  " + position), ""}

	language := languageOf(m.file)
	for _, line := range m.patch[m.offset:end] {
		lines = append(lines, renderDiffLine(line, language, m.width))
	}
	return lines
}

// renderDiffLine colours one line of a unified diff.
//
// The marker carries the meaning and the colour reinforces it, the same rule the status glyphs
// follow. A diff read with NO_COLOR set, or by somebody who cannot distinguish the two greens from
// the red, is still a diff: the plus and the minus are the first character of every line.
func renderDiffLine(line, language string, width int) string {
	if width > 4 {
		line = truncate(line, width)
	}
	if line == "" {
		return line
	}

	switch {
	case strings.HasPrefix(line, "@@"):
		return lipgloss.NewStyle().Foreground(colorPending).Render(line)
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"),
		strings.HasPrefix(line, "diff "), strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "new file"), strings.HasPrefix(line, "deleted file"),
		strings.HasPrefix(line, "similarity "), strings.HasPrefix(line, "rename "):
		return styleMuted.Render(line)
	case strings.HasPrefix(line, "+"):
		return lipgloss.NewStyle().Foreground(colorPass).Render("+") + chat.Highlight(language, line[1:])
	case strings.HasPrefix(line, "-"):
		return lipgloss.NewStyle().Foreground(colorFail).Render("-") + chat.Highlight(language, line[1:])
	case strings.HasPrefix(line, "\\"):
		// "\ No newline at end of file". Not source, and highlighting it as source looks like a bug.
		return styleMuted.Render(line)
	default:
		return " " + chat.Highlight(language, strings.TrimPrefix(line, " "))
	}
}

// languageOf reads a language from a filename, which is what the highlighter wants.
func languageOf(path string) string {
	dot := strings.LastIndex(path, ".")
	if dot < 0 || dot == len(path)-1 {
		return ""
	}
	return path[dot+1:]
}

// Footer names the keys that currently do something.
func (m ReviewModel) Footer() string {
	switch m.pane {
	case panePatch:
		return Keys(m.width, "j/k", "scroll", "space", "page", "g/G", "top/end", "esc", "files")
	case paneFiles:
		return Keys(m.width, "j/k", "move", "enter", "open", "esc", "back")
	default:
		return Keys(m.width, "j/k", "move", "enter", "changes", "tab", "ranking", "esc", "agents")
	}
}
