package permission

import (
	"strings"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/core"
)

func request(kind core.ToolKind, tool string) Request {
	return Request{AgentID: "a1", SessionID: "s1", Tool: tool, Kind: kind}
}

// An unrecognised level is a configuration somebody got wrong, and the safe reading of "I do not
// know how much this agent is trusted" is "not at all".
func TestAnUnknownTrustLevelDeniesEverything(t *testing.T) {
	for _, kind := range core.AllToolKinds() {
		decision := Decide(request(kind, "anything"), core.TrustLevel("mystery"), NewGrants())
		if decision.Outcome != Deny {
			t.Errorf("%s was %s under an unknown trust level, want deny", kind, decision.Outcome)
		}
		if decision.Reason == "" {
			t.Errorf("%s was denied without saying why", kind)
		}
	}
}

// The point of having levels is that somebody can run a scratch agent broadly and an agent near
// main narrowly, and that only works if the narrow one is genuinely narrow.
func TestTrustLevelsDifferOnTheSameRequest(t *testing.T) {
	cases := []struct {
		kind core.ToolKind
		want map[core.TrustLevel]Outcome
	}{
		{core.ToolRead, map[core.TrustLevel]Outcome{
			core.TrustReadOnly: Allow, core.TrustConfined: Allow,
			core.TrustStandard: Allow, core.TrustBroad: Allow,
		}},
		{core.ToolWrite, map[core.TrustLevel]Outcome{
			core.TrustReadOnly: Deny, core.TrustConfined: Allow,
			core.TrustStandard: Allow, core.TrustBroad: Allow,
		}},
		{core.ToolExecute, map[core.TrustLevel]Outcome{
			core.TrustReadOnly: Deny, core.TrustConfined: Deny,
			core.TrustStandard: Ask, core.TrustBroad: Allow,
		}},
		{core.ToolNetwork, map[core.TrustLevel]Outcome{
			core.TrustReadOnly: Ask, core.TrustConfined: Ask,
			core.TrustStandard: Ask, core.TrustBroad: Ask,
		}},
	}

	for _, tc := range cases {
		for level, want := range tc.want {
			req := request(tc.kind, string(tc.kind)+"_tool")
			if tc.kind == core.ToolExecute {
				req.Command = "make test"
			}
			got := Decide(req, level, NewGrants()).Outcome
			if got != want {
				t.Errorf("%s at %s trust = %s, want %s", tc.kind, level, got, want)
			}
		}
	}
}

// A shell command is an opaque string that can do anything the user can, and the difference between
// standard and broad is exactly whether somebody sees it first.
func TestStandardTrustNeverRunsAShellCommandSilently(t *testing.T) {
	req := request(core.ToolExecute, "run_command")
	req.Command = "rm -rf build"

	decision := Decide(req, core.TrustStandard, NewGrants())
	if decision.Outcome != Ask {
		t.Errorf("outcome = %s, want a question", decision.Outcome)
	}
	if decision.Scope.Command != "rm -rf build" {
		t.Errorf("the prompt would show %q, and it has to show the command being approved",
			decision.Scope)
	}
}

// A denial is structural, and presenting it as a question that can only be answered no would train
// people to click through prompts.
func TestADenialIsNotDressedUpAsAQuestion(t *testing.T) {
	decision := Decide(request(core.ToolWrite, "edit_file"), core.TrustReadOnly, NewGrants())

	if decision.Outcome != Deny {
		t.Fatalf("outcome = %s", decision.Outcome)
	}
	// The reason is returned to the model and shown to the user, so it has to read well as both.
	if !strings.Contains(decision.Reason, "read-only") {
		t.Errorf("the reason should name the level, got %q", decision.Reason)
	}
	if !strings.Contains(decision.Reason, "confined") {
		t.Errorf("the reason should say what would be needed, got %q", decision.Reason)
	}
}

