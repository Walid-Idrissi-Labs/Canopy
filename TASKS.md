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
| Claude | A1-05, key management in the TUI | `feat/keys-tui` | none |
| Codex | none | none | none |

**Re-steered on 2026-07-26.** Canopy is a coding agent harness focused on agentic parallelism and
git, not a worktree monitor. Phases 0 and 1 are unchanged and still done. Old phases 2 to 6 are
replaced by A1 to A9, and every surviving task carries its original ID in its notes. See D-21 to
D-23 and the retired tasks table at the bottom.

Done and carrying forward: P0-01 to P0-07 and P1-01 to P1-07. The core contract, the state machine,
the roll-up, the fake store, the headless harness and the dashboard.

Codex: **A2 is the obvious thing to claim.** It is independent of A1, because a provider client can
take a key as a parameter long before there is a registry to fetch one from. A3 depends on A2.

Integration cadence: no fixed calendar, see D-12. Short lived branches, merge main in before you
push.

---

## 3. Scope reminder

Read this before claiming anything.

**Canopy is a terminal coding agent built for running several agents at once.** Provider keys go in
by name, agents are dispatched from the conversation, each gets its own worktree and branch, you
watch and steer several at a time, and Canopy knows which of them actually produced working code.

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
3. **No trust level permits touching another agent's worktree or the primary checkout.** Those are
   refused rather than gated. Some actions should not have an approval path at all, and putting
   that in the type rather than in the permission layer means A4-04 cannot accidentally grant it.

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
`status: claimed | owner: Claude | branch: feat/keys-tui | depends: A1-03`
`scope: internal/tui/keys/, internal/tui/, cmd/canopy/`

Deliverable: add, list, test and remove credentials without leaving the interface. Reachable from
the dashboard, and shown on first run when no credentials exist yet.

Acceptance: a key can be added entirely in the TUI, with the value masked as it is typed and never
present in any rendered frame. The provider is chosen from a list rather than typed. A base URL is
requested only for providers that need one. Removal confirms first. On first run with no keys, the
interface says so and offers to add one rather than presenting an empty dashboard.

`verify: claude [ ]   codex [ ]`

notes: **added 2026-07-26 at Walid's request**, after A1 was otherwise finished. Recorded as a new
task rather than folded into A1-03 so the change is visible.

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
`status: todo | owner: none | branch: none | depends: A1-01`
`scope: internal/core/`

Deliverable: the interface an agent session talks to. Streaming, cancellable, provider agnostic,
with tool use in the shape from the start.

Acceptance: compiles, and a fake provider satisfies it and can script a reply.

`verify: claude [ ]   codex [ ]`

notes: designing the tool use shape before tools exist is deliberate, unlike the PTY interfaces we
correctly refused to design early. Those were speculative. Tools are a certainty two phases out,
and retrofitting tool calls into a streaming protocol is genuinely painful.

### A2-02 Anthropic client
`status: todo | owner: none | branch: none | depends: A2-01, A1-02`
`scope: internal/provider/anthropic/`

Deliverable: the Messages API with streaming, using a named key.

Acceptance: a real request returns a streamed reply. Cancellation stops the stream and releases the
connection. Recorded fixtures let tests run without network or credentials.

`verify: claude [ ]   codex [ ]`

notes: pin the API version. Check current model ids at implementation time rather than trusting
what was current when this was written.

### A2-03 Error taxonomy
`status: todo | owner: none | branch: none | depends: A2-02`
`scope: internal/core/, internal/provider/`

Deliverable: provider failures mapped to distinct states: authentication, rate limited, overloaded,
context length exceeded, network, cancelled, unknown.

Acceptance: each has a test and a message naming the next useful action. A rate limit is never
reported like a bad key.

**Provider error text is scrubbed of the credential before it leaves this package.** A planted key
does not appear in any error surfaced from a provider failure, and there is a test for it.

`verify: claude [ ]   codex [ ]`

notes: same discipline as the test state vocabulary. "Something went wrong" is the agent equivalent
of a status nobody can act on.

The scrubbing requirement came out of A1-04, which found that free text fields render verbatim.
The realistic leak is a provider replying "invalid x-api-key: sk-ant-..." and Canopy putting it on
screen and into any screenshot of it.

It belongs here rather than in the renderer. Scrubbing at render time would mean loading every
stored credential so the rendering path could search for it, which is secrets travelling further
in order to be protected. At this boundary the credential is already in scope, so the scrub is
local and complete. `TestFreeTextFieldsAreNotScrubbed` in `cmd/canopy` will fail when this lands,
and should then be narrowed to the fields this package does not own.

