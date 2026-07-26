# Canopy Task Ledger

This file is the single source of truth for what is built, what is not, and what has been
verified. Both development pairs edit it. Nothing counts as done because it compiles. A task is
done only when its acceptance behaviour has been demonstrated.

Build order is strict. Tasks are listed in the order they must be built. Do not start a task
whose dependencies are not done. Phases are separated by a phase gate that needs human sign off
before the next phase begins.

---

## 1. How to use this file

### 1.1 Roles

| Party | Who |
|---|---|
| Supervisor | Walid. Approves phase gates, owns product decisions, stays informed. |
| Supervisor | Classmate. Approves phase gates, owns product decisions, stays informed. |
| Agent C | Claude, working with Walid. |
| Agent X | Codex, working with the classmate. |

There is no fixed engine/TUI ownership split. Both pairs work down this list in order and claim
whatever is next and unclaimed. Ownership is per task, not per package, which is what makes the
claim protocol below load bearing rather than bureaucracy.

### 1.2 Task states

| State | Meaning |
|---|---|
| todo | Unclaimed. Available to whoever gets there first, if its dependencies are satisfied. |
| claimed | Someone is actively working on it. Owner and branch must be filled in. |
| review | Implemented and pushed, acceptance demonstrated by the implementer, waiting on the other agent's independent check. |
| done | Both verification boxes ticked. |
| blocked | Cannot proceed. Notes must say why and what would unblock it. |

### 1.3 Claim protocol

This is what stops the two pairs colliding now that there is no file boundary between them.

1. Pick the topmost todo task whose dependencies are all done.
2. In the same commit that starts the work, set status to claimed, fill in owner and branch, and
   list the files or packages you expect to touch in scope.
3. Push that TASKS.md change before writing code. A claim that only exists on your machine is not
   a claim.
4. Never edit a file listed in another task's scope while that task is claimed or in review. If
   you have to, say so in notes and coordinate first.
5. If a claim goes stale (no commits on its branch for 48 hours), any agent may set it back to
   todo and note that they did.

### 1.4 Verification protocol

Each task carries two boxes:

```
verify: claude [ ]  codex [ ]
```

The implementer ticks their own box once they have run the acceptance criteria themselves and
watched them pass. That is a self check, not a rubber stamp.

The other agent ticks their box only after independently reviewing the change and confirming the
acceptance criteria, by reading the diff and re-running the check, not by trusting the first box.

Both sides test their own work before handing it over. Verification is not a substitute for that,
it is a second pair of eyes on work that already passed once. An implementer who ticks their box
without running anything has broken the protocol, not saved time.

Both boxes ticked means status done. One box ticked means status review.

If the second agent disagrees, do not untick the first box. Set status to blocked, write the
disagreement in notes, and let the supervisors settle it. Disagreements are signal. They are the
reason this protocol exists.

Tick a box with a date so the history stays readable:

```
verify: claude [x] 2026-07-26   codex [ ]
```

### 1.5 Phase gates

Each phase ends with a PG-n gate. It needs both supervisors to sign off in person, having watched
the phase demo run live. Agents may not tick a phase gate. This is the mechanism that keeps the
humans in control.

### 1.6 Editing etiquette

Every task is a self contained block so two agents editing different tasks produce a clean merge.
Keep it that way. Do not reflow or reorder blocks, do not add summary tables that duplicate task
state, and keep your edits inside the block you own.

---

## 2. Now board

Update this whenever you claim or release a task. It is the ten second answer to "what is everyone
doing right now".

| Agent | Current task | Branch | Blocker |
|---|---|---|---|
| Claude | P1-06, the headless harness | `feat/core-contract` | none |
| Codex | none | none | none |

PG-0 is signed by Walid and phase 1 is underway. P1-01 through P1-05 are done and in review on
`feat/core-contract`, so the contract and the fake both exist and the four scripted worktrees
behave. P0-01, P0-03, P0-04 and P0-07 are also in review. P0-02 needs the first pull request
before its acceptance can be shown, and P0-05 follows from that. P0-06, the prior art pass, is
unclaimed and open to either pair.

Codex: `internal/core` and `internal/core/fake` are on `feat/core-contract` and ready to build
against. **P1-07, the first dashboard, is the obvious thing to claim** and nothing blocks it, the
fake gives you four workspaces and a `Touch` method that turns a passing row stale. Read the
notes on P1-01, P1-03 and P1-04 first, each lists the judgement calls that are yours to overturn.

Integration cadence: no fixed calendar, see D-12. Short lived branches, merge main in before you
push.

---

## 3. Scope reminder

Read this before claiming anything.

v0.1 is an observe only verification cockpit. It discovers worktrees that already exist and proves
whether their test and service evidence is current for the exact code in them.

v0.1 does NOT: create or remove worktrees, delete branches, prune, spawn agents, attach PTYs,
infer agent state, commit, push, open PRs, merge, discard, start services, run setup
automatically, copy files automatically, restart processes automatically, persist across restart,
parse framework specific test counts, or support Windows.

If a task seems to need any of the above, it is mis-scoped. Stop and raise it.

---

# Phase 0: foundations and agreement

Goal: a repository that builds, tests and lints, with the product decisions recorded and both
pairs signed off on the corrected scope.

### P0-01 Repository scaffold
`status: review | owner: Claude | branch: main | depends: none`
`scope: go.mod, LICENSE, README.md, .gitignore, package skeleton`

