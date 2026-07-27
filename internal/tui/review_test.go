package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// fakeReview stands in for the verifier. The engine's own behaviour is tested against a real
// repository in internal/verify; what is under test here is what the screen does with the answers.
type fakeReview struct {
	ranking core.Ranking
	queue   []core.ReadyForReview
	changes map[string][]core.FileChange
	patches map[string]string
	failure error
}

func (f *fakeReview) Rank() core.Ranking                   { return f.ranking }
func (f *fakeReview) ReadyToReview() []core.ReadyForReview { return f.queue }

func (f *fakeReview) Changes(agent string) ([]core.FileChange, error) {
	if f.failure != nil {
		return nil, f.failure
	}
	return f.changes[agent], nil
}

func (f *fakeReview) Patch(agent, path string) (string, error) {
	if f.failure != nil {
		return "", f.failure
	}
	return f.patches[agent+":"+path], nil
}

func press(m ReviewModel, keys ...string) ReviewModel {
	for _, key := range keys {
		var msg tea.KeyMsg
		switch key {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}
		m, _ = m.Update(msg)
	}
	return m
}

func loaded(t *testing.T) (ReviewModel, *fakeReview) {
	t.Helper()

	source := &fakeReview{
		queue: []core.ReadyForReview{
			{Agent: "small", Branch: "canopy/small", Diff: core.DiffStat{FilesChanged: 1, Insertions: 4},
				Why: "all required evidence is current and passing for revision abc1234, 1 file changed"},
			{Agent: "large", Branch: "canopy/large", Diff: core.DiffStat{FilesChanged: 9, Insertions: 900},
				Why: "all required evidence is current and passing for revision def5678, 9 files changed"},
		},
		ranking: core.Ranking{
			Ranked: []core.Placement{
				{Agent: "small", Rank: 1, Tests: core.TestPassing, Passing: 1, Required: 1,
					Reason: "all 1 required test pass for revision abc1234, 1 file changed"},
			},
			Unranked: []core.Placement{
				{Agent: "busy", Tests: core.TestStale,
					Reason: "not ranked: unit is stale, the worktree changed since this ran"},
			},
		},
		changes: map[string][]core.FileChange{
			"small": {{Path: "auth.go", Status: 'M', Insertions: 4, Deletions: 1}},
			"busy":  {{Path: "wip.go", Status: 'A', Insertions: 2}},
		},
		patches: map[string]string{
			"small:auth.go": "@@ -1,3 +1,4 @@\n func check() {\n-\treturn false\n+\treturn true\n }\n",
		},
	}

	model := NewReview(source)
	model.SetSize(80, 20)
	return model, source
}

func TestTheQueueLeadsWithTheEasiestReview(t *testing.T) {
	model, _ := loaded(t)

	if model.Pane() != "queue" {
		t.Errorf("the screen opens on %q, want the queue: the question somebody arrives with is "+
			"which agent to read next", model.Pane())
	}
	body := model.Body()
	if !strings.Contains(body, "small") || !strings.Contains(body, "large") {
		t.Errorf("the queue does not list both agents:\n%s", body)
	}
	if strings.Index(body, "small") > strings.Index(body, "large") {
		t.Error("the larger change is listed first")
	}
	if !strings.Contains(body, "1 file changed") {
		t.Errorf("the queue does not say how much there is to read:\n%s", body)
	}
}

func TestAnEmptyQueueExplainsItselfRatherThanShowingNothing(t *testing.T) {
	model := NewReview(&fakeReview{})
	model.SetSize(80, 20)

	body := model.Body()
	if !strings.Contains(body, "nothing is ready") {
		t.Errorf("an empty queue renders as:\n%s", body)
	}
	if !strings.Contains(body, "tests pass") {
		t.Errorf("the empty state does not say what would put an agent here:\n%s", body)
	}
}

// An unranked agent has to be visible and has to be visibly not placed. Pushing it to the bottom of
// a numbered list would read as it having come last, which is a claim about its code rather than
// about its evidence.
func TestAnUnrankedAgentIsShownWithoutAPosition(t *testing.T) {
	model, _ := loaded(t)
	model = press(model, "tab")

	if model.Pane() != "ranking" {
		t.Fatalf("tab landed on %q", model.Pane())
	}
	body := model.Body()
	if !strings.Contains(body, "busy") {
		t.Errorf("the unranked agent is missing from the ranking, so it reads as having vanished:\n%s", body)
	}
	if !strings.Contains(body, "not ranked") {
		t.Errorf("the ranking does not say why the agent has no place:\n%s", body)
	}
	if strings.Contains(body, "  2 ") {
		t.Errorf("the unranked agent was given a position:\n%s", body)
	}
	if !strings.Contains(body, "STALE") {
		t.Errorf("the state word is missing, so the row depends on colour alone:\n%s", body)
	}
}

func TestOpeningAnAgentShowsItsFilesAndThenItsDiff(t *testing.T) {
	model, _ := loaded(t)

	model = press(model, "enter")
	if model.Pane() != "files" || model.Agent() != "small" {
		t.Fatalf("enter on the queue landed on pane %q for agent %q", model.Pane(), model.Agent())
	}
	if !strings.Contains(model.Body(), "auth.go") {
		t.Errorf("the file list is missing the changed file:\n%s", model.Body())
	}

	model = press(model, "enter")
	if model.Pane() != "patch" {
		t.Fatalf("enter on a file landed on %q", model.Pane())
	}
	body := model.Body()
	if !strings.Contains(body, "return true") {
		t.Errorf("the patch is missing its content:\n%s", body)
	}
	if !strings.Contains(body, "@@") {
		t.Errorf("the patch is missing its hunk header:\n%s", body)
	}
}

