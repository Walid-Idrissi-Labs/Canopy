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
| Claude | A1-01, provider and key types | next feature branch | none |
| Codex | none | none | none |

**The roadmap was re-planned on 2026-07-26.** Canopy is an agent runtime, not a worktree monitor.
Phases 0 and 1 are unchanged and still done. Old phases 2 to 6 are replaced by A1 to A7, and every
task that survived carries its original ID in its notes so nothing already reasoned about was
thrown away. See D-22 in DECISIONS.md and the internal replan note.

Done and carrying forward: P0-01 to P0-07 and P1-01 to P1-07. The core contract, the state
machine, the roll-up, the fake store, the headless harness and the dashboard.

Codex: **A2 is the obvious thing to claim.** It is independent of A1, because a provider client can
take a key as a parameter long before there is a registry to fetch one from. A3 depends on A2.

Integration cadence: no fixed calendar, see D-12. Short lived branches, merge main in before you
push.

---

## 3. Scope reminder

Read this before claiming anything.

**Canopy is an agent runtime.** It holds provider API keys as named credentials, runs coding
agents against them, and gives each agent its own git worktree. The differentiator is that it
knows whether each agent's work actually passes, and can rank agents by that rather than by how
confident they sounded.

Same category as OpenCode and Claude Code, plus multi agent orchestration on worktrees, plus a
verification engine none of them have.

Build order, and what each phase buys:

| | | What it gets you |
|---|---|---|
| A1 | named keys, secure storage | credentials exist |
| A2 | provider client, streaming | a real reply comes back |
| A3 | chat interface | **it looks and feels like the product** |
| A4 | tools and permissions | it can actually change code |
| A5 | many agents, one worktree each | the differentiator |
| A6 | verification per agent | the thing no incumbent does |
| A7 | robustness, docs, packaging | someone else can install it |

A3 is the milestone that makes the product recognisable. A5 is where the worktree work stops being
a detour and becomes load bearing.

### What is out of scope, and stays out

- No cloud, no account, no hosted control plane. Local first, keys never leave the machine.
- No Windows until process group and terminal semantics are designed for it, not approximated.
- No headless unattended merging. A human stays in the loop on anything destructive.
- **Canopy never claims to sandbox what it runs.** See A4-04. A worktree is file isolation, not a
  security boundary, and saying otherwise would be the same class of error as a false green.

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

# Phase A1: named keys and secure storage

Goal: Canopy can hold provider credentials by name, and can be trusted with them.

Nothing here talks to a provider yet. Keys come first because every later phase depends on them,
and because credential handling is the one thing that is genuinely unpleasant to retrofit.

### A1-01 Provider and key domain types
`status: todo | owner: none | branch: none | depends: none`
`scope: internal/core/`

Deliverable: `Provider`, `KeyRef`, `KeyMetadata` and `AgentProfile` in the shared contract. A
`KeyRef` names a credential, it never carries the secret. A profile binds a name to a provider, a
key, a model, a system prompt and a set of defaults, so "claude" or "kimi" resolves to everything
needed to start an agent.

Acceptance: a `KeyRef` cannot hold a secret value, enforced by the type rather than by convention.
Round tripping a profile through JSON never produces a secret.

`verify: claude [ ]   codex [ ]`

notes: shared contract file, so changes need a joint discussion. The separation between a
reference and a secret is the whole design: if the secret is never in the type that gets logged,
serialised, snapshotted or put in an event, it cannot leak through any of them.

### A1-02 Key store on the OS keychain
`status: todo | owner: none | branch: none | depends: A1-01`
`scope: internal/keys/`

Deliverable: store and retrieve secrets through the macOS Keychain and the Linux secret service.
A file backend exists only as an explicit, loudly warned fallback, and never as a silent default.

Acceptance: a stored key survives a restart, is absent from the process's own config files, and
the file fallback refuses to run without the user opting into it by name.

`verify: claude [ ]   codex [ ]`

notes: writing plaintext credentials to disk because the keychain was awkward is exactly the kind
of shortcut that is invisible until it is a headline. If the keychain is unavailable, say so and
stop, do not quietly degrade.

### A1-03 Key management commands
`status: todo | owner: none | branch: none | depends: A1-02`
`scope: cmd/canopy/`