### A2-04 One shot ask
`status: todo | owner: none | branch: none | depends: A2-02`
`scope: cmd/canopy/`

Deliverable: `canopy ask "..."` streams a reply to stdout.

Acceptance: works against a real key, streams rather than buffering, exits non zero on failure,
cancels cleanly on interrupt.

`verify: claude [ ]   codex [ ]`

notes: the smallest proof the whole pipe works, and worth keeping permanently as a debugging tool.

### A2-05 Usage and cost accounting
`status: todo | owner: none | branch: none | depends: A2-02`
`scope: internal/core/, internal/provider/`

Deliverable: tokens in, out and cached, plus cost, per request, attributed to key and agent.

Acceptance: usage matches what the provider reported. Cost comes from a versioned, dated pricing
table, and the interface says so when the table is old.

`verify: claude [ ]   codex [ ]`

notes: exact rather than inferred, because Canopy makes the request. A stale pricing table is a way
to put a wrong number on screen, which is why it carries its date.

### A2-06 OpenAI compatible provider and local models
`status: todo | owner: none | branch: none | depends: A2-02`
`scope: internal/provider/openai/`

Deliverable: the chat completions API with tool calls and streaming, and a configurable base URL.

Acceptance: the same agent code runs unchanged on both providers. A base URL override reaches a non
OpenAI endpoint. Ollama and one hosted third party both work.

`verify: claude [ ]   codex [ ]`

notes: one task covers most of the field. Kimi, MiniMax, DeepSeek, Groq, OpenRouter and most local
runtimes speak this API. Building it second, rather than after five Anthropic only phases, is what
stops provider assumptions baking into the agent loop. Tool calling differs in shape between the
two APIs but not in meaning, and that difference belongs behind the interface, never in
`internal/agent`.

### A2-07 Prompt caching
`status: todo | owner: none | branch: none | depends: A2-05`
`scope: internal/provider/`

Deliverable: cache long stable prefixes such as system prompts and file context where the provider
supports it.

Acceptance: cached tokens are reported separately in usage, and the saving is visible.

`verify: claude [ ]   codex [ ]`

notes: large cost saving for small effort, and it compounds with several agents sharing a project
system prompt.

### A2-08 Provider fallback chains
`status: todo | owner: none | branch: none | depends: A2-03`
`scope: internal/agent/, internal/core/`

Deliverable: a profile may list ordered fallbacks. On overload or rate limit, try the next key or
provider.

Acceptance: an overloaded primary falls through without losing the turn. Authentication failures do
**not** fall through, because a wrong key should be fixed rather than routed around. Every fallback
is visible in the transcript, never silent.

`verify: claude [ ]   codex [ ]`

notes: **added 2026-07-26.** Cheap once the error taxonomy exists, and it matters the moment eight
agents run at once, which is exactly when providers start shedding load. Silent fallback would be
dishonest: you would be billed on a different key, and possibly answered by a weaker model, without
being told.

### PG-A2 Phase A2 gate
`status: todo | depends: A2-03, A2-04, A2-05, A2-06`

Both supervisors watch `canopy ask` stream a real reply on two different providers and see the
token and cost figures for each.

`signed: walid [ ]   classmate [ ]`

---

# Phase A3: chat and persistence

Goal: it looks and feels like the product, and nothing is lost when you quit.

### A3-01 Session and conversation types
`status: todo | owner: none | branch: none | depends: A2-01`
`scope: internal/core/`

Deliverable: `Session`, `Message`, `Role`, `Turn` and `AgentState`, held in the existing snapshot
store so sessions and workspaces share one authoritative view and one event stream.

Acceptance: a session rebuilds exactly from a snapshot. Streaming updates coalesce, and a completed
turn is a final event that can never be dropped.

`verify: claude [ ]   codex [ ]`

notes: token streaming is the highest volume event source this project will ever have, which is
precisely the case the coalescing rules in P1-01 were designed for. This is the first real test of
whether that design was right.

### A3-02 Session storage
`status: todo | owner: none | branch: none | depends: A3-01`
`scope: internal/session/`

Deliverable: SQLite persistence. Every session, turn, tool call and usage record written as it
happens. Resume by id. Full text search across history.

Acceptance: killing the process mid turn loses at most the in flight turn. Resuming restores the
conversation exactly. Search finds a message across sessions.

`verify: claude [ ]   codex [ ]`