Deliverable: MIT LICENSE, README.md stating the corrected positioning with no "only tool" claim,
.gitignore for Go and macOS, go.mod on Go 1.26, and the package directories matching the agreed
layout.

Acceptance: `go build ./...` and `go vet ./...` both succeed on a clean clone.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: build, vet, gofmt -l and go test all clean locally. Every package carries a doc.go stating
its responsibility and its v0.1 boundary, so the scope limits are visible from inside the code and
not only from this file. Module path is github.com/Walid-Idrissi-Labs/Canopy and stays provisional
until D-15 settles the name.

### P0-02 CI pipeline
`status: todo | owner: none | branch: none | depends: P0-01`
`scope: .github/workflows/ci.yml`

Deliverable: GitHub Actions running `go build ./...`, `go test ./...`, `go vet ./...`,
`gofmt -l` (failing on any output) and golangci-lint, across ubuntu-latest and macos-latest.

Acceptance: a pull request shows all five checks green, and a deliberately unformatted file makes
the pipeline fail.

`verify: claude [ ]   codex [ ]`

notes: the workflow is committed but acceptance is not demonstrated yet, since it needs a real
pull request to run. Deliberately left unticked. No .golangci.yml, so golangci-lint runs on its
defaults. Add a config later only if the defaults prove too loose.

### P0-03 DECISIONS.md
`status: review | owner: Claude | branch: main | depends: none`
`scope: DECISIONS.md`

Deliverable: all fifteen questions from the corrections document section 14 answered, plus an
explicit record of which review round gaps were resolved by citation and which are still open.

Acceptance: no question left unanswered, and every open item names who has to answer it.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: Codex should look hardest at D-05, D-06 and D-09. Those three resolve contradictions
between corrections sections 7, 8 and 9 by reading round 2 as confirmed, and if that reading is
wrong the scope is wrong. D-16 through D-19 are recorded as open on purpose rather than defaulted
quietly.

### P0-04 This task ledger
`status: review | owner: Claude | branch: main | depends: none`
`scope: TASKS.md`

Deliverable: ordered tasks for phases 0 to 6, the claim protocol, the dual verification protocol
and the phase gates.

Acceptance: Codex can read this file and correctly claim, implement and verify a task without
further instruction.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: acceptance is only really proven once Codex claims and completes a task through this
protocol without needing it explained. Treat the first Codex claim as the actual test of this
file, and change the protocol if it gets in the way.

### P0-05 Branch protection on main
`status: todo | owner: none | branch: none | depends: P0-02`
`scope: GitHub repository settings, no files`

Deliverable: main protected, no direct pushes, pull request required, one approving review
required, CI checks required to pass before merge.

Acceptance: a direct push to main is rejected by the server.

Needs a human. A supervisor has to do this in the GitHub UI.

`verify: claude [ ]   codex [ ]`

notes: bootstrap commits land on main before this is switched on. Enable it right after the
initial push.

### P0-06 Prior art inspection
`status: todo | owner: none | branch: none | depends: none`
`scope: internal notes only, NOT committed to this repository`

Deliverable: install or closely inspect Comux, Claude Squad, Conductor and Orca. Record what each
already does per worktree and what still needs manual work. Re-verify the Comux test and health
dashboard claim specifically, since the corrections document leans on it and round 2 could not
confirm it.

Acceptance: a written note exists in the internal uncommitted project folder covering all four
tools.

`verify: claude [ ]   codex [ ]`

notes: kept out of the repository on purpose, competitive analysis stays internal.

### P0-07 Name availability check
`status: review | owner: Walid | branch: main | depends: none`
`scope: DECISIONS.md, D-15`

Deliverable: check "Canopy" against GitHub org and repo names, Homebrew formula names, the Go
module namespace and npm, then decide keep or rename.

Acceptance: D-15 in DECISIONS.md says confirmed, or names the replacement.

Needs a human.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: Walid checked. Homebrew formula name is free, many unrelated Canopys on GitHub. Keeping the
name, see D-15. The module path github.com/Walid-Idrissi-Labs/Canopy is now settled rather than
provisional. Last cheap moment to rename is before the first tagged release.

### PG-0 Phase 0 gate
`status: review | depends: P0-01, P0-02, P0-03, P0-04, P0-05, P0-06, P0-07`

Both supervisors confirm together that they have read the corrections document and agree to the
corrected one liner, the v0.1 included and excluded lists, the truth and freshness state machine,
the repository trust behaviour, the worktree ownership states, the license, and the first
integration demo being "passing becomes stale after an edit".

`signed: walid [x] 2026-07-26   classmate [ ]`

notes: Walid gave the go ahead for phase 1 on 2026-07-26 and reports that the classmate and Codex
agree with the scope so far. The classmate's box is deliberately left for him to tick himself
rather than being ticked on his behalf.

Two dependencies are knowingly still open and phase 1 proceeds anyway, by supervisor decision.
P0-05, branch protection, cannot be configured until CI has run once on a pull request, since
GitHub only offers status checks it has already seen. P0-06, the prior art pass, is outstanding
and does not block writing the contract.

---

# Phase 1: contract and fake vertical slice

Goal: the shared types exist, the state machine is proven by tests, and the dashboard visibly
flips a row to stale, all before any real git or process code is written.

### P1-01 Core domain types
`status: review | owner: Claude | branch: feat/core-contract | depends: PG-0`
`scope: internal/core/*.go`