Deliverable: `canopy keys add`, `list`, `remove` and `test`. Adding reads from a prompt or stdin,
never from an argument.

Acceptance: no command prints a secret. `keys list` shows name, provider, created date and a
fingerprint. A key passed as a command line argument is rejected with an explanation.

`verify: claude [ ]   codex [ ]`

notes: rejecting `--key sk-...` matters because arguments end up in shell history and in the
process list, where any other user on the machine can read them.

### A1-04 Redaction guarantees
`status: todo | owner: none | branch: none | depends: A1-03`
`scope: internal/keys/, internal/core/`

Deliverable: a test suite asserting secrets cannot reach output Canopy controls.

Acceptance: a known secret is planted, then every rendered surface is searched for it: snapshot
JSON, event stream, log buffers, dashboard frames, error messages and panic output. None contain
it. Errors from a provider carry the failure without echoing the credential back.

`verify: claude [ ]   codex [ ]`

notes: the honest boundary from D-20 still applies. Canopy cannot redact what a child process
prints itself, and that limitation is documented rather than implied away.

### PG-A1 Phase A1 gate
`status: todo | depends: A1-01, A1-02, A1-03, A1-04`

Both supervisors confirm a key can be added, survives a restart, and cannot be found anywhere in
Canopy's own output.

`signed: walid [ ]   classmate [ ]`

---

# Phase A2: provider client and streaming

Goal: a real message goes to a real provider and a real reply streams back.

### A2-01 Provider interface
`status: todo | owner: none | branch: none | depends: A1-01`
`scope: internal/core/`

Deliverable: the interface an agent session talks to. Streaming, cancellable, provider agnostic,
with tool use in the shape from the start even though tools arrive in A4.

Acceptance: compiles, and a fake provider satisfies it and can script a scripted reply.

`verify: claude [ ]   codex [ ]`

notes: designing the tool use shape now is deliberate, unlike the PTY interfaces we correctly
refused to design early. The difference is that tools are a certainty three phases out, whereas
those were speculative. Retrofitting tool calls into a streaming protocol is genuinely painful.

### A2-02 Anthropic client
`status: todo | owner: none | branch: none | depends: A2-01, A1-02`
`scope: internal/provider/anthropic/`

Deliverable: the Messages API with streaming, using a named key from the store.

Acceptance: a real request returns a streamed reply. Cancellation stops the stream and releases
the connection. Recorded fixtures let the tests run without network or credentials.

`verify: claude [ ]   codex [ ]`

notes: pin the API version explicitly. Use the current model ids rather than whatever was current
when this was written, and check them at implementation time rather than trusting memory.

### A2-03 Error taxonomy
`status: todo | owner: none | branch: none | depends: A2-02`
`scope: internal/core/, internal/provider/`

Deliverable: provider failures mapped to distinct states: authentication, rate limited, overloaded,
context length exceeded, network, cancelled, unknown.

Acceptance: each has a test, and each produces a message telling the user the next useful action.
A rate limit is never reported the same way as a bad key.

`verify: claude [ ]   codex [ ]`

notes: this is the same discipline as the test state vocabulary. "Something went wrong" is the
agent equivalent of a status nobody can act on.

### A2-04 One shot ask
`status: todo | owner: none | branch: none | depends: A2-02`
`scope: cmd/canopy/`

Deliverable: `canopy ask "..."` streams a reply to stdout.

Acceptance: works against a real key, streams rather than buffering, exits non zero on failure,
and cancels cleanly on interrupt.

`verify: claude [ ]   codex [ ]`

notes: the smallest possible proof the whole pipe works. Worth having permanently as a debugging
tool, the same way the headless harness is.

### A2-05 Usage and cost accounting
`status: todo | owner: none | branch: none | depends: A2-02`
`scope: internal/core/, internal/provider/`

Deliverable: tokens in, out and cached, plus cost, recorded per request and attributed to the key
and the agent.

Acceptance: usage from a real response matches what the provider reported. Cost is computed from
a pricing table that is versioned and dated, not hardcoded inline.

`verify: claude [ ]   codex [ ]`

notes: this is exact rather than inferred, because Canopy makes the request. That is what kills
the metering proxy question in FEATURES.md sections 7.1 and 7.5. A stale pricing table is a way to
put a wrong number on screen, so it carries the date it was written and says so when it is old.