notes: sessions, audit trail, cost history and run reports are all queries over the same data, so
one storage decision buys four features. Schema migrations from day one, since the schema will
change and a tool that loses your history on upgrade is not one anyone keeps.

### A3-03 Chat view
`status: todo | owner: none | branch: none | depends: A3-01`
`scope: internal/tui/chat/`

Deliverable: message list, live streaming, input box, scrollback.

Acceptance: a reply renders token by token without flicker, follows the tail unless the user has
scrolled up, and survives a resize.

`verify: claude [ ]   codex [ ]`

notes: reuses the model, event loop and 80 column discipline from P1-07. This package still talks
to core and nothing else.

### A3-04 Markdown and code rendering
`status: todo | owner: none | branch: none | depends: A3-03`
`scope: internal/tui/chat/`

Deliverable: readable code blocks with syntax highlighting, plus inline markdown.

Acceptance: a long code block wraps or scrolls without breaking the layout, and stays readable with
colour disabled.

`verify: claude [ ]   codex [ ]`

notes: none

### A3-05 Cancel a turn in flight
`status: todo | owner: none | branch: none | depends: A3-03`
`scope: internal/tui/chat/, internal/core/`

Deliverable: interrupt a streaming reply and keep the partial output.

Acceptance: the connection closes, nothing leaks, and the partial reply is visibly marked as
interrupted rather than silently truncated.

`verify: claude [ ]   codex [ ]`

notes: a partial answer presented as complete is the chat equivalent of a stale green.

### A3-06 Context meter and compaction
`status: todo | owner: none | branch: none | depends: A3-02`
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

### A3-07 Session forking
`status: todo | owner: none | branch: none | depends: A3-02`
`scope: internal/session/, internal/tui/chat/`

Deliverable: fork a session at any turn into a new one that shares history up to that point and
diverges after.

Acceptance: forking does not modify the original. Both sessions are independently resumable. The
fork point is recorded and visible in both.

`verify: claude [ ]   codex [ ]`

notes: **added 2026-07-26.** The natural companion to branch per agent, and it maps onto how people
already think in git. "Go back three turns and try it the other way" is currently either a fresh
session with lost context, or an argument with an agent that has already committed to an approach.
At A5 a fork becomes a second agent on a second branch, which is where it earns its place.

### PG-A3 Phase A3 gate
`status: todo | depends: A3-04, A3-05, A3-06`

Both supervisors hold a real conversation, quit, resume it, and search their history. **This is the
milestone that settles whether the product feels like what we set out to build.**

`signed: walid [ ]   classmate [ ]`

---

# Phase A4: tools and permissions

Goal: the agent can change code, and cannot do so without you knowing what it did.

**Read A4-04 before claiming anything else here.** It is the dangerous part.

### A4-01 Tool interface and registry
`status: todo | owner: none | branch: none | depends: A2-01`
`scope: internal/core/, internal/tools/`

Deliverable: the tool contract, a registry, and schema generation for both provider APIs.

Acceptance: a tool declares its schema once, used for the provider call and for local argument
validation, so the two cannot drift.

`verify: claude [ ]   codex [ ]`

notes: none

### A4-02 File tools
`status: todo | owner: none | branch: none | depends: A4-01`
`scope: internal/tools/`

Deliverable: read, write, edit, glob, grep.

Acceptance: every path resolves inside the agent's worktree. Symlinks that escape are refused. An
edit against a file that changed since it was read is rejected rather than applied blind.

`verify: claude [ ]   codex [ ]`

notes: the read-then-edit check is the freshness idea from the truth engine applied to a file
instead of a test run. Applying an edit computed against content that has moved is how an agent
silently destroys work, including another agent's.

### A4-03 Shell tool
`status: todo | owner: none | branch: none | depends: A4-01`
`scope: internal/exec/, internal/tools/`

Deliverable: run a command in the agent's worktree, own process group, timeout, bounded output.

Acceptance: cancellation leaves no orphans, verified by process listing. Output above the limit
keeps head and tail and says how much was dropped.

`verify: claude [ ]   codex [ ]`

notes: carries forward the old P2-05 to P2-07 designs unchanged. Shares machinery with the test
runner at A6-03, which the old plan had as two separate efforts.

### A4-04 Per agent trust levels and permissions
`status: todo | owner: none | branch: none | depends: A4-02, A4-03`
`scope: internal/permission/`

