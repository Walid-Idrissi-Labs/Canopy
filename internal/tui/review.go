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

	// Overlaps is the conflict radar: files more than one agent has changed.
	Overlaps() ([]core.Overlap, error)

	// Draft is everything of a commit message except the subject, which no diff can supply.
	Draft(agent string) (core.CommitDraft, error)

	// Commit stages everything in an agent's worktree and records it.
	Commit(agent, message string) error
}

type reviewPane int

const (
	// paneQueue leads, because "which of these should I look at next" is the question somebody
	// arrives with. The ranking answers a narrower one, and only when several agents were given the
	// same task.
	paneQueue reviewPane = iota
	paneRanking
	paneCosts
	paneOverlap
	paneFiles
	panePatch
	paneCommit
)

// ReviewModel is the review screen.
type ReviewModel struct {
	source ReviewSource
	costs  CostOutcomeSource

	pane    reviewPane
	cursor  int
	agent   string
	files   []core.FileChange
	file    string
	patch   []string
	offset  int
	failure string
	notice  string
	width   int
	height  int

	// draft is the generated half of the commit message and subject is the half a person writes.
	// Kept apart so what is being edited is exactly the line that needs a human, rather than a
	// message somebody has to find the placeholder inside of.
	draft   core.CommitDraft
	subject string
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
	case paneCosts:
		return "cost versus outcome"
	case paneOverlap:
		return "overlap"
	case paneFiles:
		return "files"
	case panePatch:
		return "patch"
	case paneCommit:
		return "commit"
	default:
		return "queue"
	}
}

// SetCostOutcomes adds the project-local historical comparison.
func (m *ReviewModel) SetCostOutcomes(source CostOutcomeSource) { m.costs = source }

// Agent is the agent whose changes are open, if any. For tests.
func (m ReviewModel) Agent() string { return m.agent }

func (m ReviewModel) Update(msg tea.Msg) (ReviewModel, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// While the commit message is being written every printable key belongs to it, including the
	// ones that would otherwise navigate. Without this, a subject containing the letter j would move
	// the cursor instead of being typed.
	if m.pane == paneCommit {
		return m.editing(key), nil
	}

	switch key.String() {
	case "tab":
		// Only between the list panes. Tab out of a diff would lose your place in it for a keystroke
		// people press by habit, and tab out of a half typed commit message would lose the message.
		switch m.pane {
		case paneQueue:
			m.pane, m.cursor = paneRanking, 0
		case paneRanking:
			m.pane, m.cursor = paneCosts, 0
		case paneCosts:
			m.pane, m.cursor = paneOverlap, 0
		case paneOverlap:
			m.pane, m.cursor = paneQueue, 0
		}
		return m, nil

	case "esc":
		switch m.pane {
		case paneCommit:
			m.pane, m.subject, m.notice = paneFiles, "", ""
		case panePatch:
			m.pane, m.patch, m.offset = paneFiles, nil, 0
		case paneFiles:
			m.pane, m.agent, m.files, m.notice = paneQueue, "", nil, ""
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

	case "c":
		// Committing is reached from the file list, where you have just read what is being committed.
		// Offering it from the queue would let somebody commit an agent's work without having looked
		// at it, which is a keystroke away from the thing this whole screen exists to prevent.
		if m.pane == paneFiles && len(m.files) > 0 {
			draft, err := m.source.Draft(m.agent)
			if err != nil {
				m.failure = err.Error()
				return m, nil
			}
			m.pane, m.draft, m.subject, m.notice, m.failure = paneCommit, draft, "", "", ""
		}
		return m, nil

	case "enter":
		return m.open(), nil
	}
	return m, nil
}

// editing handles a keystroke while the commit message is open.
//
// Enter does not commit. It is the key people press to end a line, and wiring an irreversible
// action to it is how somebody commits a half written subject. Committing is ctrl+s, which nothing
// else on this screen uses and nobody presses by accident.
func (m ReviewModel) editing(key tea.KeyMsg) ReviewModel {
	switch key.String() {
	case "esc":
		m.pane, m.subject, m.notice = paneFiles, "", ""
		return m

	case "ctrl+s":
		if strings.TrimSpace(m.subject) == "" {
			m.failure = "a commit needs a subject, so write one before committing"
			return m
		}
		if err := m.source.Commit(m.agent, m.message()); err != nil {
			m.failure = err.Error()
			return m
		}
		// The file list is re-read rather than assumed empty. A commit that succeeded partially, or
		// a worktree with something still uncommitted in it, should show what is actually there.
		committed := m.openAgent(m.agent)
		committed.notice = "committed"
		return committed

	case "backspace":
		if runes := []rune(m.subject); len(runes) > 0 {
			m.subject = string(runes[:len(runes)-1])
		}
		m.failure = ""
		return m

	case " ", "space":
		// Both spellings, because a space arrives as one or the other depending on how the terminal
		// reports it, and a subject nobody can put a space in is not a subject.
		m.subject += " "
		return m
	}

	if key.Type == tea.KeyRunes {
		m.subject += string(key.Runes)
		m.failure = ""
	}
	return m
}

// message is the commit message as it currently stands.
func (m ReviewModel) message() string { return m.draft.Message(m.subject) }

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
	case paneOverlap:
		overlaps, err := m.source.Overlaps()
		if err != nil {
			return 0
		}
		return len(overlaps)
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
	case paneCosts:
		return "cost versus verified outcome"
	case paneOverlap:
		return "overlap between agents"
	case paneFiles:
		return "changes by " + m.agent
	case panePatch:
		return m.agent + " " + m.file
	case paneCommit:
		return "commit " + m.agent
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
	case paneCosts:
		if m.costs == nil {
			lines = []string{styleMuted.Render("cost outcome history is not available")}
		} else {
			history, err := m.costs.CostOutcomes()
			if err != nil {
				lines = []string{styleCaveat.Render(err.Error())}
			} else {
				lines = costOutcomeLines(history)
			}
		}
	case paneOverlap:
		lines = m.overlapLines()
	case paneCommit:
		lines = m.commitLines()
	case paneFiles:
		lines = m.fileLines()
	case panePatch:
		lines = m.patchLines()
	}

	if m.notice != "" {
		lines = append(lines, "", styleMuted.Render(m.notice))
	}
	if m.failure != "" {
		lines = append(lines, "", styleCaveat.Render(m.failure))
	}
	return strings.Join(lines, "\n")
}