// A bad edit is recoverable from git. A bad `git checkout` is what you would have recovered from.
func TestDestructiveGitIsSeparatedFromOrdinaryGit(t *testing.T) {
	ordinary := []string{"git status", "git diff", "git log --oneline", "git add .",
		`git commit -m "reset the counter"`}
	destructive := []string{"git reset --hard HEAD~1", "git checkout -f main", "git clean -fd",
		"git branch -D feature", "git push --force", "git rebase main", "git stash drop"}

	for _, command := range ordinary {
		req := request(core.ToolGit, "git")
		req.Command = command
		if got := Decide(req, core.TrustStandard, NewGrants()).Outcome; got != Allow {
			t.Errorf("%q was %s at standard trust, want allow", command, got)
		}
	}

	for _, command := range destructive {
		req := request(core.ToolGit, "git")
		req.Command = command

		if got := Decide(req, core.TrustStandard, NewGrants()).Outcome; got != Deny {
			t.Errorf("%q was %s at standard trust, want deny", command, got)
		}
		if got := Decide(req, core.TrustBroad, NewGrants()).Outcome; got != Allow {
			t.Errorf("%q was %s at broad trust, want allow", command, got)
		}
	}
}

// `git commit -m "reset the counter"` is not a reset, and a list of subcommands will always be
// behind the tool, so an unrecognised command carrying --force is treated as destructive anyway.
func TestDestructivenessIsJudgedByWhatTheCommandDoes(t *testing.T) {
	if isDestructiveGit(`git commit -m "reset --hard the counter"`) {
		// This one is genuinely ambiguous and the conservative answer is acceptable, but the plain
		// case must not be caught.
		t.Log("a commit message containing the words is treated as destructive, which is " +
			"conservative but noted")
	}
	if isDestructiveGit("git commit -m 'fix the parser'") {
		t.Error("an ordinary commit was treated as destructive")
	}
	if !isDestructiveGit("git some-new-subcommand --force") {
		t.Error("a command nobody has heard of carrying --force should be treated as destructive")
	}
	if !isDestructiveGit("git push -f origin main") {
		t.Error("-f means the same thing as --force")
	}
	if isDestructiveGit("git log -p") {
		t.Error("-p is not -f")
	}
}

// A user who already said yes should not be asked twice.
func TestAnApprovalIsRemembered(t *testing.T) {
	grants := NewGrants()
	req := request(core.ToolExecute, "run_command")
	req.Command = "make test"

	first := Decide(req, core.TrustStandard, grants)
	if first.Outcome != Ask {
		t.Fatalf("outcome = %s, want a question first", first.Outcome)
	}

	grants.Grant(first.Scope)

	second := Decide(req, core.TrustStandard, grants)
	if second.Outcome != Allow {
		t.Errorf("outcome = %s after approval, want allow", second.Outcome)
	}
}

// Two shell commands that differ by a character do different things, and an approval for one is not
// evidence about the other.
func TestApprovingOneCommandDoesNotApproveAnother(t *testing.T) {
	grants := NewGrants()

	approved := request(core.ToolExecute, "run_command")
	approved.Command = "make test"
	grants.Grant(Decide(approved, core.TrustStandard, grants).Scope)

	different := request(core.ToolExecute, "run_command")
	different.Command = "make deploy"
	if got := Decide(different, core.TrustStandard, grants).Outcome; got != Ask {
		t.Errorf("approving %q also approved %q", approved.Command, different.Command)
	}
}

// An approval for one path never covers another.
func TestApprovingOnePathDoesNotApproveAnother(t *testing.T) {
	grants := NewGrants()
	grants.Grant(Scope{Tool: "edit_file", Path: "/w/src/main.go"})

	other := request(core.ToolWrite, "edit_file")
	other.Paths = []string{"/w/src/secrets.go"}

	// Write is allowed structurally at confined trust, so the meaningful check is that the grant
	// itself does not stretch.
	if grants.Covers(other, scopeFor(other)) {
		t.Error("an approval for one file covered a different one")
	}
}