Deliverable: trust level as a property of the profile. Each level defines which tools run without
asking, which always ask, and which are refused outright. Path confinement, an allow and deny model
for shell, approval scopes, and a complete audit trail of every call with arguments and result.

Acceptance: no tool runs without an applicable permission. A denied call returns an error to the
model rather than killing the session. An approval for one path never covers another. A scratch
profile and a profile working near `main` behave differently on the same request. The audit trail
answers "what did this agent actually do" after the fact.

`verify: claude [ ]   codex [ ]`

notes: **the existing repository trust contract does not cover this and must not be reused as
though it does.** That governs commands the user wrote in a config file. This governs commands a
model generated. Different threat model.

Canopy does not sandbox and must never imply that it does. Per agent levels were chosen over one
global posture because the alternative forces the strictest agent's friction onto every agent, and
people respond to that by loosening everything.

### A4-05 Tool use loop
`status: todo | owner: none | branch: none | depends: A4-04`
`scope: internal/agent/`

Deliverable: the full turn. Model requests a tool, permission is checked, the tool runs, the result
returns, repeat until the model stops.

Acceptance: a multi step task completes end to end. Cancellation mid loop leaves no partial tool
execution. A failing tool is reported to the model rather than crashing the turn. Loop count and
token budget are both bounded.

`verify: claude [ ]   codex [ ]`

notes: without a loop limit and a budget, a confused model spends real money in a circle.

### A4-06 Git tools
`status: todo | owner: none | branch: none | depends: A4-04`
`scope: internal/tools/git/`

Deliverable: status, diff, log, add, commit, branch, checkout and stash as structured tools scoped
to the agent's worktree.

Acceptance: an agent inspects and commits its changes without shelling out. Destructive operations,
meaning checkout over uncommitted work, reset, branch deletion and force anything, are approved
separately from ordinary shell approval. An agent cannot operate on another agent's worktree or on
the primary checkout.

`verify: claude [ ]   codex [ ]`

notes: **not a convenience wrapper over `bash`.** A shell tool hands the permission model an opaque
string, which cannot be told apart from `git push --force`. Structured output is also far more
reliable for a model to act on than parsed porcelain, and confinement is enforceable per argument
where in a shell string it is not enforceable at all.

### A4-07 Web search and fetch
`status: todo | owner: none | branch: none | depends: A4-01`
`scope: internal/tools/web/`

Deliverable: search the web and fetch a URL as text.

Acceptance: fetched content is bounded and stripped to readable text. Failures are reported to the
model rather than crashing the turn. Requests are visible in the audit trail.

`verify: claude [ ]   codex [ ]`

notes: a model working from training data alone gets library versions wrong, confidently.

### A4-08 Checkpoint and undo
`status: todo | owner: none | branch: none | depends: A4-06`
`scope: internal/git/, internal/agent/`

Deliverable: snapshot an agent's worktree before it acts, and revert everything it did with one
key.

Acceptance: undo restores tracked and untracked files exactly, including deletions. Checkpoints are
cheap enough to take every turn. Reverting one agent never touches another's worktree.

`verify: claude [ ]   codex [ ]`

notes: with several agents editing in parallel this is the difference between experimenting freely
and being afraid to let them run. Probably implemented as a stash or a hidden ref rather than a
file copy.

### A4-09 Plan first mode
`status: todo | owner: none | branch: none | depends: A4-05`
`scope: internal/agent/, internal/tui/`

Deliverable: a profile setting where the agent produces a plan, waits for approval, then executes
without asking per tool.

Acceptance: no tool runs before the plan is approved. Approving grants only what the plan
described. An agent that departs from the plan stops and asks again.

`verify: claude [ ]   codex [ ]`

notes: **added 2026-07-26.** Approval at the task level rather than the keystroke level, and better
than either extreme. Per tool prompting on a fifty step task trains you to approve without reading,
which is worse than not asking. Reviewing one plan is something a person actually does properly.

### A4-10 Todo and plan tracking
`status: todo | owner: none | branch: none | depends: A4-05`
`scope: internal/agent/, internal/tui/`

Deliverable: a visible task list per agent that the agent maintains as it works.

Acceptance: the list is visible in the agent's pane and updates live. It survives resume.

`verify: claude [ ]   codex [ ]`

notes: cheap, and it is most of what makes a long agent run followable. It is also what makes
watching four agents at once comprehensible rather than four scrolling walls of text.

### PG-A4 Phase A4 gate
`status: todo | depends: A4-05, A4-06, A4-08, A4-09`

Both supervisors watch an agent plan a change, get approval, make it, and then undo it. Then they
read the audit trail.