### A2-06 OpenAI compatible provider
`status: todo | owner: none | branch: none | depends: A2-02`
`scope: internal/provider/openai/`

Deliverable: a second provider speaking the OpenAI chat completions API, including tool calls and
streaming, with a configurable base URL.

Acceptance: the same agent code runs unchanged against both providers. A base URL override reaches
a non OpenAI endpoint successfully.

`verify: claude [ ]   codex [ ]`

notes: this one task covers most of the field. Kimi, MiniMax, DeepSeek, Groq, OpenRouter and most
local runtimes all speak this API, so a configurable base URL turns one client into support for
nearly everything that is not Anthropic. Building it second, rather than after five more Anthropic
only phases, is also what stops provider assumptions quietly baking into the agent loop.

Tool calling differs between the two APIs in shape but not in meaning. That difference belongs
here, behind the interface, never in `internal/agent`.

### PG-A2 Phase A2 gate
`status: todo | depends: A2-03, A2-04, A2-05`

Both supervisors watch `canopy ask` stream a real reply and see the token and cost figures for it.

`signed: walid [ ]   classmate [ ]`

---

# Phase A3: the chat interface

Goal: it looks and feels like the product. This is the phase that makes Canopy recognisable.

### A3-01 Session and conversation types
`status: todo | owner: none | branch: none | depends: A2-01`
`scope: internal/core/`

Deliverable: `Session`, `Message`, `Role` and `AgentState`, held in the existing snapshot store so
sessions and workspaces share one authoritative view and one event stream.

Acceptance: a session's history rebuilds exactly from a snapshot. Streaming updates coalesce, and
a completed turn is a final event that can never be dropped.

`verify: claude [ ]   codex [ ]`

notes: token streaming is the highest volume event source this project will ever have, which is
precisely the case the coalescing rules in P1-01 were designed for. This is the first real test of
whether that design was right.

### A3-02 Chat view
`status: todo | owner: none | branch: none | depends: A3-01`
`scope: internal/tui/chat/`

Deliverable: message list, live streaming output, an input box, scrollback.

Acceptance: a reply renders token by token without flicker, the view follows the tail unless the
user has scrolled up, and resizing does not corrupt the frame.

`verify: claude [ ]   codex [ ]`

notes: reuses the model, event loop and 80 column discipline from P1-07. Same rule as before, this
package talks to core and nothing else.

### A3-03 Markdown and code rendering
`status: todo | owner: none | branch: none | depends: A3-02`
`scope: internal/tui/chat/`

Deliverable: readable code blocks with syntax highlighting, plus the usual inline markdown.

Acceptance: a long code block wraps or scrolls without breaking the layout, and remains readable
with colour disabled.

`verify: claude [ ]   codex [ ]`

notes: none

### A3-04 Cancel a turn in flight
`status: todo | owner: none | branch: none | depends: A3-02`
`scope: internal/tui/chat/, internal/core/`

Deliverable: interrupt a streaming reply and keep the partial output.

Acceptance: the connection closes, no goroutine leaks, and the partial reply stays visible and
clearly marked as interrupted rather than silently truncated.

`verify: claude [ ]   codex [ ]`

notes: a partial answer presented as complete is the chat equivalent of a stale green.

### PG-A3 Phase A3 gate
`status: todo | depends: A3-03, A3-04`

Both supervisors hold a real conversation in the terminal. **This is the milestone that settles
whether the product feels like what we set out to build.**

`signed: walid [ ]   classmate [ ]`

---

# Phase A4: tools and the permission model

Goal: the agent can change code, and cannot do so without the user knowing what it did.

**Read A4-04 before claiming anything else in this phase.** It is the dangerous part.

### A4-01 Tool interface and registry
`status: todo | owner: none | branch: none | depends: A2-01`
`scope: internal/core/, internal/tools/`

Deliverable: the tool contract, a registry, and JSON schema generation for the provider.

Acceptance: a tool declares its schema once and it is used for both the provider call and local
argument validation, so the two cannot drift apart.

`verify: claude [ ]   codex [ ]`

notes: none

### A4-02 File tools
`status: todo | owner: none | branch: none | depends: A4-01`
`scope: internal/tools/`