Deliverable: RevisionKey, WorkspaceSnapshot, WorkspaceOwnership, TestRun, TestState,
ServiceInstance, ServiceHealth, ProjectSnapshot, and a sequenced Event. States exactly as listed
in the corrections document sections 3.2 and 3.3, no extras and no omissions.

Acceptance: every state string in 3.2 and 3.3 has a constant, and a test asserts the full set so
it fails if one is added or removed without updating it.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: shared contract file. Per the collaboration rules, changing it later needs a short joint
design discussion, not a unilateral commit.

Four decisions in here are worth Codex arguing with, because they are judgement calls rather than
transcriptions of the corrections document:

1. `RevisionKey.Equal` returns false when either side is unknown, including unknown against
   unknown. If two unknowns compared equal, a result captured while the revision was uncomputable
   would keep matching forever and sit there green. Prefer a spurious stale over a spurious pass.
2. `Observation` is a three valued type instead of a bool, used for process liveness and
   readiness. A bool zero value is false, and false reads as "no", so an unfilled field would
   assert something we never observed.
3. Events carry no state payload, only a subject and a sequence. If an event carried its own copy
   of the state it could disagree with the snapshot, and then two things would each claim to be
   authoritative with no way to tell which was lying.
4. `Event.CoalesceKey` returns empty for final transitions, so they can never be dropped under
   load. Intermediate updates are safe to drop because the snapshot is authoritative.

Extra types beyond the corrections list: `Observation`, `DirtyState`, `TestSnapshot`,
`ServiceSnapshot`, `ProbeKind`, `ConfigState`, `TrustState`. The last two are v0.1 scope per
corrections section 8 items 12 and 13, and are here now so phase 3 is not a contract change.

### P1-02 Core interfaces
`status: review | owner: Claude | branch: feat/core-contract | depends: P1-01`
`scope: internal/core/interfaces.go`

Deliverable: WorkspaceSource, RevisionTracker, TestRunner, HealthChecker and SnapshotStore,
exactly as specified in the corrections document section 10.

Acceptance: compiles, and the fake from P1-05 satisfies all five.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: signatures are verbatim from section 10, nothing added. Do not design PTY, merge, PR or
worktree removal interfaces, explicitly forbidden at this stage. Second half of acceptance is
demonstrated by P1-05.

Error semantics are documented on each method rather than left to implementers, since they are
where a false green would sneak in. Three rules:

- `RevisionTracker.Current` returns the zero key plus an error on failure, never a partly filled
  key that would report itself as known.
- `TestRunner.Start` errors only when the run could not begin. A command that starts and fails is
  a successful Start followed by a run reaching failing. A missing binary is error, not failing.
- `HealthChecker.Check` returns a health with state unknown and a filled failure reason alongside
  its error, never a zero value, so a caller that ignores the error still holds something honest.

One interface is knowingly absent: reading a log buffer, which P2-10 needs. The buffer design
lands in P2-06 and guessing its shape now would be worse than adding it later. Treat it as a
contract change when it comes.

### P1-03 Test state transition rules
`status: review | owner: Claude | branch: feat/core-contract | depends: P1-01`
`scope: internal/core/transitions.go`

Deliverable: pure functions deciding the visible test state from a TestRun plus the current
RevisionKey.

Acceptance: table driven tests covering passing, failing, stale, error, unknown, cancelled and
not-configured, asserting that a run whose revision differs from current renders stale, that a
failed parser never turns exit code 0 into a failure, that a timeout is never passing, that a
cancelled run is never green, and that "no tests configured" never reads as "passed".

`verify: claude [x] 2026-07-26   codex [ ]`

notes: recorded run state and displayed state are kept apart. A run that exited zero stays
recorded as passing forever, because that is what happened. What the user sees depends on whether
that evidence still describes the current code. `ExplainTestState` returns a reason alongside
every verdict, including the unreachable paths, so the dashboard can always account for what it
shows.

There is an exhaustive test that walks every run state against every revision relationship and
fails if anything except a matching pass comes out green. That is the guard on the one claim this
product makes.

Four judgement calls for Codex to accept or overturn. None of them are transcriptions, and the
first is a possible hole in the contract itself:

1. **A configured test that has never run maps to `unknown`.** The section 3.2 vocabulary has no
   "not yet run" state. `not-configured` would be a lie, since it is configured, and `queued`
   would be a lie, since nothing was requested. `unknown` is the only honest fit and is correctly
   non-green, but it reads as "evidence cannot be trusted" when the truth is "there is no evidence
   yet". Those deserve different words in the UI. **This may mean the vocabulary is missing a
   state.** Flagging rather than adding one unilaterally, since it is a contract change.
2. **`failing` goes stale too, not just `passing`.** Section 3.2 says "the visible result becomes
   stale" without limiting it to passing, and a failure is equally a claim about specific code. If
   you edit to fix a failure, continuing to show FAIL asserts something we did not test. Section
   12 only requires passing to go stale, so this is a deliberate widening.
3. **`cancelled` and `error` do not go stale.** Neither produced a result, so there is nothing for
   a later edit to invalidate, and calling them stale would imply a result exists. Both stay
   non-green either way, so section 12 holds. This is the opposite direction to point 2 and the
   two should be judged together.