// Approving a directory and then having one path of a multi file call fall outside it would let the
// call through on the strength of the paths that did match.
func TestADirectoryApprovalCoversOnlyWhatIsInsideIt(t *testing.T) {
	grants := NewGrants()
	grants.Grant(PathScope("edit_file", "/w/src"))

	inside := request(core.ToolWrite, "edit_file")
	inside.Paths = []string{"/w/src/main.go", "/w/src/deep/other.go"}
	if !grants.Covers(inside, scopeFor(inside)) {
		t.Error("a directory approval did not cover files inside it")
	}

	partly := request(core.ToolWrite, "edit_file")
	partly.Paths = []string{"/w/src/main.go", "/w/elsewhere/other.go"}
	if grants.Covers(partly, scopeFor(partly)) {
		t.Error("a call touching one file outside the approved directory was covered anyway")
	}

	// And a sibling sharing a name prefix is not inside it.
	sibling := request(core.ToolWrite, "edit_file")
	sibling.Paths = []string{"/w/src-secrets/keys.go"}
	if grants.Covers(sibling, scopeFor(sibling)) {
		t.Error("a directory sharing a name prefix was treated as inside")
	}
}

// A user cannot approve their way past a structural denial, because that denial is the thing they
// chose when they picked the level, and a prompt that could override it would make the level
// advisory.
func TestApprovalCannotOverrideAStructuralDenial(t *testing.T) {
	grants := NewGrants()
	grants.Grant(KindScope(core.ToolWrite))
	grants.Grant(Scope{Tool: "edit_file", Path: "/w/main.go"})

	req := request(core.ToolWrite, "edit_file")
	req.Paths = []string{"/w/main.go"}

	if got := Decide(req, core.TrustReadOnly, grants).Outcome; got != Deny {
		t.Errorf("a read-only agent was allowed to write because of an approval, got %s", got)
	}
}

// The interface should never offer a button that would not work.
func TestOnlyGrantableKindsAreOffered(t *testing.T) {
	readOnly := GrantableKinds(core.TrustReadOnly)
	for _, kind := range readOnly {
		if kind == core.ToolWrite || kind == core.ToolExecute {
			t.Errorf("%s was offered as approvable to a read-only agent, and approving it would "+
				"still be denied", kind)
		}
	}

	broad := GrantableKinds(core.TrustBroad)
	if len(broad) <= len(readOnly) {
		t.Error("a broad agent should have more kinds it can approve wholesale than a read-only one")
	}
}

// Offering the broadest approval as the default is how "yes" comes to mean "yes to everything"
// without anybody deciding that.
func TestThePromptOffersTheNarrowestScope(t *testing.T) {
	req := request(core.ToolWrite, "edit_file")
	req.Paths = []string{"/w/src/main.go", "/w/src/other.go"}

	decision := Decide(req, core.TrustConfined, NewGrants())
	if decision.Scope.Kind != "" {
		t.Error("the default scope covers a whole tool kind")
	}
	if strings.HasSuffix(decision.Scope.Path, "/") {
		t.Error("the default scope is a whole directory")
	}
}