Deliverable: read, write, edit, glob and grep.

Acceptance: every path is resolved and confined to the agent's worktree. Symlinks that escape are
refused. Edits against a file that changed since it was read are rejected rather than applied
blind.

`verify: claude [ ]   codex [ ]`

notes: the read-then-edit conflict check is the same freshness idea as the truth engine, applied
to a file instead of a test run. Applying an edit computed against content that has since moved is
how an agent silently destroys work.

### A4-03 Shell tool
`status: todo | owner: none | branch: none | depends: A4-01`
`scope: internal/exec/, internal/tools/`

Deliverable: run a command in the agent's worktree, own process group, timeout, bounded output.

Acceptance: reuses the bounded buffer and process group rules already specified in the old P2-05
to P2-07. Cancellation leaves no orphans, verified by process listing.

`verify: claude [ ]   codex [ ]`

notes: carries forward the old P2-05, P2-06 and P2-07 designs, which were already worked out in
detail and are unchanged by the re-plan.

### A4-04 Permission model
`status: todo | owner: none | branch: none | depends: A4-02, A4-03`
`scope: internal/permission/`

Deliverable: per tool approval, path restrictions, an allow and deny model for shell commands,
approval scopes (once, session, always), and an audit trail of every tool call with its arguments
and result.

Acceptance: no tool runs without an applicable approval. A denied call returns an error to the
model rather than terminating the session. The audit trail is complete enough to answer "what did
this agent actually do" after the fact. An approval for one path never covers another.

`verify: claude [ ]   codex [ ]`

notes: **the existing repository trust contract does not cover this and must not be reused as
though it does.** That contract governs commands the user wrote in a config file. This governs
commands a model generated, which is a different threat model. Canopy does not sandbox what it
runs and must never imply otherwise. Recorded as consequence 1 in D-21.

### A4-05 Tool use loop
`status: todo | owner: none | branch: none | depends: A4-04`
`scope: internal/agent/`

Deliverable: the full turn: model requests a tool, permission is checked, the tool runs, the result
goes back, repeat until the model stops.

Acceptance: a multi step task completes end to end. Cancellation mid loop leaves no partial tool
execution. A tool that fails is reported to the model rather than crashing the turn.

`verify: claude [ ]   codex [ ]`

notes: needs a loop limit and a token budget, or a confused model can spend real money in a circle.

### A4-06 Git tools
`status: todo | owner: none | branch: none | depends: A4-04`
`scope: internal/tools/git/`

Deliverable: git as first class tools the agent calls directly. Status, diff, log, add, commit,
branch, checkout and stash, scoped to the agent's own worktree.

Acceptance: an agent can inspect its changes and commit them without shelling out. Every
destructive operation, meaning checkout over uncommitted work, reset, branch deletion and force
anything, requires approval separately from ordinary shell approval. An agent cannot operate on
another agent's worktree or on the primary checkout.

`verify: claude [ ]   codex [ ]`

notes: **not a convenience wrapper over `bash`.** Three reasons it has to be its own tool. Git
through a shell tool means the permission model sees an opaque string and cannot tell `git status`
from `git push --force`. Structured output is far more reliable for a model to act on than parsed
porcelain. And the worktree confinement in A4-02 is enforceable per argument here, where in a shell
string it is not enforceable at all.

This is what Walid meant by controlling branches and telling agents what to do with git. It is
also, per the corrections document's original safety rules, the place where "never stage all,
never force push, never delete a branch without a reviewed flow" has to actually live.

### PG-A4 Phase A4 gate
`status: todo | depends: A4-05`

Both supervisors watch an agent make a real change to a real file, having approved it, and then
read the audit trail of what it did.

`signed: walid [ ]   classmate [ ]`

---

# Phase A5: many agents, one worktree each

Goal: the differentiator. Several agents working in parallel, each isolated, all visible at once.

This is where the worktree work becomes load bearing rather than speculative, which is why it was
moved here.

### A5-01 Worktree discovery
`status: todo | owner: none | branch: none | depends: PG-A4`
`scope: internal/git/`

Deliverable: discover the primary checkout and existing worktrees via
`git worktree list --porcelain`, with stable IDs and ownership states.