4. **A service reporting healthy without a successful readiness probe resolves to `unknown`.**
   Liveness proves a program exists, not that it works. This deliberately overrides what the
   probe layer claims rather than trusting it, on the grounds that health reported on liveness
   alone is one of the two easiest false greens here. The other is accepting a probe from an
   unrelated process on the same port, which is handled by the instance identity check.

### P1-04 Roll-up rules
`status: review | owner: Claude | branch: feat/core-contract | depends: P1-03`
`scope: internal/core/rollup.go`

Deliverable: the workspace level green indicator, implementing corrections section 3.4.

Acceptance: tests prove green requires all of the following: every required test passing, results
matching the current RevisionKey, every required service healthy, and no required evidence
unknown, stale or missing. Optional evidence (`required: false`) never blocks green. Tests and
services stay separately addressable.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: `Green` is computed from required evidence only, while the two aggregate columns report the
worst state across everything configured. They are separate fields because section 3.4 says a
single green icon must not hide which evidence is absent.

Three additions and calls for Codex:

1. **`Rollup.Caveat` is an addition beyond section 3.4.** It names non-blocking problems that
   exist even when green, such as a failing optional test. Without it there is a real hole: a user
   who marked a test optional months ago sees a green row forever and never learns it has been
   broken the whole time. That is exactly the failure section 3.4 warns about, arriving through
   the optional flag instead of through the icon.
2. **A workspace where nothing is marked required is not green.** Corrections says optional
   evidence does not block green, which read literally would make an all-optional workspace green
   with nothing verified at all. That is the product's central lie, an unconfigured worktree
   looking like a tested one, so it returns not green with the reason "nothing is marked
   required". Worth confirming this is the intended reading.
3. **Severity ordering is a product decision, not a transcription.** Tests rank failing, error,
   unknown, stale, running, queued, passing, not-configured. Services rank crashed, unhealthy,
   unknown, stopped, stopping, starting, healthy, not-configured. An unrecognised state outranks
   everything, on the grounds that a state we have never heard of should read as a problem rather
   than as fine. Argue the ordering if you disagree, particularly stale above running.

The reason lists every blocker rather than the first one found, since someone fixing a workspace
wants the whole list.

### P1-05 Fake snapshot store
`status: review | owner: Claude | branch: feat/core-contract | depends: P1-02, P1-04`
`scope: internal/core/fake/`

Deliverable: an in-memory implementation of all five interfaces, emitting four scripted worktrees
(one passing, one failing, one that goes stale on command, one unconfigured) plus a way to inject
a revision change event.

Acceptance: a test drives the fake through a revision change and observes the visible state flip
from passing to stale.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: this doubles as the test double for the whole project. Both pairs depend on it, so treat
breaking changes to it as contract changes. Compile time assertions in fake.go prove all five
interfaces are satisfied, which is the second half of P1-02's acceptance.

Event delivery is implemented for real rather than stubbed, because the coalescing rules are part
of the contract the UI gets written against. A fake that delivered everything in order through an
unbounded buffer would let the dashboard be built on behaviour the real store cannot provide, and
the gap would only surface under load. There is a test that fires a burst of 500 and asserts the
consumer sees fewer, and another that fires 100 final transitions and asserts every one arrives.

Driving API beyond the interfaces: `Touch` for an edit, `SetRevisionUnknown`, `SetServiceHealth`,
`SetOutcome`, `SetTrust`, `RemoveWorkspace`, `BeginRun` and `CompleteRun` for observing a run in
flight, and `SetClock`.

**Known gap for whoever takes P2-08.** `Events(afterSequence)` accepts the argument and ignores
it, because this store keeps no history to replay from. A consumer that falls behind has to
recover by taking a fresh snapshot. The real store needs a bounded history to honour the signature
properly, and until it does, the recovery property in P4-11 is only half proven.

### P1-06 Headless engine harness
`status: todo | owner: none | branch: none | depends: P1-02`
`scope: cmd/canopy/, debug subcommand`

Deliverable: a CLI that prints the current ProjectSnapshot as JSON and streams events, so the
engine is testable without the TUI.

Acceptance: running it against the fake prints four workspaces and streams a revision change
event.

`verify: claude [ ]   codex [ ]`

notes: required by collaboration rule 9, the engine has to be exercisable independently of the UI.

### P1-07 First dashboard against the fake
`status: todo | owner: none | branch: none | depends: P1-05`
`scope: internal/tui/`

Deliverable: a Bubble Tea dashboard listing the fake's workspaces with test state and freshness,
driven only through the SnapshotStore interface.

Acceptance: renders four rows, and imports no package other than internal/core.

`verify: claude [ ]   codex [ ]`

notes: none

### P1-08 Integration, the stale flip
`status: todo | owner: none | branch: none | depends: P1-06, P1-07`
`scope: internal/app/`

Deliverable: the wiring that gets a fake revision change event to the dashboard.

Acceptance: this is the phase 1 definition of done. With the TUI running, injecting a revision
change visibly turns the passing row stale, with no restart.

`verify: claude [ ]   codex [ ]`

notes: this is the first demo. Record it, it is also the first thing worth showing anyone.

### PG-1 Phase 1 gate
`status: todo | depends: P1-08`

Both supervisors watch the stale flip happen live and confirm the contract is stable enough to
build the real engine against.

`signed: walid [ ]   classmate [ ]`

notes: none

---

# Phase 2: real worktrees and test evidence

Goal: four real worktrees observed, one passing, one failing, one stale, one unconfigured, with no
ambiguous green state anywhere.

