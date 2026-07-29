# Canopy Task Ledger

This file is the single source of truth for what is built, what is not, and what has been
verified. Both development pairs edit it. Nothing counts as done because it compiles. A task is
done only when its acceptance behaviour has been demonstrated.

Every agent changing this repository must also read `AGENTS.md`. It records the authority order,
the D-33 workspace and permission contract, and the files and tests that must move together when a
safety rule changes.

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
| partial | Some of the acceptance is met and the rest is not. Notes must say which half is which, or the word means nothing. |
| review | Implemented and pushed, acceptance demonstrated by the implementer, waiting on the other agent's independent check. |
| done | Both verification boxes ticked. |
| blocked | Cannot proceed. Notes must say why and what would unblock it. |
| deferred | Deliberately not in this release. Notes must say which release it is out of, why, and what exists already. Distinct from todo, which is work nobody has got to yet. |

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

### 1.7 Safety contract changes

D-33 is authoritative for direct workspaces, isolated worktrees, primary-checkout lifecycle safety,
and the shell boundary. These are cross-cutting rules, so a change to workspace selection, tool
kinds, trust levels, shell execution, worktree ownership, or approval scope is not complete when
only the code changes. The same branch must reconcile:

1. DECISIONS.md, including an explicit supersession when a settled rule changes.
2. Every affected acceptance block in this ledger.
3. README.md and LIMITATIONS.md, so the user-facing promise matches enforcement.
4. The direct/isolated by trust-level tests, including audit outcomes for refusals.

If any two disagree, set the affected task to `blocked`, record the exact disagreement, and ask the
supervisors. Do not infer a safety guarantee from a tool's working directory, and do not weaken one
because the current code has not implemented it.

---

## 2. Now board

Update this whenever you claim or release a task. It is the ten second answer to "what is everyone
doing right now".

**Rewritten 2026-07-28.** The previous board was three days stale in a way worth noting, because it
is the failure this section exists to prevent: it named branches that had already merged and quoted
counts of 69 review, 25 todo and three blocked when the real numbers were 80, 16 and one. A board
that is wrong is worse than no board, because it is read instead of the ledger.

| Agent | Current task | Branch | Blocker |
|---|---|---|---|
| Claude | U-15, the mode key settling on what it stops on, at Walid's direction. A8-05's visible hook-failure surface after it | `tui/mode-settle` | none |
| Codex | Independent verification of the unsigned lines, the eleven phase gates, the six product runs | `verify/independent-pass` | none |

### 2.0 Where this actually stands

Counted on this branch, after phases E and U were planned and U-15 built: 81 review, 39 todo, four
partial, one claimed, six deferred, **zero done**. Counted rather than carried over, because a board
quoting a number taken before the commit it sits in is the failure this section exists to prevent,
and that has now happened twice. The previous figures here, 84 review and 13 todo, were taken before
the two new phases existed and are what this recount replaces.

The number that matters is a different one. **77 task lines carry `claude [x]` and nine carry
`codex [x]`.** By the definition in section 1.2 that means one pair has built nine phases and the
other has independently checked almost none of them, and no amount of further building changes it. That is why
the split for this round is not another feature split: one side finishes the contract and safety
work, the other converts `review` into `done`, and only the second of those can produce the first
`done` this project has ever had.

Nothing reaches `done` on one signature. An agent may not sign its own work, which is the whole
mechanism, so the verification column is structurally not Claude's to fill.

### 2.1 File boundary for this round

Both pairs are building at the same time, so the hot files get a single owner rather than a
convention nobody can enforce. Recorded here because a previous merge stopped compiling in exactly
one of them.

| Pair | Owns |
|---|---|
| Claude and Walid | `internal/tui/**`, `internal/agent`, `internal/tools`, `internal/config`, `internal/exec`, `internal/hooks`, `internal/verify`, `cmd/canopy` |
| Codex and Ali | the verification pass, which touches only the `verify:` lines |

**`internal/tui/**` moved to Claude and Walid on 2026-07-28, by Walid's direction**, along with
A9-02. It had been the other pair's for the whole of the previous round. The reassignment is what
makes the mosaic view, the header bar and the rest of the interface work this pair's to do, and it is
recorded here rather than left to be inferred from who happened to touch the files.

`internal/core` is frozen for both sides. Changing it needs a joint discussion first, which is the
rule P1-01 already set.

**The hot file this round is `TASKS.md` rather than `app.go`**, because one pair is editing task
blocks to sign them and the other is editing the blocks it finishes. So the boundary runs inside a
task block: **Codex touches only the `verify:` line, Claude touches only `status:` and the body.**
Different lines in the same block, which git merges without a conflict.

Both asks that were outstanding to Codex are now this pair's own work, since the interface came with
them:

- **A place to show hook failures**, fed by `verification.HookFailures()`, which already exists and
  already returns them. A8-05 cannot leave `claimed` without it, because half of its acceptance is
  that a failing hook is visible and today one is only printed on exit.
- **A screen for A4-09's plan approval**, if that task is ever picked up. It is deferred for 0.1
  (D-40) and the engine behind it is built and unreachable.

Two conventions that follow from the merge on 2026-07-27, both still in force:

- **Merge `main` into your branch every day, not at the end.** `feat/permissions-and-confinement`
  was cut from `0bc7308` and never took `main` again, which turned what would have been several
  trivial merges into one manual resolution across four files.
- **On a conflict in DECISIONS.md, OPEN-QUESTIONS.md, or a task block, keep both sides.** They are
  additive prose and both changes are real. **In code, do not.** Keeping both sides of a conflict in
  `app.go` is what left an `else if` with no closing brace and failed gofmt, build, vet and test at
  once.

The dependency rule for this round, by supervisor decision: a dependency counts as satisfied at
`review`. Read literally, section 1.3 plus an unsigned PG-A7 makes every remaining task unclaimable,
which is not what the rule is for.

### 2.2 What is left before 0.1

1. **A8-05 remains claimed.** The hook loop is closed by D-39, but failing hooks still need a
   visible in-session surface instead of appearing only on exit.
2. **Q-18/D-38 needs both supervisors.** Servers now start once for the project, so isolated agents
   get no MCP tools. That is authoritative rather than proposed now that `mcp/hardening` has merged.
3. **The independent verification pass**, which is the whole of Codex's column above and the only
   route to a signed phase gate.
4. **A9-02**, interface robustness: 80 columns, resize, rapid updates, no colour, large output, and
   quit with several agents live.
5. **PG-M**, which is the release blocker. No tag until M-01, M-03 and M-06 are signed, and only the
   pair that did not build them can sign them.
6. **A clean-machine install**, and a first run with nobody coaching.

Outside the unfinished tasks named above, the remaining 0.1 scope is either implemented and waiting
for independent verification, or cut on purpose and recorded in D-40.

### 2.3 How the project got here

Kept because the ledger's shape does not explain itself, and a reader who does not know this reads
the phase names as arbitrary.

**Re-steered on 2026-07-26** (D-21 to D-23). Canopy is a coding agent harness focused on agentic
parallelism and git, not a worktree monitor. Phases 0 and 1 were unaffected. Old phases 2 to 6 were
replaced by A1 to A9, and every surviving task carries its original ID in its notes. The retired
tasks table is at the bottom of this file.

P0-01 to P0-07 and P1-01 to P1-07 carry forward: the core contract, the state machine, the roll-up,
the fake store, the headless harness and the dashboard. They are built and, like everything else
here, unsigned by the second pair.

**As of 2026-07-28** phases 0, 1, A1 through A7 and M are built and pass their own tests. The
verification engine is real: revision keys hash content, the poller feeds a per agent roll-up, tests
run per worktree, and agents are ranked on evidence or explicitly refused a placement. A8's
extensibility layer is built and, as of this round, actually reachable. What is not built is listed
in 2.2 and what is cut is listed in D-40.

Integration cadence: no fixed calendar, see D-12. Short lived branches, merge main in before you
push.

---


## 3. Scope reminder

Read this before claiming anything.

**Canopy is a terminal coding agent built for running several agents at once.** Provider keys go in
by name, agents are dispatched from the conversation, you watch and steer several at a time, and
Canopy knows which of them actually produced working code. Isolating an agent on its own worktree
and branch is an option you reach for when you want it, not the standard way agents run.

Same category as OpenCode and Claude Code, plus agentic parallelism and git as first class
concerns rather than afterthoughts.

| | | What it gets you |
|---|---|---|
| A1 | named keys, keychain, profiles | credentials exist |
| A2 | providers, streaming, cost, fallback | a real reply comes back, on any vendor |
| A3 | chat, persistence, compaction | **it looks and feels like the product** |
| A4 | tools, permissions, checkpoints, plan mode | it can change code, safely |
| A5 | many agents, dispatch, panes, steering | the differentiator |
| A6 | verification and ranking per agent | the thing no incumbent does |
| A7 | diff review, commits, conflict radar | finished work becomes clean history |
| A8 | sub agents, config, hooks, MCP, skills | the ceiling |
| A9 | robustness, docs, packaging | someone else can install it |

A3 is where the product becomes recognisable. A5 is where the worktree work becomes load bearing.
**A6 is a gate, not a stretch goal**, and is the only thing here no competitor does.

### Out of scope, and staying out

- No cloud, no account, no hosted control plane, no telemetry. Keys never leave the machine.
- No unattended merging. A human approves anything destructive.
- **Canopy never claims to sandbox what it runs.** There is a permission model. It is not
  containment, and saying otherwise would be the same class of error as a false green.
- Not an editor. It reads and writes code, it is not where you live.
- No Windows until process group and terminal semantics are designed for it, not approximated.

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
`status: review | owner: Claude | branch: main | depends: P0-01`
`scope: .github/workflows/ci.yml`

Deliverable: GitHub Actions running `go build ./...`, `go test ./...`, `go vet ./...`,
`gofmt -l` (failing on any output) and golangci-lint, across ubuntu-latest and macos-latest.

Acceptance: a pull request shows all five checks green, and a deliberately unformatted file makes
the pipeline fail.

`verify: claude [ ]   codex [ ]`

notes: first attempt failed on PR #1. The workflow pinned `golangci-lint-action@v6` with
`version: latest`, and latest now resolves to golangci-lint v2.12.2, which v6 cannot drive. It
died in seven seconds without linting anything. Fixed by moving to action v9 and pinning the tool
to v2.12.2. Both the action major and the tool version are pinned now, since an unpinned version
means CI can start failing on a day nobody touched the code.

That also uncovered eight real findings the failing job had been hiding, all unchecked write
errors in `cmd/canopy`. Fixed properly rather than excluded: an `errWriter` defers error handling
across a run of writes and reports the first failure once, and the two places where a write
genuinely cannot be acted on drop it explicitly at the call site.

`.golangci.yml` uses the v2 schema with the standard linter set. Deliberately **no exclusion for
unchecked write errors**, which is the usual thing to exclude. This project reports on other
people's processes and exists to not quietly lose information, so silently dropped writes are the
wrong default here.

Still unticked until CI is observed green on a pull request. Verified locally against the same
tool version: `golangci-lint run ./...` reports 0 issues.

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
`status: review | owner: Walid | branch: main | depends: P0-02`
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
`status: review | owner: Claude | branch: feat/core-contract | depends: P1-02`
`scope: cmd/canopy/`

Deliverable: a CLI that prints the current ProjectSnapshot as JSON and streams events, so the
engine is testable without the TUI.

Acceptance: running it against the fake prints four workspaces and streams a revision change
event.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: required by collaboration rule 9, the engine has to be exercisable independently of the UI.

Three commands. `canopy snapshot` prints the project as JSON, `canopy watch` streams events as
JSON lines, `canopy demo` drives the stale flip and prints a before and after table.

`canopy demo` is worth running before touching anything else. It is the entire product argument in
one screen, and it currently works:

```
before the edit
  WORKSPACE     BRANCH        REVISION      TESTS           SERVICES        VERIFIED
  feat-login    feat/login    a1b2c3d       passing 1/1     healthy 1/1     yes
  fix-cache     fix/cache     b2c3d4e       failing 0/1     healthy 1/1     no
  refactor-api  refactor/api  c3d4e5f       passing 1/1     not-configured  yes
  spike-search  spike/search  d4e5f6a       not-configured  not-configured  no

after the edit
  refactor-api  refactor/api  c3d4e5f+edit  stale 0/1       not-configured  no
```

The JSON reports **derived** state, not stored state. Printing what is on the record would show a
run marked passing and leave the reader to work out that it no longer applies, which is the
confusion this product exists to remove. Every test carries its state, its reason, and the
revision it actually covered. Every service reports liveness and readiness separately.

`-source` exists and rejects anything except `fake`, so nothing silently falls back to fake data
once real discovery lands in P2-01.

### P1-07 First dashboard against the fake
`status: review | owner: Claude | branch: feat/dashboard | depends: P1-05`
`scope: internal/tui/, cmd/canopy/`

Deliverable: a Bubble Tea dashboard listing the fake's workspaces with test state and freshness,
driven only through the SnapshotStore interface.

Acceptance: renders four rows, and imports no package other than internal/core.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: first dependencies land here, bubbletea v1.3.10 and lipgloss v1.1.0. `canopy` with no
arguments opens the dashboard, `canopy dashboard` does the same explicitly.

The import rule is enforced by a test that parses the package's own non-test files and fails on
any internal import except `internal/core`. Worth having because the first accidental import will
compile perfectly well, and the whole value of the contract is that this package cannot tell the
fake from the real engine.

Decisions in here worth Codex's attention:

1. **Selection is held by workspace ID, not row index.** Worktrees appear and disappear outside
   Canopy, so an index quietly starts pointing at a different workspace than the user chose.
   Acting on the wrong workspace is about the worst bug this product could have. That is P4-10's
   acceptance criterion met early, and it has a test.
2. **The selected row explains itself.** The roll-up reason is rendered under the table, so a
   verdict is never shown without the evidence that produced it. The caveat line surfaces failing
   optional evidence that a green row would otherwise hide.
3. **Verified reads YES or NO, not a bare tick.** A tick alone invites the eye to see a green
   shape and stop looking. The word pushes the reader on to the columns that say why.
4. **Column widths are fixed and budgeted to 80 columns**, so a state change can never shift a
   column and make the table twitch. There is a test asserting no line exceeds 80, which caught
   the layout being 81 wide and hiding it by truncating the `VERIFIED` heading to `VERI...`.

Two things fixed while building this rather than deferred:

- `canopy` with no arguments falls back to printing usage when stdout is not a terminal. It
  previously failed with "could not open a new TTY: device not configured", which tells a reader
  nothing about what they did.
- The D-10 glyph table in DECISIONS.md was ASCII placeholders. Updated to what actually shipped.

### P1-08 Integration, the stale flip
`status: review | owner: Claude | branch: feat/verification-and-release | depends: P1-06, P1-07`
`scope: internal/app/`

Deliverable: the wiring that gets a fake revision change event to the dashboard.

Acceptance: this is the phase 1 definition of done. With the TUI running, injecting a revision
change visibly turns the passing row stale, with no restart.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: this is the first demo. Record it, it is also the first thing worth showing anyone.

Was shipped and left marked todo. `canopy demo` prints the flip in one screen, and the running
interface does it live: the chat command touches a workspace on a timer and the dashboard turns that
row stale with no restart. Both paths were checked on 2026-07-27.

The real version arrived later and is A6-02, where the revision change comes from a worktree
somebody actually edited rather than from the fake.

### PG-1 Phase 1 gate
`status: todo | depends: P1-08`

Both supervisors watch the stale flip happen live and confirm the contract is stable enough to
build the real engine against.

`signed: walid [ ]   classmate [ ]`

notes: none

---

# Phase A1: keys and profiles

Goal: Canopy holds provider credentials by name, and can be trusted with them.

Nothing talks to a provider yet. Keys come first because everything depends on them, and because
credential handling is genuinely unpleasant to retrofit.

### A1-01 Provider, key and profile types
`status: review | owner: Claude | branch: feat/keys-and-profiles | depends: none`
`scope: internal/core/`

Deliverable: `Provider`, `KeyRef`, `KeyMetadata`, `TrustLevel` and `AgentProfile`. A profile binds
a name to a provider, key, model, system prompt, tool set and trust level, so `claude` or `kimi`
resolves to everything needed to start an agent.

Acceptance: `KeyRef` is structurally incapable of holding a secret, enforced by the type rather
than by convention. Round tripping a profile through JSON never produces one.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: shared contract, so changes need a joint discussion. The reference/secret split is the whole
design. If the secret is never in the type that gets logged, serialised, snapshotted or put in an
event, it cannot leak through any of them.

`core.Secret` closes every route Go offers for turning a value into text: `String` for `%s` and
`%v`, `Format` for the verbs that bypass Stringer such as `%q` and `%x`, `GoString` for `%#v`, and
`MarshalJSON` for anything encoded. `Reveal()` is the only way out and is deliberately conspicuous
so a reviewer can grep for it. `UnmarshalJSON` refuses, which removes the supported path for
putting a credential into a config file somebody then commits.

Two tests are the real deliverable. One plants a credential and asserts it appears in none of
sixteen renderings and five JSON encodings. The other walks the field graph of every published
type, `KeyRef`, `KeyMetadata`, `AgentProfile`, `WorkspaceSnapshot`, `ProjectSnapshot`, `TestRun`,
`ServiceHealth` and `Event`, and fails if any of them can reach a `Secret`.

Three calls for Codex:

1. **Key names are constrained to `^[a-z0-9][a-z0-9_-]{0,30}$`.** This is a safety feature rather
   than tidiness. A name is displayed, logged, put into events and written into transcripts, so a
   key named after its own value would travel everywhere the name does. Real credentials fail the
   pattern, so a paste becomes an error rather than a permanent leak, and the rejection message
   truncates what it rejected because errors get logged.
2. **Four trust levels**, read-only, confined, standard and broad, with standard as the default.
   Unknown values resolve to read-only rather than the default, so a typo in a config file reduces
   what an agent can do instead of quietly granting the usual amount.
3. **Workspace mode and trust level are separate decisions.** A direct agent uses the repository
   where Canopy started, which may be the primary checkout. An isolated agent's structured tools
   are rooted at its Canopy-owned worktree, and no trust level widens those tools into another
   workspace. Shell is the explicit non-contained exception in D-33.

### A1-02 Key store on the OS keychain
`status: review | owner: Claude | branch: feat/keys-and-profiles | depends: A1-01`
`scope: internal/keys/`

Deliverable: macOS Keychain and Linux secret service. A file backend exists only as an explicit,
loudly warned opt-in.

Acceptance: a key survives a restart, is absent from any config file, and the file backend refuses
to run unless named explicitly.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: verified against the real macOS Keychain, not only the fake. A key was added, read back from
a fresh process, and then removed, and the keychain was checked afterwards to confirm nothing was
left behind.

Secrets and metadata are stored separately, which is deliberate. The OS credential store protects
a value and cannot hold structure; a JSON file is the opposite. The cost is that they can disagree,
so that case is handled explicitly: a key whose metadata exists but whose secret has vanished
reports exactly that and names the fix, rather than reporting a plain absence and sending the user
looking for something they can still see in `keys list`.

Writes go secret first, then metadata. If the second step fails there is an orphaned secret nobody
can reach, which is harmless. The other order would leave metadata claiming a credential exists
when it does not, which is a lie the rest of the system would act on. Both files are written
atomically and are mode 0600.

Checked rather than assumed: on macOS the underlying library pipes its command through the
`security` binary's **stdin** rather than passing the credential as an argument, so the value never
appears in the process list.

### A1-03 Key and profile commands
`status: review | owner: Claude | branch: feat/keys-and-profiles | depends: A1-02`

`scope: cmd/canopy/`

Deliverable: `canopy keys add|list|remove|test` and `canopy profiles list|show`. Adding reads from
a prompt or stdin.

Acceptance: no command prints a secret. `keys list` shows name, provider, created date and a
fingerprint. A key passed as a command line argument is refused with an explanation.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: refusing `--key sk-...` matters because arguments land in shell history and the process
list, where any other user on the machine can read them.

Flags such as `-key`, `-secret`, `-token` and `-password` are **defined only so they can be
refused with an explanation**. Left undefined they would produce "flag provided but not defined",
which tells someone they made a typo rather than that they were about to write a credential into
their shell history.

Three refusal paths, all with tests: a value as a second positional argument, a value in one of
those flags, and a value used as the name. The last one says "that looks like a credential" rather
than "too long", and every rejection truncates what it rejected, because errors get logged too.

One real bug found while testing: Go's `flag` stops parsing at the first positional, so
`keys add kimi -provider openai-compatible`, which is both the natural form and the one in the
help text, was failing. The name is now taken off the front before flags are parsed, and both
orders work.

`keys test` recomputes the fingerprint from what came back rather than trusting the record, so a
credential changed outside Canopy is caught instead of used. It says plainly that it checks storage
only, since no provider client exists until A2.

### A1-04 Redaction guarantees
`status: review | owner: Claude | branch: feat/keys-and-profiles | depends: A1-03`

`scope: cmd/canopy/, internal/keys/, internal/core/`

Deliverable: a test suite proving secrets cannot reach output Canopy controls.

Acceptance: a planted secret is absent from snapshot JSON, the event stream, log buffers, rendered
frames, error messages and panic output. Provider errors report the failure without echoing the
credential.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: a canary is planted once and every surface Canopy controls is searched for it: command
output, error text including wrapped and `%q` formatted errors, snapshot JSON including a
deliberately smuggled secret, and rendered dashboard frames with the colour stripped.

The point of doing it here rather than only per package is that each package's own redaction test
already passed. What this catches is a credential reaching a surface because two individually
correct components were joined, which is how it actually happens.

**It found something.** Free text fields, a revision error or a probe failure reason, render
verbatim. The realistic version is a provider replying "invalid x-api-key: sk-ant-..." and Canopy
putting it on screen and into any screenshot of it.

Fixed at the provider boundary rather than in the renderer, and added as an acceptance criterion
on **A2-03**. Scrubbing at render time would mean loading every stored credential so the rendering
path could search for it, which is secrets travelling further in order to be protected. At the
provider boundary the credential is already in scope, so the scrub is local and complete.

`TestFreeTextFieldsAreNotScrubbed` asserts the current boundary deliberately, and will fail when
A2-03 lands. That is intentional: it forces the limitation to be re-examined rather than quietly
outliving the documentation that describes it.

### A1-05 Key management in the TUI
`status: review | owner: Claude | branch: feat/keys-tui | depends: A1-03`
`scope: internal/tui/keys/, internal/tui/, cmd/canopy/`

Deliverable: add, list, test and remove credentials without leaving the interface. Reachable from
the dashboard, and shown on first run when no credentials exist yet.

Acceptance: a key can be added entirely in the TUI, with the value masked as it is typed and never
present in any rendered frame. The provider is chosen from a list rather than typed. A base URL is
requested only for providers that need one. Removal confirms first. On first run with no keys, the
interface says so and offers to add one rather than presenting an empty dashboard.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: **added 2026-07-26 at Walid's request**, after A1 was otherwise finished. Recorded as a new
task rather than folded into A1-03 so the change is visible.

`internal/tui/keys` takes a narrow `Store` interface with no method that returns a secret value, so
no mistake in that package can render one. The test types the canary a character at a time and
checks every intermediate frame, because the leak that matters is the one visible mid keystroke,
not the one at the end.

The name field masks itself the moment its contents look like a credential, and is cleared on
rejection, since the likeliest explanation for a key in the name field is a paste into the wrong
place and it should not sit on screen while the user works that out.

`K` opens the screen from the dashboard. Lowercase `k` only does so when the dashboard is empty,
because it is otherwise move-up and hijacking it would throw a navigating user onto another screen.

Three bugs found by the tests rather than by luck:

1. **Value receiver aliasing.** The shared text field handler took `&m.draftName` from the caller
   while returning its own copy, so every keystroke was silently discarded. Fixed by moving the
   handlers to pointer receivers, which removes the class rather than the instance.
2. **The dashboard was quitting on esc while typing.** Keys were forwarded to every screen, so
   cancelling a field exited the program. Keystrokes now go only to the screen in front, while
   engine events still reach both, so the dashboard is not stale when you return to it.
3. **The architecture test was too strict.** It forbade the interface importing anything but
   `core`, which caught `app.go` composing screens. The rule was wrong, not the code: the real
   constraint is no *engine* packages. Sibling `internal/tui/*` imports say nothing about where
   state comes from. Narrowed deliberately rather than deleted.

The CLI stays. Piping from a password manager is a real workflow and a TUI cannot replace it.

The masked input field is the risk here. The value exists as a plain string in the model while
being typed, which is the one place in this design where that is unavoidable, so it is cleared as
soon as it reaches the store and the redaction suite from A1-04 is extended to cover the frames
rendered while typing.

### PG-A1 Phase A1 gate
`status: todo | depends: A1-01, A1-02, A1-03, A1-04, A1-05`

Both supervisors add a key, restart, confirm it survived, and fail to find it anywhere in Canopy's
own output. Adding is done once from the command line and once from the TUI.

`signed: walid [ ]   classmate [ ]`

---

# Phase A2: providers

Goal: a real message reaches a real provider and a real reply streams back, on more than one
vendor.

### A2-01 Provider interface
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A1-01`
`scope: internal/core/`

Deliverable: the interface an agent session talks to. Streaming, cancellable, provider agnostic,
with tool use in the shape from the start.

Acceptance: compiles, and a fake provider satisfies it and can script a reply.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: designing the tool use shape before tools exist is deliberate, unlike the PTY interfaces we
correctly refused to design early. Those were speculative. Tools are a certainty two phases out,
and retrofitting tool calls into a streaming protocol is genuinely painful.

Checked against the current API reference rather than written from memory, which changed two
decisions. Both are recorded as D-30 and D-31.

**The interface is called `ProviderClient`, not `Provider`.** `Provider` is already the vendor enum
on `KeyRef` from A1-01, and two things called Provider in one package would be a coin flip at every
call site.

Four contract rules that are not obvious and produce a 400 or a crash rather than a worse answer:

1. **`refusal` is a stop reason, not an error.** A declined request returns success with possibly
   empty content, so `StopReason` has to be checked before reading content. There is a test
   asserting it never appears in the error vocabulary too, or callers would handle it twice.
2. **Sampling parameters are rejected by current models.** `AgentProfile.Temperature` exists from
   A1-01, and the provider layer is where it gets dropped rather than sent.
3. **Thinking depth is effort, not a token budget.** A budget is rejected, and thinking is on by
   default, so `MaxTokens` sized for the answer alone truncates.
4. **`AllowsFallback` is deliberately narrower than `Retryable`.** A wrong key must never route to
   another credential: the user would be billed elsewhere, possibly answered by a weaker model, and
   never told the key was wrong. A network blip is retryable but says nothing about the credential.

`Usage.CostKnown` distinguishes "free" from "we could not price this", and an unknown cost poisons
any total it is summed into. A partial sum shown as a figure is a wrong number on screen, which is
worse than an absent one.

### A2-02 Anthropic client
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A2-01, A1-02`
`scope: internal/provider/anthropic/`

Deliverable: the Messages API with streaming, using a named key.

Acceptance: a real request returns a streamed reply. Cancellation stops the stream and releases the
connection. Recorded fixtures let tests run without network or credentials.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: model ids and parameter names were checked against the SDK source and the current API
reference at implementation time, not recalled. `claude-opus-5` is the default. The effort constants
were read out of the SDK rather than guessed.