Acceptance: a temp repository with three added worktrees is discovered in full, and the primary is
correctly identified and protected.

`verify: claude [ ]   codex [ ]`

notes: was P2-01, unchanged. The ownership states from P1-01 now all become reachable, since
Canopy creates worktrees in A5-03.

### A5-02 Branch, HEAD, dirty state
`status: todo | owner: none | branch: none | depends: A5-01`
`scope: internal/git/`

Deliverable: per worktree branch or detached HEAD, HEAD SHA, dirty counts, last activity.

Acceptance: correct for clean, dirty, untracked only, and detached HEAD.

`verify: claude [ ]   codex [ ]`

notes: was P2-02, unchanged.

### A5-03 Create and remove a worktree
`status: todo | owner: none | branch: none | depends: A5-02`
`scope: internal/git/`

Deliverable: create a worktree and branch for an agent, and remove it afterwards.

Acceptance: removal refuses on a dirty worktree without explicit confirmation. The primary
checkout can never be removed. A failed creation leaves nothing behind.

`verify: claude [ ]   codex [ ]`

notes: **new, and previously excluded.** The old plan forbade this entirely. Under the agent
runtime it is required, since each agent needs its own tree. The guards from the old exclusion list
survive as behaviour: never touch the primary, never remove a dirty tree silently.

### A5-04 Agent registry
`status: todo | owner: none | branch: none | depends: A5-03, A3-01`
`scope: internal/agent/`

Deliverable: named agents, each bound to a worktree, a key, a model and a session.

Acceptance: several agents run concurrently without touching each other's worktrees or sessions.
Usage and cost are attributed per agent.

`verify: claude [ ]   codex [ ]`

notes: this is where the named key model from A1 pays off. Naming an agent `claude` runs it on the
Anthropic key while another runs elsewhere.

### A5-05 Per agent view and switching
`status: todo | owner: none | branch: none | depends: A5-04, A3-02`
`scope: internal/tui/`

Deliverable: a list of agents, switch into any one's conversation, come back out.

Acceptance: switching is instant and never shows one agent's output in another's view. An agent
that needs input is visibly distinct from one that is working.

`verify: claude [ ]   codex [ ]`

notes: selection stays keyed by ID rather than row index, for the same reason as P1-07.

### A5-06 Steering without interrupting
`status: todo | owner: none | branch: none | depends: A5-05`
`scope: internal/tui/, internal/agent/`

Deliverable: two separate mechanisms, deliberately not one.

**Steer.** Queue guidance for a working agent. It is delivered at the next turn boundary and the
current turn finishes normally. The agent never loses its place.

**Interrupt.** Stop the current turn now, keep the partial output, mark it interrupted.

Acceptance: steering a working agent does not cancel its in flight request, and the guidance is
visibly part of the next turn's context. Interrupting stops the stream within a second and leaves
no orphan processes. Both reach the right agent and only that agent. The interface never offers
one where the user meant the other.

`verify: claude [ ]   codex [ ]`

notes: Walid asked for steering **without interrupting**, and the distinction is the whole feature
rather than a detail. Cancelling a turn to inject a correction throws away the work in progress and
usually the reasoning with it. Queueing to the turn boundary keeps both. Building only interrupt
and calling it steering would produce something that looks right in a demo and is useless in
practice.

### A5-07 Natural language agent dispatch
`status: todo | owner: none | branch: none | depends: A5-04`
`scope: internal/agent/, internal/tools/`

Deliverable: dispatch agents from the chat. "use 2 claude sonnet 5 agents for this" creates two
agents on the named profile, each with its own worktree and branch, and hands them the task.

Acceptance: the count, the profile and the task are all extracted correctly, and each is confirmed
to the user before anything spawns. An ambiguous request asks rather than guesses. A request naming
a profile that does not exist says which profiles do exist. Spawning respects the concurrency and
budget limits from A4-05.

`verify: claude [ ]   codex [ ]`

notes: **probably the most differentiating single feature in this plan**, and it is the reason the
named key model from A1 is load bearing rather than cosmetic. Saying "two sonnet agents and a kimi
agent" only means anything if names resolve to credentials and profiles.

Implemented as tools the orchestrating agent calls, `spawn_agents` and `list_profiles`, not as
regex over the user's message. Pattern matching on phrasing would break the first time somebody
wrote it differently, and the model is already good at exactly this.