### P2-01 Worktree discovery
`status: todo | owner: none | branch: none | depends: PG-1`
`scope: internal/git/`

Deliverable: discover the primary checkout and every existing worktree via
`git worktree list --porcelain`. Assign stable workspace IDs. Assign ownership: primary for the
original checkout, external-read-only for everything else.

Acceptance: a temp repository with three added worktrees is discovered in full, the primary is
correctly identified, and no worktree is modified in any way.

`verify: claude [ ]   codex [ ]`

notes: the managed and adopted ownership states exist in the type but are unreachable in v0.1.

### P2-02 Branch, HEAD, dirty state, last activity
`status: todo | owner: none | branch: none | depends: P2-01`
`scope: internal/git/`

Deliverable: per worktree, the branch name (or detached HEAD), the HEAD SHA, dirty or clean, and a
last activity timestamp.

Acceptance: correct for a clean worktree, a dirty worktree, a worktree with only untracked files,
and a detached HEAD.

`verify: claude [ ]   codex [ ]`

notes: none

### P2-03 RevisionKey computation
`status: todo | owner: none | branch: none | depends: P2-02`
`scope: internal/git/`

Deliverable: HeadSHA plus DirtyDigest per corrections section 3.1, digesting `git diff --binary
HEAD`, the sorted output of `git ls-files --others --exclude-standard -z`, and a content hash per
untracked file.

Acceptance: the key changes when a tracked file is edited, when content is staged, and when a
non-ignored untracked file is added. It does not change when a git-ignored file changes. Symlinks
hash their target string and are not followed. Submodules contribute their HEAD SHA and are not
recursed into. An untracked file above `untracked_file_hash_limit_mb` (default 25) forces the
revision to unknown with a readable reason, never silently ignored and never green.

`verify: claude [ ]   codex [ ]`

notes: staleness is deliberately coarse and whole worktree. See D-16 in DECISIONS.md. Whether that
causes stale fatigue is flagged for Codex, and must not be "fixed" by adding an ignore list
without an explicit joint decision.

### P2-04 Revision poller
`status: todo | owner: none | branch: none | depends: P2-03`
`scope: internal/git/`

Deliverable: poll each worktree's revision on `observation.revision_poll_interval` (default 2s)
and emit a revision change event.

Acceptance: editing a file produces a revision change event within one poll interval, and polling
N worktrees does not saturate a CPU core.

`verify: claude [ ]   codex [ ]`

notes: none

### P2-05 Test runner
`status: todo | owner: none | branch: none | depends: P1-05`
`scope: internal/exec/`

Deliverable: execute a configured test command as an argv array in its own process group, inside
the worktree, capturing exit code and duration and recording the RevisionKey at start.

Acceptance: exit code 0 gives passing for the captured revision, non-zero gives failing, and a
command that cannot start (missing binary) gives error, never failing and never passing.

`verify: claude [ ]   codex [ ]`

notes: exit code is the only source of pass/fail truth in v0.1. No framework parsers.

### P2-06 Bounded output capture
`status: todo | owner: none | branch: none | depends: P2-05`
`scope: internal/exec/`

Deliverable: a bounded ring buffer per output source, default `logs.max_lines_per_source: 5000`,
keeping head and tail and marking dropped lines explicitly.

Acceptance: a command emitting a million lines neither exhausts memory nor blocks the producer,
the buffer states how many lines were dropped, and the final state transition is never dropped.

`verify: claude [ ]   codex [ ]`

notes: none

### P2-07 Cancellation
`status: todo | owner: none | branch: none | depends: P2-05`
`scope: internal/exec/`

Deliverable: cancel a running test with a graceful signal, a grace period, then termination of the
whole process group.

Acceptance: a test that spawns child processes leaves nothing behind after cancellation, verified
by process listing. A cancelled run is never green.

`verify: claude [ ]   codex [ ]`

notes: none

### P2-08 Authoritative snapshot store
`status: todo | owner: none | branch: none | depends: P1-04`
`scope: internal/store/`

Deliverable: one authoritative in-memory state store per corrections section 3.5, with immutable
snapshots for UI reads, monotonically increasing event sequence numbers, coalescing of replaceable
updates, separate bounded log buffers and explicit drop rules.

Acceptance: a consumer can discard its state, call Snapshot(), resume from Events(afterSequence)
and land in exactly the same state. No final state transition is ever coalesced away.

`verify: claude [ ]   codex [ ]`

notes: the event channel notifies, it does not own truth. Reviewers should check this
specifically.

### P2-09 Dashboard on real data
`status: todo | owner: none | branch: none | depends: P2-04, P2-08`
`scope: internal/tui/`

Deliverable: render real worktrees with branch, dirty state, test state, result age and freshness.

Acceptance: four real worktrees render correctly and update live.

`verify: claude [ ]   codex [ ]`

notes: none

### P2-10 Focused log view
`status: todo | owner: none | branch: none | depends: P2-09, P2-06`
`scope: internal/tui/`

Deliverable: open the captured output for a selected workspace's test run, scrollable.

Acceptance: the failure output for a failing run is readable in the UI, including the dropped line
marker when it was truncated.

`verify: claude [ ]   codex [ ]`

notes: none

### P2-11 Manual rerun
`status: todo | owner: none | branch: none | depends: P2-09, P2-05`
`scope: internal/tui/, internal/app/`