Built on the official SDK per D-30. The credential is revealed once, in `New`, and handed straight
to the SDK, which is the entire window in which it exists as an ordinary string here.

**The A1-04 finding is fixed here.** Provider error text is scrubbed of the credential before it
leaves the package, so a reply of "invalid x-api-key: sk-ant-..." cannot reach the screen. This is
the right layer: the credential is already in scope, so the scrub is local and complete, whereas
doing it at render time would mean loading every stored key so the renderer could search for it.
Tested with a planted canary.

Four things worth Codex's attention:

1. **Sampling parameters are dropped here, not trusted to callers.** `AgentProfile.Temperature`
   exists from A1-01 and current models reject it with a 400. The test asserts on the marshalled
   request body rather than the struct, because the body is what actually goes over the wire.
2. **Thinking is only mentioned when disabled.** It is on by default on current models, so a
   request that says nothing gets thinking. `DefaultMaxTokens` is 32000 because the cap covers
   thinking and answer together, and a value sized for the answer alone truncates mid sentence.
3. **Tool calls are emitted only once complete**, at the end of the stream, while text streams
   live. A partially received tool input is not something a caller can act on.
4. **The done event always fires**, on every path including failure and cancellation. Without it a
   failed stream is indistinguishable from one still running, and usage already billed goes
   unaccounted for.

One real bug found while testing: the SDK's `Error` method dereferences the HTTP response it was
built from, so an error constructed without one panics. That would turn a provider failure into a
lost session and everything in it. `safeMessage` guards it and falls back to naming the status.

### A2-03 Error taxonomy
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A2-02`
`scope: internal/core/, internal/provider/`

Deliverable: provider failures mapped to distinct states: authentication, rate limited, overloaded,
context length exceeded, network, cancelled, unknown.

Acceptance: each has a test and a message naming the next useful action. A rate limit is never
reported like a bad key.

**Provider error text is scrubbed of the credential before it leaves this package.** A planted key
does not appear in any error surfaced from a provider failure, and there is a test for it.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: same discipline as the test state vocabulary. "Something went wrong" is the agent equivalent
of a status nobody can act on. The vocabulary lives in `core` from A2-01 and the mapping in the
Anthropic client from A2-02, so this task was satisfied by those two rather than adding a third
layer.

Each class carries a message naming the next action, tested. A rate limit and a rejected key read
completely differently, because sending someone hunting for a bad key when they were merely rate
limited wastes real time.

The scrubbing requirement came out of A1-04, which found that free text fields render verbatim.
The realistic leak is a provider replying "invalid x-api-key: sk-ant-..." and Canopy putting it on
screen and into any screenshot of it.

It belongs here rather than in the renderer. Scrubbing at render time would mean loading every
stored credential so the rendering path could search for it, which is secrets travelling further
in order to be protected. At this boundary the credential is already in scope, so the scrub is
local and complete. `TestFreeTextFieldsAreNotScrubbed` in `cmd/canopy` will fail when this lands,
and should then be narrowed to the fields this package does not own.

### A2-04 One shot ask
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A2-02`
`scope: cmd/canopy/`

Deliverable: `canopy ask "..."` streams a reply to stdout.

Acceptance: works against a real key, streams rather than buffering, exits non zero on failure,
cancels cleanly on interrupt.

`verify: claude [ ]   codex [ ]`

notes: **deliberately unticked.** Everything except "works against a real key" is built and tested
against a scripted stream. The real network call needs a credential, which is the supervisors' to
make at PG-A2. That is the honest state, not a formality.

The command is the smallest proof the whole pipe works, and worth keeping permanently as a way to
check a key or a model without opening the interface.

Four decisions in the output handling:

1. **The stop reason is checked before anything is presented as an answer.** A refusal arrives as
   a successful response with possibly empty content, so printing the text and exiting zero would
   present a declined request as an answered one.
2. **A truncated reply exits non zero.** It looks complete on screen, which is the whole problem,
   so the exit status is the only thing distinguishing it. The partial is still shown, since it
   was paid for.
3. **A stream that ends with no done event is an error**, not a success. That is a bug in a
   provider adapter, and exiting zero on an answer nobody received would hide it.
4. **Cost prints only when known.** A zero rendered as a dollar figure reads as "this was free",
   which is a different claim from "we could not price it". Pricing lands in A2-05.

With several usable credentials it refuses and lists them rather than picking one. Silently
choosing which key gets billed is not a decision to make on someone's behalf.

### A2-05 Usage and cost accounting
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A2-02`
`scope: internal/pricing/, internal/core/provider.go, cmd/canopy/ask.go`

Deliverable: tokens in, out and cached, plus cost, per request, attributed to key and agent.

Acceptance: usage matches what the provider reported. Cost comes from a versioned, dated pricing
table, and the interface says so when the table is old.

`verify: claude [x]   codex [ ]`

notes: exact rather than inferred, because Canopy makes the request. A stale pricing table is a way
to put a wrong number on screen, which is why it carries its date.

`internal/pricing` holds the table, dated by `AsOf` and announced as approximate once past `MaxAge`.
Old numbers keep being used, since an old figure beats no figure; they just stop being presented as
current.

Three things worth knowing about the shape it took:

1. **Rates are only recorded where the endpoint determines the price.** Anthropic first party, and
   local runtimes which are genuinely free. Nothing else in the OpenAI compatible family is priced
   by model name, because the gateway sets the price and there are many gateways: pricing
   `anthropic/claude-opus-5` at Anthropic's rate when it was reached through OpenRouter would be a
   guess presented as a fact. Unpriced turns say which endpoint has no rate, so "cost unknown" reads
   as a gap in the table rather than as a broken tool. **This means the NVIDIA key shows no cost
   figure at PG-A2.** A2-09 is the fix; see the note there.
2. **Free and unpriced are different claims and Canopy makes both.** A local model bills nothing,
   which is a known cost of zero. That is the whole reason `CostKnown` exists next to `CostUSD`.
3. **`Usage.Add` has no identity element, and `core.Sum` exists because of it.** `Usage{}` cannot
   serve as one: an empty running total and a turn nobody could price are the same value, so
   folding a list from zero would mark every total unpriced, and a session of perfectly priced
   turns would report its cost as unknown. Found while wiring this up, and it would have been
   invisible until a per session total appeared on screen reading "cost unknown" for no reason.

Cache reads and writes are derived from the input rate by multiplier rather than typed out per
model, since they are properties of the API rather than of any one model, and a hand copied cache
column is somewhere for a typo to hide. Where a published introductory rate undercuts the standard
one, the standard rate is used: overstating cost is the safer error, because understating hides
spend that is really happening.

### A2-09 User supplied prices
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A2-05`
`scope: internal/keys/, internal/pricing/, internal/core/key.go, cmd/canopy/keys.go`

Deliverable: a rate can be attached to a stored credential, so an endpoint Canopy has no table entry
for still shows a real cost.

Acceptance: setting a rate on a key produces a figure on screen. A key with no rate still reads as
unpriced rather than free. The user's own figure is labelled as theirs, never as a checked one.

`verify: claude [x]   codex [ ]`

notes: **added 2026-07-26.** Falls straight out of A2-05. Canopy will never hold rates for every
gateway in the OpenAI compatible family and should not pretend to, but the person who signed up for
the gateway knows what they pay. This turns "we cannot price this" into "tell us once". Distinguish
their figure from a checked one in the interface: the point of the dated table is that Canopy is
honest about where a number came from, and quietly absorbing a user's rate into it would throw that
away.

`pricing.Source` is the three states, all distinguishable on screen: a checked price, the user's
price, and no price. A user rate wins over the table where both exist, because they are the one
being billed and theirs answers the question actually being asked, which is "what will this cost
me" rather than "what is the list price".

Three decisions:

- **`canopy keys rate` is its own command, not a flag on `add`.** Correcting a price must not
  require re typing a secret, and a flow that asks for one is a flow where people paste keys into
  shell history.
- **Rotating a key keeps its rate.** The endpoint charges what it charges regardless of which
  credential reaches it, and dropping the price would turn a working cost figure into "unknown" for
  no reason the user could see.
- **An unstated cache rate is assumed to be full price.** Most gateways in this family either do not
  cache or do not say what they charge for it, so assuming a discount nobody promised would
  understate the bill.

A rate of zero is refused, because it is a claim rather than an absence: it would report every turn
as free. Somebody who really pays nothing should leave it unset and let it read as unpriced, or use
a local endpoint, which Canopy already knows is free. **This may be wrong for the NVIDIA free tier**,
which genuinely bills nothing at personal volumes. See Q-01.

Verified against a real endpoint: `canopy keys rate nim -in 0.30 -out 1.20` then `canopy ask` showed
`$0.0005` with "priced at your own rate for this key". The rate was cleared afterwards rather than
left on the key, since a figure Canopy invented on somebody's behalf is exactly what D-32 exists to
prevent.

### A2-06 OpenAI compatible provider and local models
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A2-02`
`scope: internal/provider/openai/, cmd/canopy/ask.go`

Deliverable: the chat completions API with tool calls and streaming, and a configurable base URL.

Acceptance: the same agent code runs unchanged on both providers. A base URL override reaches a non
OpenAI endpoint. Ollama and one hosted third party both work.

`verify: claude [x]   codex [ ]`

notes: one task covers most of the field. Kimi, MiniMax, DeepSeek, Groq, OpenRouter and most local
runtimes speak this API. Building it second, rather than after five Anthropic only phases, is what
stops provider assumptions baking into the agent loop. Tool calling differs in shape between the
two APIs but not in meaning, and that difference belongs behind the interface, never in
`internal/agent`.

Hand rolled, unlike A2-02, per D-30. The surface needed is small, and pointing an SDK written for
one vendor at arbitrary base URLs is the case those SDKs handle worst.

Four things this had to get right that the Anthropic client did not:

1. **Tool calls arrive as fragments.** An index, sometimes an id, sometimes a name, and the
   arguments a few characters at a time across many chunks. They are accumulated and emitted whole,
   because half a JSON argument string is not something a caller can act on. Emitted in index
   order, because map iteration is random and a caller executing them should see them in the order
   the model asked for.
2. **`content_filter` is this family's refusal**, and like Anthropic's it arrives on a successful
   response. Mapped to `StopRefusal`, so a declined request is never presented as an answered one.
3. **Usage has to be asked for.** Without `stream_options.include_usage` most implementations
   report nothing at all on a streamed request, and a turn with no usage cannot be costed.
4. **Effort and sampling parameters are deliberately not sent.** There is no effort field this
   family agrees on and several reject unknown fields outright, which would break exactly the
   providers this client exists to reach. Recorded here so the gap is a decision, not an oversight.

**A stall watchdog, added after a live run.** A provider that accepts a request and then goes silent
left the turn waiting on the HTTP client's own timeout, which is half an hour, and an agent hung for
half an hour looks like an agent thinking. Two minutes of complete silence now ends it.

The watchdog cancels a context derived from the caller's rather than setting a read deadline. A
deadline can only be set on a connection and an `http.Response.Body` is not one: the type assertion
that looks like it would work quietly does not, and the first version of this was a no-op that read
like a fix. Deriving the context also keeps a stall distinguishable from somebody pressing escape,
which matters because the two need entirely different words.

**Its test shipped broken and CI caught it, not the local suite.** The watchdog landed in the second
to last commit of the night and the last full race run came before it, so the branch went out red.
The test kept a plain bool, wrote it from the timer goroutine and read it from the test goroutine,
which the race detector rejects on its own. It was also a real flake: the guard marks itself fired
and drops its lock before calling cancel, so polling `Fired` and then reading the bool can land in
the window between the two and report a watchdog that fired without cancelling anything. It waits on
the callback now, which closes the window because fired is already set by the time it runs.

The lesson is narrower than "run the tests". Run the full race suite against the commit being
pushed, not against the one before it.

Base URL is required rather than defaulted: this provider *is* its endpoint. Which provider gets
spoken is decided by the credential, not by a flag, which is the point of naming keys. There is no
default model for the same reason a base URL has none, so `-model` is required and says so.

The acceptance line's "Ollama and one hosted third party both work" needs a person at a terminal,
so it belongs to PG-A2 alongside A2-04's live check, not to this box.

### A2-07 Prompt caching
`status: partial | owner: Claude | branch: feat/providers (merged) | depends: A2-05`
`scope: internal/provider/anthropic/, internal/pricing/, cmd/canopy/ask.go`

Deliverable: cache long stable prefixes such as system prompts and file context where the provider
supports it.

Acceptance: cached tokens are reported separately in usage, and the saving is visible.

`verify: claude [x]   codex [ ]`

notes: large cost saving for small effort, and it compounds with several agents sharing a project
system prompt.

A prompt is sent in a fixed order, tools then system then messages, and a breakpoint caches
everything before it. So the useful places are the boundaries between what stays the same and what
changes, which for a coding agent is nearly everything. Three of the four available breakpoints are
used: the last tool definition, the system prompt, and the end of the previous exchange.

Two placements were deliberately avoided:

- **Nothing on the newest message.** It would write an entry that the next turn invalidates by
  appending to it, paying the write premium for a read that never happens.
- **Nothing on a conversation shorter than three messages.** There is no prefix worth caching yet
  and the breakpoint would only cost a write. A test asserts both, since either would be invisible
  in normal use and would show up only as a bill.

The saving is reported net and can be negative. A cache write costs more than plain input, so the
turn that fills a cache genuinely pays a premium and the interface says "caching cost $x extra on
this turn, which later turns read back". Reporting only the reads would be the flattering version
and would make the numbers impossible to calibrate against. A single read more than repays the
premium on the same tokens, so a session pays for its cache on the second turn.

The OpenAI compatible client sends nothing for this: caching is automatic on the endpoints in that
family that do it at all, and we hold no rates for them, so there is no counterfactual to report a
saving against. `pricing.Saving` stays silent rather than printing a zero.

Worth noticing later: caching is the thing that degrades silently. If a breakpoint stops matching,
nothing breaks, the bill just goes up. That is why the saving is on screen rather than in a log.

**Set back to partial on 2026-07-28.** The paragraph above describes a screen that does not exist.
`pricing.Saving` has one caller and it is headless `canopy ask`; nothing under `internal/tui`
reads the cache counters, so in the product, the place people actually sit, the saving is
invisible and so is the degradation the previous paragraph warns about. The breakpoints and the
arithmetic are done and tested. The visible half is E-05.

### A2-08 Provider fallback chains
`status: partial | owner: Claude | branch: feat/providers (merged) | depends: A2-03`
`scope: internal/provider/chain.go, internal/core/provider.go, cmd/canopy/ask.go`

Deliverable: a profile may list ordered fallbacks. On overload or rate limit, try the next key or
provider.

Acceptance: an overloaded primary falls through without losing the turn. Authentication failures do
**not** fall through, because a wrong key should be fixed rather than routed around. Every fallback
is visible in the transcript, never silent.

`verify: claude [x]   codex [ ]`

notes: **added 2026-07-26.** Cheap once the error taxonomy exists, and it matters the moment eight
agents run at once, which is exactly when providers start shedding load. Silent fallback would be
dishonest: you would be billed on a different key, and possibly answered by a weaker model, without
being told.

`provider.Chain` is itself a `ProviderClient`, so nothing above has to know whether it is holding
one provider or five. `AllowsFallback` from A2-01 already drew the line and this consumes it:
overload and rate limits fall through, authentication and invalid requests and cancellation do not,
and an unrecognised error defaults to not, since routing around something nobody has reasoned about
spends money on a guess.

Two things the obvious implementation gets wrong:

1. **Watching only the call that opens the stream is not enough.** The Anthropic SDK hands back a
   stream immediately and reports an overload on the first read, so a chain that checked only the
   constructor's error would sit there having never fallen back, in exactly the case it exists for.
   The chain watches the stream too, swallows the failed done event, and delivers the real one from
   whichever link answers.
2. **Falling back stops the moment any of the answer has been delivered.** A replacement stream
   starts its answer from the beginning, so splicing it onto a half delivered one produces a reply
   that reads as though the model contradicted itself mid sentence. Better to report the failure on
   a partial answer than to hide it under a seam.

Fallbacks are reported through a new `EventNotice`, kept separate from text because it comes from
Canopy rather than from the model and merging it into the reply would read as the model saying it.
The notice names both ends: what could not take the turn, why, and what did.

Not yet wired to profiles. `AgentProfile` gains its fallback list in A3, where there is somewhere
for a user to configure one; the mechanism exists and is tested ahead of it.

**Set back to partial on 2026-07-28.** The sentence above said A3 would wire it and A3 came and
went. `NewChain` has no caller outside its tests: the resolver always returns a single client, so
no fallback has ever been possible in the shipped product and the acceptance above has only ever
passed inside the package. LIMITATIONS.md already admits this. The wiring is E-09.

### PG-A2 Phase A2 gate
`status: todo | depends: A2-03, A2-04, A2-05, A2-06`

Both supervisors watch `canopy ask` stream a real reply on two different providers and see the
token and cost figures for each.

`signed: walid [ ]   classmate [ ]`

**Half of this is already proved.** `canopy ask -key nim -model minimaxai/minimax-m2.7` streams a
real reply from NVIDIA NIM with token counts. The cost figure reads "cost unknown" and names the
endpoint, for the reason in D-32 and Q-01. The Anthropic side still needs a real key, which neither
agent has.

`internal/session/live_test.go` runs the same path under test, gated behind `CANOPY_LIVE_KEY` so it
skips unless asked for:

```
CANOPY_LIVE_KEY=<name> CANOPY_LIVE_MODEL=<model> go test ./internal/session/ -run Live -v
```

Worth knowing before the gate: **that file found two real bugs on its first run**, both about
cancellation, and neither was findable by a scripted test. Recorded in A3-08's notes.

---

# Phase A3: chat and persistence

Goal: it looks and feels like the product, and nothing is lost when you quit.

### A3-00 Application shell
`status: review | owner: Claude | branch: feat/tui-shell | depends: none`
`scope: internal/tui/`

Deliverable: the frame everything else lives in. A splash on launch, a layout that fills and
reflows with the terminal, consistent chrome across screens, and a styling layer where every
colour resolves through one palette.

Acceptance: the interface occupies the full terminal and reflows on resize with no artefacts, down
to 80x24 and up to a large window. A splash appears on launch and gives way to the application.
Screens share one header, footer and key handling. No colour is written at a call site, so adding
a theme is a data change rather than a refactor. Everything still reads correctly with colour
disabled.

`verify: claude [x] 2026-07-26   codex [ ]`

notes: **added 2026-07-26 at Walid's request.** The dashboard from P1-07 renders a few lines into
whatever space it is given, which was right for proving the contract and is not what the product
should feel like.

Screens now return `Body()`, `Context()` and `Footer()` and the frame composes them, so chrome is
written once. Each keeps a standalone `View()` for driving it directly in tests.

`internal/tui/theme` is the only place a colour is constructed, and names are by meaning rather
than appearance: `Danger` survives a theme change, `red` does not. Styles are built once when a
theme is selected rather than per render, which matters once several agents are streaming.

Below 60x12 the application refuses to draw and says why. That is the honest option: a squeezed
layout produces wrapped, overlapping output that reads as a rendering bug, and a user cannot tell
that apart from the program being broken.

Three things worth noting:

1. **The splash prints the name as text as well as art.** Block letters are unreadable to a screen
   reader, unrecognisable in a narrow terminal and unsearchable in a pasted bug report.
2. **Any key dismisses the splash and is then swallowed.** The first keystroke after launch is
   usually impatience rather than a command, and acting on it would land the user somewhere they
   did not ask for.
3. **`Init` now batches the event subscription with the splash timer**, and a batched command
   yields a `tea.BatchMsg` rather than the event, so tests driving the event path need the
   subscription alone. `SubscribeCmd` exists for that and nothing else.

The frame owns the footer indent rather than each caller, after one screen rendered flush left
while the others did not. The comparison is Claude Code, Codex CLI, Gemini CLI and OpenCode: full screen,
composed, obviously a program rather than a script.

Themes ship at A9-03 but are **provisioned here**. The expensive version of theming is retrofitting
it after two hundred call sites have picked their own colours. One palette from the start makes it
a data change later, and costs nothing now.

Placed at the head of A3 because the chat interface is what will live inside it, and building the
frame after the contents is the more expensive order.

**The entry point is provisional.** It currently opens on the dashboard, or on credentials when
there are none. Once A3-03 lands, **chat becomes the home screen** and the dashboard becomes a view
reached from it. Recorded 2026-07-26 after Walid pointed out that opening on a monitor puts the
least common activity first and makes Canopy look like something you watch rather than something
you talk to.

### A3-01 Session and conversation types
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A2-01`
`scope: internal/core/session.go, internal/core/event.go, internal/core/project.go`

Deliverable: `Session`, `Message`, `Role`, `Turn` and `AgentState`, held in the existing snapshot
store so sessions and workspaces share one authoritative view and one event stream.

Acceptance: a session rebuilds exactly from a snapshot. Streaming updates coalesce, and a completed
turn is a final event that can never be dropped.

`verify: claude [x]   codex [ ]`

notes: token streaming is the highest volume event source this project will ever have, which is
precisely the case the coalescing rules in P1-01 were designed for. This is the first real test of
whether that design was right.

**The design held, and the reason it held is worth writing down.** Events carry no payload, so a
reader who sees one notification where three were sent takes a snapshot and finds every token that
arrived, because the turn's text grows in the snapshot rather than travelling in the event. Had
events carried their own copy of the text, coalescing would drop characters. Keyed per turn rather
than per session, so two agents streaming at once never swallow each other.

`Message` and `Role` already existed on the provider contract, and `Turn` reuses them rather than
defining a parallel pair. `Session.History()` is then a copy rather than a translation, and a
translation between two shapes that mean the same thing is exactly where a tool result loses its
error flag.

`TurnState` is deliberately wider than it first looks. Complete, interrupted and failed are the
obvious three; **refused and truncated are the ones that matter**. Both arrive as successful
responses carrying text a reader would take for a finished answer, so both are their own state and
`Whole()` returns true for exactly one of the eight. That is the chat form of the stale green
problem the whole project is built around: plausible, and wrong in the direction that costs you.

Three invariants that Validate enforces, each because the failure is invisible until it is not:

- **A terminal turn must record when it ended**, or its duration grows forever and a finished turn
  counts up on screen as though it were still running.
- **A failed turn must say why.** "Something went wrong" is not something a user can act on.
- **Only the last turn may be in flight.** An earlier one still streaming was abandoned without
  being closed out, and it would show as running for the life of the session.

Sessions live in `ProjectSnapshot` beside the workspaces rather than in a store of their own, so
"this agent is working in that worktree, whose tests are failing" is one read of one consistent
view. Two stores would mean two reads, and two reads mean a moment where the answer is half from
before and half from after.

### A3-02 Session storage
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A3-01`
`scope: internal/session/storage.go, internal/session/engine.go, cmd/canopy/`

Deliverable: SQLite persistence. Every session, turn, tool call and usage record written as it
happens. Resume by id. Full text search across history.

Acceptance: killing the process mid turn loses at most the in flight turn. Resuming restores the
conversation exactly. Search finds a message across sessions.

`verify: claude [x]   codex [ ]`

notes: sessions, audit trail, cost history and run reports are all queries over the same data, so
one storage decision buys four features. Schema migrations from day one, since the schema will
change and a tool that loses your history on upgrade is not one anyone keeps.

**Written twice per turn, not per token.** Once when the turn starts, so the question is on disk
before the answer is asked for, and once when it reaches a terminal state. Per token writing would
turn one streamed reply into thousands of transactions to buy a guarantee nobody asked for, which is
that the last few words of a reply still arriving when the process died should also be kept. What
was asked for is "at most the turn in flight", and this is exactly that.

Six decisions worth keeping:

1. **`modernc.org/sqlite`, the pure Go driver.** The cgo one is a smaller download and would make
   `go install` fail on any machine without a C toolchain, which is most of them. Costs about 9 MB
   of binary and 148 MB in the module cache. Flagged to Walid, who is storage aware. See Q-06.
2. **Migrations from the first version**, tracked in `PRAGMA user_version`, each in its own
   transaction. The alternative is discovering you need them while holding somebody's history in a
   shape you cannot read.
3. **A newer schema is refused rather than downgraded.** Running an older build over a newer file
   silently drops whatever the newer one added, and the user finds out when their history has holes.
4. **One connection.** SQLite answers concurrent writers with "database is locked", which is what
   two agents finishing turns at once would produce. Serialising costs nothing at this scale and
   removes a class of intermittent failure that is miserable to reproduce.
5. **An external content FTS5 index**, so text is stored once rather than twice, with the trigger
   set that keeps it honest. Without the delete trigger a search keeps returning a conversation the
   user deleted, which people notice and do not forgive.
6. **A turn that was in flight when the process died comes back as interrupted.** Nothing is going
   to finish it, and left as streaming it would spin forever on screen and make the session fail its
   own validation.

`Engine.Close` waits for outstanding writes and closes the storage it was given. A turn becomes
visibly terminal a moment before it is on disk, because the state is set under the lock and the
write happens after it is released so that a disk write never blocks the interface from reading.
**A test caught exactly this**: it watched a turn finish, closed storage, and hit "database is
closed" from a write still in flight.

`canopy ask` deliberately does not persist. It is a diagnostic for checking a key or a model, and
filling somebody's searchable history with throwaway key checks would be noise.

Proved live end to end, not only against scripted streams: `TestLiveHistorySurvivesARestart` asks a
real provider for a sentence, quits, reopens the file and finds the reply by full text search. The
text a real model returns is what actually goes through the index, and a reply full of punctuation
or code fences is the case a hand written fixture never covers.

### A3-03 Chat view
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A3-01`
`scope: internal/tui/chat/, internal/tui/app.go, cmd/canopy/`

Deliverable: message list, live streaming, input box, scrollback. **This becomes the home screen**:
running `canopy` in a directory opens a chat there, and every other screen is somewhere you go from
it.

Acceptance: a reply renders token by token without flicker, follows the tail unless the user has
scrolled up, and survives a resize. `canopy` with no arguments lands here, not on the dashboard.
Someone who has never used it can install it, run it and start working without reading anything.

`verify: claude [x]   codex [ ]`

notes: reuses the model, event loop and 80 column discipline from P1-07. This package still talks
to core and nothing else.

The screen holds no conversation state. It reads the session from the engine on every refresh, and
there is no local copy being appended to as events arrive, which is exactly why a coalesced or
dropped notification cannot lose a token: the next refresh reads whatever is there now. The spinner
tick doubles as that refresh beat, so the screen catches up even during a stretch where coalescing
delivered nothing.

Four decisions worth keeping:

1. **Navigation out of chat is on control keys only.** Every printable key belongs to the message
   box, so a plain letter opening another screen would mean that letter could never be typed in a
   message. `ctrl+d` for agents, `ctrl+k` for credentials.
2. **`esc` stops the turn rather than leaving the screen**, and `ctrl+c` stops before it quits.
   Somebody hitting either during a long reply means stop, and navigating away or exiting would
   abandon a running turn out of sight or throw the conversation away.
3. **A failed send keeps the message in the box.** Clearing it would mean retyping what was just
   written because a provider was busy.
