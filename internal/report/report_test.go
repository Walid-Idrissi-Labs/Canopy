package report

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

// green is a run that genuinely passed, as the baseline the dishonest cases are compared against.
func green() Run {
	return Run{
		Agent: "parser", Branch: "feat/parser", Base: "main",
		Rollup: core.Rollup{
			Green: true, Reason: "every required test passed against the current revision",
			Tests: core.TestPassing, TestsPassing: 3, TestsTotal: 3, RequiredTests: 3,
		},
		Diff:  core.DiffStat{FilesChanged: 2, Insertions: 40, Deletions: 5},
		Files: []core.FileChange{{Path: "parse.go", Status: 'M'}},
		Usage: core.Usage{InputTokens: 1000, OutputTokens: 200, CostUSD: 0.0123, CostKnown: true},
		Turns: 6,
	}
}

// The acceptance criterion, and the only one that matters. A report is pasted into a pull request
// and read by somebody who cannot see the screen it came from, so a confident report is a worse
// failure than an ugly one.
func TestAStaleResultIsNeverReportedAsAPass(t *testing.T) {
	run := green()
	run.Rollup = core.Rollup{
		Green:  false,
		Reason: "the tests passed against a revision this worktree has moved on from",
		Tests:  core.TestStale, TestsPassing: 0, TestsTotal: 3, RequiredTests: 3,
	}

	out := Markdown(run)
	if strings.Contains(out, "**Verified.**") {
		t.Errorf("a stale result was reported as verified:\n%s", out)
	}
	if !strings.Contains(out, "Not verified") {
		t.Errorf("the report does not say it is unverified:\n%s", out)
	}
	// Omitting the tests would be the more flattering lie: it reads as a change with no test story
	// rather than as one whose evidence expired.
	if !strings.Contains(out, string(core.TestStale)) {
		t.Errorf("the stale state is not named anywhere:\n%s", out)
	}
	if !strings.Contains(out, "moved on from") {
		t.Errorf("the reason was dropped:\n%s", out)
	}
}

// The state most easily mistaken for a clean run, and the one the whole product exists to name.
func TestNothingConfiguredIsNotReportedAsAPass(t *testing.T) {
	run := green()
	run.Rollup = core.Rollup{}

	out := Markdown(run)
	if strings.Contains(out, "**Verified.**") {
		t.Errorf("an unconfigured project was reported as verified:\n%s", out)
	}
	if !strings.Contains(out, "Nothing is configured") {
		t.Errorf("the report does not say why there is no evidence:\n%s", out)
	}
	// And it must not invent a test line out of zeroes, which would read as "0 of 0 passing" and
	// look like a suite that ran.
	if strings.Contains(out, "0 of 0") {
		t.Errorf("an empty test count was rendered as a result:\n%s", out)
	}
}

// The hole core.Rollup's Caveat field exists to close. A green with a failing optional test is a
// row somebody looks at for months without learning it has been broken the whole time, and a report
// that dropped the caveat would be the one place that hole reopened.
func TestAGreenWithACaveatStillCarriesTheCaveat(t *testing.T) {
	run := green()
	run.Rollup.Caveat = "the optional lint test has been failing since Tuesday"

	out := Markdown(run)
	if !strings.Contains(out, "**Verified.**") {
		t.Fatalf("a genuine pass was not reported as verified:\n%s", out)
	}
	if !strings.Contains(out, "lint test has been failing") {
		t.Errorf("the caveat was dropped from a green report:\n%s", out)
	}
}

// The ranking already refuses to place an agent whose evidence it cannot stand behind. That refusal
// is information: an agent omitted from a report looks like one nobody compared.
func TestAnUnrankedAgentSaysSoAndSaysWhy(t *testing.T) {
	run := green()
	run.WasRanked = true
	run.Ranked = core.Placement{
		Agent: "parser", Rank: 0,
		Reason: "its evidence is stale, so it cannot be compared with the others",
	}

	out := Markdown(run)
	if !strings.Contains(out, "Not ranked") {
		t.Errorf("an unplaced agent does not say so:\n%s", out)
	}
	if !strings.Contains(out, "cannot be compared") {
		t.Errorf("the refusal gives no reason:\n%s", out)
	}
	if strings.Contains(out, "Ranked 0") {
		t.Errorf("rank zero was rendered as a placement:\n%s", out)
	}
}