Deliverable: a keybinding that reruns the selected workspace's tests.

Acceptance: rerunning a stale workspace whose code has not changed returns it to passing, and
rerunning after a breaking edit returns failing.

`verify: claude [ ]   codex [ ]`

notes: manual triggering is the v0.1 default. Auto run on change is P4-13, not here.

### P2-12 Missing and invalid configuration in the UI
`status: todo | owner: none | branch: none | depends: P2-09`
`scope: internal/tui/`

Deliverable: clear states for "no configuration file", "configuration invalid" and "workspace has
no configured evidence".

Acceptance: none of the three ever renders as green or as failing, and each explains the next safe
action.

`verify: claude [ ]   codex [ ]`

notes: "no tests configured" being visibly distinct from "tests passed" is a core promise.

### P2-13 Integration, real stale flip
`status: todo | owner: none | branch: none | depends: P2-11`
`scope: internal/app/`

Deliverable: end to end freshness on real worktrees.

Acceptance: this is the phase 2 integration checkpoint. Run tests to green, edit a file in that
worktree, and the green result goes stale promptly without restarting Canopy.

`verify: claude [ ]   codex [ ]`

notes: none

### P2-14 The four worktree demo
`status: todo | owner: none | branch: none | depends: P2-13, P2-12`
`scope: demonstration only`

Deliverable: the target demo running on a real repository.

Acceptance: this is the phase 2 definition of done. Four real worktrees visible, one passing for
its current revision, one failing with useful output, one stale immediately after an edit, one
with no configured evidence. No ambiguous green anywhere.

`verify: claude [ ]   codex [ ]`

notes: this is the demo the whole project gets judged on. Record it.

### PG-2 Phase 2 gate
`status: todo | depends: P2-14`

Both supervisors watch the four worktree demo live and confirm no state is ambiguous.

`signed: walid [ ]   classmate [ ]`

notes: none

---

# Phase 3: configuration trust and service health

Goal: no project command ever runs without explicit approval, and a live but broken service is
visibly unhealthy.

### P3-01 Configuration schema v1
`status: todo | owner: none | branch: none | depends: PG-2`
`scope: internal/config/`

Deliverable: load and validate the schema from corrections section 9. schema_version required.
Unknown executable fields are errors, not warnings. Durations and port ranges validated before
anything runs. Template references resolve before execution. A relative cwd may not escape the
worktree.

Acceptance: each rule has a test with a fixture that fails validation for the right reason, and
`cwd: "../.."` is rejected.

`verify: claude [ ]   codex [ ]`

notes: tests is an array, with name, required, command, cwd, timeout and trigger, confirmed in
round 2 section 4.2. The required flag feeds the roll-up from P1-04.

### P3-02 Command representation and shell opt-in
`status: todo | owner: none | branch: none | depends: P3-01`
`scope: internal/config/`

Deliverable: command.argv is the default form, command.shell requires `allow_shell: true`, and
defining both forms is rejected.

Acceptance: tests cover argv only (accepted), shell without the flag (rejected), shell with the
flag (accepted, marked higher risk), and both forms present (rejected).

`verify: claude [ ]   codex [ ]`

notes: none

### P3-03 Secret handling
`status: todo | owner: none | branch: none | depends: P3-01`
`scope: internal/config/`

Deliverable: environment variables markable as secret, never printed in the UI or in logs that
Canopy formats.

Acceptance: a secret value appears nowhere in the trust screen, the service detail view, or any
captured log line Canopy renders.

`verify: claude [ ]   codex [ ]`

notes: Canopy cannot redact secrets a child process prints itself. Say so honestly in the README
rather than implying a guarantee.

### P3-04 Trust store
`status: todo | owner: none | branch: none | depends: P3-01`
`scope: internal/trust/`

Deliverable: trust decisions stored outside the repository, keyed by repository identity plus
configuration hash.

Acceptance: a freshly discovered repository executes nothing until approved, approval survives a
restart, and the trust record is not written inside the observed repository.

`verify: claude [ ]   codex [ ]`

notes: a worktree is file isolation, not a security sandbox. Canopy must never claim to sandbox
anything.

### P3-05 Trust invalidation
`status: todo | owner: none | branch: none | depends: P3-04`
`scope: internal/trust/`

Deliverable: changing any executable configuration field invalidates the previous approval.

Acceptance: editing a test command re-prompts, editing a non-executable field such as project.name
does not.

`verify: claude [ ]   codex [ ]`

notes: round 2 section 3.4 raised setup friction here, since repeatedly tweaking a test command
re-prompts every time. No low friction mode is approved yet, see D-17. Do not invent one
unilaterally.

### P3-06 Health probes
`status: todo | owner: none | branch: none | depends: PG-2`
`scope: internal/health/`

Deliverable: process alive, TCP connect and HTTP probes with expected_status, interval, timeout
and failure_threshold. Each check records checked_at, latency, failure reason and consecutive
failure count.

Acceptance: a running process failing its HTTP probe is unhealthy, not healthy. A probe timeout
records a useful reason. Process state and readiness state stay separately visible.

`verify: claude [ ]   codex [ ]`

notes: Canopy observes services in v0.1, it does not start them. `managed: false` is the only
supported value. See D-06.

### P3-07 Service identity binding
`status: todo | owner: none | branch: none | depends: P3-06`
`scope: internal/health/`

Deliverable: record workspace, service instance, PID and process group, port and start time, so a
response cannot be attributed to the wrong process.