4. **Escape from the credential screen returns where you came from**, tracked rather than assumed,
   because that screen is reachable from both chat and the dashboard.

The transcript asks the turn state what a reply is rather than deciding for itself. `Whole()` is
true for exactly one state, and every other one gets a label: stopped, declined, cut off, or the
failure reason. A completed answer carries no label at all, because a line under every reply saying
"complete" trains people to stop reading the ones that matter.

The message box is hand written rather than pulled from a widget library, because the cursor has to
sit inside wrapped text. A single line field that scrolls sideways is the wrong shape for what
people type here, which is several sentences and sometimes a pasted stack trace.

One bug worth recording because it was invisible in every test that did not measure: the box and
the text inside it computed their widths from two different constants, so at 80 columns the box came
out 81 wide and wrapped the entire frame. Both now come from `boxChrome`. The layout test that
measures every line at three terminal sizes is what caught it.

### A3-04 Markdown and code rendering
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A3-03`
`scope: internal/tui/chat/markdown.go, internal/tui/theme/`

Deliverable: readable code blocks with syntax highlighting, plus inline markdown.

Acceptance: a long code block wraps or scrolls without breaking the layout, and stays readable with
colour disabled.

`verify: claude [x]   codex [ ]`

notes: **written by a Sonnet agent working in parallel, reviewed and integrated by Claude.** Recorded
because who wrote a piece of code is worth knowing when reading it back, and because the parallel
working turned up a coordination problem worth learning from. See the note at the end.

No new dependencies. Glamour and chroma were both available and neither was taken: the highlighter
is a keyword, string, comment and number lexer written here, which is what a terminal transcript
needs and roughly a thousandth of the surface. It handles Go, Python, JavaScript and TypeScript,
JSON and shell, and falls back to unhighlighted for anything else.

Two design points from the implementation:

- **Structural markers are kept literally rather than replaced by colour.** A heading still reads
  `# Heading`, a list item still starts `- `. That satisfies "readable with colour disabled"
  directly rather than by having a second code path for it, and the two cannot then disagree.
- **Text is wrapped before it is styled, never after.** The hard break path cuts by rune position,
  and a pre styled string's rune positions do not line up with escape code boundaries, so styling
  first would cut a line in the middle of a colour sequence.

Known limits, recorded rather than hidden: no cross line lexer state, so a block comment or an
unterminated string that spans a wrap boundary falls back to plain text after the break; no nested
emphasis; TypeScript reuses the JavaScript keyword table.

The reply is rendered as markdown and **the question is not**. What somebody typed is what they
typed, and rendering their asterisks as emphasis would change their own words back at them.

**Coordination note.** Two agents were editing `internal/tui/chat/` at the same time. Nothing was
lost, but the markdown files were swept into commit `2a83944`, whose message is about the tool
contract and says nothing about markdown. The history is therefore misleading at that commit and is
left that way deliberately: the branch is shared and rewriting pushed history unsupervised is worse
than a wrong commit message. Recorded here so the record is right even where the log is not. The
lesson for next time is to give a parallel agent a package nobody else is in.

### A3-05 Cancel a turn in flight
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A3-03`
`scope: internal/session/, internal/provider/, internal/tui/chat/`

Deliverable: interrupt a streaming reply and keep the partial output.

Acceptance: the connection closes, nothing leaks, and the partial reply is visibly marked as
interrupted rather than silently truncated.

`verify: claude [x]   codex [ ]`

notes: a partial answer presented as complete is the chat equivalent of a stale green.

Escape stops the turn, and in chat it means stop rather than "leave this screen", because a screen
that navigated away instead would abandon a running turn out of sight. `ctrl+c` stops before it
quits, since somebody hitting it during a long reply almost always means stop and quitting would
throw the conversation away.

Three things this needed that were not obvious:

1. **The context has to be checked after the blocking read, not only before it.** Cancelling
   unblocks a waiting read with a transport error, so both clients were classifying every stopped
   turn as a turn that broke. Found by the live tests, not by any scripted one.
2. **A cancel can land before the first byte.** A provider can take seconds to respond, and a cancel
   in that window never reaches the stream. The engine asks the context rather than the error, so
   there is one answer instead of one per vendor's phrasing of "cancelled".
3. **`Close` has to wait for turns to close out.** Cancelling is not the same as finished: the
   context comes down, the stream unwinds, and only then does the turn record that it was
   interrupted and keep what had arrived. Quitting without waiting lost the partial that cancelling
   went to the trouble of keeping. A test caught it.

"Nothing leaks" has its own test, because it is the half that is invisible when broken: a goroutine
still reading an abandoned response body holds a connection open, and eight agents doing that is a
program that slowly stops working for reasons nobody can see.

### A3-06 Context meter and compaction
`status: partial | owner: Claude | branch: feat/agent-runtime (merged) | depends: A3-02`
`scope: internal/agent/, internal/tui/chat/`

Deliverable: a visible context meter, automatic compaction near the limit, and manual compaction on
demand.

Acceptance: the meter is always visible and accurate. Auto compaction announces itself in the
transcript and says what was summarised. The full pre-compaction history is still in storage and
still searchable. Manual compaction is available before the limit.

`verify: claude [ ]   codex [ ]`

notes: silently dropping context so an agent quietly gets dumber is the same class of lie as a
false green. Compaction is always visible, and it never destroys history, it only shortens what
gets sent.

**Set back to partial on 2026-07-28, by an audit of the send path rather than of this block.**
Which half is which: the meter, manual compaction on ctrl+r and /compact, the transcript marker
and the storage guarantee are built and real. The word "automatic" in the deliverable is not:
`Engine.Compact` has exactly one caller and it is the keybinding, so nothing compacts by itself
and nothing consumes `ErrContextLength`, which both adapters classify and nobody reads. And the
word "accurate" is not: the estimator in `internal/core/context.go` counts request text, reply
and thinking only, so tool call inputs, tool results, tool schemas and the system prompt are all
invisible to it, which means it under-reports exactly when the window is filling fastest. The
remaining halves are E-01 and E-02, which depend on this block staying honest about what it is.

### A3-07 Session forking
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A3-02`
`scope: internal/session/fork.go, internal/core/session.go, internal/session/storage.go`

Deliverable: fork a session at any turn into a new one that shares history up to that point and
diverges after.

Acceptance: forking does not modify the original. Both sessions are independently resumable. The
fork point is recorded and visible in both.

`verify: claude [x]   codex [ ]`

notes: **added 2026-07-26.** The natural companion to branch per agent, and it maps onto how people
already think in git. "Go back three turns and try it the other way" is currently either a fresh
session with lost context, or an argument with an agent that has already committed to an approach.
At A5 a fork becomes a second agent on a second branch, which is where it earns its place.

Turns are copied rather than shared. A shared backing array would mean the next turn appended to
either session could land in the other, which is the exact failure forking exists to avoid, and it
would be intermittent rather than obvious.

The fork point is recorded on **both** ends, because the question gets asked from both directions:
"where did this come from" when reading a fork, and "what did I try from here" when reading the
original. A one sided record answers half of it. The `forks` table is stored explicitly rather than
derived from a `forked_from` query, so a fork whose child has been deleted still shows that
something was tried from here and is gone, rather than reading as though it never happened.

Two refusals:

- **Forking from a turn still in flight.** It would copy an answer that is still arriving, and the
  copy would stop growing while the original kept going: two conversations meant to be identical up
  to a point, differing at that point.
- **Carrying a compaction the fork does not cover.** It would tell the model that turns which are
  not there have been summarised, and the summary would describe a conversation the fork never had.

No interface yet. The mechanism and its persistence are done and tested; the screen for choosing a
turn to fork from belongs with the session switcher, which is A5 work.

### A3-08 Session engine
`status: review | owner: Claude | branch: feat/providers (merged) | depends: A3-01`
`scope: internal/session/, internal/store/`

Deliverable: the thing the interface talks to. Owns every session, runs a turn in the background,
folds the provider stream into the authoritative view as it arrives, and publishes one notification
per update.

Acceptance: a turn streams into the snapshot and is readable while still arriving. Cancelling keeps
the partial and marks it. One turn per session at a time. Every terminal state publishes a final
event.

`verify: claude [x]   codex [ ]`

notes: **added 2026-07-26.** Not in the original plan because A3-01 said sessions live in "the
existing snapshot store", and the only store that existed was the fake from P1. A3-03 needs
something real to talk to and A3-02 is persistence rather than runtime, so this is the piece
between them.

`Send` returns as soon as the turn is registered rather than when the answer arrives, because a
terminal that blocked until the reply landed could not draw the reply landing. Everything after
that point reaches the interface through the snapshot and the event stream, which is what lets the
interface hold no conversation state of its own.

Every exit path from a turn goes through one `finish`, which is the only place a turn becomes
terminal. One exit means one place that sets the end time, marks the event final and releases the
cancel, rather than three paths somebody remembered and a fourth they did not.

`ErrBusy` is its own error rather than a generic failure, because the caller's response differs: a
second message while the first is still streaming is a person typing ahead, and the interface
should queue rather than show something that reads as broken.

The event broker moved to `internal/store` as part of this. It was inside the P1 fake, and there
are now two stores that need it with persistence to follow. Coalescing is subtle enough that two
copies would drift, and the way they would drift is a dropped final transition under load, which is
the one failure the design exists to prevent. The fake's own event tests still pass unchanged,
which is what makes the extraction safe to believe.

### PG-A3 Phase A3 gate
`status: todo | depends: A3-00, A3-04, A3-05, A3-06`

Both supervisors hold a real conversation, quit, resume it, and search their history. **This is the
milestone that settles whether the product feels like what we set out to build.**

`signed: walid [ ]   classmate [ ]`

---

# Phase A4: tools and permissions

Goal: the agent can change code, and cannot do so without you knowing what it did.

**Read A4-04 before claiming anything else here.** It is the dangerous part.

### A4-01 Tool interface and registry
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A2-01`
`scope: internal/core/tool.go`

Deliverable: the tool contract, a registry, and schema generation for both provider APIs.

Acceptance: a tool declares its schema once, used for the provider call and for local argument
validation, so the two cannot drift.

`verify: claude [x]   codex [ ]`

notes: one schema, from one declaration, for both the provider request and the local check. Two
declarations drift the first time somebody adds a field to one, and the failure is a model
confidently passing an argument that is silently ignored.

`ToolKind` is coarse on purpose: read, write, execute, network, git. The permission model asks "may
this agent write files" rather than "may this agent call `edit`", because a per tool allow list has
to be updated every time a tool is added, and the update that gets forgotten is the one that grants
more than intended. Git is separate from write because a bad edit is recoverable from git and a bad
`git checkout` is what you would have recovered from. Network is separate from read because the risk
runs both ways: what comes back is untrusted, and what goes out has left.

**`Run` returns a result, not an error, for anything the model could act on.** A tool that failed
because the file was not there has told the model something useful, and turning that into a Go error
would end the turn instead of letting it try a different path. The error return is only for failures
the model cannot do anything about.

Validation is deliberately shallow and is not a JSON Schema implementation. It catches the mistakes
models actually make, a missing required field and a value of entirely the wrong type, and says
which one: a model told "path is required" fixes it next turn, one told "invalid input" guesses. A
number is accepted where an integer is declared, since JSON has one number type and refusing that
would reject well formed calls for a reason nobody could fix.

Tools come back in registration order rather than sorted, because models weight earlier definitions
more heavily and that order is a choice. A duplicate name is refused rather than replacing, since
otherwise whichever registration ran last wins and that is decided by import order.

### A4-02 File tools
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A4-01`
`scope: internal/tools/`

Deliverable: read, write, edit, glob, grep.

Acceptance: every path resolves inside the agent's worktree. Symlinks that escape are refused. An
edit against a file that changed since it was read is rejected rather than applied blind.

`verify: claude [x]   codex [ ]`

notes: the read-then-edit check is the freshness idea from the truth engine applied to a file
instead of a test run. Applying an edit computed against content that has moved is how an agent
silently destroys work, including another agent's.

**Confinement is in exactly one function.** `Workspace.Resolve` is the only place in the package
that turns a model's string into a path on disk. One place to get right, one to test, one to read
when somebody asks how confinement works. A tool that resolved its own paths would be one tool away
from a bug that lets an agent write outside its worktree, and that bug is not recoverable: by the
time anyone notices, the file is gone.

The check is on the **resolved** path. `../../etc/passwd` is the obvious attack and the easy one;
a symlink inside the workspace pointing outside it looks entirely innocent until it is followed.
Paths that do not exist yet are confined through their nearest existing ancestor, which is every
file an agent creates. And `/work/project-secrets` is not inside `/work/project`, despite the string
prefix.

Two things found while writing it:

- **The workspace root has to be symlink resolved too.** On macOS the temporary directory is itself
  a symlink, so a workspace opened there has a root that matches no path resolved through it, and
  every single call is refused.
- **A refusal must not disclose where the path resolved to.** That is a description of the
  filesystem outside the workspace, which is the thing the caller was not allowed to learn. It still
  names what was asked for, or the model has nothing to correct.

The read ledger is per workspace, not global, because two agents editing the same file in different
worktrees have genuinely independent views of it and a shared ledger would let one agent's read
satisfy the other's edit. An edit updates the ledger rather than clearing it, so several edits to
one file do not each need a re read, which would triple the cost of a multi line change.

An edit matching more than once is refused rather than guessed at: replacing the first is a guess
about which was meant, and replacing all is a different edit from the one asked for. Overwriting an
existing file gets the same freshness rule as an edit, since that is the destructive case.

`glob` implements `**` itself, because `filepath.Match` has none and its `*` does not cross
separators, so `**/*.go`, the pattern every model reaches for first, matches nothing. A tool whose
most obvious input silently returns nothing is one a model concludes the codebase is empty from. For
the same reason a malformed pattern says so rather than matching nothing: a model told "no matches"
for a typo stops looking, which is far more expensive than a syntax error.

### A4-03 Shell tool
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A4-01`
`scope: internal/exec/, internal/tools/shell.go`

Deliverable: run a command in the agent's worktree, own process group, timeout, bounded output.

Acceptance: cancellation leaves no orphans, verified by process listing. Output above the limit
keeps head and tail and says how much was dropped.

`verify: claude [x]   codex [ ]`

notes: carries forward the old P2-05 to P2-07 designs unchanged. Shares machinery with the test
runner at A6-03, which the old plan had as two separate efforts.

**Every command gets its own process group and the whole group is killed together.** A test runner
spawns workers, a dev server spawns a bundler. Killing only the process we started leaves those
holding ports and file handles, and the next run fails with "address already in use" for reasons
nobody can see. Tested by starting a background child that keeps touching a file, cancelling, and
checking the file stops changing.

SIGTERM first, SIGKILL shortly after. A process asked to stop politely cleans up its temporary
directories; one killed outright leaves them. A process that ignores SIGTERM is exactly what the
second signal is for.

Windows has no process group in this sense and the equivalent is a job object, which is more work
than this needs today. `process_windows.go` says so out loud rather than being an empty file that
reads as though the problem were handled. **On Windows a cancelled command may leave children
running.** Recorded as a gap, not a decision.

Output keeps the **head and the tail**, not just the tail. The two ends carry different information
and both are usually wanted: the first compiler error is at the top and how many there were is at
the bottom. The gap is marked in the output itself, because a model that cannot see the gap answers
as though the two halves were adjacent.

There is no "no timeout" option. A hung command is a session that looks like it is thinking, and the
failure mode of forgetting to set one should be a command that stops rather than one that never
does. A timeout and a cancellation are separate fields, because "it took too long" and "you pressed
escape" lead somewhere different.

The command goes to `/bin/sh -c` rather than being split into arguments here. It will contain pipes,
redirections and globs, and splitting it ourselves would run something subtly different from what
was asked for and approved.

**The shell tool is the one that is not confined by construction.** Structured path tools are
limited by what `Workspace.Resolve` will resolve; a shell command is an opaque string that can do
anything the user can, and inspecting it does not change that. The permission model in A4-04
controls whether the process starts; it does not contain the process afterwards. Its kind is
`execute` precisely so the model can treat that risk differently. Canopy does not sandbox and must
never imply that it does.

### A4-04 Per agent trust levels and permissions
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A4-02, A4-03`
`scope: internal/permission/`

Deliverable: trust level as a property of the profile. Each level defines which tools run without
asking, which always ask, and which are refused outright. Path confinement, an allow and deny model
for shell, approval scopes, and a complete audit trail of every call with arguments and result.

Acceptance: no tool runs without an applicable permission. A denied call returns an error to the
model rather than killing the session. An approval for one path never covers another. Structured
path tools cannot leave their assigned direct or isolated workspace. Read-only and confined agents
cannot invoke shell; standard sees the exact command before it runs; broad is explicitly
uncontained. A scratch profile and a profile working near `main` behave differently on the same
request. The audit trail answers "what did this agent actually do" after the fact.

`verify: claude [x]   codex [ ]`

notes: **the existing repository trust contract does not cover this and must not be reused as
though it does.** That governs commands the user wrote in a config file. This governs commands a
model generated. Different threat model.

Canopy does not sandbox and must never imply that it does. Per agent levels were chosen over one
global posture because the alternative forces the strictest agent's friction onto every agent, and
people respond to that by loosening everything.

**Deny is a separate outcome from Ask**, and that separation is the load bearing part. A denial is
structural: this level does not include this, and clicking yes is not on offer. Dressing it up as a
question that can only be answered no is how people learn to click through prompts, which is the
failure this whole design exists to avoid. An approval cannot override a denial either, because the
denial is what the user chose when they picked the level, and a prompt that could override it would
make the level advisory.

An unknown trust level denies everything. "I do not know how much this agent is trusted" reads as
"not at all", and a configuration somebody got wrong should fail closed.

**Approvals default to the narrowest scope that covers the call**, and a wider one is something a
user chooses explicitly. Offering the broad one as the default is how "yes" comes to mean "yes to
everything" without anybody deciding that. They are per session and never persisted: an approval
that outlives the conversation it was given in is one nobody remembers granting.

A directory approval covers a call only when **every** path it touches is inside. Approving a
directory and then letting a multi file call through on the strength of the paths that did match is
the obvious hole and it is not obvious from the outside.

**Command case is preserved when judging git.** `git branch -d` deletes a merged branch and
`git branch -D` deletes any branch, and lowercasing to simplify matching conflates them and quietly
allows the destructive one at a level that should ask. A test caught this. An unfamiliar git command
carrying `--force` or a bare `-f` is treated as destructive anyway, because a list of subcommands
will always be behind the tool.

The audit trail records refusals as well as successes, because an agent that tried to write outside
its workspace nine times and was stopped nine times is a very different thing from one that never
tried, and only the trail can tell them apart.

**The prompt lives at the bottom of the transcript**, under the reasoning that led to the call,
rather than in a dialogue over it. A modal that covers the conversation asks somebody to decide with
the context hidden.

`y` allows once, `a` allows everything of that shape for the session, and **anything else refuses,
including enter and escape**. The reflex key on a prompt somebody has not read is enter, and enter
meaning no is the difference between a misread prompt costing a retry and costing a repository. A
question takes the keyboard entirely while it is up, because typing an answer into a text field and
wondering why nothing happens is a bad minute to give somebody.

The engine is its own approver, which is what lets a blocking question from a background goroutine
reach an event loop that must never block: the loop parks the question in the snapshot and waits on
a channel, the interface notices it the same way it notices everything else, and the answer travels
back through the channel. Having the loop call into the interface instead would run the interface's
update loop inside a provider goroutine, which is the shape of every deadlock a TUI ever has.

A second question while one is open is refused rather than replacing it, because silently dropping
somebody's question to ask a different one is worse than declining the second. Cancelling a turn
while a question is open refuses it: they never answered, and a cancelled turn should not leave a
command running behind it.

**Codex review 2026-07-27:** blocked after independent disposable-repository tests. Choosing `y`
stored the approval for later identical calls, and a path stopped by workspace confinement appeared
in the audit trail as allowed, run and failed rather than denied and not run. Proposed fixes and
regression tests are on `feat/permissions-and-confinement`. Verification remains unticked until the
changed behavior is independently rerun. D-33 now supplies the controlling workspace and shell
contract.

**Claude rerun 2026-07-27:** both findings were real, both fixes hold, and the task returns to
`review`. Each fix was mutation tested rather than read. Putting the loop's `Grants.Grant` back on
every approval fails `TestAOneTimeApprovalIsAskedForAgain`; deleting the refusal branch fails
`TestAToolRefusalIsAuditedAsDeniedAndNotRun`. A one-time yes asks again on the next identical call
and a remembered yes still does not, which `TestApprovingWithoutRememberingDoesNotWiden` and
`TestRememberingWidensTheApprovalToLaterCalls` hold apart, so the fix narrowed `y` without breaking
`a`.

One thing the fix left behind. The comment in `internal/session/approval.go` still said the loop
grants the decision's scope on every approval, which is exactly the behaviour that was removed, so
the file explained its design by describing the bug. Corrected here. In a codebase where the
comments carry the reasoning, one that survives the code it described is worth treating as a defect
rather than as tidying.

### A4-05 Tool use loop
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A4-04`
`scope: internal/agent/, internal/session/engine.go, cmd/canopy/`

Deliverable: the full turn. Model requests a tool, permission is checked, the tool runs, the result
returns, repeat until the model stops.

Acceptance: a multi step task completes end to end. Cancellation mid loop leaves no partial tool
execution. A failing tool is reported to the model rather than crashing the turn. Loop count and
token budget are both bounded.

`verify: claude [x]   codex [ ]`

notes: without a loop limit and a budget, a confused model spends real money in a circle. Both
bounds are enforced and both say which one stopped the turn, because "the model finished" and "we
stopped it because it was going in circles" are different things to tell a user.

**Every path through a tool call produces exactly one audit entry and exactly one result.** A call
with no entry is one nobody can find afterwards; a call with no result leaves the model waiting for
an answer that is never coming, which it responds to by asking again, which is how a loop that
should have taken three steps takes fifty.

A denied call, a failed tool, an unknown tool name and invalid arguments all come back to the model
as results rather than ending the turn. A refusal is information: the model can try something within
its remit, which is usually what it should do, and ending the turn would throw away everything it
had worked out. The unknown tool case names the tool, because "unknown tool" alone leaves the model
guessing which of the three it just asked for was wrong.

Cancellation is checked between tool calls, not only around the model call. A turn that was stopped
should not run the remaining three tools it had queued up.

One bug worth recording, because it cost ten minutes of test time to find: **the loop must return at
the done event rather than reading past it.** The done event is by contract the last thing a stream
has to say, so continuing to read means depending on the stream also reporting that it is finished,
promptly. A stream that simply stops producing events hangs the turn instead of ending it, which is
far worse than leaving an event unread. There is now a test with a stream that does exactly that.

**Proved live against a real provider**, which is the only thing that can check the part scripted
tests cannot: whether a real model, given these tool descriptions and these schemas, reaches for the
right tool and fills it in correctly. That is a property of the descriptions as much as of the code.
`internal/agent/live_test.go` has an agent read a file and answer from it, get refused when its
trust level says no and carry on, and create a file with specific content. All three pass against
`minimaxai/minimax-m2.7` on NVIDIA NIM.

### A4-06 Git tools
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A4-04`
`scope: internal/tools/git.go`

Deliverable: status, diff, log, add, commit and branch as structured tools scoped to the agent's
assigned workspace. Checkout of an existing branch, stash, reset, clean, push and rebase are not
structured tools in this release.

Acceptance: an agent inspects, stages and commits its changes without shelling out. The structured
surface exposes no checkout of an existing branch, reset, branch deletion, force operation, stash,
push or rebase. If an agent instead requests Git through the opaque shell, it receives the shell
trust decision for that exact command; Canopy does not claim to parse it into a separate Git
approval. Structured Git path arguments cannot leave the agent's assigned workspace. That
workspace may be the primary checkout in direct mode; in isolated mode it is the agent's
Canopy-owned worktree. Worktree lifecycle operations never target the primary checkout or an
unowned worktree. See D-33.

`verify: claude [x]   codex [ ]`

notes: **not a convenience wrapper over `bash`.** A shell tool hands the permission model an opaque
string, which cannot be told apart from `git push --force`. Structured output is also far more
reliable for a model to act on than parsed porcelain, and confinement is enforceable per argument
where in a shell string it is not enforceable at all.

Six tools: status, diff, log, add, commit and branch. Classified for the permission model as read,
read, read, write, write and write respectively. Branch creation mutates Git state, so presenting
the same tool at read-only trust merely because an empty call lists branches would be a permission
hole.

Three deliberate omissions, all reachable through the shell tool where they are visible and approved
as what they are:

- **`commit` takes no `--amend` and no `-a`.** Amending rewrites a commit that may already have been
  pushed, and `-a` stages files the model never looked at.
- **`branch` can only create**, via `checkout -b`. Switching to an existing branch can discard
  uncommitted work, which belongs behind the destructive gate rather than inside a tool called
  "branch".
- No `reset`, `clean`, `stash`, `push` or `rebase` at all yet.

Every path argument goes after a `--` separator and is refused if it starts with a dash, because git
reads a leading dash as an option wherever it appears and a path called `-f` becomes a flag. Branch
names are validated here rather than left to git: git's own error for a bad ref name is written for
somebody who has read the ref format documentation, and a model reading it tries something adjacent
rather than something correct.

A successful git command that printed nothing says so explicitly. Git is famously silent on success
and a model handed an empty string cannot tell that apart from a failure it did not notice, which it
answers by running the command again.

**Registered before the shell tool**, since models weight earlier tool definitions more heavily and
the tools that can be governed per argument should be reached for before the one that cannot.

Worktree confinement is not enforced here yet: the tools run git in the agent's workspace, which is
the right directory, but nothing stops a `git_add` path from naming something git tracks elsewhere
via a submodule. Recorded rather than claimed. A5-03 is where worktrees become real.

**Codex review 2026-07-27:** blocked. A read-only agent created and switched branches because the
mixed list/create tool was classified as ordinary Git and its `create` field never became a
permission command. D-33 resolved the conflicting primary-checkout language: direct agent tool
execution is allowed by trust, while primary-checkout lifecycle operations remain forbidden. The
branch-mutation fix is on `feat/permissions-and-confinement`; the task remains blocked until that
change and the reconciled acceptance are independently rerun.

**Claude rerun 2026-07-27:** confirmed, and back to `review`. Reclassifying `git_branch` to
`core.ToolGit` fails `TestGitToolsAreClassifiedForThePermissionModel`, so the fix is guarded.

That guard is weaker than it looks, though, and worth saying so. It asserts a constant matches a
table, and the bug was never that the constant was wrong in the table: it was that the permission
model did not act on it. The test would have passed throughout the period the hole was open. So the
rerun added `TestAReadOnlyAgentIsRefusedGitStateChangesWithoutBeingAsked`, which drives the real
loop with an approver that says yes to everything and asserts the call is denied structurally, never
offered, never run, and recorded as denied and not run. It reads "changing files needs at least
confined trust, and this agent is read-only".

One consequence recorded rather than fixed. `git_branch` with no `create` argument only lists, which
is a read, and the whole tool is now a write, so a read-only agent can no longer list branches. That
is the safe direction, and splitting the mixed tool would be a schema change to a shipped surface,
so it stays as it is.