func TestARankedAgentReportsItsPlacement(t *testing.T) {
	run := green()
	run.WasRanked = true
	run.Ranked = core.Placement{Agent: "parser", Rank: 1, Reason: "3 of 3 required tests passing"}

	if out := Markdown(run); !strings.Contains(out, "Ranked 1st") {
		t.Errorf("the placement is missing:\n%s", out)
	}
}

// Zero is a claim. "We could not price this" is the absence of one, and rendering the first as the
// second is the mistake core.Usage.CostKnown exists to prevent.
func TestAnUnpricedRunNeverReadsAsFree(t *testing.T) {
	run := green()
	run.Usage = core.Usage{InputTokens: 1000, OutputTokens: 200, CostUSD: 0, CostKnown: false}

	out := Markdown(run)
	if strings.Contains(out, "$0.0000") {
		t.Errorf("an unpriced run was rendered as costing nothing:\n%s", out)
	}
	if !strings.Contains(out, "not known") {
		t.Errorf("the report does not say the cost is unknown:\n%s", out)
	}
}

// A partial sum is worse than no number, because a number on the page is read as the answer.
func TestAPartlyPricedRunIsReportedAsAFloor(t *testing.T) {
	run := green()
	run.Usage = core.Usage{InputTokens: 1000, OutputTokens: 200, CostUSD: 0.5, CostKnown: false}

	out := Markdown(run)
	if !strings.Contains(out, "at least $0.5000") {
		t.Errorf("a partial cost was not reported as a floor:\n%s", out)
	}
	if !strings.Contains(out, "higher") {
		t.Errorf("the report does not say the real figure is higher:\n%s", out)
	}
}

// A hundred file list buries the part a reviewer needs, and a list that silently stops reads as the
// whole change.
func TestALongFileListSaysWhatItLeftOut(t *testing.T) {
	run := green()
	run.Diff = core.DiffStat{FilesChanged: 60, Insertions: 900, Deletions: 40}
	for i := range 60 {
		run.Files = append(run.Files, core.FileChange{
			Path: string(rune('a'+i%26)) + "file.go", Status: 'M',
		})
	}

	out := Markdown(run)
	if !strings.Contains(out, "more") {
		t.Errorf("a truncated list does not say how much it dropped:\n%s", out)
	}
	if strings.Count(out, "- `") > 30 {
		t.Errorf("the list was not bounded:\n%s", out)
	}
}

// Two reports of the same work should read the same, or a reviewer comparing them sees noise.
func TestTheFileOrderIsStable(t *testing.T) {
	files := []core.FileChange{
		{Path: "z.go", Status: 'M'}, {Path: "a.go", Status: 'A'}, {Path: "m.go", Status: 'D'},
	}
	sorted := Sorted(files)
	if sorted[0].Path != "a.go" || sorted[2].Path != "z.go" {
		t.Errorf("order = %v", sorted)
	}
	// And the caller's slice is untouched, or sorting for the report would reorder whatever the
	// caller was holding.
	if files[0].Path != "z.go" {
		t.Error("Sorted mutated its argument")
	}
}

// A status letter nobody has handled should look strange rather than be reported as a modification,
// which is the most common status and therefore the most invisible wrong answer.
func TestAnUnrecognisedChangeIsNotCalledAModification(t *testing.T) {
	run := green()
	run.Files = []core.FileChange{{Path: "odd.go", Status: 'U'}}

	out := Markdown(run)
	if strings.Contains(out, "`odd.go` (modified)") {
		t.Errorf("an unknown status was reported as a modification:\n%s", out)
	}
	if !strings.Contains(out, `"U"`) {
		t.Errorf("the unrecognised status is not shown:\n%s", out)
	}
}

// The report says where it came from, so a reader does not assume it checked more than it did.
func TestTheReportSaysWhatItIsAndWhatItCovers(t *testing.T) {
	out := Markdown(green())
	if !strings.Contains(out, "Canopy") {
		t.Errorf("the report does not name what produced it:\n%s", out)
	}
	if !strings.Contains(out, "nothing after it") {
		t.Errorf("the report does not bound what its results describe:\n%s", out)
	}
}