// overlapLines is the conflict radar.
//
// It reports overlap, not conflict, and the wording says so. Two agents editing different functions
// in one file usually merge cleanly, and running a real three way merge per pair on every render to
// find out would cost more than it saves.
func (m ReviewModel) overlapLines() []string {
	overlaps, err := m.source.Overlaps()
	if err != nil {
		return []string{styleCaveat.Render(err.Error())}
	}
	if len(overlaps) == 0 {
		return []string{
			styleMuted.Render("no two agents have touched the same file."),
			"",
			styleMuted.Render("this is the good case, and it is worth checking again before merging,"),
			styleMuted.Render("since it is a statement about right now rather than about the merge."),
		}
	}

	lines := []string{styleHeader.Render("files more than one agent has changed")}
	for i, overlap := range overlaps {
		marker := "  "
		path := overlap.Path
		if i == m.cursor {
			marker, path = "> ", styleSelected.Render(overlap.Path)
		}

		note := strings.Join(overlap.Agents, ", ")
		if overlap.Contested() {
			// A delete against an edit is the one most likely to actually conflict, so it is said
			// rather than left to be inferred from a list of names.
			note += "  " + styleCaveat.Render("deleted by "+strings.Join(overlap.Deleted, ", "))
		}
		lines = append(lines, marker+path, "    "+styleReason.Render(note))
	}
	return lines
}

// commitLines shows the message being written.
//
// The generated body is shown, not hidden. Somebody about to commit should see the whole thing, and
// a body that appears only in the history is a body nobody has read.
func (m ReviewModel) commitLines() []string {
	prefix := m.draft.Prefix
	if prefix != "" {
		prefix += ": "
	}

	// A block cursor after the typed text, so an empty subject looks like something waiting to be
	// typed into rather than a field that is broken.
	typed := m.subject
	if typed == "" {
		typed = styleMuted.Render("write what this change does")
	}

	lines := []string{
		styleHeader.Render("commit " + m.agent),
		"",
		styleMuted.Render(prefix) + styleSelected.Render(typed) + styleMuted.Render("\u2588"),
		"",
	}
	for _, line := range strings.Split(m.draft.Body, "\n") {
		lines = append(lines, styleReason.Render(truncate(line, m.width)))
	}
	lines = append(lines, "", styleMuted.Render("ctrl+s commits. nothing is staged until then."))
	return lines
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
		return Keys(m.width, "j/k", "move", "enter", "diff", "c", "commit", "esc", "back")
	case paneCommit:
		return Keys(m.width, "type", "the subject", "ctrl+s", "commit", "esc", "cancel")
	case paneOverlap:
		return Keys(m.width, "j/k", "move", "tab", "queue", "esc", "agents")
	case paneCosts:
		return Keys(m.width, "tab", "overlap", "esc", "agents")
	case paneRanking:
		return Keys(m.width, "j/k", "move", "enter", "changes", "tab", "costs", "esc", "agents")
	default:
		return Keys(m.width, "j/k", "move", "enter", "changes", "tab", "ranking", "esc", "agents")
	}
}