Acceptance: a port occupied by an unrelated process is not accepted as proof that the configured
service is healthy.

`verify: claude [ ]   codex [ ]`

notes: this is one of the easiest ways to ship a false green. Reviewers should attack it directly.

### P3-08 Named ports, declaration only
`status: todo | owner: none | branch: none | depends: P3-01`
`scope: internal/config/`

Deliverable: named ports resolvable in templates, so `{{ ports.web }}` works in health URLs and
env values.

Acceptance: templates resolve before execution, and an unresolvable reference is a validation
error.

`verify: claude [ ]   codex [ ]`

notes: declaration only, there is no port allocator in v0.1. Canopy cannot allocate a port for a
process it does not start. The allocator, leases and bind collision recovery described in
corrections section 7 move to phase 6 alongside managed services. See D-06.

### P3-09 Required versus optional roll-up
`status: todo | owner: none | branch: none | depends: P3-06, P1-04`
`scope: internal/core/`

Deliverable: wire real service health into the roll-up.

Acceptance: an unhealthy required service blocks green, an unhealthy optional service does not and
is still visible.

`verify: claude [ ]   codex [ ]`

notes: none

### P3-10 Trust review screen
`status: todo | owner: none | branch: none | depends: P3-04`
`scope: internal/tui/`

Deliverable: show the fully resolved command, working directory and non-secret environment, and
require an explicit approval action.

Acceptance: nothing executes before approval, the screen shows the resolved command rather than
the template, and secrets are absent.

`verify: claude [ ]   codex [ ]`

notes: none

### P3-11 Process versus readiness display
`status: todo | owner: none | branch: none | depends: P3-06`
`scope: internal/tui/`

Deliverable: show "process alive" and "ready" as separate signals.

Acceptance: a running but unhealthy service is unmistakably distinguishable from both a healthy
one and a stopped one.

`verify: claude [ ]   codex [ ]`

notes: none

### P3-12 Service detail and error screens
`status: todo | owner: none | branch: none | depends: P3-11`
`scope: internal/tui/`

Deliverable: per service detail with last check time, latency, failure reason and consecutive
failures.

Acceptance: after a probe failure the reason is visible without leaving the TUI.

`verify: claude [ ]   codex [ ]`

notes: none

### PG-3 Phase 3 gate
`status: todo | depends: P3-05, P3-07, P3-09, P3-10, P3-12`

Both supervisors confirm that a changed executable configuration requires fresh approval, that a
live but unhealthy service reads as unhealthy, and that no command runs before trust.

`signed: walid [ ]   classmate [ ]`

notes: none

---

# Phase 4: robustness and acceptance

Goal: every acceptance test in corrections section 12 passes, on two real repositories.

### P4-01 Timeouts and process group termination
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/exec/`

Acceptance: a timeout terminates the correct process group and produces error or failing per the
documented setting, never passing. No orphans remain.

`verify: claude [ ]   codex [ ]`

notes: the timeout outcome setting has to be documented, not implicit. See D-18.

### P4-02 No dropped final transitions
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/store/`

Acceptance: under a flood of rapid events, no final state transition is lost or coalesced away,
verified by a stress test.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-03 Large output and backpressure
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/exec/, internal/store/`

Acceptance: enormous test output cannot freeze the UI or corrupt the final state.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-04 Paths and branch names with spaces
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/git/, internal/exec/`

Acceptance: a repository at a path containing spaces, and a branch name containing spaces, both
render and execute correctly.

`verify: claude [ ]   codex [ ]`

notes: the development folder itself contains spaces, so this is not hypothetical.

### P4-05 Externally removed worktrees
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/git/`

Acceptance: a worktree removed outside Canopy disappears from the dashboard safely, with no crash
and no stale row.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-06 Interrupted and crashed commands
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/exec/`

Acceptance: a killed or crashed command produces error, distinguishable from a real test failure.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-07 Cleanup hardening
`status: todo | owner: none | branch: none | depends: P4-01`
`scope: internal/exec/, cmd/canopy/`

Acceptance: quitting Canopy leaves no child processes behind, including during an in-flight test
run.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-08 Terminal resize and narrow layouts
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/tui/`

Acceptance: four or more worktrees stay readable in an 80 column terminal, and resizing does not
corrupt the frame.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-09 Distinguishable without color
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/tui/`

Acceptance: passing, failing, stale, unknown and not-configured are distinguishable with color
disabled (NO_COLOR=1), by glyph and word rather than hue.

`verify: claude [ ]   codex [ ]`

notes: accessibility is part of the truth contract here. A status nobody can read is a status
nobody can trust. Wording is fixed by D-10.

### P4-10 Selection stability under rapid updates
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/tui/`

Acceptance: rapid updates never leave the selected row pointing at a different workspace than the
one the user selected.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-11 Rebuild from a fresh snapshot
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/tui/, internal/store/`

Acceptance: the UI can discard all local state, request a snapshot and recover identically.
Restarting the TUI cannot resurrect a stale green state from an old event.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-12 Help and recovery states
`status: todo | owner: none | branch: none | depends: PG-3`
`scope: internal/tui/`

Acceptance: every error message explains the next safe action, and a keybinding help overlay
exists.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-13 Debounced auto run on change, opt-in
`status: todo | owner: none | branch: none | depends: P4-01, P4-02, P2-07`
`scope: internal/app/, internal/config/`