`signed: walid [ ]   classmate [ ]`

---

# Phase A5: many agents

Goal: the differentiator. Several agents working in parallel, each isolated, all visible, all
steerable.

### A5-01 Worktree discovery
`status: todo | owner: none | branch: none | depends: PG-A4`
`scope: internal/git/`

Deliverable: discover the primary checkout and existing worktrees via
`git worktree list --porcelain`, with stable IDs and ownership states.

Acceptance: a temp repository with three worktrees is discovered in full, and the primary is
identified and protected.

`verify: claude [ ]   codex [ ]`

notes: was P2-01, unchanged.

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

Deliverable: create a worktree and branch for an agent, remove it afterwards.

Acceptance: removal refuses on a dirty worktree without explicit confirmation. The primary checkout
can never be removed. A failed creation leaves nothing behind.

`verify: claude [ ]   codex [ ]`

notes: previously forbidden outright, now required. The guards from that exclusion survive as
behaviour: never touch the primary, never remove a dirty tree silently.

### A5-04 Worktree environment setup
`status: todo | owner: none | branch: none | depends: A5-03`
`scope: internal/git/, internal/config/`

Deliverable: bring a fresh worktree to a runnable state. An optional setup command with a timeout
and captured output, and an explicit allow list of git ignored files that may be copied from the
primary checkout.

Acceptance: an agent spawned into a new worktree can run the project's tests. Copying happens only
for allow listed paths and only after confirmation. A file that is not git ignored is never copied
without separate confirmation. Setup failure is a visible state, not a silent one, and secret
contents are never printed.

`verify: claude [ ]   codex [ ]`

notes: **this is a hole in the re-plan, caught 2026-07-26, not a nice to have.** A fresh worktree
has no `.env`, no `node_modules`, no virtualenv and no build cache. Without this an agent spawns
into a tree where nothing runs, and A6 then reports failures that have nothing to do with the
agent's code, which is a false red and just as damaging as a false green.

Carries forward the environment contract from corrections section 6, which the re-plan dropped at
exactly the point it became more necessary rather than less. Its limits stand: a port does not
isolate a database, Redis, a queue or an OAuth callback, and Canopy supplies templated values
without promising isolation it cannot deliver.

### A5-05 Agent registry
`status: todo | owner: none | branch: none | depends: A5-04, A3-01`
`scope: internal/agent/`

Deliverable: named agents, each bound to a worktree, a profile, a key and a session.

Acceptance: several agents run concurrently without touching each other's worktrees or sessions.
Usage and cost are attributed per agent.

`verify: claude [ ]   codex [ ]`

notes: where the named key model from A1 pays off.

### A5-06 Per agent view and switching
`status: todo | owner: none | branch: none | depends: A5-05, A3-03`
`scope: internal/tui/`

Deliverable: a list of agents, switch into any one's conversation, come back out.

Acceptance: switching is instant and never shows one agent's output in another's view. An agent
needing input is visibly distinct from one working.

`verify: claude [ ]   codex [ ]`

notes: selection stays keyed by ID rather than row index, for the reason established in P1-07.

### A5-07 Steering without interrupting
`status: todo | owner: none | branch: none | depends: A5-06`
`scope: internal/tui/, internal/agent/`

Deliverable: two mechanisms, deliberately not one. **Steer** queues guidance delivered at the next
turn boundary, and the current turn finishes normally. **Interrupt** stops the turn now, keeps
partial output, marks it interrupted.

Acceptance: steering does not cancel the in flight request, and the guidance is visibly part of the
next turn's context. Interrupting stops within a second with no orphans. Both reach the right agent
and only that agent. The interface never offers one where the user meant the other.

`verify: claude [ ]   codex [ ]`

notes: the distinction is the whole feature. Cancelling a turn to inject a correction throws away
the work in progress and usually the reasoning with it. Building only interrupt and calling it
steering would demo fine and be useless in practice.

### A5-08 Natural language dispatch
`status: todo | owner: none | branch: none | depends: A5-05`
`scope: internal/agent/, internal/tools/`

Deliverable: dispatch from the chat. "use 2 claude sonnet agents for this" creates two agents on
that profile, each with its own worktree and branch, and hands them the task.

Acceptance: count, profile and task are extracted correctly and confirmed before anything spawns.
An ambiguous request asks rather than guesses. An unknown profile name says which profiles exist.
Spawning respects concurrency and budget limits.

`verify: claude [ ]   codex [ ]`