### A4-07 Web search and fetch
`status: deferred | owner: none | branch: none | depends: A4-01, Q-11`
`scope: internal/tools/web.go`

Deliverable: search the web and fetch a URL as text.

Acceptance: fetched content is bounded and stripped to readable text. Failures are reported to the
model rather than crashing the turn. Requests are visible in the audit trail.

`verify: claude [ ]   codex [ ]`

**Deferred out of 0.1 on 2026-07-28 (D-40), in half.** `fetch_url` is built, registered in
`toolsFor`, bounded, stripped to text and audited, and it ships. Web **search** is what is out: it
needs a search provider and an account, which is Q-11 and still unanswered. Nothing is half-wired as
a result, because search was never started; the tool list simply has one network tool in it rather
than two.

notes: a model working from training data alone gets library versions wrong, confidently.

**Fetch is done. Search is not, and needs a decision rather than an implementation.** Every usable
search API wants a key and an account: Brave, Tavily, Exa, Serper. Which one, and whose account, is
a question for the four of us rather than something to pick unilaterally at three in the morning.
Scraping a search engine's HTML was considered and rejected: it breaks constantly, and it is rude in
a way that reflects on whoever ships it. See Q-11. The verify box stays unticked until search lands
or is cut.

`fetch_url` is `ToolNetwork`, which the permission model asks about at **every** trust level
including broad. What comes back is text somebody else wrote, it lands in the model's context, and
the model has no reliable way to tell it apart from the user's instructions.

Nothing here can make fetched text safe. What it does instead is make the boundary visible: the
result names its source, wraps the page in explicit begin and end markers, and says in words that
the text was written by somebody else and is not an instruction. That is the only thing standing
between "this page says" and "this page told me to", and a test uses a page containing "ignore your
previous instructions" to check the markers survive.

Other decisions:

- **Only http and https.** `file://` would read the filesystem through a tool whose permission kind
  says network, which is exactly the confusion a permission model cannot survive.
- **Redirects are followed but each hop is rechecked**, because a redirect is a URL the user never
  approved, and without the check approving `example.com` approves wherever it decides to send you.
- **A small HTML stripper rather than a parser.** The failure mode of doing this badly is some stray
  angle brackets costing a few tokens; the failure mode of pulling in a full parser is a dependency
  the size of the rest of the program. Script, style and svg content is dropped whole, which is most
  of a modern page by weight.
- **A page with nothing readable says why.** A model handed an empty result concludes the page is
  empty rather than that it is built by JavaScript and cannot be read this way.
- The user agent identifies Canopy honestly. A tool that pretends to be a browser is one whose
  traffic nobody can attribute when it goes wrong, and being blocked by a site that does not want
  automated traffic is a correct outcome rather than a problem to route around.

### A4-08 Checkpoint and undo
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A4-06`
`scope: internal/git/, internal/session/`

Deliverable: snapshot an agent's worktree before it acts, and revert everything it did with one
key.

Acceptance: undo restores tracked and untracked files exactly, including deletions. Checkpoints are
cheap enough to take every turn. Reverting one agent never touches another's worktree.

`verify: claude [x]   codex [ ]`

notes: with several agents editing in parallel this is the difference between experimenting freely
and being afraid to let them run. Probably implemented as a stash or a hidden ref rather than a
file copy.

**A hidden commit under `refs/canopy/checkpoints/`, not a stash and not a copy.** A stash is a stack,
and a stack is shared mutable state: two agents stashing in the same repository interleave, and
`stash pop` gives one of them the other's work. A copy is expensive on a large repository and gets
the ignored files wrong in one direction or the other. A commit object is content addressed, so two
agents cannot collide; cheap, because git already holds most of those blobs; and it captures exactly
what git captures, which is the definition the rest of the tool already uses.

Under `refs/canopy/` rather than `refs/heads/`, so checkpoints never appear in `git branch`, never
get pushed by a plain `git push`, and never turn up in tab completion.

Three things this needed that are not obvious:

1. **A temporary index.** Staging into the real one would silently stage every untracked file in the
   user's working tree, and their next commit would include things they never chose. There is a test
   that takes a checkpoint and asserts `git status --porcelain` is byte for byte unchanged.
2. **`read-tree -u --reset` followed by `clean -fd`.** Restoring the tree alone brings file contents
   back and leaves files the agent created still sitting there, because nothing removes what is not
   in the tree. An undo that left the new files behind is not an undo.
3. **A minimal environment.** A user's `GIT_DIR`, `GIT_INDEX_FILE` or `GIT_WORK_TREE` would redirect
   Canopy's own bookkeeping somewhere unexpected, and the failure would be a checkpoint silently
   taken of the wrong repository.

Per turn rather than per session, because the question people ask is "undo what it just did", and a
session level checkpoint throws away the four turns they were happy with along with the one they
were not.

**Undo restores the files and leaves the conversation alone.** Reverting both would destroy the
record of what was tried, which is exactly what somebody undoing wants to look at afterwards to work
out what to ask for instead.

A failure to checkpoint does not stop the turn, it is reported. Refusing to answer because a snapshot
could not be taken would be a tool that stops working in a directory it cannot fully manage.
Somebody who thinks they can undo and cannot is worse off than somebody who knows they cannot, so
the report matters more than the failure.

No interface yet: `Engine.Undo` exists and is tested, and the key to reach it belongs with the turn
list, which is A5 work.

### A4-09 Plan first mode
`status: deferred | owner: none | branch: none | depends: A4-05`
`scope: internal/agent/plan.go`

Deliverable: a profile setting where the agent produces a plan, waits for approval, then executes
without asking per tool.

Acceptance: no tool runs before the plan is approved. Approving grants only what the plan
described. An agent that departs from the plan stops and asks again.

`verify: claude [ ]   codex [ ]`

**Deferred out of 0.1 on 2026-07-28 (D-40).** `Loop.Plan` and `Loop.Execute` are built and tested,
and nothing calls either of them, which makes this one of the five places in this repository where
complete tested code is unreachable. Reaching it needs two things: a profile setting to turn the mode
on, and a screen to show a plan and take an approval. The screen is `internal/tui`, which is the
other pair's side of the file boundary, so this is not something to finish quietly on the way past.

The engine stays. Its tests keep running. Whoever picks this up gets a working half rather than a
blank file, and the acceptance criteria are unchanged.

notes: **added 2026-07-26.** Approval at the task level rather than the keystroke level, and better
than either extreme. Per tool prompting on a fifty step task trains you to approve without reading,
which is worse than not asking. Reviewing one plan is something a person actually does properly.

**The mechanism is done. The profile setting and the interface are not**, because there are no
profiles until A5 and the screen for reviewing a plan belongs with them. `Loop.Plan` and
`Loop.Execute` exist and are tested; nothing calls them yet. The verify box stays unticked.

**Planning is enforced, not requested.** The planning phase runs with a read-only trust level, an
approver that refuses everything, and a fresh grant set, so a model that ignores the instruction and
calls a tool anyway is stopped by the permission layer rather than by its own good behaviour. The
fresh grant set matters on its own: reusing the session's would mean an earlier "always allow edits"
quietly turning plan mode into ordinary mode. Both are tested with an agent at **broad** trust,
because relying on the agent's own level being low is exactly the mistake.

The tools are still described to the planning model. A plan written by something that does not know
what it can do is a plan that proposes the impossible.

The plan is asked for in prose rather than a structured format, because the reader is a person
deciding whether to allow this and a person reads prose. Nothing parses it back out: what a plan
authorises is a phase, not a checklist to verify against.

On execution the plan goes back into the conversation **as the agent's own words** and the approval
as the user's reply. A model that reads its own plan as something it said follows it; one handed the
same text as an instruction from somebody else argues with it.

"An agent that departs from the plan stops and asks again" is currently done by telling it to, in the
approval message, rather than by detecting the departure. That is honest about what it is: cheaper
than any mechanism that tries to catch a departure afterwards, and weaker. Worth discussing whether
the stronger version is worth building, since detecting it properly means comparing intent against
action, which is the hard problem in the middle of this whole product.

### A4-10 Todo and plan tracking
`status: deferred | owner: none | branch: none | depends: A4-05`
`scope: internal/agent/, internal/tui/`

Deliverable: a visible task list per agent that the agent maintains as it works.

Acceptance: the list is visible in the agent's pane and updates live. It survives resume.

`verify: claude [ ]   codex [ ]`

**Deferred out of 0.1 on 2026-07-28 (D-40).** The tool half is real: `TodoTool` is registered per
agent registry, so an agent can keep a list and the list is per worktree rather than shared. The
acceptance is about the list being visible in the agent's pane and updating live, and that half went
to M-03 and did not come back. A pane of its own is not worth building for 0.1 when the list already
appears in what the agent says it is doing.

The `claude [x]` on this task was given for the tool half against acceptance that describes the pane,
and it is removed rather than left to read as coverage of something nobody built.

notes: cheap, and it is most of what makes a long agent run followable.

The engine half is done and tested. The list is maintained by the agent through `set_tasks` rather
than inferred from what it wrote, because inferring it means a second model guessing at the first
one's output, which is a new way to be wrong about the only summary the user is actually reading.

Exactly one item may be in progress, enforced rather than requested. A list where four things are
in progress is a list of everything the agent has ever touched, which is what every one of these
degenerates into if nothing stops it.

**Partial:** the list is registered as a tool and its state is echoed back in the tool result, so it
is visible in the transcript. It is not yet shown in the agent's own pane, which needs the list
plumbed from the registry through the engine to the agents view.

That remaining half is now **M-03**, which also asks for more detail than this task ever did. This
one stays partial and points there rather than being reopened, so there is one task for the screen
rather than two.

### PG-A4 Phase A4 gate
`status: todo | depends: A4-05, A4-06, A4-08, A4-09`

Both supervisors watch an agent plan a change, get approval, make it, and then undo it. Then they
read the audit trail.

`signed: walid [ ]   classmate [ ]`

---

# Phase A5: many agents

Goal: the differentiator. Several agents working in parallel, all visible, all steerable.

**Isolation is a mode, not the definition of an agent.** An agent runs in your repository by
default, exactly as any single agent tool does. A5-01 through A5-04 build the optional isolated
mode, where an agent gets its own worktree and branch. That is necessary when several agents would
touch the same files, and required for fan-out at A6-05. It is not required to have an agent, and
the registry at A5-05 does not depend on it.

D-33 names the boundary precisely. Direct mode may use the primary checkout and must say so.
Isolated mode confines structured file and Git paths to the owned worktree. Shell is denied at
read-only and confined trust, but it is never a worktree containment mechanism when enabled.

Build A5-05 through A5-10 first if you want agents working sooner. The isolation tasks are ordered
first only because A6 needs them.

### A5-01 Worktree discovery
`status: review | owner: Claude | branch: feat/agent-runtime | depends: PG-A4`
`scope: internal/git/`

Deliverable: discover the primary checkout and existing worktrees via
`git worktree list --porcelain`, with stable IDs and ownership states.

Acceptance: a temp repository with three worktrees is discovered in full, and the primary is
identified and protected.

`verify: claude [x]   codex [ ]`

notes: was P2-01, unchanged.

**Canopy discovers worktrees; it does not assume it created them.** Somebody may already have three
for reasons of their own, and the primary checkout is theirs twice over. So discovery reads and never
writes, and ownership is recorded rather than inferred.

The record is a **marker file, not a naming convention**, because a naming convention is something a
user can accidentally satisfy: somebody whose own worktree happens to be called `canopy-feature`
should not find Canopy willing to delete it. A test creates exactly that worktree by hand and checks
Canopy refuses to remove it, with and without force.

The marker lives in the worktree's **git directory**, not in the worktree. In a linked worktree
`.git` is a file containing a pointer rather than a directory, so there is nothing to write into
there. A test caught that immediately. Resolving the real git directory also keeps the marker out of
`git status`, so there is nothing for a user to wonder about in their own repository.

Workspace IDs are derived from the path and hashed. Derived so a worktree keeps its ID across
restarts; hashed because the ID appears in events, transcripts and audit entries, and an absolute
path carries somebody's home directory name into every one of them.

### A5-02 Branch, HEAD, dirty state
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A5-01`
`scope: internal/git/`

Deliverable: per worktree branch or detached HEAD, HEAD SHA, dirty counts, last activity.

Acceptance: correct for clean, dirty, untracked only, and detached HEAD.

`verify: claude [x]   codex [ ]`

notes: was P2-02, unchanged.

The original A5 placeholder built the dirty digest from status plus modification times. A6-01
superseded that implementation with content hashing: a round-trip edit now restores the original
key, because the question is "is this the same code" rather than "did something happen here".

A clean tree gets an empty digest rather than a hash of nothing, so a clean tree at the same commit
compares equal to itself across runs, which is what `RevisionKey.Equal` needs to not report a
permanent false stale.

Status is read with `-z`, because filenames containing newlines are legal and do exist, and without
it one such file becomes two entries and every count is wrong from then on.