Deliverable: `trigger: on_change` as an opt-in per test setting, debounced, cancelling any
in-flight run for that test.

Acceptance: rapid successive edits produce exactly one run after the debounce window, and
cancelling the superseded run leaves no orphan processes.

`verify: claude [ ]   codex [ ]`

notes: blocked on a product decision, see D-19. The corrections document defers on_change until
debounce and cancellation are proven, while round 2 section 3.2 argues v0.1 may not clear the "why
not just run the tests myself" bar without it. Do not build this until the supervisors and Codex
settle whether it ships in v0.1 or after the pilot.

### P4-14 Acceptance test suite
`status: todo | owner: none | branch: none | depends: P4-01 through P4-12`
`scope: internal/**, testdata/`

Deliverable: an executable test for every checkbox in corrections section 12, covering truth and
freshness, tests, services, git and ownership, trust and configuration, and the TUI.

Acceptance: all section 12 boxes pass in CI. Any box that cannot be automated is listed explicitly
with a documented manual procedure.

`verify: claude [ ]   codex [ ]`

notes: this task is the real definition of "v0.1 works". Do not shortcut it.

### P4-15 End to end on two real repositories
`status: todo | owner: none | branch: none | depends: P4-14`
`scope: validation only`

Acceptance: the full flow verified on one Go repository and one Node or Python repository. See
D-04, the two repositories have to be chosen by the supervisors first.

`verify: claude [ ]   codex [ ]`

notes: none

### P4-16 Honest limitations document
`status: todo | owner: none | branch: none | depends: P4-15`
`scope: LIMITATIONS.md, README.md`

Deliverable: document what Canopy does not guarantee, including no sandboxing, no database or
dependency isolation, coarse whole worktree staleness, no framework specific test counts, secrets
printed by child processes not being redactable, and macOS and Linux only.

Acceptance: a reader can tell within a minute whether Canopy will lie to them, and how.

`verify: claude [ ]   codex [ ]`

notes: truthfulness is the product. Underclaiming in the docs is on brand, not a weakness.

### P4-17 Installable preview
`status: todo | owner: none | branch: none | depends: P4-16`
`scope: .github/workflows/, docs`

Acceptance: a stranger can install a preview build and reach a useful dashboard on their own
repository.

`verify: claude [ ]   codex [ ]`

notes: none

### PG-4 Phase 4 gate
`status: todo | depends: P4-14, P4-15, P4-16, P4-17`

Both supervisors confirm every acceptance criterion in corrections section 12 passes and that the
limitations document is honest.

`signed: walid [ ]   classmate [ ]`

notes: none

---

# Phase 5: pilot

Goal: find out whether developers actually care about the verification view.

### P5-01 Developer interviews
`status: todo | owner: none | branch: none | depends: none`

Deliverable: 8 to 12 interviews using the evidence based questions in corrections section 13. Ask
for demonstrations, not opinions.

Acceptance: written notes for each interview.

`verify: claude [ ]   codex [ ]`

notes: can start any time, it does not depend on the build. Starting early is better.

### P5-02 Recruit pilot users
`status: todo | owner: none | branch: none | depends: PG-4`

Acceptance: at least five people outside the two person team install it, preferably people already
keeping three or more worktrees active.

`verify: claude [ ]   codex [ ]`

notes: aspirational rather than blocking, see D-01. The goal is portfolio and learning.

### P5-03 Observed onboarding
`status: todo | owner: none | branch: none | depends: P5-02`

Acceptance: installation and configuration watched directly rather than instructed by message,
with setup time recorded.

`verify: claude [ ]   codex [ ]`

notes: none

### P5-04 Pilot report
`status: todo | owner: none | branch: none | depends: P5-03`

Deliverable: setup time, false states observed, real failures caught, repeat usage, feature
requests, and a continue, change or stop recommendation.

Acceptance: the false green count is recorded explicitly and is zero. Stars and compliments do not
count as usage.

`verify: claude [ ]   codex [ ]`

notes: none

### PG-5 Phase 5 gate
`status: todo | depends: P5-04`

Both supervisors decide together: continue, change direction, or stop.

`signed: walid [ ]   classmate [ ]`

notes: none

---

# Phase 6: conditional expansion

Locked until PG-5 is signed. After the pilot, pick exactly one repeatedly requested expansion. Do
not start several at once.

Candidates, deliberately left unelaborated so nobody starts building them early:

- managed service start and stop, including everything in corrections section 7: the port
  allocator, leases, bind collision recovery, startup timeouts, graceful shutdown, restart policy
- setup hooks and the ignored file allow-list
- automatic test on change, if D-19 has not already settled it
- notifications
- worktree lifecycle: create, remove, prune, adopt
- agent PTY and session management
- merge and pull request integration

### P6-01 Choose the single expansion
`status: blocked | owner: none | branch: none | depends: PG-5`

Acceptance: the pilot report names one expansion and the reason, it is recorded in DECISIONS.md,
and the remaining candidates stay unstarted.

`verify: claude [ ]   codex [ ]`

notes: blocked by design, not by an obstacle.

---

## Appendix: change log for this file

Append one line per structural change (adding, removing or reordering tasks). Do not log ordinary
status or verification updates.

| Date | Agent | Change |
|---|---|---|
| 2026-07-26 | Claude | Created ledger. Phases 0 to 6 derived from Canopy-Pre-Build-Corrections.md sections 11 and 12. |