notes: **probably the most differentiating single feature here**, and the reason named keys are
load bearing rather than cosmetic. Implemented as tools the orchestrating agent calls,
`spawn_agents` and `list_profiles`, never regex over the user's message, which would break the
first time somebody phrased it differently.

### A5-09 Cost preview and budget guardrails
`status: todo | owner: none | branch: none | depends: A5-08, A2-05`
`scope: internal/agent/, internal/tui/`

Deliverable: before spawning, show an estimated cost range based on this project's own history for
similar tasks. Per agent and per session caps that pause an agent at the limit.

Acceptance: the estimate names what it is based on and how confident it is, and says so plainly
when there is not enough history to estimate. A cap pauses before the next request rather than
reporting the overspend afterwards. A paused agent can be resumed with a raised cap.

`verify: claude [ ]   codex [ ]`

notes: **added 2026-07-26.** Nearly free once A2-05 records exact history, and it is the real answer
to a misparsed 20 instead of 2. Enforcement before the request rather than after is the difference
between a guardrail and a receipt. An estimate presented more confidently than the data supports
would be its own small lie, so the range carries its basis.

### A5-10 Split screen agent views
`status: todo | owner: none | branch: none | depends: A5-06`
`scope: internal/tui/`

Deliverable: several agents at once in split panes, two up and four up, focus by keyboard and by
mouse.

Acceptance: four agents stream simultaneously without tearing. Focus follows a click. Layout
degrades sensibly on a narrow terminal. Keyboard remains sufficient for everything.

`verify: claude [ ]   codex [ ]`

notes: this is what makes running many agents feel like supervising rather than tab switching. Four
live streams into one terminal is also where the coalescing rules from P1-01 stop being
theoretical. Mouse support is additive only, since the tool has to stay usable over ssh where mouse
reporting may not survive.

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
`status: todo | owner: none | branch: none | depends: A5-02`
`scope: internal/git/`

Deliverable: HeadSHA plus DirtyDigest, per the truth contract.

Acceptance: the key changes on a tracked edit, on staged content and on a new untracked file, and
does not change when a git ignored file changes. Symlinks hash their target, submodules contribute
their HEAD SHA, and an oversized untracked file forces the revision to unknown with a readable
reason.

`verify: claude [ ]   codex [ ]`

notes: was P2-03, unchanged. D-09 and D-16 apply.

### A6-02 Revision poller
`status: todo | owner: none | branch: none | depends: A6-01`
`scope: internal/git/`

Deliverable: poll each worktree and emit a revision change event.

Acceptance: an edit produces the event within one poll interval, and polling many worktrees does
not saturate a core.

`verify: claude [ ]   codex [ ]`

notes: was P2-04, unchanged. D-07 applies.

### A6-03 Test runner
`status: todo | owner: none | branch: none | depends: A6-01, A5-04`
`scope: internal/exec/`

Deliverable: run a configured test command per agent worktree, capturing exit code, duration and
the revision at start.

Acceptance: exit zero is passing for the captured revision, non zero is failing, and a command that
cannot start is error rather than either.

`verify: claude [ ]   codex [ ]`

notes: was P2-05, unchanged. Depends on A5-04, because running tests in a worktree with no
dependencies installed measures the environment rather than the code.

### A6-04 Verification per agent
`status: todo | owner: none | branch: none | depends: A6-03, A5-06`
`scope: internal/tui/`

Deliverable: every agent carries its verification state, using the existing roll-up.

Acceptance: an agent that edits its worktree turns stale, and re-running clears it. Wording and
glyphs are the ones fixed in D-10.

`verify: claude [ ]   codex [ ]`

notes: the old P2-09 to P2-14 demo, per agent instead of per worktree. The P1-07 dashboard already
renders this against the fake.

### A6-05 Rank agents by outcome
`status: todo | owner: none | branch: none | depends: A6-04`
`scope: internal/agent/`

Deliverable: give several agents the same task, then rank the results by tests passing for the
current revision, with diff size as a tiebreak.

Acceptance: the ranking refuses to rank anything whose evidence is stale or unknown rather than
guessing. The reason for each placement is visible.

`verify: claude [ ]   codex [ ]`

notes: **the strategic argument for the entire project.** Orca fans out across agents. Nobody
appears to use test truth to rank the results. This is a gate, not a stretch goal.

### A6-06 Ready to review queue
`status: todo | owner: none | branch: none | depends: A6-04`
`scope: internal/tui/`