### A5-03 Create and remove a worktree
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A5-02`
`scope: internal/git/`

Deliverable: create a worktree and branch for an agent, remove it afterwards.

Acceptance: removal refuses on a dirty worktree without explicit confirmation. The primary checkout
and every worktree Canopy does not own can never be removed or otherwise lifecycle-managed. A failed
creation leaves nothing behind.

`verify: claude [x]   codex [ ]`

notes: previously forbidden outright, now required. The lifecycle guards from that exclusion
survive as behaviour: never manage the primary, never manage an unowned worktree, and never remove a
dirty tree silently. D-33 distinguishes this from tool execution by a direct agent.

Three refusals, in order, each answering a way somebody loses work:

1. **Never remove or lifecycle-manage the primary**, at any level of confirmation. It is the
   user's own checkout.
2. **Never one Canopy did not create.** Finding a worktree is not the same as owning it, and force
   does not change that.
3. **Never a dirty one without saying so.** Uncommitted work is work, and an agent's abandoned
   experiment is sometimes the only copy of an idea. The refusal names what is there, because "it is
   dirty" is not a decision anybody can make.

Being unable to tell whether a worktree is dirty is treated as a refusal too. The safe reading of "I
do not know whether there is work here" is that there might be.

Worktrees are created **beside** the repository, never inside it. One nested in the primary checkout
appears in every glob, every grep and every build, and the first thing anybody notices is their test
suite running twice.

A failed creation leaves nothing behind: the marker is written last, and if writing it fails the
worktree is removed. A half created worktree is worse than none, because it is a directory that looks
usable, is not registered as Canopy's, and will never be cleaned up.

### A5-04 Worktree environment setup
`status: review | owner: Claude | branch: feat/isolated-agents | depends: A5-03`
`scope: internal/git/, internal/config/`

Deliverable: bring a fresh worktree to a runnable state. An optional setup command with a timeout
and captured output, and an explicit allow list of git ignored files that may be copied from the
primary checkout.

Acceptance: an agent spawned into a new worktree can run the project's tests. Copying happens only
for allow listed paths and only after confirmation. A file that is not git ignored is never copied
without separate confirmation. Setup failure is a visible state, not a silent one, and secret
contents are never printed.

`verify: claude [x]   codex [ ]`

notes: **this is a hole in the re-plan, caught 2026-07-26, not a nice to have.** A fresh worktree
has no `.env`, no `node_modules`, no virtualenv and no build cache. Without this an agent spawns
into a tree where nothing runs, and A6 then reports failures that have nothing to do with the
agent's code, which is a false red and just as damaging as a false green.

Carries forward the environment contract from corrections section 6, which the re-plan dropped at
exactly the point it became more necessary rather than less. Its limits stand: a port does not
isolate a database, Redis, a queue or an OAuth callback, and Canopy supplies templated values
without promising isolation it cannot deliver.

**Built 2026-07-27 in `internal/git/setup.go`.** Two mechanisms, deliberately unequal. A setup
command rebuilds what can be rebuilt and copies nothing, which is where the work should happen. A
short allow list covers what cannot be rebuilt because it is secret, which in practice means `.env`.

Four decisions worth arguing with in review:

- **`Confirm` has two callbacks rather than one with a flag.** A path git ignores and a path git
  tracks are different questions: the tracked one replaces committed content, so the agent then
  works from a baseline that exists in no commit anywhere and every diff it produces is measured
  from the wrong place. Two fields makes that structural. A caller who wired up only the ordinary
  case cannot answer the other one by accident, and a nil callback means no.
- **Symlinks are refused rather than followed**, at the top level and inside a copied directory.
  Materialising one inside an isolated worktree is a route back out of it that nobody chose.
- **Sizes are measured before the question is asked.** Answering yes to a directory without being
  told it is four hundred megabytes is not an answer. `CopyRequest.Large` exists so the wording can
  change rather than the outcome.
- **A failing setup command is a state, not an error.** The worktree still exists and still has the
  code in it. `Prepared.Summary` is what makes it visible, and that visibility is the whole point:
  a broken worktree looks completely healthy from the outside.

An allow list of literal paths rather than globs. `.env` is a decision somebody makes once and can
read back at a glance; `config/**` is a decision whose real scope they discover later, which is the
wrong way round for the one feature here that moves secrets.

Known limit, worth a decision in review: a large directory is copied rather than cloned. On APFS and
on Btrfs a reflink copy would be nearly free, and `cp -c` / `cp --reflink=auto` would get it, at the
cost of platform branching and a dependency on `cp` behaviour. Reporting the size up front is what
protects somebody today. Recorded as Q-14.

### A5-05 Agent registry
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A3-01`
`scope: internal/session/agents.go`

Deliverable: named agents, each bound to a profile, a key, a session and a working directory. The
working directory defaults to the repository, and is a worktree only for agents put into isolated
mode.

Acceptance: several agents run concurrently in the same repository without their sessions
interfering. Usage and cost are attributed per agent. An agent can be created without any worktree
existing for it.

`verify: claude [x]   codex [ ]`

notes: where the named key model from A1 pays off. Each agent carries its own credential and model,
so one agent on Claude and one on a local model is a configuration rather than a fork.

**Agents are listed with whatever needs a person first**, not alphabetically and not by creation
order. With eight running, the useful question is not "where is the one called docs" but "which of
these has stopped and cannot start again without me". The name is how you find a specific agent;
the ordering is how you find the one that matters. Each waiting agent also says what it is waiting
for, or the list sends you somewhere without saying why.

Names are chosen rather than generated, because a list of eight agents called agent-1 through
agent-8 is a list nobody can navigate. A duplicate name is refused rather than replacing, since two
agents with one name makes every later reference ambiguous, including the ones in the audit trail,
where ambiguity costs most.

**Removing an agent keeps its conversation.** An agent is a worker and its transcript is a record of
what was done; dismissing the worker is not a reason to burn the record, and it would leave the
audit trail pointing at a session nobody can open.

`Agents()` returns them in creation order, which is the order somebody built them in and therefore
the order they already have in their head. `AgentStatuses()` is the one that sorts by need. Two
methods rather than one with a flag, because they answer different questions and a caller that had
to remember which order it was getting would eventually get it wrong.

**Deliberately does not depend on the isolation tasks.** Coupling the registry to worktree creation
would make "run an agent" mean "make a branch", which is not how anyone works most of the time and
would have made the common case pay for the rare one.

### A5-11 Isolated agent mode
`status: review | owner: Claude | branch: feat/isolated-agents | depends: A5-05, A5-03, A5-04`
`scope: internal/agent/`

Deliverable: opt an agent into its own worktree and branch, and return it afterwards.

Acceptance: an isolated agent's structured file and path-scoped Git tools cannot reach outside its
worktree or reach another agent's. Its shell starts in the worktree but is available only at
standard or broad trust and is explicitly not contained there. A direct agent works in the
repository where Canopy started, which may be the primary checkout, and the interface identifies
that before write-capable work begins. Fan-out never silently shares a direct workspace. Ending an
isolated agent offers to keep or remove the worktree and never removes a dirty one silently. See
D-33.

`verify: claude [x]   codex [ ]`

notes: **added 2026-07-26.** This is the seam that was previously assumed rather than built,
because the plan treated every agent as isolated. Making it explicit is what lets the ordinary
case stay ordinary.

Fan-out at A6-05 requires this. Ranking three agents on one task is meaningless if they are all
editing the same files.

**Built 2026-07-27 in `internal/session/isolate.go`.** Isolation is three things, and the middle one
is the one that matters:

1. A worktree and a branch of its own, named after the agent, so `git branch` afterwards reads as a
   list of who did what.
2. **A structured tool registry rooted at that worktree.** This is the mechanism for file and
   path-scoped Git tools. `tools.Workspace` refuses to resolve a path outside its root, so building
   one registry per isolated agent makes those operations structurally confined. Shell is the
   deliberate exception in D-33: its process starts at the root but is not contained there.
   `Isolation.Tools` is a function of a directory supplied by the caller rather than a registry
   built in the engine, because the engine has never known what tools exist and should not start
   now.
3. A disposition when it ends.

`Disposition` has three values rather than two. Keep, remove, and discard. Remove refuses a dirty
worktree and says what is in there; discard is the second explicit answer given after that refusal.
They must not be reachable by the same keystroke, because an agent's abandoned experiment is
sometimes the only copy of an idea.

A refused removal leaves the agent registered, which looks like a detail and is not. Forgetting the
agent and then failing to remove its worktree would leave a directory on disk with uncommitted work
in it and nothing in Canopy that still refers to it, which is exactly how work gets lost quietly.

**A gap this uncovered, fixed here: per agent trust was stored and never read.** A4-04 put a
`TrustLevel` on every agent and the turn kept reading the engine's, so an agent configured read only
ran at whatever the engine was set to. `toolsForLocked` now resolves both the registry and the trust
level per session. Isolation is where that would have bitten hardest, since the usual reason to
confine an agent to a worktree is to let it work more freely inside one. Worth a second pair of eyes
on whether anything else from A4-04 is stored but unread.

`AddAgent` takes a context now, because making a worktree runs git. Preparation is a separate call
rather than part of creation: making a worktree takes a moment, installing dependencies into one
takes minutes, and an interface that blocked on the second while appearing to do the first would
look frozen at exactly the moment somebody is watching.

Tests drive the real tool registry rather than a fake, and the confinement cases were mutation
tested: rooting the tools at the repository instead of the worktree makes three assertions fail.

**Codex review 2026-07-27:** blocked on the shell clause in acceptance. The file-tool boundary is
real and the cross-worktree file test passes. The shell only starts in the agent's worktree; under
broad trust it can write through `..` or an absolute path with the user's account permissions. That
matches the no-sandbox decision and `LIMITATIONS.md`. D-33 now makes that exception explicit and the
acceptance above no longer claims shell confinement. The task remains blocked until the reconciled
contract and permission changes are independently rerun.

Not done here, and deliberately: there is no key in the agents view that creates an isolated agent
yet. The engine API is the deliverable; the affordance belongs with A5-06 and A5-10, and an isolated
creation has to go through a command rather than straight from a keypress because it runs git.

**Claude rerun 2026-07-27:** confirmed, and back to `review`. Making Enter create the agent
immediately, skipping the confirmation entirely, fails `TestCreatingAnAgent`, which asserts nothing
was created at that point and that Direct mode, the exact workspace path, "primary checkout" and
"not contained" are all on screen before `y` does anything.
`TestEscFromDirectConfirmationReturnsToTheName` covers going back without losing what was typed. The
reworded acceptance no longer claims shell confinement, which is what D-33 settled and what
LIMITATIONS.md already said, so the disagreement was between the ledger and the rest of the
repository rather than between the two agents.

**Direct-mode warning added on `feat/permissions-and-confinement`.** Enter now finishes the name
without creating anything. A separate screen names Direct mode, shows the exact workspace, warns
that it may be the primary checkout, and states that an enabled shell is not contained there. Only
`y` creates the agent; escape returns to the name without losing it. Tests prove Enter alone cannot
create the agent and every required warning is visible. A5-11 remains blocked until the whole
reconciled branch receives the other agent's independent rerun.

### A5-06 Per agent view and switching
`status: review | owner: Claude | branch: feat/agent-runtime | depends: A5-05, A3-03`
`scope: internal/tui/agents/, internal/tui/chat/, internal/tui/app.go`

Deliverable: a list of agents, switch into any one's conversation, come back out.

Acceptance: switching is instant and never shows one agent's output in another's view. An agent
needing input is visibly distinct from one working.

`verify: claude [ ]   codex [ ]`

notes: selection stays keyed by ID rather than row index, for the reason established in P1-07.

**Three layouts, as Walid asked for**, because they answer different questions and no single one
answers all three. List is "what is everyone doing", one line each. Split is "watch two of them",
for when two agents are working on related things and the interesting part is how their answers
differ. Focus is "read one properly", full width. `1`, `2`, `3` go straight to one, and `v` cycles
for people who would rather not remember three keys.

**Split shows two, not four.** A terminal split four ways gives each pane twenty columns, and twenty
columns of a code discussion is not readable. When even two will not fit it falls back to focus
rather than drawing something torn, which is the same reasoning as refusing to draw below the
minimum terminal size.

**The cursor follows the agent, not the position.** The ordering moves as agents change state: one
that starts waiting jumps to the top. A cursor holding index 3 would follow the row rather than the
agent somebody was looking at, and when its agent disappears entirely it falls back to the top
rather than to the same index, because the same index is now a different agent.

Every state is a word as well as a colour. A row of coloured dots is meaningless in a pasted bug
report and invisible to anyone who has turned colour off.

`enter` opens an agent's conversation and `n` starts a new one. Opening is a **message to the
application** rather than something the view does, because which screen is showing belongs in one
place and a view that could change it is one that can put the program somewhere the application
never agreed to.

A new agent inherits the credential and model of the one you are looking at, and the prompt says so.
The first thing somebody wants from a second agent is another of what they already have; choosing
differently per agent is a real thing to want and belongs with a profile picker. A failed creation
keeps the typed name, since both reasons it fails are fixable in a keystroke and clearing the box
would make somebody retype a name they nearly had.

Changing which conversation the chat screen shows clears the scroll position and the half typed
message with it. Carrying either across would mean arriving in one agent's conversation scrolled to
a position from another's, and finding text in the box that was meant for somebody else.

### A5-07 Steering without interrupting
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A5-06`
`scope: internal/tui/, internal/agent/`

Deliverable: two mechanisms, deliberately not one. **Steer** queues guidance delivered at the next
turn boundary, and the current turn finishes normally. **Interrupt** stops the turn now, keeps
partial output, marks it interrupted.

Acceptance: steering does not cancel the in flight request, and the guidance is visibly part of the
next turn's context. Interrupting stops within a second with no orphans. Both reach the right agent
and only that agent. The interface never offers one where the user meant the other.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: the distinction is the whole feature, and it holds.

Steer queues guidance and lets the turn finish; interrupt stops it and keeps the partial output.
Guidance is delivered as an ordinary message at the next turn boundary, so it is visible in the
transcript and it is what the next turn's context is built from.

Several corrections typed during one answer arrive as one message. Sent as three turns the agent
would answer the first before it had read the third.

Steering an idle session sends immediately, because somebody who typed a correction into a session
that is not running meant to send a message. Cancelled guidance is returned rather than dropped, so
the interface can put it back in the box.

### A5-08 Natural language dispatch
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A5-05`
`scope: internal/agent/, internal/tools/`

Deliverable: dispatch from the chat. "use 2 claude sonnet agents for this" creates two agents on
that profile, each with its own worktree and branch, and hands them the task.

Acceptance: count, profile and task are extracted correctly and confirmed before anything spawns.
An ambiguous request asks rather than guesses. An unknown profile name says which profiles exist.
Spawning respects concurrency and budget limits.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: **probably the most differentiating single feature here**, and the reason named keys are
load bearing rather than cosmetic. Implemented as tools, `spawn_agents` and `list_profiles`, never
regex over the user's message.

The model reads, and the extraction arrives as arguments that get checked against reality. An
unknown profile is refused with the list of real ones. A count past the limit is refused with the
limit rather than trimmed, because silently spawning six when twenty were asked for is the worst of
both. A fan out is isolated by default and a single agent is not, since an agent is not a branch.

Nothing spawns until somebody has seen the count, the profile, the task, the cost and the warnings.
The confirmation routes through the same approver the tool calls use, so there is one place a person
answers questions rather than two that behave differently.

Spawned agents do not get these tools. An agent that can spawn agents that can spawn agents is
A8-01, which has its own design, and inheriting it by accident would let a fan out multiply.

**Hardening 2026-07-28 on `fix/dispatch-hardening`.** Four gaps between this block's prose and the
code, closed with regression tests:

- The sentence above was stated but not enforced: a non-isolated spawned agent shared the engine's
  registry, which is exactly where the dispatch tools were attached. Spawned agents are now marked
  and the dispatch tools are structurally removed from their tool set in `toolsForLocked`.
- A spawn onto a profile no agent had run yet copied its model from whichever agent already
  existed, pairing one provider's key with another provider's model name. The profile's default
  model now travels with the dispatch, and a profile with no default model is refused with the
  `canopy keys model` command that fixes it.
- "use 3 agents for this" names no profile. The deterministic default is the profile the
  orchestrating conversation itself runs on, disclosed in the tool description and marked in
  `list_profiles`. With nothing to default to, the refusal lists the real names. Count and task
  stay strict: an unclear count still asks rather than guesses.
- A profile named in the wrong case resolves to the real name, exact matches winning so two
  profiles differing only in case both stay reachable, and a name matching two by case is refused
  with both candidates.

### A5-09 Cost preview and budget guardrails
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A5-08, A2-05`
`scope: internal/agent/, internal/tui/`

Deliverable: before spawning, show an estimated cost range based on this project's own history for
similar tasks. Per agent and per session caps that pause an agent at the limit.

Acceptance: the estimate names what it is based on and how confident it is, and says so plainly
when there is not enough history to estimate. A cap pauses before the next request rather than
reporting the overspend afterwards. A paused agent can be resumed with a raised cap.

`verify: claude [x] 2026-07-27   codex [x] 2026-07-27`

notes: **added 2026-07-26.** Enforcement before the request rather than after is the difference
between a guardrail and a receipt.

The cap is checked before the turn is registered. A paused agent is paused rather than cancelled, so
raising the cap continues from where it stopped instead of destroying the work the budget was
protecting the value of.

A request on a profile with no known rate is counted as uncosted rather than counted as free. A cap
that has silently not been counting half the requests is worse than no cap, because it reads as
reassurance, and Budget.Status says so.

The estimate is crude and says so: a median cost per turn from this project's turns whose
significant task words overlap, times a range of four to twenty five turns per agent. Below three
similar priced turns it shows no number at all rather than pretending one expensive turn is a rate.

**Noted 2026-07-28, without reopening the status:** everything verified below is true of the
engine and none of it is reachable by a person. Nothing under `internal/tui` or `cmd/canopy`
references the budget package, so no cap can be set, seen or raised from inside the product; the
verified path has only ever been driven by tests. The interface is U-07. The verification below
stands for what it checked.

**Independent Codex verification 2026-07-27.** The cap path passed focused race tests: it refuses
before registering the next turn, preserves the prior transcript, and resumes after a raised cap.
The estimate did not meet its own prose: it ignored the task and sampled every loaded session from
the shared history database. Fixed on `feat/commands-and-cost` by assigning new sessions a project
identity, filtering to that project, matching significant task words, naming confidence, and
refusing below three similar priced turns. Cross-project, unrelated-task and undersized-history
tests cover the correction.

### A5-10 Agents view
`status: review | owner: Claude | branch: feat/agent-runtime (merged) | depends: A5-06`
`scope: internal/tui/agents/`

Deliverable: one screen showing every running agent, in **three modes the user switches between**:

- **tabbed**, one agent at a time, tab and shift-tab to move
- **split**, several at once in panes, two up and four up
- **list**, a compact row per agent showing what each is currently doing

Acceptance: four agents stream simultaneously in split mode without tearing. Switching modes keeps
the same agent focused. Focus follows a click. Layout degrades sensibly on a narrow terminal, and
split falls back to tabbed when there is not room. Keyboard remains sufficient for everything.

`verify: claude [ ]   codex [ ]`

notes: three modes rather than one because they answer different questions. Tabbed is for working
with one agent while others run. Split is for watching two compete. List is for "what is everything
doing right now" at a glance, which is the question you have most often with six agents going.

Reached from the chat, which is the home screen. This is where the P1-07 dashboard ends up living.

Four live streams into one terminal is also where the coalescing rules from P1-01 stop being
theoretical. Mouse support is additive only, since the tool has to stay usable over ssh where mouse
reporting may not survive.

### A5-11 Mosaic agents view
`status: review | owner: Claude | branch: tui/agent-mosaic | depends: A5-10`
`scope: internal/tui/agents/, internal/tui/app.go (agents routing and footer only), internal/tui/help.go (agents rows only), internal/tui/chat/model.go (permission prompt panel and its wording only)`

Deliverable: the agents screen becomes the place you see every agent at once, not two of them.
Four layouts:

- **mosaic**, a tiled grid of all agents, up to eight panes, sized by count and terminal width,
  with paging when there are more agents than tiles
- **hero**, the selected agent across the whole top half, everyone else sharing the bottom half in
  vertical slices
- **list** and **focus**, unchanged in spirit from A5-10

Every pane carries its own chrome: the agent's name, state and model in its top border, and the
ember from the chat box riding its bottom border, lit with a dancing tip while that agent works,
grey coals when it is not. The wordmark stays on the application header and appears in no pane.

Digits 1 to 8 jump to a pane. The digit of the pane you are already on opens its conversation, as
does enter, so reaching an agent to say something is two keystrokes from anywhere. h/j/k/l and the
arrows move spatially in the grid. Layouts cycle on v.

Acceptance: eight agents render as a full grid with no line wider than the terminal and no pane
narrower than readable. A ninth agent is reachable by paging and the screen says it is off screen
rather than hiding it. Each pane's ember reflects its own agent's state, not the cursor's. Jumping
by digit lands on the agent the pane shows. Falls back layout by layout on a narrow terminal
rather than tearing. The animation schedules no tick while the screen is not showing or nothing is
working.

`verify: claude [x] 2026-07-28   codex [ ]`

notes: claimed 2026-07-28 at Walid's direction, which is why this crosses the section 2.1 TUI
boundary: the supervisor asked for it directly and the app.go and help.go edits are confined to
the agents screen's own rows. This supersedes the split mode from A5-06 and the pane half of
A5-10: split-of-two becomes the two agent case of mosaic. The A5-10 acceptance line "four agents
stream simultaneously in split mode" is inherited here as the four agent mosaic case.

Built on `tui/agent-mosaic`, all in `internal/tui/agents/mosaic.go` plus the model rework. Every
acceptance line has a test: the eight agent grid and its width invariant, the ninth agent
declared off screen and reached by paging, the per pane ember judged from the pane's agent and
never the cursor, digit jumping, the narrow fallbacks, and the no-tick-while-hidden rule. Full
suite, vet, gofmt and golangci-lint are clean.

Three decisions worth Codex's attention on review:

1. **An uneven page tiles perfectly rather than leaving a hole.** Five agents on a two column
   grid draw as rows of three and two, each row dividing the full width, because a grid with an
   empty cell reads as a missing agent, which is the exact fear this screen exists to remove.
2. **The pane fires run on one ticker with a generation guard, owned by the agents model.** The
   application now keeps the agents view's command in its broadcast path instead of dropping it,
   and tells the view whether it is in front. Without the first the ticker dies on the next
   engine event; without the second it burns frames behind other screens.
3. **The chat screen's layout is untouched.** A single conversation looks exactly as it did,
   wordmark and all. Pane chrome exists only inside the agents screen's body, which is what the
   supervisor asked for out loud.

Two follow-ups landed on the same branch at Walid's direction on 2026-07-28, and the second
widens the scope line above. The panes now render through chat.Transcript rather than their own
summary, so a pane is the conversation screen in miniature. And the permission prompt, the one
place a person authorises agents being created on their account, moved from a thin rounded box to
a heavy frame with a reverse video needs-you chip, because it was not being seen; the dispatch
confirmation now also says "start more agents" instead of "run a command", and the direct-agent
confirmation on the agents screen wears the same frame. The prompt panel edit is inside
`internal/tui/chat/model.go`, which section 2.1 gives to Codex's pair: it is confined to
promptPanel, describeRequest and directPrompt, and is flagged here so it is impossible to merge
unseen.

### PG-A5 Phase A5 gate
`status: todo | depends: A5-07, A5-08, A5-09, A5-10`

Both supervisors type "use 3 agents for this", see the cost estimate, watch three agents spawn onto
their own branches, see all three at once, and steer one without interrupting it.

`signed: walid [ ]   classmate [ ]`

---

# Phase A6: verification

Goal: the thing no incumbent does. Canopy knows whose code actually works.

The contract, state machine, roll-up and fake for this already exist from P1-01 to P1-06.

### A6-01 RevisionKey
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A5-02`
`scope: internal/git/`

Deliverable: HeadSHA plus DirtyDigest, per the truth contract.

Acceptance: the key changes on a tracked edit, on staged content and on a new untracked file, and
does not change when a git ignored file changes. Symlinks hash their target, submodules contribute
their HEAD SHA, and an oversized untracked file forces the revision to unknown with a readable
reason.

`verify: claude [x] 2026-07-28 (at 8f3e5f9, see A6 verification note)   codex [x] 2026-07-28`

notes: was P2-03, unchanged. D-09 and D-16 apply.

Content is hashed rather than summarised. The previous placeholder used the status output plus mtime
and size, which answers "did something happen here" and not "is this the same code": reverting an
edit left a green result permanently stale. Staged content comes from `git diff --cached --raw`
rather than from disk, because the index holds content that exists nowhere in the working tree.

Hashes are cached because A6-02 asks for this every two seconds per worktree. Since the 2026-07-28
corrective pass, a cache hit requires the same file identity, size, modification time and filesystem
change time. On a platform where change time is unavailable, the cache misses rather than weakening
the key.

The size limit is applied to any content read from the worktree rather than to untracked files
alone, which extends D-09 slightly. A modified tracked binary of the same size poses the identical
problem and deserves the identical answer.

Uncovered a quiet bug on the way: with -z, `git status` emits a rename's old path as its own field,
and the old loop read it as a second entry, so every rename counted as one staged and one unstaged
change too many. Both readings now share one parser.

**Codex corrective pass 2026-07-28.** A new dirty-to-dirty regression test proved that the shared
Git runner trimmed the leading space from porcelain v1. That byte is the index status column, so an
ordinary unstaged ` M path` became staged `M  ath`; later edits to the already-dirty file produced
the same key and could preserve a false green. Every NUL-delimited Git caller now uses a byte-exact
path. A second regression proved that size plus mtime could preserve a cached hash after an edit;
cache identity now also uses file identity and filesystem change time, and a file changing during
its own read makes the revision unknown. Race-enabled package tests pass. Codex wrote the corrective
diff, so Claude must rerun these tests and refresh its dated check before this task becomes done.

### A6-02 Revision poller
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A6-01`
`scope: internal/git/`

Deliverable: poll each worktree and emit a revision change event.

Acceptance: an edit produces the event within one poll interval, and polling many worktrees does
not saturate a core.

`verify: claude [x] 2026-07-28 (at 8f3e5f9, see A6 verification note)   codex [x] 2026-07-28`

notes: was P2-04, unchanged. D-07 applies.

Polling rather than filesystem notification. A dead watcher looks exactly like a worktree nobody is
touching, and that failure mode is a green result that never goes stale, which is the one outcome
this product cannot have.

Concurrency is capped, because every poll forks git at least twice and the unbounded version turns
twenty agents into forty processes arriving together every two seconds.

Two bugs came out of writing the tests. A cancelled poll was recording observations nobody made,
because a select between an available slot and a cancelled context picks at random. And a cancelled
`rev-parse` was being reported as "this branch has no commits yet", which is a small lie a cancelled
poll produced every single time.

**Codex corrective pass 2026-07-28.** The production follow loop replaces the watched set while a
poll may still be reading. A slow observation could land afterwards and resurrect a removed
workspace or overwrite its replacement. Polls now carry the watched-set version and discard an
observation whose source changed in flight. The many-worktree test now measures concurrent revision
reads at the semaphore rather than measuring the already-serial callback. Race-enabled package
tests pass. Claude's earlier check predates this diff and must be rerun before done.

### A6-03 Test runner
`status: review | owner: Claude | branch: contract/test-commands | depends: A6-01, A5-04`
`scope: internal/exec/, internal/config/testcommand.go, canopy.json`

Deliverable: run a configured test command per agent worktree, capturing exit code, duration and
the revision at start.

Acceptance: exit zero is passing for the captured revision, non zero is failing, and a command that
cannot start is error rather than either.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: was P2-05, unchanged. Depends on A5-04, because running tests in a worktree with no
dependencies installed measures the environment rather than the code.

Three outcomes stay three. A command that never started, or started and could not finish, says
nothing about the code at all: reporting a missing binary as a failing test tells somebody their
code is broken when what is broken is their configuration.

Logs stay out of run state per D-08, so RunTest returns the run and the output separately rather
than putting a log buffer inside a state record.

**Was blocked by independent Codex review 2026-07-28, and is unblocked by implementing D-05 as
written.** The schema accepted only a string and the runner always ran `/bin/sh -c`, so a missing
executable exited 127 and was recorded as FAIL, contradicting this task's own acceptance sentence.
The test that covered it observed the mismatch and logged rather than failed.

Both supervisors recommended the same path independently, and D-22 says the pivot left D-05
untouched, so this was drift from a settled decision rather than a choice still open. `command` is
now an object: `{"argv": [...]}` by default, `{"shell": "...", "allow_shell": true}` when a pipeline
is genuinely needed, both set is a validation error. Canopy's own `canopy.json` is migrated.

The acceptance sentence is now true rather than aspirational, because with argv there is no
ambiguity to recover from: the executable exists or `Start` fails, which are different objects
instead of one integer. Mutation checked, and reverting the argv dispatch fails the named subtest
with exactly the old symptom, exit 127 and state `failing`.

What the shell form still costs is asserted rather than left to a comment. A second subtest runs the
same missing program through a shell and pins that it comes back as `failing`, so the difference
between the two forms is visible in the test file and a reader can see what opting in buys and loses.

The bare string is refused with a message that shows both forms, because Go's own "cannot unmarshal
string into Go value of type config.TestCommand" says nothing about what to write instead, and this
is the one error most people will meet exactly once.

An earlier corrective branch fixed an adjacent truth-path defect: a RUNNING update carries the same
start revision as its terminal result, so the interface renders RUN instead of UNKNOWN for the
duration.

Codex verification stays unchecked, as does every other task's.

### A6-04 Verification per agent
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A6-03, A5-06`
`scope: internal/tui/`

Deliverable: every agent carries its verification state, using the existing roll-up.

Acceptance: an agent that edits its worktree turns stale, and re-running clears it. Wording and
glyphs are the ones fixed in D-10.

`verify: claude [x] 2026-07-28 (at 8f3e5f9, see A6 verification note)   codex [x] 2026-07-28`

notes: the old P2-09 to P2-14 demo, per agent instead of per worktree.

Nothing here stores staleness. A run carries the revision it was measured against, the worktree
carries the revision it is at now, and the roll-up in core compares them, which is why an edit turns
a result stale without anything going around marking it.

The snapshot is assembled on each call rather than kept, so there is no second copy of the truth to
fall out of date.

**Codex corrective pass 2026-07-28.** Reusing an agent name for another workspace kept the first
workspace's revision and test map. When both worktrees had the same RevisionKey, the replacement
inherited green evidence for a test it never ran. Evidence is now cleared whenever the workspace
identity or path changes. A second race let an older slow run finish after a newer rerun started and
overwrite the current RUNNING state; only updates from the authoritative run may now advance it.
Two direct agents sharing one checkout were also attributed by map iteration; verification and
ranking now refuse that shared workspace and tell the user to isolate the agents, as D-33 requires
for concurrent work. Race-enabled package tests pass. Claude must independently rerun the
corrective cases.

### A6-05 Rank agents by outcome
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A6-04`
`scope: internal/agent/`

Deliverable: give several agents the same task, then rank the results by tests passing for the
current revision, with diff size as a tiebreak.

Acceptance: the ranking refuses to rank anything whose evidence is stale or unknown rather than
guessing. The reason for each placement is visible.

`verify: claude [x] 2026-07-28 (at 8f3e5f9, see A6 verification note)   codex [x] 2026-07-27`

notes: **the strategic argument for the entire project.** Orca fans out across agents. Nobody
appears to use test truth to rank the results.

The refusal is what makes it honest rather than a leaderboard. An agent whose worktree moved after
its tests ran is not placed fourth, it is not placed at all, and the reason says so. A current
failure does get a place, because an agent missing from the screen reads as one that vanished.

Diff size only ever breaks ties, and that is a judgement rather than a measurement: between two
changes that both pass, the shorter one has less to review and less to be wrong.

Mutation tested. Turning the refusal into an ordinary "did not pass" makes two tests fail, and the
mutant produced exactly the failure mode the design exists to prevent: a stale agent ranked second
with "no required test passes", which reads as a verdict about its code.

**Independent Codex verification 2026-07-27.** Focused race-enabled tests independently exercised
passing-before-failing order, diff-size tiebreaks, stale refusal, never-run refusal and unknown
revision refusal. Source tracing confirmed that only current required-test verdicts enter the
ranked slice; unranked entries retain their visible refusal reason.

**Corrective extension 2026-07-28.** A diff measurement failure previously became an empty diff,
which made missing tiebreak evidence look like the smallest change. Ranking now refuses that agent
with the Git reason. Because Codex wrote this extension, Claude must rerun the ranking suite against
this branch even though both older identity checks are already dated above. **Rerun 2026-07-28 at
8f3e5f9**: the ranking and review-queue tests pass, including the refusal of an unmeasurable diff and
the refusal of a shared workspace, and the signature above is dated to that run.

### A6-06 Ready to review queue
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A6-04`
`scope: internal/tui/`

Deliverable: surface agents that are green for their current code and have a meaningful diff,
ordered so the easiest review comes first.

Acceptance: an agent whose result went stale leaves the queue immediately. An agent with a green
result and an empty diff never enters it.

`verify: claude [x] 2026-07-28 (at 8f3e5f9, see A6 verification note)   codex [x] 2026-07-28`

notes: **added 2026-07-26.** Nearly free, since the truth engine already knows all of this.

Derived on every call rather than maintained, which is why an agent whose result goes stale leaves
immediately: there is no cached membership to forget to invalidate. Green with an empty diff never
enters, because a passing suite over no changes is the state every repository starts in.

**Codex corrective pass 2026-07-28.** The queue now excludes agents while their diff is unavailable
instead of treating missing evidence as empty. Untracked-file line counting is constant-memory and
never follows a symlink outside the worktree; an unknown revision skips diff measurement entirely,
which prevents the oversized file that forced UNKNOWN from then being read into memory for a queue
it cannot enter. Exact whitespace in NUL-delimited filenames is preserved. Race-enabled package
tests pass. Claude's earlier check must be rerun on the corrective diff before done.

### PG-A6 Phase A6 gate
`status: todo | depends: A6-05, A6-06`

Both supervisors give three agents the same task and watch Canopy pick the winner on evidence.

`signed: walid [ ]   classmate [ ]`

### A6 verification at 8f3e5f9

`recorded: Claude 2026-07-28`

The four A6 tasks above carried a `claude [x]` from 2026-07-27 that predated the corrective work on
`verify/freshness-and-ranking`, which Codex marked honestly rather than letting the old signature
stand for code it never saw. This is the rerun at the current head, so the signatures mean what they
say again.

Reviewed rather than only rerun. The corrections that carry the branch, each confirmed by reading
the code rather than by trusting the commit message:

- **`strings.TrimSpace` was being applied to `git status --porcelain=v1 -z`.** The leading character
  of that format is the first status column and it is a space for an unstaged modification, so
  trimming turned ` M file` into `M file` and reported it as staged. Split into `runRaw` for every
  machine-readable caller.
- **The content hash cache could be fooled by restoring an mtime.** Change time is compared as well
  now, with `os.SameFile` and a re-stat after the read, and cache hits are disabled outright on a
  platform where change time is unavailable rather than weakened.
- **Two races.** A poll in flight while `Watch` changed could resurrect a removed workspace or
  overwrite the first observation of its replacement; an older test run finishing after a newer one
  started could overwrite the newer result.
- **Evidence is cleared when the workspace behind an agent name is replaced.** A replacement worktree
  can legitimately have the same revision key, so the old run would have made it green before it ran
  anything.
- **`countLines` no longer follows symlinks or reads whole files into memory.** Git records the link
  target as the content, so following it let a size measurement read outside the worktree.

Two follow-ups were added on the same branch after review:

- `Observe` compared a subject's directory to a change's path as raw strings while `sharedWorkspaces`
  cleaned both. A trailing separator on either side made `Observe` match nothing, which reads as a
  worktree where nothing ever changes rather than as anything going wrong.
- `ReadyToReview` did not check whether a workspace was shared while `placementFor` did. The first
  version of this was recorded as mutation checked and was not: the test drove the public flow, where
  sharing clears the evidence before the queue is read, so deleting the guard changed nothing and the
  test passed anyway. Codex caught the false claim by deleting the guard. The test now constructs a
  green-but-shared state directly, which is the only way to make the guard the thing under test, and
  deleting it fails.

The path normalisation was mutation checked from the start, in both directions: a trailing separator
hiding a workspace, and the same directory written two ways escaping the sharing check.

Reran at 8f3e5f9 on darwin: `go build ./...`, `go test -count=1 ./...`, `go test -race` on
`internal/git`, `internal/verify` and `internal/exec`, `go vet ./...`, `golangci-lint run` (0 issues),
`gofmt -l .` (clean).

**A6-03 stays blocked and this note does not clear it.** Q-16 is a genuine contract violation found
by this pass: `canopy.json` accepts only a shell string while D-05 requires an argument array by
default, so a missing executable is observed as shell exit 127 and reported as a failing test rather
than a command-start error. That needs both supervisors to either implement D-05 as written or
supersede it, and no agent should clear it by matching shell output or redefining exit 126 and 127.

---

# Phase A7: git workflow

Goal: turn finished agent work into clean history without leaving the tool.

### A7-01 Diff review in the TUI
`status: review | owner: Claude | branch: feat/verification-and-release | depends: PG-A6`
`scope: internal/tui/diff/`

Deliverable: read an agent's changes per file, syntax highlighted, scrollable.

Acceptance: a large diff stays responsive. Readable at 80 columns and without colour.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: the alternative is a second terminal per agent, which defeats the point of watching them
here.

The diff view renders a window rather than a file, because a two thousand line diff styled in full
on every keystroke stutters when you hold a movement key. It reuses the markdown renderer's
highlighter rather than growing a second one: a diff hunk is source code with a marker in front of
it, and two lexers would drift the first time a keyword list changed.

Readable without colour, since the plus and the minus are the first character of every line.

### A7-02 Commit helper
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A7-01`
`scope: internal/tui/, internal/git/`

Deliverable: stage, draft a conventional commit message from the diff, commit and push, keyboard
only.

Acceptance: the drafted message is editable before committing. Nothing is staged or pushed without
explicit confirmation.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: the draft does not invent a subject. A diff says which files changed and says nothing about
why, so a generated line like "update auth.go" is worse than a blank one: plausible enough to commit
by accident and useless to whoever reads the history later. The type and the scope come from the
files; the sentence is left for a person. An edit that adds nothing is drafted as a chore rather
than a fix.

Enter does not commit. It is the key people press to end a line. Committing is ctrl+s, and pushing
is separate again, because committing is local and undoable and pushing is neither.

Not done: staging a subset. Partial staging needs a way to select hunks, which is its own screen,
and a half version that silently staged whole files would be worse than none.

### A7-03 Cross agent conflict radar
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A7-01`
`scope: internal/git/, internal/tui/`

Deliverable: show which files several agents have all touched, before merging.

Acceptance: overlap is visible per file, and each entry names the agents involved.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: preempts a pain that exists only because you are running agents in parallel.

Deliberately not a merge simulation. Two agents editing different functions in one file usually
merge cleanly, and a real three way merge per pair on every render would cost more than it saves.
What is reported is overlap, and the empty state says so rather than reading as a promise.

A rename counts against both names. Without that, one agent renaming a file while another edits it
under its old name never shows as overlapping, which is the version where the edit quietly
disappears. A delete against an edit is called out separately, since it is the case most likely to
actually conflict.

### PG-A7 Phase A7 gate
`status: todo | depends: A7-02, A7-03`

Both supervisors review an agent's diff, commit it, and see the overlap between two agents before
merging either.

`signed: walid [ ]   classmate [ ]`

---

# Phase M: the pre-alpha anyone can actually use

Goal: someone who has never seen Canopy opens it, adds a key, talks to a model, and watches it
change a file, without being told how.

**This phase takes priority over A8 and runs before it.** Added 2026-07-27 after Walid used the
built program rather than its tests, which is a different activity and found different things. The
engine is nine phases deep and the surface in front of it is one phase shallow, and everything in
A8 makes that ratio worse rather than better.

The bar is deliberately not polish. It is that nothing in the first ten minutes requires reading the
source to get past.

### M-01 System tools, proven from the chat
`status: review | owner: Claude | branch: feat/mvp-usability | depends: A4-02, A4-03, A4-06`
`scope: internal/tools/, internal/tui/chat/`

Deliverable: reading, writing and running things works from a real conversation, and is followable
while it happens.

Acceptance: in a live session against a real provider the model reads a file, edits it, and runs a
command, and each call is visible in the transcript with its arguments, its outcome and how long it
took. A refused call leaves the turn recoverable rather than ending it. A tool that fails returns
text the model can act on. Output too large to show is truncated with the amount hidden stated.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: the tools themselves were already built. What had never happened is a real model calling
them in a real conversation with somebody watching.

**It does now, and it was worth doing.** `cmd/canopy/live_test.go` drives the whole stack against a
stored credential, skipped unless `CANOPY_LIVE_KEY` is set so it never runs in CI. Against
nvidia/nemotron-3-ultra-550b-a55b the model read a seeded file, decided on its own to write a
corrected one, and the file was there afterwards. Two tool calls, both rendered.

The visible half was the real work, and it was missing entirely: a tool call rendered as `[read_file]`
and nothing else. It now shows which file or command, whether it worked, and how long it took.
Arguments are summarised generically rather than from a table of known tools, so anything added
later, MCP included, gets a label instead of a bare name. Results are paired to calls by ID rather
than by position, since they come back in whatever order the tools finish. Output is summarised, not
printed: a thousand line file in the conversation buries the reply underneath it.

Two things came out of running it that no fake would ever have produced. Sub-millisecond calls
rendered as "0ms", which reads as a measurement that failed rather than a call that was fast, and
most calls are that fast. And a turn that ends on a stop reason mapping to failure with nothing
attached showed "the turn failed without an explanation", which is true and useless; it now names
the provider's own stop reason.

### M-02 Input history
`status: review | owner: Claude | branch: feat/mvp-usability | depends: A3-02`
`scope: internal/tui/chat/input.go`

Deliverable: up and down through what you have already sent.

Acceptance: up recalls the previous message and down walks back toward the box you were typing in.
A half typed message survives the trip and is still there when you come back down. The list holds
60 and drops the oldest. It is per conversation, not shared between agents. Opening an old
conversation rebuilds it from that conversation's own messages, so it works before you have sent
anything.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: the single cheapest thing on this list and the one whose absence is felt every minute.
Retyping a prompt to change one word is the most common thing a person does with a coding agent.

Per conversation matters: shared history would offer you the message you sent to a different agent,
which is at best noise and at worst sent by accident.

Two decisions worth recording. A message the engine refused is not filed, because it is still in the
box, and filing it would put the same message on screen and in the history at once. Editing a
recalled message detaches it from the history rather than tracking the edit against the original,
since the alternative is an edit that silently disappears on the next arrow key.

**The wheel had to be separated from the arrow keys, and it was not optional.** In the alternate
screen most terminals translate the wheel into arrow key sequences, which is how `less` scrolls and
is fine right up until the arrow keys mean something. The moment up recalled the last message,
scrolling back to reread an answer replaced what was being typed with an old prompt, and nothing
downstream can tell the two apart because by then they are the same bytes. Canopy now asks for
mouse reporting, so the wheel arrives as a wheel and scrolls the conversation, and the arrow keys
are the message box's alone.

That costs the terminal's own text selection: copying out of Canopy means holding option on macOS
or shift elsewhere while dragging. It is the standard price a full screen program pays for the
wheel, and it is recorded in LIMITATIONS.md rather than left to be discovered.

Three guarantees are mutation tested: dropping the saved draft, the wheel recalling history instead
of scrolling, and the wheel reaching screens other than the one in front. The last of those found a
missing test rather than a bug, which is the point of running them.

### M-03 Task list, detailed and on screen
`status: review | owner: Claude | branch: feat/mvp-usability | depends: A4-10`
`scope: internal/agent/todo.go, internal/tui/`

Deliverable: a task list the agent maintains as it works, detailed enough to follow a long run
without reading the transcript.

Acceptance: the pane shows every item with its state, exactly one in progress, and updates within
the turn it changed. It survives quitting and reopening. A list longer than the pane scrolls rather
than pushing the conversation off screen. A completed item can carry what actually happened, not
only that it is done.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: takes over the remaining half of A4-10, which built the engine and stopped at the screen.
A4-10 stays partial and points here rather than being reopened.

The list reaches the screen on the session snapshot, which is the same path everything else takes,
and persists in one JSON column added by migration 5. The engine pulls it from the tool registry
after each tool call rather than the tool pushing it, because a tool holding a reference to its
session is the thing that breaks the moment an agent moves into a worktree with its own registry.
Reading the registry for that session is also what keeps two agents' lists apart.

Writing the plumbing test found the wiring bug it was written to find: `Tasks()` was on the list and
the registry holds tools, so the engine saw a registry full of tools none of which reported a task
list. The list was maintained correctly and displayed nowhere.

A list longer than six items collapses to where the work is rather than being cut off at an
arbitrary item, since the end of a list is where the unfinished work lives. The pane is measured out
of the conversation's height, so it can never push the message box off the bottom.

The one genuinely better idea than the obvious version: an item records its outcome when it closes,
so a finished list reads as an account of what happened rather than a list of intentions that all
say done. That is the difference between a progress bar and a report, and it costs one field.

Exactly one in progress stays enforced rather than requested. A list with four things in progress is
a list of everything the agent has touched, which is what all of these become if nothing stops it.

### M-04 New conversation
`status: review | owner: Claude | branch: feat/mvp-usability | depends: A3-02`
`scope: internal/tui/chat/, internal/tui/app.go`

Deliverable: start a fresh conversation without leaving the program.

Acceptance: the transcript and the input box are empty and the launch screen is shown again. The
previous conversation is still in the session list and is not deleted. The credential and model you
had chosen carry over. Starting one while a turn is in flight asks first.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: quitting and restarting to get a clean context is what people do when there is no key for
this, and it costs them their credential choice every time.

Not deleting is the point. A key that silently destroys an hour of conversation is a key nobody
presses twice.

On `ctrl+n`, and on the shape of the question. The confirmation is the same key pressed again rather
than a yes or no dialogue, because a dialogue takes the whole keyboard for a decision that is not
dangerous, and this one is not: the old conversation keeps its turn and finishes it whether or not
anybody is watching. The notice says so in those words, since the fear on being asked is that the
running reply is about to be thrown away.

The confirmation lapses on the next keystroke. One that outlived it would eventually fire on a key
somebody meant for something else, which is the worst possible moment to replace what is on screen.
That is the mutation tested guarantee here.

### M-05 A logo worth keeping
`status: review | owner: Claude | branch: feat/mvp-usability | depends: none`
`scope: internal/tui/brand/, internal/tui/layout.go, internal/tui/chat/transcript.go`

Deliverable: a mark that looks deliberate, on the launch screen and on an empty conversation.

Acceptance: it renders correctly at 80 columns and falls back to plain text below the width it
needs. The word Canopy appears as text beside it. It survives a theme change with only its colour
altered, and it is drawn again by M-04 rather than only at startup.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: the old one was five lines of slanted ASCII chosen for being small and out of the way, which
was the right call for a program nobody had seen and the wrong one for a project asking people to
try it. The new mark is a canopy over a trunk.

The text beside it is not decoration. Block letters are unreadable to a screen reader,
unrecognisable in a narrow terminal, and unsearchable in a pasted bug report.

It lives in `internal/tui/brand` because the launch screen and the empty conversation both draw it
and neither package can import the other. Two copies would drift, and the drift would be somebody's
idea of the logo showing up on one screen and not the other.

Drawn from `█`, `▀` and `▄` only. The quadrant and corner blocks look better in the two fonts that
render them correctly and like a row of missing glyph boxes everywhere else, and the first thing a
new user sees is not where to find out which font they are running. Asserted by a test, along with
the silhouette being symmetric about its centre on every row, since an asymmetric one reads as a
rendering fault rather than as a drawing.

Below twenty five columns the art is dropped rather than clipped. Half a tree looks like the program
is broken; the wordmark alone looks deliberate.

### M-06 The first ten minutes
`status: review | owner: Claude | branch: feat/mvp-usability | depends: M-01, M-02, M-04, A9-03`
`scope: internal/tui/`

Deliverable: the interface explains itself well enough that a new user gets to a working agent
unaided.

Acceptance: a person who has not seen Canopy adds a credential, sends a message, gets a reply, and
watches the model edit a file, without being told how and without reading the source. Every screen
says what to press to leave it. Starting with no credential says what to do instead of showing an
empty list. Every error names what to do next. Nothing is reachable only by a key that appears in
no footer and no help screen.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: the acceptance criterion is a person, not a checklist, and it is meant to be run on someone
who was not in the room while it was built. Every one of the three bugs found on 2026-07-26 passed
its tests and failed the first person who touched it.

**Audited on 2026-07-27 by reading every screen against the question above, then fixed.** In order
of how badly each one would stop somebody.

**The worktree monitor was showing invented data.** `runChat` handed the dashboard `fake.New()`,
which seeds four worktrees called feat-login, fix-cache, refactor-api and spike-search, rooted at
"/repo", and a goroutine edited one of them every six seconds to make the screen look alive. It was
the right call when there was no real engine to read from and it stopped being the right call the
moment there was one. Nothing on screen said any of it was invented. For a program whose whole
argument is that it will not show a state the evidence does not support, a screen of fabricated
evidence is the worst available bug. It now reads the same verifier the review screen reads, so the
two cannot disagree, and outside a repository it says so instead of telling you to run
`git worktree add` in a directory that is not one.

**`q` on the worktree monitor quit the program outright**, with no confirmation, from a screen two
keystrokes away from an hour long conversation. It was undocumented in both the footer and the help
overlay, and it contradicted the agents screen where `q` means back. Intercepted at the application
rather than changed at the far end, because the standalone monitor's `q` is correct for what it is.

**The agents footer said `? keys`**, which reads as credentials and opens the help overlay. The key
that opens credentials from there is `K` and it was in no footer at all.

**`?` did not open the help overlay from chat**, because the check that stops a keystroke being
stolen out of a message treated chat as always being typed into. So the one key that lists every
other key could not be pressed from the screen the program opens on. It now opens help when the
message box is empty, and the footer says which of the two meanings is in effect.

**The help overlay was not exhaustive and did not fit.** It was missing `1`/`2`/`3` and `tab` on the
agents screen, `K` on two screens, and six of the eight keys the credential screen handles, while
claiming `ctrl+c` worked everywhere when two screens ignore it. It was also taller than the terminal
and had been since it was written, so the bindings past the fold were unreachable. It now lays out
in two columns where they fit, measured rather than assumed, falls back to one where they do not,
and scrolls with `j` and `k` while any other key still closes it.

Scoped by that criterion rather than by taste, or this becomes the task that is never finished. It
is pre-alpha. The bar is that nothing blocks you, not that everything is beautiful.

### M-07 The interface for the release
`status: review | owner: Claude | branch: feat/interface-polish | depends: A9-03`
`scope: internal/tui/theme/, internal/tui/styles.go, internal/tui/brand/, internal/tui/layout.go, internal/tui/chat/transcript.go`

Deliverable: the palette, the mark and the drawn name, so the program looks like something somebody
chose rather than something that accumulated.

Acceptance: selecting a theme changes every screen and not some of them. Every state stays
distinguishable with the colour stripped. The mark and the drawn name each declare a width that
matches what they actually draw.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: **added 2026-07-27**, at Walid's request, ahead of the release.

It started by finding that the theme system was not connected. `internal/tui/theme` opens by saying
no other package constructs a colour and `styles.go` declared six of its own, duplicating the
default palette by value, so selecting the monochrome theme changed the chat and the agents view and
left the worktree monitor, the review screen and the help overlay in full colour. The test meant to
cover it looped over both themes and then asserted every state has a word and a single width glyph,
which is true whatever the palette is. Third instance this week of a test whose body cannot fail for
the reason its name implies, after the `git_branch` kind and the context window table.

Styles resolve at render time now, so no call site changed and none of them can capture whichever
palette happened to be current at package initialisation. The few places needing a bare colour
rather than a style are refreshed through a change hook, because `lipgloss.TerminalColor` has an
unexported method and cannot be implemented outside lipgloss.

Worth knowing before writing any test near this: under `go test` lipgloss finds no terminal. It
renders with the styling stripped and resolves every adaptive colour to black, so both obvious
assertions, comparing rendered text and comparing resolved RGBA, compare two identical things and
pass whatever the styles are. The tests compare the colour value the style is holding.

The brand colours are the supervisors': `#0c87b7`, `#b4cc03`, `#b7b7b7`. Danger stays red and
warning stays amber deliberately and are not brand colours, because those two meanings are carried
by convention across every program a user has ever seen and overriding them to fit a palette is how
a failure comes to look like a success. No theme sets a background, so the terminal's own shows
through, including through the doorway of the mark.

The mark is a tent with a campfire beside it. It was a tree canopy first, which read as a mushroom
cloud, and the per-row symmetry rule was dropped to allow the fire: symmetry is right for a lone
silhouette and wrong for a scene, and what it was really protecting is checked directly now.

### M-08 The new conversation screen, and the mark in motion
`status: review | owner: Claude | branch: feat/interface-polish | depends: M-07`
`scope: internal/tui/chat/model.go, internal/tui/chat/opening.go, internal/tui/app.go, cmd/canopy/`

Deliverable: opening Canopy puts you in a new conversation, composed the way the supervisors asked
for it: the drawn name centred above the message box, the box itself near the middle of the screen,
the commands along the bottom, and the mark animated in the bottom right corner. Ending a session
prints a code, and `canopy pickup <code>` returns to that conversation.

Acceptance: a fresh start never lands in somebody's previous conversation. The animation stops
costing anything the moment the conversation is no longer empty. A printed code resumes the exact
session it names and says so plainly when it does not match one.

Still open, and deliberately not done here: the drawn name in the top right of every other screen.
It was asked for on 2026-07-27 and then superseded for this screen on the same day by "written logo
at the center", so what remains is whether the other screens carry it. It costs two rows of chrome
everywhere, on screens somebody is reading rather than arriving at, and that is a decision rather
than a detail. Left for the supervisors.

`verify: claude [x] 2026-07-28   codex [ ]`

notes: **added 2026-07-27, unblocked and built 2026-07-28** once PR #19 merged and the four files
this needed were free.

A product bug was sitting in the middle of it. The application built its chat screen on a hardcoded
`session-1`, and the engine loads saved conversations at startup and numbers new ones from the
highest ID it finds, so after the first run `session-1` is the oldest chat in the database. Every
launch reopened it while the agent that had just been started talked into a session with no screen
attached to it. Which conversation to open is passed in now, and nothing named starts a new one.

The empty conversation is composed rather than being a transcript with nothing in it. The middle of
the space between the drawn name and the message box lands on the middle of the screen, which is the
requirement stated precisely: centring the block instead is right only while the box and the name
are the same height, and it stopped being right the moment the box grew to three lines.

The mark is dropped rather than clipped when the screen is too short for it, which is the rule the
brand package already applies to width. That threshold is a real cost and is worth writing down:
with the taller box it needs about thirty four rows, so an eighty by twenty four terminal gets the
name and the box and no mark.

Two things found while wiring it. The animation overlaid the fire a column left of where the mark
draws it, so starting it slid the campfire sideways under a tent that stayed put; `Frame(0)` is the
still mark now and a test says so. And `internal/agent/plan.go` has a complete plan and execute
phase that is called from nowhere, which is the same shape as the theme being unreachable at M-07.
Plan mode here uses the trust level rather than that code, because a level is enforced by the
permission layer and a prompt is not.

Added along the way at the supervisors' request: the mode and the model written into the top edge of
the message box, `shift+tab` between planning and building, the campfire in the secondary brand
colour, and no launch screen.

A chat picker is still out of scope and comes later. The code printed on exit is the resume path for
0.1, and it is the conversation's number rather than a token, because a token needs a table mapping
it back and would still only work on the machine that printed it.

### M-09 The mode ladder
`status: review | owner: Claude | branch: feat/modes-and-commands | depends: M-08`
`scope: internal/core/mode.go, internal/agent/loop.go, internal/session/gate.go, internal/session/aside.go, internal/tui/chat/, cmd/canopy/gate.go`

Deliverable: five implemented modes on `shift+tab`, each one a capability, an approval policy and a
prompt, switchable while a turn is running. The original plan also named three off-cycle postures,
`ask`, `sealed` and `fixed`; those remain unbuilt pending the capability/approval split below. D-41
added the fifth cycle posture, `confined`, so the displayed mode, prompt and enforced trust ceiling
cannot disagree.

Acceptance: every implemented mode is enforced by the permission layer rather than by the prompt,
and a test proves each one refuses what it claims to refuse. Changing mode mid turn takes effect on
the next tool call rather than the next message. `runway` and `cruise` refuse to engage where their
safety net is missing rather than quietly behaving like the mode below them.

`verify: claude [x] 2026-07-28   codex [ ]`

notes: **added 2026-07-28**, at the supervisors' request, after reading what the field does.

M-08 shipped plan and build and they are enforced properly: `permission.Decide` runs at
`loop.go:328` before `tool.Run`, structural denial is checked before grants so an earlier "always
allow" cannot unlock a write in plan mode, and an unrecognised level denies. The model cannot argue
its way past any of that. Three things are wrong with it, and this task is those three.

**The prompt is missing entirely.** Plan mode is pure enforcement, so the model is never told it is
planning: it tries to edit, gets `refused: changing files needs at least confined trust`, and
thrashes. Enforcement without a prompt is safe and wasteful; a prompt without enforcement is
theatre. Both, always. `internal/agent/plan.go` already holds a written `PlanPrompt` that nothing
calls, which is the same shape as the theme being unreachable at M-07.

**Trust is a snapshot.** `engine.go` reads the level once at turn start and hands it to the loop, so
changing mode while a reply is arriving does nothing until the next message. `agent.Loop` should
hold a function rather than a value and call it per tool call. Tightening mid turn will start
refusing a model that is halfway through a sequence, which is correct and is what Claude Code does.

**Capability and approval are one field and should be two.** `TrustLevel` says both what an agent
can do and what it asks about, so `standard` means "writes silently and asks about shell" as a
single value. That is why review-every-edit is not expressible today. Codex CLI is the one that gets
this right, with `approval_policy` and `sandbox_mode` as separate axes. The existing ladder stays as
the capability axis and an ask policy layers on top, so `permission.Decide` is extended rather than
rewritten.

The cycle, ordered by how much can go permanently wrong rather than by how much is allowed:

| mode | can do | asks about | told |
|---|---|---|---|
| `plan` | read | nothing, it cannot | write the plan and stop |
| `confined` | read, structured writes in assigned workspace | network | use structured tools; shell is unavailable |
| `build` | read, write in workspace | shell, network | nothing. The default |
| `runway` | read, write, shell | nothing | you may break things while working, not when you stop |
| `cruise` | everything, destructive git included | nothing | you have the wheel |

Off the cycle, reachable by flag: `ask`, every action asks including reads, for working next to
something that matters; `sealed`, full capability confined to its own worktree with no network, for
fanning several agents out at once; `fixed`, only pre-approved tools run and nothing prompts, for CI
and cron.

**`runway` is the one no other harness can build, and it is the reason to do this at all.** The agent
writes and runs whatever it likes, and a turn is not accepted until the workspace verifies green: red
tests roll back to the checkpoint already taken and the model is told what broke. Every other tool
asks what an agent may touch. This asks what state it may leave you in, which is the question people
actually have. Canopy can ask it because it already has all three pieces, a checkpoint before every
turn, a verifier that knows test state, and staleness detection that knows whether a green result
still applies to this revision. A classifier guesses at intent; this checks an outcome.

Named `runway` by the supervisors, over ratchet, because it carries the shell and it lands before
cruise: a runway is where you commit to running flat out while an abort is still available.

The honesty rule from A8-08 applies to both of the bottom two. Outside a git repository, or with no
tests configured, `runway` refuses to engage rather than silently behaving like `cruise`, and
`cruise` refuses outside a repository too, because cruise with no undo is recklessness and cruise
with a checkpoint per turn is a trade somebody can defend.

Deliberately not included: Aider's architect split, an expensive model proposing and a cheap one
editing. Canopy is better placed for it than Aider is, since per agent credential and model already
exist and cost tracking could show what the split saved, but it changes how a turn executes rather
than what it may do, and putting it on the mode key would merge the two axes this task exists to
separate. Its own task when somebody wants it.

Read before starting: Claude Code's six modes and its classifier fallback, Kimi Code's read-only
plan sub-agent, OpenCode's per tool allow/ask/deny, Codex CLI's two axes, Aider's three chat modes.

**Corrected 2026-07-28 under D-41.** The original four-mode cycle exposed a real contradiction:
a profile configured at confined trust opened with `build` on screen while its effective trust was
clamped below build. The shell was safely absent after the ceiling fix, but the visible mode and
model prompt still promised the wrong capability. `confined` is now an explicit fifth cycle mode,
and bare confined profiles resolve to it. A broad default also goes through the same undo
prerequisite as an explicit cruise selection instead of bypassing that guard. Direct and isolated
registry tests cover every trust level and tool kind, and denial tests prove a command requested by
an adversarial provider stays unrun and audited in both registry paths.

**Done 2026-07-28.** The five cycle modes, each carrying a level, a prompt and a name. The prompt
goes out as the system prompt, which the engine was not setting at all, so that half was a seam
nothing used rather than a change to anything. The level is asked before every tool call rather
than once at the top of the turn, so switching mid reply takes hold on the next thing the model
tries. A mode can lower what an agent may do and can never raise it above what its configuration
allows, and the key skips past what it cannot reach so it still does something on a confined agent.

**The green gate landed the same day.** A turn in runway is checked after it finishes and put back
where it did not verify, with the reason kept on the turn even though the changes are not: a rolled
back turn that left no trace would look like nothing happened.

Three outcomes and deliberately not two. Green keeps it silently. Red rolls it back. An error means
the question could not be asked, and that keeps the turn, because rolling work back because the test
runner fell over would destroy it to punish an infrastructure problem. `RolledBack` is its own field
rather than `Error` for the same reason: the turn did not fail, it worked and was not kept.

Only a turn that reached `TurnComplete` is checked, and only in runway. A cancelled turn has nothing
worth keeping, and checking in build would make every message pay for a full test run.

`NeedsUndo` and `KeepsGreen` are both enforced at `SetMode`, so runway and cruise are refused where
their safety net is missing rather than quietly behaving like the mode below them.

**`/btw` landed too.** Its own request against the conversation's history, no tools, nothing
recorded, and a turn in flight is undisturbed. The no-tools part is what makes it safe beside a
running turn rather than merely polite: a side question that could call one would be a second agent
on the same worktree with no checkpoint of its own, which is the situation Canopy exists to stop
people getting into by accident.

**Left: the capability and approval split.** The structural half, and not needed by any of the four
cycle modes, which is why they shipped without it: all four are expressible with the existing trust
ladder. It is what `ask`, `sealed` and `fixed` need, and what makes review-every-edit sayable at all.

Worth knowing before starting it: `cmd/canopy/gate.go` polls the runner for terminal state because
`exec.Runner` reports state rather than offering a channel to wait on. It is the one part of the
gate that only runs against a real repository and so the hardest part to test, which is why the
behaviour lives in the engine against a stub and only the waiting lives there.

**Corrected on 2026-07-28**, after Codex reviewed the merged PRs. Two of the promises above were
not being kept.

**The green gate never waited for the tests.** It asked whether the roll-up's verdict was running
rather than whether the run was, and those are different questions: a verdict says what the evidence
means for the code in the worktree, and a queued run has recorded no revision yet, so the honest
verdict on one is unknown. Unknown is not running. Every test therefore read as finished the instant
it was started, the gate read the roll-up of a suite that had not run, and runway reverted every turn
it was given. That is the acceptance criterion above inverted: the mode did not quietly behave like
the one below it, it destroyed work instead. Missed because the gate's behaviour is tested in the
engine against a stub and `cmd/canopy/gate.go` had no tests of its own. It has four now.

**A mode did not survive a fork or a restart.** Forking a planning conversation produced one that
could edit, because the fork carried the history and not the mode and then resolved its level from
configuration. Quitting did the same thing: modes were never written down. The mode is stored by
name now, resolved on first use rather than at load since runway's gate is attached later in
startup, and a stored mode that cannot be honoured falls back to build rather than to the level it
shares with cruise.

The capability and approval split described above is still outstanding and is still the right shape.

### PG-M Phase M gate
`status: todo | depends: M-01, M-02, M-03, M-04, M-05, M-06`

Both supervisors watch someone who has never used Canopy get from a fresh install to a model
editing a file, without helping them.

`signed: walid [ ]   classmate [ ]`

notes: the person doing the walkthrough should not be either of you, and should not be told which
key does what. Watching where they stop is the entire value of the gate.

**The next release is cut when this phase closes, and not before.** Walid's call on 2026-07-27:
`v0.1.0-alpha.2` goes out once M-01, M-03 and M-06 are done. M-01 is specifically watching the
system tools run against a real provider with somebody looking, and tagging before it means
shipping a build whose central feature nobody has seen work. That is the same mistake as the two
bugs found on 2026-07-26, both of which passed their tests and failed the first person who touched
them.

The prerelease label is automatic now. `v0.1.0-alpha.1` published as a stable release because
`release: prerelease: auto` was missing, which is fixed, but that release is still mislabelled on
GitHub until somebody ticks the box by hand. Homebrew waits for the first non-prerelease tag.

---

# Phase A8: advanced orchestration and extensibility

Goal: the ceiling. Everything that makes Canopy worth extending rather than just using.

### A8-01 Sub agents
`status: deferred | owner: none | branch: none | depends: PG-A7`
`scope: internal/agent/`

Deliverable: an agent may spawn helper agents for subtasks.

Acceptance: sub agent cost is attributed to the parent's budget. The audit trail shows the tree,
not a flat list. Depth and fan-out are bounded.

`verify: claude [ ]   codex [ ]`

**Deferred out of 0.1 on 2026-07-28 (D-40).** Not started. It needs its own depth and fan-out limits
and its own cost attribution before it can exist at all, because inheriting dispatch by accident
turns one confirmation into an unbounded fan out. That is a feature with a safety design attached,
not an afternoon.

notes: powerful for decomposition, and it multiplies cost while making the audit trail and budget
accounting considerably harder to keep honest. Which is why it comes after human driven dispatch
works.

### A8-02 Agent handoff and model escalation
`status: deferred | owner: none | branch: none | depends: A8-01`
`scope: internal/agent/`

Deliverable: hand a worktree and a context summary from one agent to another, so a cheap model can
explore and a stronger one can act on what it found.

Acceptance: the receiving agent gets the summary and the worktree, not the whole transcript. The
handoff is visible in both sessions. Cost is attributed to each agent separately.

`verify: claude [ ]   codex [ ]`

**Deferred out of 0.1 on 2026-07-28 (D-40).** Depends on A8-01, which is also deferred.

notes: **added 2026-07-26.** A real cost lever that only exists because keys have names. Exploring a
large codebase is mostly reading, which a cheap model does adequately, while the fix wants the
strongest model available. Doing that by hand today means copying context between tools.

### A8-03 Project configuration file
`status: partial | owner: Claude | branch: feat/verification-and-release | depends: PG-A7`
`scope: internal/config/`

Deliverable: a committed per project file defining profiles, test commands, permission posture and
project instructions.

Acceptance: unknown executable fields are errors rather than warnings. Templates resolve before
execution. A relative path cannot escape the worktree.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: carries forward the validation discipline from the old P3-01. Needed by A6 anyway for per
project test commands, so this is the point where it stops being optional, exactly as predicted.

`canopy.json` at the repository root. JSON rather than something friendlier because TOML and YAML
both want a dependency, and YAML in particular wants a parser with a history of surprising people
about what a bare `no` means. The real cost is no comments, which is in LIMITATIONS.md rather than
pretended away.

Unknown fields are errors through DisallowUnknownFields, which is the acceptance criterion and the
whole argument: a field named "test" where "tests" was meant would otherwise load cleanly and run
nothing, and every agent would go green on a suite nobody executed.

Only three template names resolve. A general template language here would be a way to build a
command at execution time out of values Canopy does not control, and the point of the file being
committed is that a reviewer can read it and know what it will do.

**Set back to partial on 2026-07-28.** The deliverable lists project instructions and the field
exists in name only: `config.Instructions` carries a doc comment promising it is prepended to
every agent's system prompt, and the system prompt is built from the mode alone, so the field
parses, validates and reaches nothing. Which half is which: profiles, test commands and posture
are real and validated; instructions are the fifth complete mechanism shipped with no caller.
The injection is E-07.

### A8-04 Custom slash commands
`status: review | owner: Codex | branch: feat/commands-and-cost | depends: A8-03`
`scope: internal/config/commands.go, internal/tui/, internal/agent/ only if expansion cannot stay at the input boundary`

Deliverable: user defined reusable prompts as `/commands`, per project and globally.

Acceptance: a project command is available in that project only. Arguments are substituted safely.

`verify: claude [ ]   codex [x] 2026-07-27`

notes: cheap once chat exists, and the first thing power users ask for.

**Claimed 2026-07-27 by Codex.** The round file boundary is collision control, not permission to
ship half a feature. Command discovery, completion, argument expansion and visible invocation belong
at the chat input boundary. Project/global precedence and substitution rules belong beside the
`config.Command` type. If the expanded prompt can enter the existing send path without changing
`internal/agent`, that package stays untouched; otherwise the smallest explicit seam will be
coordinated and documented.

**Built 2026-07-27 by Codex.** Project commands in `canopy.json` shadow the optional global
`commands.json` definition only for that run. `/commands` lists the active name, description and
scope; `//` escapes a literal slash. `$ARGUMENTS` is copied literally in one non-recursive pass, and
arguments are appended visibly when no placeholder exists. Unknown commands remain in the input
box and never reach the model. Both files are strict, and a broken global file degrades only that
layer with a warning. D-34, README and limitations record the contract.

### A8-05 Hooks and automations
`status: claimed | owner: Claude | branch: hooks/loop-guard | depends: A8-03, PG-A6`
`scope: internal/hooks/, internal/config/hooks.go, internal/verify/verify.go`

Deliverable: run something on an event. Tests green, auto commit. Tests red, notify. Agent idle,
nudge.

Acceptance: a hook fires only on a real state transition, never on a stale or unknown one. A
failing hook is visible and never silently swallowed.

`verify: claude [ ]   codex [ ]`

notes: **still claimed on 2026-07-28, with one of the two open clauses closed and one open.** The
earlier signature was given when nothing called any of this, which is why it came off.

**Closed: the self-retrigger loop.** Q-17 is resolved and recorded as D-39. A revision that appeared
between a hook firing and that hook returning is claimed as the hook's own, so a committing hook no
longer fires on its own commit. What made this tractable is that the recognisable thing is the
interval rather than the revision: every property of a revision fails to identify who made it, but
the runner already holds the revision the hook fired at and can read it again when the hook returns.
The interval is also marked in flight before execution, so a fast poll and test pass cannot start a
second batch while the first command is still running. Multiple hooks for one event share the batch
interval until the last returns.
Mutation checked, and without the guard the loop test runs the hook ten times out of ten instead of
once. A companion test pins that work somebody actually did still fires, because over-suppressing here
would silently skip the commit for the next piece of real work.

**Open: a failing hook is only visible when Canopy exits.** The report exists, carries the command,
the output and the error, and reaches `recordHook`, and there is nowhere on screen for it to go. A
long session can therefore hide a broken hook for hours, which is the exact failure the second
acceptance clause names, since the point of automation is that somebody stops watching.

That half needs a surface in `internal/tui`, which is Codex and Ali's side of the file boundary for
this round. Per the boundary rule this is asked for rather than reached across: **a place to show hook
failures, fed by `verification.HookFailures()`, which already exists and already returns them.** Until
that lands this task cannot go to review, because half of its acceptance sentence is about being
visible.

where verification and orchestration compound. The truth engine is what makes the triggers
trustworthy, so hooks firing on unverified state would poison both.

Its own package, `internal/hooks`, rather than a file under `internal/agent`. The scope line said
otherwise and the scope line was wrong: hooks fire on verification state, which is the poller's
output rather than the agent loop's, and `internal/session` was being edited on another branch at
the time. Deciding what fires is split from running it, so the rules are testable without a shell.

Three rules carry the acceptance criterion, and each is a way this goes quietly wrong.

**A hook fires once per revision, not once per state.** The poller says where every workspace stands
every couple of seconds, so firing on the state would be an auto-commit every two seconds for as long
as the build stayed green.

**It does not end the commit loop, and this document said it did.** Commit on green moves HEAD, which
moves the revision, which makes the results stale, which makes them run and pass again, and a pass at
a new revision is a new event by the rule below. So a committing hook fires again: `git commit -am`
fails harmlessly the second time, and one using `--allow-empty` keeps going. The claim contradicted
the passing-stale-passing rule three lines further down, and it was invisible for as long as nothing
called the package. Q-17 is where the rule for recognising a hook-originated revision has to be
decided.

**Nothing fires on stale or unknown evidence.** A green that no longer describes the code is the
failure this project exists to refuse, and a hook that commits on the strength of one writes that
failure permanently into history. Stale is still recorded so the edge is tracked, which means passing,
stale, passing fires twice. That is correct: the second pass is evidence about different code.

**Agent events keep the edge rule instead**, because they are not claims about code. Keying `agent-idle`
on the revision would fire it again because somebody edited a file, which the agent had nothing to do
with.

That last distinction was found by mutation testing rather than by design. Removing the edge check
changed no test, which meant either the check was redundant or the tests were not covering it. It was
worse than redundant: an agent whose tests pass, then works, then passes again would have had the
second event suppressed if the poller never caught an intermediate state, silently skipping the commit
for the second piece of work. Five mutations now, all caught.

A hook learns why it ran from the environment rather than from substitution into its command, so the
command in `canopy.json` is the command that runs. A reviewer should not have to simulate an expansion
to know what will execute, and an agent name containing a quote should not be able to change the shape
of a shell command.

Unknown event names are refused at config load with the vocabulary listed, because a hook that
silently never fires cannot be told apart from one that fires and does nothing.

Not wired yet. The package is complete and tested and nothing calls `Observe`, which needs the poller
in `internal/verify` or `cmd/canopy`, both of which had another branch open in them. That wiring is
what PG-A8 will actually be demonstrating, so it is not optional.

**Wired on 2026-07-28**, after Codex reviewed the merged PR and found nothing calling it. Every rule
above was correct and unreachable: `Observe` had no caller, so a hook validated at load and then
never ran. `internal/hooks` was imported by `internal/config` and by nothing else.

Observed on the poll tick rather than when a revision changes, because half the vocabulary is about
the agent rather than the code. An agent going idle or getting blocked moves nothing in git, and a
hook keyed to a worktree poll would never see it.

Failures are collected and printed when Canopy exits, which is the weak part and is recorded as
such. The acceptance criterion is that a failing hook is visible, and there is nowhere on screen for
one yet: an event here is deliberately a thin notification that a consumer answers by re-reading a
snapshot, and a hook failure has no snapshot to re-read. Giving it a place on screen is its own
piece of work.

### A8-06 MCP client
`status: review | owner: Claude | branch: mcp/hardening | depends: A4-04`
`scope: internal/tools/mcp/, internal/config/mcp.go, internal/config/config.go, cmd/canopy/mcp.go`

Deliverable: connect to MCP servers and expose their tools to agents.

Acceptance: third party tools pass through the same permission model as built in ones, with no
exemption. A failing server degrades that server only.

`verify: claude [x]   codex [ ]`

notes on 2026-07-28: **the deliverable was not met at all until this branch, and the reason is worth
recording because it is the fifth instance of the same pattern.** Nothing imported
`internal/tools/mcp`. The package was complete and well tested, `internal/config` parsed and
validated an `mcp` block, and no line of code ever read it. Every agent ran with exactly the tools it
would have had with no server configured, so "expose their tools to agents" was not partially done,
it was undone, and the acceptance criteria were being checked against a package rather than against
the program. The other four are `internal/agent/plan.go`, the theme work at M-07, `internal/hooks`
and `internal/report`. A test that exercises a package proves the package.

The three transport findings from the independent A8-06 pass are also fixed here:

- **Server request id collisions.** MCP is bidirectional and both sides number from one, so matching
  a frame to a waiter by id alone hands a server's request to whoever waits on that number, and the
  real reply then arrives to find nobody there. The discriminator is now the presence of `method`.
  Ids are also kept as raw bytes, because the protocol permits a string id and decoding into an
  int64 dropped such a frame whole, which from the server's side is a client that never answers.
  Server requests get a method-not-found reply rather than silence.
- **Silent truncation past fifty pages.** The bound was right and the silence was not. A session now
  carries a note saying what was left out and `Describe` repeats it, and there is a tool count bound
  as well, because a server that paginates honestly and offers thousands of tools is not a loop but
  is those definitions in every request thereafter.
- **Server children surviving shutdown.** A server is often a launcher, and waiting on the process
  Canopy started said nothing about the process it started. The group is signalled before the reap
  rather than after a failed wait, which is the only ordering that works here: stdout is a pipe this
  package owns rather than something Go copies, so nothing holds `Wait` open and it returns as soon
  as the leader exits. See D-37.

While fixing the third: `stillRunning` in the tests called `os.Process.Signal(nil)`, which Go rejects
as an unsupported signal type before it reaches the kernel, so it returned false for every pid.
`TestClosingASessionStopsTheServer` had been passing vacuously since it was written. Fixed, and both
teardown tests now fail if the fix is reverted.

**What is verified and what is read.** The four new tests in `cmd/canopy/mcp_test.go` call
`attachMCP` directly and are mutation checked. That `runChat` calls it is established by reading the
call site, not by a test, because `runChat` opens the interface and there is no seam to drive it
from. That gap is exactly how this defect survived, so it is written down rather than implied.

D-38 records that servers start once for the project and that isolated agents therefore do not get
their tools. Q-18 carries the per worktree design that would lift it.

notes: one protocol gets an entire ecosystem of tools other people maintain. Deliberately after A5,
so the multi agent core is built on tools we control. The permission point is not negotiable: a
third party tool is exactly the thing that most needs the same scrutiny as our own.

### A8-07 Cost versus outcome
`status: review | owner: Codex | branch: feat/commands-and-cost | depends: PG-A6`
`scope: internal/tui/, read-only session and verification interfaces; internal/core remains frozen`

Deliverable: did the more expensive model actually pass more tests, on this project's own history.

Acceptance: the comparison names its sample size and refuses to draw a conclusion from too little
data.

`verify: claude [ ]   codex [x] 2026-07-27`

notes: only meaningful because A2 makes cost exact and A6 makes outcome exact. Almost nothing else
can answer this honestly.

**Claimed 2026-07-27 by Codex.** This view will consume the existing exact-cost and current-evidence
contracts rather than add a second scoring model. Unknown cost, stale evidence and undersized
samples remain named states, not zeroes. Any missing read interface will be added at the narrowest
existing owner rather than widening `internal/core`.

**Built 2026-07-27 by Codex.** The review cycle now includes a project-only cost-versus-outcome
pane. It persists one idempotent observation per session and verified revision, groups exact samples
by model, shows median cost, required-test pass share and green count, and names unknown-cost and
currently unrankable exclusions. Sessions that switched models are excluded rather than charged to
their current model. It refuses a conclusion until at least two models have three exact
samples each and labels a positive result as association rather than causation. SQLite schema 6
adds explicit project scoping and the outcome table; `internal/core` remains unchanged.

### A8-08 Run report export
`status: review | owner: Claude | branch: feat/run-report | depends: PG-A6`
`scope: internal/report/, cmd/canopy/report.go`

Deliverable: one command producing a markdown summary of an agent's changes, test results and
cost, suitable for a pull request body.

Acceptance: the report never claims a verification state the evidence does not support.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: cheap, and it is the artefact that makes agent work reviewable by someone who was not
watching.

The rendering lives in `internal/report` rather than in `cmd/canopy`, so the honesty rules are
testable without standing up a verifier and a repository.

The acceptance criterion is one sentence about honesty and says nothing about formatting, which is
right, because a pull request body is read by somebody deciding whether to merge, usually quickly,
usually trusting it, and always without the screen it came from. Everywhere else in Canopy an
unverified state is a colour that gets refreshed. Here it is a paragraph that outlives the run and
gets pasted somewhere else.

Five things it refuses to do, each with a test and each mutation checked:

1. **Stale is never a pass, and never quietly omitted.** Dropping the tests because they had gone
   stale reads as a change with no test story, which is a more flattering lie than saying the
   evidence expired.
2. **Nothing configured says so.** Rendering the zeroes would produce "0 of 0 required passing",
   which looks like a suite that ran and found nothing wrong.
3. **A green carries its caveat**, or this becomes the one place the failing optional test that
   `Rollup.Caveat` exists to surface disappears again.
4. **An unranked agent says it is unranked and why.** One omitted from a report looks like one
   nobody compared, when it was compared and its evidence was not good enough to place.
5. **Cost has three states**: a figure, a floor when some turns could not be priced, and not known.
   Zero is a claim and would read as free.

Nothing here computes a verdict of its own. Every state comes from the roll-up and the ranking,
which already have tests proving they refuse to guess, and a second opinion assembled in a
formatting package is how the two would come to disagree.

Not wired to a command yet. Gathering a run means reading the verifier and the session store, which
lives in `cmd/canopy/verification.go`, and that file had another branch open in it. The wiring is
mechanical and the honesty rules are the part worth reviewing.

**Wired on 2026-07-28**, after Codex reviewed the merged PR and found the command missing. The scope
line named `cmd/canopy/report.go` and the file was never written, so the deliverable did not exist
and every honesty rule above was tested against a renderer nothing called.

Two things came out of writing it. The command runs the checks itself rather than reading whatever
was last left lying around, because a report describes a revision and evidence gathered earlier does
not. And it polls git once before running anything, since the verifier learns which revision it is
looking at from the poller alone: without that the report said the revision could not be determined,
the tests were unknown, and nothing had changed, and the third of those is the one a reader believes.

Also fixed here: a value that arrives from the repository could write Markdown into the document.
The branch name, the file paths and the reason a check failed all come from whoever opened the pull
request, and the report is pasted into a pull request body. A path ending its code span and opening a
heading that says the run was verified, above a section saying it was not, is this task's acceptance
criterion failing through the one route it did not consider. And the test line printed the passing
count against the required count, so four tests with three required read as "4 of 3".

### A8-09 Shareable skills
`status: deferred | owner: none | branch: none | depends: A8-04`
`scope: internal/agent/`

Deliverable: packaged prompt fragments plus config that users install and share.

Acceptance: an installed skill declares what tools and permissions it expects, and installing it
never silently widens an agent's trust level.

`verify: claude [ ]   codex [ ]`

**Deferred out of 0.1 on 2026-07-28 (D-40).** A distribution format is a compatibility promise, and
making one before there is anybody to make it to is the wrong order. The acceptance clause that
matters, that installing a skill never silently widens trust, is worth keeping exactly as written for
whenever this is picked up.

notes: the contribution flywheel for an open source project, and worth nothing without users, which
is why it is last. The permission point matters: a skill that quietly escalates trust is a supply
chain problem.

### PG-A8 Phase A8 gate
`status: todo | depends: A8-03, A8-05, A8-06`

Both supervisors configure a project, add a slash command, connect an MCP server, and watch a hook
fire on a real state change.

`signed: walid [ ]   classmate [ ]`

---

# Phase A9: robustness, docs, packaging

Goal: someone who is not us installs it and gets value without being told how.

### A9-01 Robustness sweep
`status: review | owner: Claude | branch: mcp/hardening | depends: PG-A8`
`scope: internal/exec/, internal/store/broker.go, internal/git/worktree.go, tests in internal/core, internal/session, internal/tools`

Acceptance: timeouts terminate the process group Canopy started, no final state transition is
dropped under load, huge output cannot freeze the UI, paths and branch names with spaces work,
externally removed worktrees disappear safely, and quitting leaves no child processes behind.

`verify: claude [x]   codex [ ]`

notes: **the first acceptance clause is closed rather than narrowed, as of 2026-07-28. See D-37.** It
had been narrowed on the grounds that after the leader is reaped there is no way to prove a group id
is still ours, which is true, and the conclusion drawn from it was wrong. The premise that made the
post-reap signal look necessary was that declining it would leave orphaned test workers alive. That
was measured and it does not hold: `Wait` does not return until the output pipes close and a child
inherits them, so for the case in question the leader is unreaped and the ordinary path already
reaches the whole group safely. The post-reap probe bought only the case of a child that redirects
its own output, and paid for it with a signal that can land on an unrelated group.

So no group is signalled once its leader has been waited on. Exit is first observed without reaping
(`waitid` with `WNOWAIT` on Linux, kqueue `NOTE_EXIT` on macOS), then the actual reap and every signal
are serialized under the same lock. This closes the gap between `cmd.Wait` returning and a Go flag
being updated; `exec.Child` exists to hold that invariant. What it gives up is stated in D-37 and in
LIMITATIONS: a detached daemon that
outlives its parent is left running. Both halves are mutation checked, the reaped guard and the
atomicity of the guard, and removing either fails a named test.

carries forward P4-01 to P4-07.

**Claimed against an unsigned PG-A8, on instruction rather than by the rule.** A8-05 and A8-06 are
being built right now, so the gate this depends on is not merely unsigned, its own dependencies are
open. Recorded rather than glossed: the engine half of this sweep does not touch the extensibility
layer, so nothing here waits on it, but the ledger's own dependency order was not followed and
somebody reading it later should not have to work that out from the dates.

Three of the six held already and are now covered rather than merely true. Three did not.

- **Timeouts and the right process group.** The escalation was addressing a process group by the
  leader's pid a quarter of a second after that leader may already have been reaped. A group id is
  only reserved while the group has a member in it, so the second signal could land on a job started
  by somebody else in the meantime. **Closed on 2026-07-28 by D-37**: no group is signalled after its
  leader has been reaped. Supported platforms first observe exit without reaping, then serialize
  the actual reap and every signal under one lock.
  The behavioural half, a timeout taking grandchildren with it, held and now has its own test rather
  than sharing the cancellation one.
- **Huge output.** The bound held for what the buffer kept and not for what it allocated: a single
  large write was copied in whole and then trimmed, so eight megabytes arriving in one call cost
  eight megabytes. It is trimmed before the copy now. This is the engine half only. See A9-02 for
  the rest.
- **Externally removed worktrees.** They did not disappear. Git keeps listing a worktree whose
  directory was deleted, marked `prunable`, and discovery read the entry as an ordinary worktree
  that happened to be somebody else's, which is the one ownership answer that stops Canopy tidying
  it up. Prunable entries are dropped from discovery now.
- **Quitting.** `Runner.CancelAll` asked every run to stop and returned without waiting for any of
  them to. The signalling and the reaping happen on each run's own goroutine, so the program exited
  in the gap. It waits now, bounded, for the same reason `exec.Run`'s own wait is bounded.
- **Final transitions under load** held, and did not have a test with more than one publisher in it.
  It does now. The adjacent guarantee did not hold: sequence numbers were assigned under the
  broker's lock and queued outside it, so two publishers could deliver 7 before 6. `core.Event` says
  they are never reordered, and with several agents streaming that claim was only true when one
  goroutine was publishing. Queued under the lock now.
- **Paths with spaces** held throughout, by construction rather than by accident: nothing in the git
  or exec paths builds a shell command. **Branch names with spaces do not work and cannot**, because
  git has no such ref. Canopy refuses one before building a command from it, which is what the
  acceptance line can honestly mean, and the test establishes git's own refusal rather than assuming
  it.

Not done here, deliberately: the interface half of huge output, which is A9-02, and the Now board
row, which has one line per agent and is being edited by the A8 work at the same time.

### A9-02 Interface robustness
`status: todo | owner: none | branch: none | depends: PG-A8`

Acceptance: readable at 80 columns with several agents, resize does not corrupt the frame, every
state is distinguishable without colour, rapid updates never move the selection, the UI rebuilds
from a fresh snapshot, and every error says what to do next.

`verify: claude [ ]   codex [ ]`

notes: carries forward P4-08 to P4-12. P1-07 already meets the 80 column and selection criteria.

For whoever claims this, measured on 2026-07-28 so it does not have to be rediscovered: the
footer drops hints from the right at width minus three, and at 80 columns that arithmetic
removes `ctrl+d agents`, the history hint, the compact hint and the quit hint from the chat
footer, and everything after `? help` from the agents footer, so the keys that reach the rest of
the product are exactly the ones that vanish on the default terminal width. The help overlay is
unreachable while anything is typed, which at 80 columns leaves a mid-draft user no visible route
to help at all. "Readable at 80 columns" should be read as including "navigable at 80 columns".

### A9-03 Themes and help
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A9-02`

Acceptance: at least two themes, both passing the no colour requirement. A keybinding overlay
covering every binding.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: the second theme is monochrome, and it exists to prove the first one is not cheating. Every
state carries a word and a single width glyph, and a palette with no colour in it is the test of
that: if the interface is unreadable there, a meaning is being carried by a hue and it was already
invisible to a colour blind reader and to anybody running with NO_COLOR set. The test asserts it for
both palettes rather than for the default one.

The overlay is generated from one table, so a binding that is added and not listed is a table nobody
edited rather than a screen somebody forgot. Exhaustive on purpose: one that lists most of the keys
teaches people it cannot be trusted, and then they stop opening it.

Question mark opens it, and only when nothing is being typed into, or a message containing one could
never be written. Any key closes it, because somebody who opened it by accident should not have to
find the one key that closes it.

Not done: choosing a theme. The palette is switchable and both are tested, but there is no setting
and no key for it, so the monochrome one is currently reachable only from code.

### A9-04 Honest limitations document
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A9-01, A9-02`
`scope: LIMITATIONS.md`

Deliverable: what Canopy does not guarantee. No sandboxing. No database or dependency isolation
between worktrees. Coarse whole worktree staleness. Secrets a child process prints are not
redactable. macOS and Linux only. Cost figures depend on a dated pricing table.

Acceptance: a reader can tell within a minute whether Canopy will lie to them, and how.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: LIMITATIONS.md, and it is meant to be read before anything else.

Grouped by how somebody would hit them rather than by phase. Every entry traces to a decision or a
task in this repository, because a limitation nobody wrote down can only be rediscovered by getting
burned by it.

Needs re-reading before each release. Several entries were already stale by the time it was
committed, which is the failure mode this kind of document has.

### A9-05 Packaging and install
`status: review | owner: Claude | branch: feat/verification-and-release | depends: A9-04`

Acceptance: a stranger installs a build, adds a key, and runs an agent without being talked
through it.

`verify: claude [x] 2026-07-27   codex [ ]`

notes: goreleaser for darwin and linux on both architectures, a release workflow on a `v*` tag, a
makefile, and a version stamped through the same three ldflags whether the binary came from `make
install` or from a release archive.

Windows is not built. The process handling uses unix process groups and the Windows equivalent has
not been designed, so shipping it half working would be worse than not shipping it.

Homebrew is written and commented out, because the tap repository does not exist yet and a release
that fails on a missing tap is worse than one that ships without a formula.

Not verified end to end: no tag has been pushed, so the release workflow has never run.

### PG-A9 Phase A9 gate
`status: todo | depends: A9-05`

Both supervisors watch someone outside the team install it and use it.

`signed: walid [ ]   classmate [ ]`

---

# Phase E: what the window holds and what the tokens cost

Goal: Canopy spends fewer tokens than any competitor for the same work, can prove the saving on
screen, and never gets quietly dumber doing it.

**Nothing in this phase blocks 0.1.** The tag waits on PG-M and on nothing here. E is engine work
and U below is interface work, so once the supervisors set a 2.1 style boundary the two phases can
run as parallel lanes, with strict order applying inside each lane.

Planned 2026-07-28 from an audit of the send path rather than of the ledger, which is why it reads
like a list of gaps: history is rebuilt in full on every step of every turn with no elision
anywhere; tool results are re-sent verbatim forever, so three reads of the same file sit in the
window three times; the token estimator counts request text, reply and thinking only, so the tool
traffic that actually fills the window is invisible to the meter; compaction is manual despite
D-28 saying automatic; `ErrContextLength` is classified by both adapters and read by nobody; cache
savings are computed and shown only in headless `ask`; and `config.Instructions`,
`AgentProfile.SystemPrompt`, `AgentProfile.MaxTokens`, `Loop.MaxTokens` and the fallback chain are
all declared and unreachable. The principles these tasks implement are D-42. The unreachable
inventory is D-44.

### E-01 One honest token estimator
`status: todo | owner: none | branch: none | depends: PG-M`
`scope: internal/core/context.go, internal/session/compaction.go, internal/session/engine.go`

Deliverable: context accounting that counts what is actually sent. Tool call inputs, tool result
contents, tool schemas and the system prompt join the count; the two duplicated bytes-per-token
constants become one estimator; the provider's reported figure is preferred the moment one
exists. Usage incurred on a failed turn is recorded rather than discarded, because the provider
billed for it whether or not the turn ended well.

Acceptance: on a recorded tool-heavy session, the pre-response estimate lands within fifteen
percent of the provider's next reported input count, where today it misses by the whole size of
the tool traffic. The meter and the compaction split read the same estimator. `Estimated` still
renders as "about". A turn that fails after consuming tokens shows those tokens in the session
totals.

`verify: claude [ ]   codex [ ]`

notes: every saving in this phase is measured by this meter, so it comes first. `internal/core`
is frozen under the P1-01 rule; the estimator lives there, so this task starts with the joint
discussion rather than discovering the freeze at review. Worth checking while in there: whether
the provider's count-tokens endpoint is worth a pre-flight call on Anthropic, recorded either way.

### E-02 Compaction happens by itself, and says so
`status: todo | owner: none | branch: none | depends: E-01`
`scope: internal/session/, internal/tui/chat/`

Deliverable: the unbuilt half of D-28 and A3-06. Automatic compaction near the limit, and
compact-and-retry when a request comes back `ErrContextLength`, which today is classified twice
and consumed nowhere.

Acceptance: crossing the threshold compacts at the next turn boundary, never mid-turn, and
announces itself in the transcript before the next request is sent. A turn refused for length is
compacted and retried once, visibly. Stored history stays complete and searchable. A failed
summary call fails the turn with the provider's own words rather than silently shrinking
anything. Auto can be turned off in config; ctrl+r and /compact stay.

`verify: claude [ ]   codex [ ]`

notes: D-28 decided this in 2026 July and A3-06's deliverable claimed it; the honest ledger now
says partial. The announcement is not politeness, it is the contract: an agent that got a
summary where its memory was is an agent whose next answer needs reading differently.

### E-03 The compaction bill is bounded
`status: todo | owner: none | branch: none | depends: E-02`
`scope: internal/session/compaction.go`

Deliverable: the summary call gets an output bound, where today it inherits the provider default
of 32k output tokens, and its cost stays on screen like any other turn. If Q-19 is answered yes,
the summary may run on a cheaper named key; if not, the option does not exist.

Acceptance: a compaction can never spend more output than its bound, and the bound is visible in
the transcript marker next to the before and after figures. The cheap-key option appears only
behind a yes on Q-19, and when used the transcript names which key summarised.

`verify: claude [ ]   codex [ ]`

notes: compaction exists to save money and currently has no ceiling on what it may spend doing
so. The named-key architecture makes the cheap-summariser lever one field, which is exactly why
it needs a supervisor answer first: the whole conversation transits whichever provider the key
names.

### E-04 Superseded reads leave the outgoing window
`status: todo | owner: none | branch: none | depends: E-01`
`scope: internal/session/ (a transform on the outgoing request; internal/core stays frozen)`

Deliverable: when the same deterministic read, same tool and same canonical arguments, has a
newer result later in the conversation, or a file read has been invalidated by a later edit to
the same path, the older result in the outgoing request is replaced by a one line stub naming
what superseded it. Stored history is untouched and the transcript still shows everything.

Two lines hold it honest and profitable. Only Canopy's own deterministic reads are elidable:
read_file, glob, grep. Shell output and MCP results ride along verbatim forever, because nothing
can re-derive them and shortening them is destroying evidence. And an elision rewrites the
prefix, which forfeits the provider cache, so elisions take effect only when the prefix is being
rewritten anyway, at a compaction, after the cache has gone cold, or when the pricing table says
the saving beats the rewrite premium. Computed, not assumed, and stated in the transcript.

Acceptance: reading the same large file three times costs its size once from the next rewrite
onward. An edited file's stale read is stubbed. Shell and MCP output is never elided. The meter
counts the rewritten request. Disabling elision in config restores verbatim history. The
storage diff before and after is empty.

`verify: claude [ ]   codex [ ]`

notes: this is where the token bill actually lives for a coding agent, and it is D-42's second
principle made mechanical. It is a per-request policy, not history, which is why it belongs in
`internal/session` rather than in the frozen core. The read ledger the edit tools already keep
for freshness is the natural supersession index.

### E-05 Cache health on screen
`status: todo | owner: none | branch: none | depends: A2-07, E-01`
`scope: internal/tui/, internal/pricing (read only)`

Deliverable: the visible half of A2-07, in the product rather than only in headless `ask`.
Cached against fresh input per turn, the net saving including the negative filling turn, and a
plain warning when a long Anthropic conversation's cache reads collapse to zero, which is the
silent degradation A2-07's notes predicted and nothing currently watches for.

Acceptance: the numbers come from `pricing.Saving` and recorded usage, not a second calculation.
A session whose cache has stopped being read says so in words. Unpriced endpoints say nothing
rather than zero, per D-32.

`verify: claude [ ]   codex [ ]`

notes: caching is the saving that degrades silently, so its absence must be as visible as its
presence. This is also the screen that makes E-04's rewrite arithmetic auditable by eye.

### E-06 Reading less at the source
`status: todo | owner: none | branch: none | depends: PG-M`
`scope: internal/tools/files.go`

Deliverable: `read_file` takes an optional line range and always reports the total line count so
the model knows the file continues; `grep` returns context lines around matches so a match stops
forcing a whole-file read.

Acceptance: a ranged read costs the range. Line numbers agree between ranged and full reads. The
1 MiB refusal stands for unranged reads and now names the range syntax as the way in. The read
ledger still catches an edit after a ranged read.

`verify: claude [ ]   codex [ ]`

notes: the cheapest token is the one never fetched. This is deliberately the only task in the
phase that changes what the model can do rather than what Canopy sends, and it is additive: the
old call shape keeps working.

### E-07 Project instructions reach the model
`status: todo | owner: none | branch: none | depends: A8-03`
`scope: internal/session/, internal/config (read only)`

Deliverable: the injection half of A8-03. Project instructions prepended to the system prompt for
every agent in the project, dispatched agents included, size-bounded, and placed under the system
cache breakpoint so they are cached rather than re-billed each turn.

Acceptance: an instruction visibly changes behaviour for a direct and an isolated agent alike.
Oversized instructions are refused with a named error, never truncated silently. The E-08
inspector lists them as their own line with their own size.

`verify: claude [ ]   codex [ ]`

notes: every competitor treats a project instructions file as table stakes and Canopy parses one
into a field nothing reads. The bound exists because instructions sit in every request of every
agent in the project; an unbounded file is a standing tax nobody sees.

### E-08 The window inspector
`status: todo | owner: none | branch: none | depends: E-01, E-04`
`scope: internal/tui/chat/`

Deliverable: `/context` grows from a percentage into an inventory of the next request: which
mode's system prompt, project instructions, how many tool schemas and their size, the compaction
summary, elided stubs, verbatim turns, per-item token estimates, and where the cache breakpoints
sit with the last turn's cache read share.

Acceptance: a test builds the real request and asserts the inspector's list agrees with the
wire. Same parts, same order, sizes within the estimator's stated error. Every mechanism in this
phase is auditable from this one screen.

`verify: claude [ ]   codex [ ]`

notes: the transparency flagship. Competitors make you guess what is in the window; Canopy builds
the request, so Canopy can show the request. It is also the tool that keeps E-02 and E-04 honest,
which is why it shares their dependencies rather than trailing the phase.

### E-09 Dead levers wired or cut
`status: todo | owner: none | branch: none | depends: E-01`
`scope: internal/session/, internal/provider/chain.go, internal/agent/, internal/core (joint discussion required)`

Deliverable: each declared-and-unreachable efficiency lever either works or is removed with a
D-40 style note: `AgentProfile.SystemPrompt`, `AgentProfile.MaxTokens`,
`AgentProfile.Temperature`, `Loop.MaxTokens` which the engine never sets, and the fallback chain
that `NewChain` builds and no resolver ever returns.

Acceptance: nothing in the tree is declared, documented and unreachable. Whatever is wired has a
test proving the setting changes the request; whatever is cut is recorded in DECISIONS with what
existed. A2-08 leaves partial in the same commit that wires or cuts the chain.

`verify: claude [ ]   codex [ ]`

notes: D-44 names the pattern; this task clears the efficiency third of the inventory. The
interface levers are U's problem and listed there.

### E-10 Thinking blocks in the tool loop, checked against the live contract
`status: todo | owner: none | branch: none | depends: PG-M`
`scope: internal/agent/loop.go, internal/provider/anthropic/, DECISIONS.md (D-31 companion)`

Deliverable: an answer from the current API reference and a live test, not from memory, to
whether the tool loop must return thinking blocks with the preceding assistant turn. Today
thinking is captured, stored, displayed and never sent back, on models where D-31 records that
thinking is on by default. If replay is required, the fix.

Acceptance: the contract is recorded next to D-31's other checked rules with the date checked.
If replay is required, the loop carries thinking within a turn and a live test in the M-01 style
proves a multi-step tool turn survives it. The token cost of whichever answer is measured and
written in these notes, not estimated.

`verify: claude [ ]   codex [ ]`

notes: filed in the efficiency phase because both answers move the bill: replay costs tokens,
and a contract violation costs retries. A plausible wrong value here is worse than an obvious
one, which is the same sentence D-31 opens with.

### PG-E Phase E gate
`status: todo | depends: E-02, E-04, E-05, E-08`

Both supervisors watch the same task run twice on a mid-sized repository, once on the 0.1 build
and once on this phase, and see the second run cost measurably fewer tokens with nothing lost
that the inspector cannot explain.

`signed: walid [ ]   classmate [ ]`

---

# Phase U: the interface at its best

Goal: the person running several agents is never trapped, never deaf, and never guessing. Every
affordance the engine already has reaches their hands.

**Nothing in this phase blocks 0.1** and the lane note at the top of Phase E applies here too.

Planned 2026-07-28 from a screen-by-screen audit. The pattern it found is the mirror of Phase E's:
the engine has hands the interface never grew. Budgets pause agents and nothing can set a cap.
Steering can be cleared and nothing offers it. Grants can be listed and revoked and nothing shows
them. Retryability is classified and nothing retries. An agent can be removed and no key does it.
And two genuine traps: every keystroke answers a pending permission prompt, so scrolling up to
read what you are approving refuses it; and the screens that would tell you another agent is
blocked are locked exactly when your own agent is blocked.

### U-01 Reading the prompt is not answering it
`status: todo | owner: none | branch: none | depends: PG-M`
`scope: internal/tui/chat/`

Deliverable: while a permission prompt is pending, navigation navigates. Scroll keys, arrows,
page keys and the wheel move the transcript so the person can reread the reasoning above the
prompt; only an explicit answer answers.

Acceptance: pgup, pgdown, arrows, ctrl+home and ctrl+end scroll the transcript while a prompt is
pending, and none of them decides anything. `y` and `a` still decide. Enter, esc and every other
non-navigation key still refuse, because the reflex key on an unread prompt meaning no is the
safety property, and it is kept. A test enumerates the navigation set so a new binding cannot
silently join the refusal path.

`verify: claude [ ]   codex [ ]`

notes: D-43's first rule. Today `pgup` on a prompt is a refusal indistinguishable from a
deliberate one, which punishes exactly the person who wanted to read before deciding.

### U-02 The always that grants nothing
`status: todo | owner: none | branch: none | depends: A5-08`
`scope: cmd/canopy/verification.go, internal/session/dispatch.go, internal/permission/`

Deliverable: the `spawn_agents` confirmation gets a real scope. Today it is built with an empty
`Decision.Scope`, so the prompt renders "a always," with nothing after the comma, and pressing
`a` records a grant that can never match anything. On the one prompt that spends real money on
several agents, the always affordance is a visible blank and a silent no-op.

Acceptance: the prompt names the scope in words, the same way run and edit prompts do. Pressing
`a` grants something `Grants.Covers` actually matches, scoped to dispatch and to the session. A
test fails on any prompt whose scope renders empty, so the next confirmation added cannot ship
the same blank.

`verify: claude [ ]   codex [ ]`

notes: found by reading the render path, not by a report from a user, which is the cheap time to
find it.

### U-03 Attention crosses screens
`status: todo | owner: none | branch: none | depends: PG-M`
`scope: internal/tui/, internal/tui/chat/, internal/tui/agents/`

Deliverable: an agent needing a person is visible from every screen, and no screen is ever
locked. The header carries "N need you" everywhere, not only inside the agents screen's own
context line. Navigation away from a chat with a pending prompt is allowed; leaving is not
answering, and the prompt is still there on return. An optional terminal bell on the transition
into needing attention, off by default, configurable.

Acceptance: with the chat focused and a background agent hitting a permission prompt, the header
says so within a second, in a word and a glyph that survive NO_COLOR. ctrl+d, ctrl+k and ctrl+n
work while the current chat has a pending prompt. The pending prompt is unchanged on return. The
bell fires once per transition, never per frame, and can be turned off.

`verify: claude [ ]   codex [ ]`

notes: D-43's second rule, and the one the product's premise depends on. The current behaviour
is the worst combination: the only screen that shows who needs you is unreachable exactly when
someone needs you.

### U-04 A way back to every conversation
`status: todo | owner: none | branch: none | depends: A3-02`
`scope: internal/tui/, internal/tui/chat/, internal/session (read interfaces only)`

Deliverable: the session picker whose absence the code already apologises for in two comments. A
screen listing this project's conversations with title, code, last activity, cost and fork
lineage, opened from chat, searchable with the full-text index that already exists. Enter
resumes in place; `canopy pickup` stays for the terminal.

Acceptance: a conversation from yesterday is reachable in under five seconds without quitting.
ctrl+n no longer strands the conversation it left. A forked session shows where it came from.
Search narrows by content, not only title. Another project's sessions do not appear, matching
the pickup rule.

`verify: claude [ ]   codex [ ]`

notes: sessions persist, survive quit, and are unreachable from inside the product; the only
handle is a code printed on exit. The picker is also where A3-07's fork-point display finally
gets its screen, which that task's notes deferred to exactly here.

### U-05 A failed turn can be retried
`status: todo | owner: none | branch: none | depends: PG-M`
`scope: internal/tui/chat/, internal/session/`

Deliverable: recovery affordances for the failure taxonomy A2 built and nothing consumes. A
failed turn offers retry without retyping. A rate limit shows the Retry-After the provider
already sent, counting down. A network failure says plainly that the network failed. Retryable
kinds retry; the kinds that deliberately do not fall through, authentication above all, say what
to fix instead.

Acceptance: a rate-limited turn shows a countdown and a retry key, and retrying does not
duplicate the failed turn in history. An auth failure never offers retry and names the
credential. Killing the network mid-turn produces words about the network, not a bare red line.
When E-09 wires the fallback chain, a fallback taken is announced with both ends named, per
A2-08; until then this task does not wait on it.

`verify: claude [ ]   codex [ ]`

notes: `Retryable()` has no non-test caller today, so every classified error ends the same way,
with the user retyping. The taxonomy was built for this task; it just took a year of subjective
time to arrive.

### U-06 The first run holds your hand
`status: todo | owner: none | branch: none | depends: PG-M`
`scope: internal/tui/keys/, internal/tui/, cmd/canopy/`

Deliverable: the add-key wizard ends with a selected, tested credential. Storing a key selects
it. The wizard offers the same live check `canopy keys test` already performs, so a typo is
found before the first message rather than by it. Rate entry for OpenAI-compatible endpoints
exists in the interface, not only as a CLI nobody is told about. Startup warnings, history not
saving, tools unavailable, verification not running, appear inside the interface instead of on a
stderr the alt screen erases.

Acceptance: on a machine with no key, a person adds one and sends a message without touching the
CLI or being coached, and the key they added is the key that answers. A wrong key is named
before chat. An unpriced endpoint can be given a rate in the interface and the header prices the
next turn with it, labelled as the user's rate per D-32. A startup warning is readable after the
alt screen opens.

`verify: claude [ ]   codex [ ]`

notes: M-06 got the screens to explain themselves; this closes the two holes the audit found
past explanation: a stored-but-unselected key that works with one credential by coincidence and
breaks with two, and warnings printed into a void.

### U-07 Budgets reach the user
`status: todo | owner: none | branch: none | depends: A5-09`
`scope: internal/tui/chat/, internal/tui/agents/`

Deliverable: the interface for the caps A5-09 built and verified. Set a per-agent or per-session
cap from the conversation, see proximity to it in the header, and when an agent pauses at its
cap, be told where and offered the raise, exactly the resume path the engine already supports.

Acceptance: a cap set in the interface pauses the agent before the request that would cross it,
the pause names the cap and the spend, raising resumes without losing anything, and the
`Budget.Status` honesty about uncosted requests reaches the screen verbatim. No cap is ever
enforced that the screen does not show.

`verify: claude [ ]   codex [ ]`

notes: the engine half was verified by Codex in July and has never once been driven by a person,
which the A5-09 note now records. This is the smallest task in the phase relative to how often
its absence will be noticed.

### U-08 Steering you can take back
`status: todo | owner: none | branch: none | depends: A5-07`
`scope: internal/tui/chat/, internal/session/steer.go (caller only)`

Deliverable: queued guidance can be cancelled before delivery. The steering pane that already
shows the queue verbatim gains a cancel affordance wired to `Engine.ClearSteering`, which exists
and is called by nothing. Delivered guidance stays marked in the transcript so what the agent
was told remains readable afterwards.

Acceptance: queue a correction, cancel it, and the agent's next turn shows no trace of it.
Cancel with nothing queued is a no-op that says so. Delivery still happens only at the turn
boundary, per A5-07; this task adds no new interruption path.

`verify: claude [ ]   codex [ ]`

notes: steering without interrupting is the feature; steering without take-backs is a trap for
anyone who typed the wrong correction. The engine grew the take-back and forgot to tell the
interface.

### U-09 No reflex spends money
`status: todo | owner: none | branch: none | depends: E-03`
`scope: internal/tui/chat/`

Deliverable: no single unconfirmed keystroke starts a paid model call. ctrl+r, which half the
world's fingers press expecting history search, currently fires a compaction, a real request on
a real key, locking the input while it runs. It gains a one-line confirmation naming what it
will summarise, on which key, within which bound, and what it roughly costs. The resolver's
stale "press k" message becomes "press ctrl+k" in the same commit, since it is two lines away.

Acceptance: ctrl+r alone spends nothing. The confirmation states turns, key and bound before
anything is sent. /compact goes through the same confirmation. A test walks every binding in the
help table and asserts none reaches a provider call without a confirmation or an explicit send.

`verify: claude [ ]   codex [ ]`

notes: D-43's third rule. Whether ctrl+r, ctrl+k and ctrl+d should keep their current meanings
at all is taste, so it is Q-21 for the supervisors rather than decided here; making the paid one
free to press by accident is not taste.

### U-10 The input box under your fingers
`status: todo | owner: none | branch: none | depends: M-02`
`scope: internal/tui/chat/input.go`

Deliverable: the editing keys a multiline box implies. Vertical caret movement inside a draft,
with history recall only from the boundary rows so up on line three moves the caret instead of
destroying the draft. Word left and right. Kill to end of line to pair with the existing kill to
start. A large paste collapses to one line naming its size, "pasted 214 lines", expandable,
instead of silently showing the last six lines of a box that scrolls.

Acceptance: a four line draft is fully navigable by keyboard. Up from the top row recalls
history exactly as M-02 defined; up from any other row moves the caret; the draft-preservation
rule from M-02 is unchanged. A 200 line paste shows its count before send and sends its full
content. No existing binding changes meaning.

`verify: claude [ ]   codex [ ]`

notes: M-02 built recall correctly and left the caret one-dimensional. The paste collapse is
honesty as much as ergonomics: a box showing six lines of a 200 line paste is a screen
under-reporting what is about to be sent, to a model, at a price.

### U-11 Copy and find without the mouse
`status: todo | owner: none | branch: none | depends: PG-M`
`scope: internal/tui/chat/, internal/tui/clipboard/`

Deliverable: the transcript's contents reachable by keyboard. Copy the last reply. Copy the last
code block, which is the thing actually wanted nine times in ten. Find within this conversation
with match navigation, using the search machinery that already indexes every turn, so the only
in-product search stops being the CLI.

Acceptance: both copies work over ssh, which means the OSC 52 path, with the same visible
confirmation the mouse path shows. Find highlights matches, n and N walk them, and the scroll
position survives closing the find. Neither affordance requires mouse reporting to be on.

`verify: claude [ ]   codex [ ]`

notes: drag-to-copy exists and costs native terminal selection while mouse reporting is on,
which LIMITATIONS already documents. Keyboard copy is the version that works everywhere the
product claims to work, ssh included.

### U-12 The agents screen grows hands
`status: todo | owner: none | branch: none | depends: A5-11`
`scope: internal/tui/agents/`

Deliverable: acting on an agent from where you see it. Stop a running agent's turn. Remove a
stopped agent, wired to `Engine.RemoveAgent`, confirmed, since it discards a conversation.
Per-agent cost and tokens on the list rows and pane borders, which `summary()` already computes
and only a placeholder currently shows. Tool calls in panes get their kind labels by threading
the registry through, so the same call reads the same in a pane as in the chat.

Acceptance: a runaway agent is stopped from the mosaic in two keystrokes without switching
screens. Remove asks, names the agent, and refuses while a turn is in flight. Every pane border
shows what its agent has cost, degrading gracefully at narrow widths. The stop key appears in
the footer and the help overlay, per M-06's rule that nothing is reachable only by an unlisted
key.

`verify: claude [ ]   codex [ ]`

notes: the screen for watching several agents currently cannot act on any of them; every action
is a screen switch away, which at six agents is the difference between a glance and a tour.

### U-13 Grants on the table
`status: todo | owner: none | branch: none | depends: PG-M`
`scope: internal/tui/chat/, internal/permission (callers only)`

Deliverable: what has been allowed this session, visible and revocable. A `/grants` view backed
by `Grants.Granted()`, whose doc comment has promised an interface since the day it was written,
with revocation through `Grants.Revoke`. Scope rendered in the same words the prompt used when
it was granted.

Acceptance: press `a` on a prompt, and the grant appears with its scope. Revoke it, and the next
matching call prompts again. An empty list says no standing permissions exist rather than
showing nothing. The audit trail keeps recording either way; this shows standing permissions,
`/trail` stays the record of what happened.

`verify: claude [ ]   codex [ ]`

notes: `a` is one keystroke and currently irreversible for the life of the session, invisible
five minutes later. A standing permission nobody can enumerate is a small lie of omission about
who can do what, which is the exact class of thing this product exists to refuse.

### U-14 Dispatch comes back with an answer
`status: todo | owner: none | branch: none | depends: A5-08, A6-04`
`scope: internal/session/, internal/tui/chat/`

Deliverable: the fan-out gets a join. When a dispatched agent reaches a terminal state, the
orchestrating conversation receives a bounded result at its next turn boundary, steering style:
the agent's final text capped to a fixed budget, and its verification standing taken from the
roll-up, never from the agent's own claim. The orchestrator can then compare and synthesise
instead of the human relaying between screens.

Acceptance: dispatch two agents at the same task and the orchestrator learns of both completions
without the user switching screens, each summary within its token budget, each carrying the
verifier's verdict for that agent's current revision, stale marked stale per A6. The injection
is visible in the transcript as what it is. A child that produced nothing says so rather than
being omitted. No summary is injected mid-turn.

`verify: claude [ ]   codex [ ]`

notes: today the tool result says the agents are working and the parent never hears another
word, so the comparison the product exists for is done by a person with a memory. Turn-boundary
delivery reuses A5-07's queue semantics on purpose: one mechanism for things that arrive while
the model is thinking. The cap plus the roll-up keeps this from becoming the context leak
sub-agents were deferred for in D-40, and it deliberately does not resurrect A8-01; the children
here are the flat dispatch A5-08 already ships, not nested agents.

### U-15 Cycling past a mode is not choosing it
`status: review | owner: claude | branch: tui/mode-settle | depends: none`
`scope: internal/tui/chat/, internal/tui/app.go, internal/session/engine.go`

Deliverable: `shift+tab` moves a selection, and the mode it stops on is applied a short wait after
the last press. Walking the ladder from cruise to build no longer puts the conversation into plan
and confined on the way. The wait is never the last word: sending a message, naming a mode with
`/mode`, leaving the conversation and quitting all apply the selection immediately. The box says
what is enforced and what is coming without confusing them, `cruise → plan`, and the ladder asks
the engine which modes it would accept rather than finding out by attempting each one.

Acceptance: from cruise, three presses that land on build leave the conversation in cruise
throughout and in build alone afterwards, with no mode entered on the way. A single press applies
by itself with no further keystroke. A timer belonging to a mode that was passed through is
dropped rather than applied late. Press then send, and the message is sent in the chosen mode. The
top edge names the mode in effect first and the selection second, and the ceiling test still holds:
no keystroke sequence promotes a read-only agent.

`verify: claude [x] 2026-07-29   codex [ ]`

notes: D-45, raised by Walid on 2026-07-29 and built the same day, ahead of the lane order because
it is a defect in a shipped safety setting rather than an addition. Out of order under 1.3, which
is recorded here rather than left to be inferred. The engine gained one exported method,
`ModeUnusable`, which is the refusal `SetMode` already made asked as a question; a session test
holds the two answers together so they cannot drift. `internal/session/engine.go` is outside this
round's file boundary in section 2.1 and the change is additive, with no existing behaviour
touched.

### PG-U Phase U gate
`status: todo | depends: U-01, U-03, U-04, U-05, U-06`

Both supervisors watch a person who has used Canopy once before run three agents at the same
task, get rate limited, retry, answer a prompt from another screen, return to yesterday's
conversation, and stop a runaway agent, all without touching the CLI or asking a question.

`signed: walid [ ]   classmate [ ]`

---

# Retired tasks

Replaced by the 2026-07-26 re-plan. Kept so the reasoning is not lost.

| Old | Fate |
|---|---|
| P2-01, P2-02 | now A5-01, A5-02, unchanged |
| P2-03, P2-04 | now A6-01, A6-02, unchanged |
| P2-05 to P2-07 | now A6-03 and A4-03, which turn out to be the same machinery |
| P2-08 | folded into A3-01, the store now carries sessions as well as workspaces |
| P2-09 to P2-14 | now A6-04, per agent instead of per worktree |
| P3-01, P3-02 | config validation, now A8-03 |
| P3-03 | secret handling, now A1-04 and stronger |
| P3-04, P3-05 | repository trust. Superseded by A4-04, which covers a harder problem |
| P3-06 to P3-12 | service health. Deferred past A9. D-06 reopens then |
| P4-01 to P4-12 | now A9-01 and A9-02 |
| P4-13 | auto run on change, D-19. Still open, now answered in the A6 context |
| P4-14, P4-15 | acceptance suite, folded into each phase gate rather than saved for the end |
| P4-16, P4-17 | now A9-04, A9-05 |
| P5-01 to P5-04 | pilot. Worth doing, after A9 |
| P6-01 | the conditional expansion menu is gone. The expansions became the plan |
| corrections section 6 | environment and setup contract, restored as A5-04 after being dropped in error |

---

## Appendix: change log for this file

Append one line per structural change (adding, removing or reordering tasks). Do not log ordinary
status or verification updates.

| Date | Agent | Change |
|---|---|---|
| 2026-07-26 | Claude | Created ledger. Phases 0 to 6 derived from Canopy-Pre-Build-Corrections.md sections 11 and 12. |
| 2026-07-26 | Claude | Re-planned. Canopy is an agent runtime, so phases 2 to 6 became A1 to A7. Phases 0 and 1 untouched. Surviving tasks keep their old IDs in their notes, and a retired tasks table records where each one went. |
| 2026-07-26 | Claude | Expanded to A1 to A9 after a full spec and features review. Added persistence, compaction, fallback chains, session forking, plan mode, checkpoints, web tools, sub agents, handoff, MCP, hooks, slash commands, skills, diff review, commit helper, conflict radar, cost preview, ready-to-review queue. Restored the environment setup contract as A5-04, which the previous re-plan dropped in error. |
| 2026-07-27 | Claude | Added phase M between A7 and A8, from Walid using the built program rather than its tests. Six tasks: system tools proven from the chat, input history, a detailed task list on screen, a new conversation key, a better logo, and the first ten minutes. Runs before A8. A4-10 hands its remaining half to M-03 and stays partial. Nothing renumbered. |
| 2026-07-27 | Claude | Phase M built. Added `internal/tui/brand` for the mark, `internal/core/task.go` for the shared task shape, `cmd/canopy/worktrees.go` for a real worktree store, and `cmd/canopy/live_test.go` for the opt-in provider test. Storage schema went to version 5 for the task column. |
| 2026-07-28 | Claude | Follow-ups from Codex's review of PRs #20 to #25, on `fix/review-followups`. Six defects fixed and two unreachable features wired. Storage schema went to version 7 for the mode column. The severe one was found on the way: the green gate never waited for the tests, so runway reverted every turn it was given. A8-05 and A8-08 were built and never called from anywhere, which is now the fourth time a complete package has shipped with nothing reaching it. |
| 2026-07-28 | Claude | Added phases E and U after PG-A9, from an audit of the send path and of every screen rather than of this ledger: ten efficiency tasks and fourteen interface tasks, none of which blocks 0.1. Four blocks set back to partial where their prose outran the code: A3-06 (no auto compaction, meter blind to tool traffic), A2-07 (saving visible only in headless ask), A2-08 (chain has no caller), A8-03 (instructions parse and reach nothing). Notes added to A5-09 and A9-02. Principles recorded as D-42 to D-44, new questions Q-19 to Q-21. |
| 2026-07-29 | Claude | Added U-15 from Walid using the built program: the mode key applied every rung it walked past, so cycling from cruise to build put a working agent through plan. Built the same day, out of lane order, since it is a defect in a shipped safety setting. Recorded as D-45. The engine gained `ModeUnusable`, the refusal `SetMode` already made asked as a question. Section 2.0 recounted, which the two new phases had left stale. |