Confirmation before spawning is not optional. Spawning agents costs real money against real keys,
and a misparsed "20" instead of "2" should be a question, not an invoice.

### A5-08 Split screen agent views
`status: todo | owner: none | branch: none | depends: A5-05`
`scope: internal/tui/`

Deliverable: watch several agents at once in split panes, two up and four up, with focus moving by
keyboard and by mouse.

Acceptance: four agents stream simultaneously without tearing or flicker. Focus follows a click.
Layout degrades sensibly on a narrow terminal rather than becoming unreadable. Keyboard remains
sufficient for everything, so nothing requires a mouse.

`verify: claude [ ]   codex [ ]`

notes: this is what makes running many agents feel like supervising rather than tab switching.

Rendering several live streams at once is the point where the coalescing rules from P1-01 stop
being theoretical: four agents streaming tokens into one terminal is precisely the load that model
was designed for.

Mouse support is additive only. Keyboard first is not negotiable, since the tool has to stay usable
over ssh in a session where mouse reporting may not survive.

### PG-A5 Phase A5 gate
`status: todo | depends: A5-06, A5-07, A5-08`

Both supervisors type "use 3 agents for this" into the chat, watch three agents spawn onto their
own branches, see all three at once in split panes, and steer one of them without interrupting it.

`signed: walid [ ]   classmate [ ]`

---

# Phase A6: verification per agent

Goal: the thing no incumbent does. Canopy knows whether each agent's work actually passes.

Everything here already exists as a design and partly as code. P1-01 to P1-06 built the contract,
the state machine, the roll-up and the fake.

### A6-01 RevisionKey
`status: todo | owner: none | branch: none | depends: A5-02`
`scope: internal/git/`

Deliverable: HeadSHA plus DirtyDigest, per the truth contract.

Acceptance: the key changes on a tracked edit, on staged content and on a new untracked file, and
does not change when a git ignored file changes. Symlinks hash their target, submodules contribute
their HEAD SHA, and an untracked file above the size limit forces the revision to unknown with a
readable reason.

`verify: claude [ ]   codex [ ]`

notes: was P2-03, unchanged. D-09 and D-16 still apply.

### A6-02 Revision poller
`status: todo | owner: none | branch: none | depends: A6-01`
`scope: internal/git/`

Deliverable: poll each worktree and emit a revision change event.

Acceptance: an edit produces the event within one poll interval, and polling many worktrees does
not saturate a core.

`verify: claude [ ]   codex [ ]`

notes: was P2-04, unchanged. D-07 still applies.

### A6-03 Test runner
`status: todo | owner: none | branch: none | depends: A6-01`
`scope: internal/exec/`

Deliverable: run a configured test command per agent worktree, capturing exit code, duration and
the revision at start.

Acceptance: exit zero is passing for the captured revision, non zero is failing, and a command
that cannot start is error rather than either.

`verify: claude [ ]   codex [ ]`

notes: was P2-05, unchanged. Shares the process group and bounded output machinery with A4-03,
which is a simplification the re-plan buys us: the shell tool and the test runner are the same
problem.

### A6-04 Verification per agent in the UI
`status: todo | owner: none | branch: none | depends: A6-03, A5-05`
`scope: internal/tui/`

Deliverable: every agent row carries its verification state, using the existing roll-up.

Acceptance: an agent that edits its worktree turns stale, and re-running clears it. The wording
and glyphs are the ones fixed in D-10.

`verify: claude [ ]   codex [ ]`

notes: this is the old P2-09 to P2-14 demo, now per agent instead of per worktree. The dashboard
from P1-07 already renders it, against the fake.

### A6-05 Rank agents by outcome
`status: todo | owner: none | branch: none | depends: A6-04`
`scope: internal/agent/`

Deliverable: give several agents the same task, then rank the results by tests passing for the
current revision, and by diff size as a tiebreak.

Acceptance: the ranking refuses to rank anything whose evidence is stale or unknown, rather than
guessing.

`verify: claude [ ]   codex [ ]`

notes: **the crown jewel from FEATURES.md pillar 2.** Orca fans out across agents. Nobody uses
test truth to rank the results. This task is the entire strategic argument for the project, and it
is only possible because A6 exists.