// Escape unwinds one level at a time. Dropping straight out of a diff to the queue would lose the
// file list somebody was working through.
func TestEscapeUnwindsOneLevelAtATime(t *testing.T) {
	model, _ := loaded(t)
	model = press(model, "enter", "enter")

	model = press(model, "esc")
	if model.Pane() != "files" {
		t.Errorf("escape from the patch landed on %q, want the file list", model.Pane())
	}
	model = press(model, "esc")
	if model.Pane() != "queue" {
		t.Errorf("escape from the file list landed on %q", model.Pane())
	}
	if model.Agent() != "" {
		t.Errorf("leaving the file list kept %q open", model.Agent())
	}
}

// An unranked agent still opens. Refusing to place it is a statement about its evidence, and
// reading the diff is often how somebody decides whether to bother re-running the tests.
func TestAnUnrankedAgentCanStillBeOpened(t *testing.T) {
	model, _ := loaded(t)
	model = press(model, "tab", "j", "enter")

	if model.Agent() != "busy" {
		t.Errorf("opening the unranked agent gave %q", model.Agent())
	}
	if !strings.Contains(model.Body(), "wip.go") {
		t.Errorf("its changes are not shown:\n%s", model.Body())
	}
}

// A large diff must not be rendered whole on every keystroke. What is asserted is the observable
// consequence: the body holds a window, not the file.
func TestALargeDiffRendersOnlyWhatFits(t *testing.T) {
	source := &fakeReview{
		queue:   []core.ReadyForReview{{Agent: "one", Diff: core.DiffStat{FilesChanged: 1}}},
		changes: map[string][]core.FileChange{"one": {{Path: "big.go", Status: 'M'}}},
		patches: map[string]string{},
	}
	var patch strings.Builder
	for i := range 4000 {
		patch.WriteString("+\tline ")
		patch.WriteString(strings.Repeat("x", i%20))
		patch.WriteString("\n")
	}
	source.patches["one:big.go"] = patch.String()

	model := NewReview(source)
	model.SetSize(80, 20)
	model = press(model, "enter", "enter")

	rendered := strings.Count(model.Body(), "\n") + 1
	if rendered > 25 {
		t.Errorf("%d lines rendered for a 4000 line diff, so holding a movement key would re-style "+
			"the whole file on every repeat", rendered)
	}
	if !strings.Contains(model.Body(), "of 4001") {
		t.Errorf("the header does not say where in the file this is:\n%s",
			strings.SplitN(model.Body(), "\n", 2)[0])
	}

	// And the end is reachable rather than the view running off past it.
	atEnd := press(model, "G")
	if strings.Contains(atEnd.Body(), "of 4001\n\n\n") {
		t.Error("the end of the file scrolled past into blank lines")
	}
}

// Readable without colour, which is the same rule the status glyphs follow. The marker character is
// the first thing on every line and carries the meaning on its own.
func TestADiffIsReadableWithoutColour(t *testing.T) {
	model, _ := loaded(t)
	model = press(model, "enter", "enter")

	// Tabs arrive expanded. A terminal tab jumps to the next tab stop rather than occupying a fixed
	// number of cells, and with a marker character in front of every line that would put the
	// indentation of an added line and an unchanged one in different columns.
	for _, want := range []string{"-    return false", "+    return true"} {
		plain := stripANSI(model.Body())
		if !strings.Contains(plain, want) {
			t.Errorf("stripped of colour, the diff does not contain %q:\n%s", want, plain)
		}
	}
}

func TestANarrowTerminalDoesNotOverflow(t *testing.T) {
	model, _ := loaded(t)
	model.SetSize(40, 12)
	model = press(model, "enter", "enter")

	for _, line := range strings.Split(stripANSI(model.Body()), "\n") {
		if len([]rune(line)) > 40 {
			t.Errorf("a line is %d columns wide in a 40 column terminal: %q", len([]rune(line)), line)
		}
	}
}

func TestAFailureToReadTheDiffIsShownRatherThanSwallowed(t *testing.T) {
	model, source := loaded(t)
	source.failure = errors.New("this worktree has gone away")

	model = press(model, "enter")
	if model.Pane() != "queue" {
		t.Errorf("a failed read still changed pane to %q", model.Pane())
	}
	if !strings.Contains(model.Body(), "gone away") {
		t.Errorf("the failure is not on screen:\n%s", model.Body())
	}
}

func TestWithoutARepositoryTheScreenSaysSo(t *testing.T) {
	model := NewReview(nil)
	model.SetSize(80, 20)

	if !strings.Contains(model.Body(), "not a git repository") {
		t.Errorf("the screen renders as:\n%s", model.Body())
	}
	// And nothing panics on the keys that would otherwise descend.
	press(model, "enter", "j", "tab", "esc")
}

// stripANSI removes escape sequences so an assertion is about the text rather than the styling.
func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