Deliverable: surface agents that are green for their current code and have a meaningful diff,
ordered so the easiest review comes first.

Acceptance: an agent whose result went stale leaves the queue immediately. An agent with a green
result and an empty diff never enters it.

`verify: claude [ ]   codex [ ]`

notes: **added 2026-07-26.** Nearly free, since the truth engine already knows all of this, and it
turns the dashboard from a status display into a work queue. With six agents running, "which of
these should I look at next" is the actual question, and nothing else on this list answers it.

### PG-A6 Phase A6 gate
`status: todo | depends: A6-05, A6-06`

Both supervisors give three agents the same task and watch Canopy pick the winner on evidence.

`signed: walid [ ]   classmate [ ]`

---

# Phase A7: git workflow

Goal: turn finished agent work into clean history without leaving the tool.

### A7-01 Diff review in the TUI
`status: todo | owner: none | branch: none | depends: PG-A6`
`scope: internal/tui/diff/`

Deliverable: read an agent's changes per file, syntax highlighted, scrollable.

Acceptance: a large diff stays responsive. Readable at 80 columns and without colour.

`verify: claude [ ]   codex [ ]`

notes: the alternative is a second terminal per agent, which defeats the point of watching them
here.

### A7-02 Commit helper
`status: todo | owner: none | branch: none | depends: A7-01`
`scope: internal/tui/, internal/git/`

Deliverable: stage, draft a conventional commit message from the diff, commit and push, keyboard
only.

Acceptance: the drafted message is editable before committing. Nothing is staged or pushed without
explicit confirmation.

`verify: claude [ ]   codex [ ]`

notes: none

### A7-03 Cross agent conflict radar
`status: todo | owner: none | branch: none | depends: A7-01`
`scope: internal/git/, internal/tui/`

Deliverable: show which files several agents have all touched, before merging.

Acceptance: overlap is visible per file, and each entry names the agents involved.

`verify: claude [ ]   codex [ ]`

notes: preempts a pain that exists only because you are running agents in parallel, which makes it
a differentiator rather than table stakes.

### PG-A7 Phase A7 gate
`status: todo | depends: A7-02, A7-03`

Both supervisors review an agent's diff, commit it, and see the overlap between two agents before
merging either.

`signed: walid [ ]   classmate [ ]`

---

# Phase A8: advanced orchestration and extensibility

Goal: the ceiling. Everything that makes Canopy worth extending rather than just using.

### A8-01 Sub agents
`status: todo | owner: none | branch: none | depends: PG-A7`
`scope: internal/agent/`

Deliverable: an agent may spawn helper agents for subtasks.

Acceptance: sub agent cost is attributed to the parent's budget. The audit trail shows the tree,
not a flat list. Depth and fan-out are bounded.

`verify: claude [ ]   codex [ ]`

notes: powerful for decomposition, and it multiplies cost while making the audit trail and budget
accounting considerably harder to keep honest. Which is why it comes after human driven dispatch
works.

### A8-02 Agent handoff and model escalation
`status: todo | owner: none | branch: none | depends: A8-01`
`scope: internal/agent/`

Deliverable: hand a worktree and a context summary from one agent to another, so a cheap model can
explore and a stronger one can act on what it found.

Acceptance: the receiving agent gets the summary and the worktree, not the whole transcript. The
handoff is visible in both sessions. Cost is attributed to each agent separately.

`verify: claude [ ]   codex [ ]`

notes: **added 2026-07-26.** A real cost lever that only exists because keys have names. Exploring a
large codebase is mostly reading, which a cheap model does adequately, while the fix wants the
strongest model available. Doing that by hand today means copying context between tools.

### A8-03 Project configuration file
`status: todo | owner: none | branch: none | depends: PG-A7`
`scope: internal/config/`

Deliverable: a committed per project file defining profiles, test commands, permission posture and
project instructions.

Acceptance: unknown executable fields are errors rather than warnings. Templates resolve before
execution. A relative path cannot escape the worktree.

`verify: claude [ ]   codex [ ]`

notes: carries forward the validation discipline from the old P3-01. Needed by A6 anyway for per
project test commands, so this is the point where it stops being optional.

### A8-04 Custom slash commands
`status: todo | owner: none | branch: none | depends: A8-03`
`scope: internal/agent/, internal/tui/`

Deliverable: user defined reusable prompts as `/commands`, per project and globally.

Acceptance: a project command is available in that project only. Arguments are substituted safely.

`verify: claude [ ]   codex [ ]`