### PG-A6 Phase A6 gate
`status: todo | depends: A6-05`

Both supervisors give three agents the same task and watch Canopy pick the winner on evidence.

`signed: walid [ ]   classmate [ ]`

---

# Phase A7: robustness, docs, packaging

Goal: someone who is not us can install it and get value without being told how.

### A7-01 Robustness sweep
`status: todo | owner: none | branch: none | depends: PG-A6`

Acceptance: the surviving criteria from the old P4 set. Timeouts terminate the right process
group, no final state transition is ever dropped under load, huge output cannot freeze the UI,
paths and branch names with spaces work, externally removed worktrees disappear safely, and
quitting leaves no child processes behind.

`verify: claude [ ]   codex [ ]`

notes: carries forward P4-01 to P4-07.

### A7-02 Interface robustness
`status: todo | owner: none | branch: none | depends: PG-A6`

Acceptance: readable at 80 columns with several agents, resize does not corrupt the frame, every
state is distinguishable with colour disabled, rapid updates never move the selection, the UI
rebuilds from a fresh snapshot, and every error says what to do next.

`verify: claude [ ]   codex [ ]`

notes: carries forward P4-08 to P4-12. P1-07 already met the 80 column and selection criteria.

### A7-03 Honest limitations document
`status: todo | owner: none | branch: none | depends: A7-01, A7-02`
`scope: LIMITATIONS.md`

Deliverable: what Canopy does not guarantee. No sandboxing. No database or dependency isolation
between worktrees. Coarse whole worktree staleness. Secrets a child process prints are not
redactable. macOS and Linux only. Cost figures depend on a pricing table with a date on it.

Acceptance: a reader can tell within a minute whether Canopy will lie to them, and how.

`verify: claude [ ]   codex [ ]`

notes: carries forward P4-16. Underclaiming is on brand, not a weakness.

### A7-04 Packaging and install
`status: todo | owner: none | branch: none | depends: A7-03`

Acceptance: a stranger can install a build, add a key, and run an agent without being talked
through it.

`verify: claude [ ]   codex [ ]`

notes: carries forward P4-17.

### PG-A7 Phase A7 gate
`status: todo | depends: A7-04`

Both supervisors watch someone outside the team install it and use it.

`signed: walid [ ]   classmate [ ]`

---

# Retired tasks

Replaced by the 2026-07-26 re-plan. Kept as a record so the reasoning is not lost.

| Old | Fate |
|---|---|
| P2-01, P2-02 | now A5-01, A5-02, unchanged |
| P2-03, P2-04 | now A6-01, A6-02, unchanged |
| P2-05, P2-06, P2-07 | now A6-03 and A4-03, which turn out to be the same machinery |
| P2-08 | folded into A3-01, the store now carries sessions as well as workspaces |
| P2-09 to P2-14 | now A6-04, per agent instead of per worktree |
| P3-01 to P3-05 | config and repository trust. Deferred. A4-04 supersedes the trust model for agent execution, and per project config returns when there is something to configure |
| P3-06 to P3-12 | service health. Deferred to after A7. It was Pillar 1 scope and is not on the path to the agent product |
| P4-01 to P4-12 | now A7-01 and A7-02 |
| P4-13 | auto run on change, D-19. Still open, now decided in the A6 context |
| P4-14, P4-15 | acceptance suite, folded into each phase gate rather than saved for the end |
| P4-16, P4-17 | now A7-03, A7-04 |
| P5-01 to P5-04 | pilot. Still worth doing, after A7 |
| P6-01 | the conditional expansion menu is gone. The expansions became the plan |

---

## Appendix: change log for this file

Append one line per structural change (adding, removing or reordering tasks). Do not log ordinary
status or verification updates.

| Date | Agent | Change |
|---|---|---|
| 2026-07-26 | Claude | Created ledger. Phases 0 to 6 derived from Canopy-Pre-Build-Corrections.md sections 11 and 12. |
| 2026-07-26 | Claude | Re-planned. Canopy is an agent runtime, so phases 2 to 6 became A1 to A7. Phases 0 and 1 untouched. Surviving tasks keep their old IDs in their notes, and a retired tasks table records where each one went. |