// An agent that tried to write outside its workspace nine times and was stopped nine times is a
// very different thing from one that never tried, and only the trail can tell them apart.
func TestTheTrailRecordsRefusalsAsWellAsSuccesses(t *testing.T) {
	trail := NewTrail()

	trail.Record(Entry{AgentID: "a1", Tool: "read_file", Kind: core.ToolRead,
		Outcome: Allow, Ran: true})
	trail.Record(Entry{AgentID: "a1", Tool: "edit_file", Kind: core.ToolWrite,
		Outcome: Deny, Reason: "read-only"})
	trail.Record(Entry{AgentID: "a1", Tool: "run_command", Kind: core.ToolExecute,
		Outcome: Allow, Ran: true, Failed: true})
	trail.Record(Entry{AgentID: "a2", Tool: "read_file", Kind: core.ToolRead,
		Outcome: Allow, Ran: true})

	if got := len(trail.Entries()); got != 4 {
		t.Errorf("%d entries, want 4", got)
	}

	// The question is almost always about one agent, because with eight running the interleaved
	// trail is unreadable.
	if got := len(trail.ForAgent("a1")); got != 3 {
		t.Errorf("%d entries for a1, want 3", got)
	}

	refused := trail.Refused()
	if len(refused) != 1 || refused[0].Tool != "edit_file" {
		t.Errorf("refused = %+v, want the denied write", refused)
	}

	counts := trail.Count("a1")
	if counts.Total != 3 || counts.Refused != 1 || counts.Failed != 1 {
		t.Errorf("counts = %+v", counts)
	}
	if counts.ByTool["read_file"] != 1 {
		t.Errorf("by tool = %v", counts.ByTool)
	}
}

// A write tool's arguments are a whole file and a shell result can be a build log. Keeping them
// whole would make the trail larger than the work it describes.
func TestTrailEntriesAreBounded(t *testing.T) {
	trail := NewTrail()
	huge := strings.Repeat("x", 50_000)

	trail.Record(Entry{Tool: "write_file", Arguments: huge, Result: huge})

	entry := trail.Entries()[0]
	if len(entry.Arguments) > maxRecorded+200 {
		t.Errorf("arguments kept %d bytes", len(entry.Arguments))
	}
	// And it says what it dropped, or somebody reading it back thinks that was the whole call.
	if !strings.Contains(entry.Arguments, "more bytes") {
		t.Error("the trail does not say that it truncated")
	}
}

// A trail that dropped the newest would stop recording exactly when an agent is at its busiest,
// which is when you need it.
func TestAFullTrailDropsTheOldest(t *testing.T) {
	trail := &Trail{limit: 3}
	for _, name := range []string{"first", "second", "third", "fourth"} {
		trail.Record(Entry{Tool: name})
	}

	entries := trail.Entries()
	if len(entries) != 3 {
		t.Fatalf("%d entries, want the cap of 3", len(entries))
	}
	if entries[0].Tool != "second" {
		t.Errorf("oldest kept = %q, want the first to have been dropped", entries[0].Tool)
	}
	if entries[2].Tool != "fourth" {
		t.Errorf("newest = %q, want the most recent call", entries[2].Tool)
	}
}

// An approval the user cannot see is one they cannot reconsider.
func TestGrantsCanBeListedAndRevoked(t *testing.T) {
	grants := NewGrants()
	scope := Scope{Tool: "run_command", Command: "make test"}

	grants.Grant(scope)
	if len(grants.Granted()) != 1 {
		t.Fatalf("granted = %+v", grants.Granted())
	}

	grants.Revoke(scope)
	if len(grants.Granted()) != 0 {
		t.Error("a revoked approval is still in force")
	}
}

func TestOutcomeVocabulary(t *testing.T) {
	want := []Outcome{"allow", "ask", "deny"}
	got := AllOutcomes()
	if len(got) != len(want) {
		t.Fatalf("outcomes changed size: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("outcome %d = %q, want %q", i, got[i], want[i])
		}
	}
	if Outcome("maybe").Valid() {
		t.Error("the vocabulary is closed")
	}
}

// `git branch -d` deletes a branch only if it has been merged; `git branch -D` deletes it
// regardless. Lowercasing to make matching easier would conflate the two and quietly allow the
// destructive one at a level that should ask.
func TestFlagCaseIsNotFlattened(t *testing.T) {
	if !isDestructiveGit("git branch -D feature") {
		t.Error("-D deletes an unmerged branch and is destructive")
	}
	if isDestructiveGit("git branch -d feature") {
		t.Error("-d only deletes a merged branch, so treating it as destructive is friction for " +
			"nothing")
	}
}