notes: cheap once chat exists, and the first thing power users ask for.

### A8-05 Hooks and automations
`status: todo | owner: none | branch: none | depends: A8-03, PG-A6`
`scope: internal/agent/`

Deliverable: run something on an event. Tests green, auto commit. Tests red, notify. Agent idle,
nudge.

Acceptance: a hook fires only on a real state transition, never on a stale or unknown one. A
failing hook is visible and never silently swallowed.

`verify: claude [ ]   codex [ ]`

notes: where verification and orchestration compound. The truth engine is what makes the triggers
trustworthy, so hooks firing on unverified state would poison both.

### A8-06 MCP client
`status: todo | owner: none | branch: none | depends: A4-04`
`scope: internal/tools/mcp/`

Deliverable: connect to MCP servers and expose their tools to agents.

Acceptance: third party tools pass through the same permission model as built in ones, with no
exemption. A failing server degrades that server only.

`verify: claude [ ]   codex [ ]`

notes: one protocol gets an entire ecosystem of tools other people maintain. Deliberately after A5,
so the multi agent core is built on tools we control. The permission point is not negotiable: a
third party tool is exactly the thing that most needs the same scrutiny as our own.

### A8-07 Cost versus outcome
`status: todo | owner: none | branch: none | depends: PG-A6`
`scope: internal/tui/`

Deliverable: did the more expensive model actually pass more tests, on this project's own history.

Acceptance: the comparison names its sample size and refuses to draw a conclusion from too little
data.

`verify: claude [ ]   codex [ ]`

notes: only meaningful because A2 makes cost exact and A6 makes outcome exact. Almost nothing else
can answer this honestly.

### A8-08 Run report export
`status: todo | owner: none | branch: none | depends: PG-A6`
`scope: cmd/canopy/`

Deliverable: one command producing a markdown summary of an agent's changes, test results and
cost, suitable for a pull request body.

Acceptance: the report never claims a verification state the evidence does not support.

`verify: claude [ ]   codex [ ]`

notes: cheap, and it is the artefact that makes agent work reviewable by someone who was not
watching.

### A8-09 Shareable skills
`status: todo | owner: none | branch: none | depends: A8-04`
`scope: internal/agent/`

Deliverable: packaged prompt fragments plus config that users install and share.

Acceptance: an installed skill declares what tools and permissions it expects, and installing it
never silently widens an agent's trust level.

`verify: claude [ ]   codex [ ]`

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
`status: todo | owner: none | branch: none | depends: PG-A8`

Acceptance: timeouts terminate the right process group, no final state transition is dropped under
load, huge output cannot freeze the UI, paths and branch names with spaces work, externally removed
worktrees disappear safely, and quitting leaves no child processes behind.

`verify: claude [ ]   codex [ ]`

notes: carries forward P4-01 to P4-07.

### A9-02 Interface robustness
`status: todo | owner: none | branch: none | depends: PG-A8`

Acceptance: readable at 80 columns with several agents, resize does not corrupt the frame, every
state is distinguishable without colour, rapid updates never move the selection, the UI rebuilds
from a fresh snapshot, and every error says what to do next.

`verify: claude [ ]   codex [ ]`

notes: carries forward P4-08 to P4-12. P1-07 already meets the 80 column and selection criteria.

### A9-03 Themes and help
`status: todo | owner: none | branch: none | depends: A9-02`

Acceptance: at least two themes, both passing the no colour requirement. A keybinding overlay
covering every binding.

`verify: claude [ ]   codex [ ]`

notes: none

### A9-04 Honest limitations document
`status: todo | owner: none | branch: none | depends: A9-01, A9-02`
`scope: LIMITATIONS.md`

Deliverable: what Canopy does not guarantee. No sandboxing. No database or dependency isolation
between worktrees. Coarse whole worktree staleness. Secrets a child process prints are not
redactable. macOS and Linux only. Cost figures depend on a dated pricing table.

Acceptance: a reader can tell within a minute whether Canopy will lie to them, and how.

`verify: claude [ ]   codex [ ]`

notes: underclaiming is on brand, not a weakness.

### A9-05 Packaging and install
`status: todo | owner: none | branch: none | depends: A9-04`

Acceptance: a stranger installs a build, adds a key, and runs an agent without being talked
through it.

`verify: claude [ ]   codex [ ]`

notes: GoReleaser, Homebrew tap, `go install`.

### PG-A9 Phase A9 gate
`status: todo | depends: A9-05`

Both supervisors watch someone outside the team install it and use it.

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
