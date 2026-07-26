# Canopy Decisions

Answers to the fifteen questions both maintainers have to record before implementation, plus the
items that are still genuinely open. Every entry is either decided, with the reasoning written
down so it can be revisited on purpose instead of by drift, or open, naming who has to answer it.

A decision is not changed by editing this file alone. Change it in a pull request, with the
reason, and update the affected tasks in TASKS.md.

Last reviewed: 2026-07-26

---

## D-01 What is the primary goal? Decided.

Learning and a portfolio project, released as open source. Not currently a commercial venture.

So all of the engineering rigour from the review documents is adopted: the truth contract, the
trust model, the acceptance test suite. The startup flavoured validation gates (five external
installs, a paid pilot, economic buyer analysis) are treated as aspirational signals rather than
blockers on shipping v0.1. The developer interviews are still worth doing on their own merits.

## D-02 Is v0.1 definitely a companion for existing worktrees? Decided.

Yes, unambiguously.

Canopy discovers worktrees that already exist and reports evidence about them. It does not create
them, does not remove them, and does not take ownership of anyone's agent sessions or git
lifecycle. It has to work alongside whatever tool the user already uses to manage worktrees,
whether that is plain git, a script, or another agent manager.

The v0.1 objective, verbatim: from an existing Git repository, discover its active worktrees and
show trustworthy, current test and service evidence for each worktree without taking ownership of
its agent sessions or Git lifecycle.

## D-03 Which operating systems? Decided.

macOS and Linux. Both tested in CI.

Windows is deferred until process group and terminal semantics are designed and tested for it.
Shipping a half working Windows build would break the same "do not claim what you cannot prove"
principle the product is built on.

## D-04 Which two repositories for dogfooding? Decided.

Canopy itself as the Go repository, once phase 2 lands. A Node repository with a real test suite
as the second. Not Python, by preference.

The second repository deliberately is not Go. Dogfooding on two Go projects would prove nothing,
because the entire point is to catch the places where we accidentally baked in Go assumptions:
process behaviour, exit codes, output volume, how long a suite takes, what a test runner does to
its children on cancellation. A Node suite exercises all of that differently.

The specific Node repository is still to be picked, but any real one with a test suite slow enough
to be worth watching will do.

## D-05 Which test command format is supported first? Decided.

command.argv, an explicit argument array, is the default and preferred form:

```yaml
command:
  argv: ["go", "test", "./..."]
```

A shell string is available but has to be opted into explicitly, and is marked as higher risk:

```yaml
command:
  shell: "pnpm test -- --runInBand"
  allow_shell: true
```

Defining both forms in one command is a validation error.

tests is an array, not a single command, from the first version. Each entry has name, required,
command, cwd, timeout and trigger. This resolves the contradiction between corrections section 8
("one arbitrary test command") and section 9 (a tests list). The array wins because required is
load bearing for the roll-up rules in section 3.4, and retrofitting it later would mean changing
the truth contract rather than adding to it. Confirmed in round 2 section 4.2.

Process exit code is the only source of pass/fail truth in v0.1. Zero framework specific parsers
ship. Parsed pass/fail counts are optional metadata for a later version, and a failed parser may
never turn a zero exit code into a failure. Confirmed in round 2 section 4.3.

## D-06 Observe external services only, or start one managed service? Decided.

Observe only. Canopy does not start services in v0.1. `managed: false` is the only supported
value.

This resolves the contradiction between corrections section 7, which describes starting services,
startup timeouts, graceful shutdown, process group termination and restart policy, and sections 8
and 9, which include only health checks for configured external services and mark them
`managed: false`. Round 2 section 4.1 proposed exactly this reading and it was confirmed.

The asymmetry is deliberate and has to be stated plainly in the README: Canopy executes test
commands itself, but only observes services the user started.

Two consequences follow, and both are easy to get wrong.

1. Everything in corrections section 7, meaning the port allocator, lease retention, bind
   collision detection and retry, startup timeouts, graceful shutdown grace periods and restart
   policy, is phase 6 work rather than v0.1. It is listed under phase 6 in TASKS.md.
2. There is no port allocator in v0.1. Canopy cannot allocate a port for a process it does not
   start. Named ports are declared in configuration and resolved in templates (`{{ ports.web }}`),
   nothing more. A range form is not supported yet, a fixed value is.

## D-07 How quickly must a changed file invalidate a green status? Decided.

Within one revision poll interval. `observation.revision_poll_interval` defaults to 2s, so the
practical target is under two seconds and the worst acceptable case is five.

It has to happen without restarting Canopy, and it has to be visible in the dashboard rather than
only recorded internally. This is the phase 2 integration checkpoint and the first demo.

## D-08 What is the maximum acceptable test log size? Decided.

`logs.max_lines_per_source` defaults to 5000 lines, held in a bounded ring buffer per source.

Once the limit is passed, the buffer keeps the head and the tail and records explicitly how many
lines were dropped in between, as a visible marker rather than a silent truncation. The reasoning
matches the rest of the product: the first lines usually carry the command and the setup, the last
lines carry the failure, and pretending nothing was lost would be a small lie of exactly the kind
this tool exists to avoid.

Log buffers are bounded and separate from state. A final state transition is never dropped,
whatever the log pressure.

Logs stay local, are never uploaded, and may contain secrets that a child process printed itself.
See D-20.

## D-09 Policy for untracked files above the fingerprint limit? Decided.

`observation.untracked_file_hash_limit_mb` defaults to 25 MB.

If a non-ignored untracked file goes over the limit, Canopy cannot compute a trustworthy revision
key, so the revision becomes unknown and a readable reason names the offending file.

Unknown is never green and never silently ignored. Skipping the file and carrying on would be the
tempting choice and it is the wrong one, because it would let a large untracked fixture change
without invalidating a green result, which is exactly the false green this product refuses.

Related revision key rules, decided at the same time:

- Symlinks are not followed. The link target string is hashed. Following them risks escaping the
  worktree and recursing forever.
- Submodules contribute their HEAD SHA. Canopy does not recurse into submodule contents in v0.1,
  and the limitation is documented rather than hidden.
- Git-ignored files do not affect the revision key. This is a known and documented hole, since a
  .env change can alter test outcomes without changing the revision. It goes in LIMITATIONS.md
  rather than getting papered over.

## D-10 Wording that distinguishes passing, stale, unknown and not configured. Decided.

Every state carries a word and a glyph, never color alone, so it survives NO_COLOR=1, a monochrome
terminal, and color blind readers. Color is an accelerant, never the only carrier.

Test states:

| State | Label | Glyph | Reads as |
|---|---|---|---|
| passing | PASS | v | verified green for the current code |
| failing | FAIL | x | the suite ran and did not pass |
| stale | STALE | ~ | needs a re-run, not broken and not trusted |
| running | RUN | ... | in flight |
| queued | QUEUED | > | waiting to start |
| error | ERROR | ! | could not run, distinct from failing |
| cancelled | CANCEL | - | stopped by the user |
| unknown | UNKNOWN | ? | evidence cannot be trusted |
| not-configured | NOT SET | . | nothing was ever configured to run |

Service states:

| State | Label | Glyph |
|---|---|---|
| healthy | UP | ^ |
| unhealthy | SICK | v |
| starting | START | > |
| stopping | STOP | < |
| stopped | DOWN | _ |
| crashed | CRASH | x |
| unknown | UNKNOWN | ? |
| not-configured | NOT SET | . |

Final glyph choice belongs to the TUI work and may use box drawing or Nerd Font characters
instead, as long as every state stays distinguishable without color. The words above are fixed.

STALE is deliberately styled neutral or amber, never red. Round 2 section 3.1 makes the point
sharply: a dashboard that screams alarm at every edit trains users to ignore it, and an ignored
status is untrustworthy for a different reason than a false one. STALE has to read as "I do not
know yet, ask me again", not as "this is broken".

NOT SET and PASS must never be confusable. "No tests configured" is not "tests passed", and that
is one of the loudest promises the product makes.

## D-11 Which packages does each maintainer own? Decided.

No fixed ownership split. Both pairs work down TASKS.md in order and claim the next available
task, whichever part of the stack it touches.

This is a deliberate departure from the engine and TUI split proposed in both roadmaps. It trades
the clean file boundary for shared understanding of the whole system, which suits a two person
learning project, since neither person ends up having seen only half of it.

Because the boundary is gone, the claim protocol in TASKS.md section 1.3 is doing the work the
file boundary used to do. Two rules matter most:

1. A claim has to be pushed before the work starts. A local claim is not a claim.
2. Never edit a file listed in another task's scope while that task is claimed or in review.

internal/core stays a shared contract regardless. Changing it needs a short joint design
discussion, never a unilateral commit.

## D-12 Integration checkpoint cadence. Decided.

No fixed calendar. Both pairs work whenever they want, including at the same time, on short lived
branches, and integrate when a branch is ready rather than on a schedule.

This departs from the corrections document, which asked for at least two integrations per week on
named days. The reasoning for departing: this is a part time project for two students, a calendar
nobody agreed to is a calendar nobody keeps, and the actual failure mode it was guarding against
is branches diverging for weeks rather than an absence of ceremony.

So the guard moves from the calendar to the branch. Three rules replace it:

1. Branches stay short lived, ideally under two days. If a branch has been open longer than that,
   it is too big and should be split.
2. Merge or rebase main into your branch before you push, every time.
3. Claim tasks in TASKS.md before starting, per section 1.3. That file is what stops us colliding
   now that there is no fixed ownership split and no sync meeting.

If branches start living for a week, this decision was wrong and the calendar comes back.

## D-13 What evidence would make us stop or pivot? Decided.

Recorded in advance, because deciding this after seeing the results is not deciding it at all.

- Any false green observed in real use. A single one falsifies the product's only claim. This is
  the hard stop. Everything else below is a signal to change direction.
- Stale fatigue in practice, meaning users report the dashboard is stale so often that they
  stopped reading it. That means the freshness model needs scoping, not that the project should
  end.
- Nobody keeps it installed for a week, including us.
- Median time from clone to a useful dashboard stays above ten minutes and does not improve.
- A competitor ships rigorous revision aware freshness first and does it well. The honest response
  then is to say so and decide whether anything is left worth building.

## D-14 License. Decided.

MIT. Permissive, familiar to the Go and terminal tooling audience, and consistent with the
comparable tools in this space.

## D-15 Is "Canopy" available and worth keeping? Decided, keep it.

Checked: the Homebrew formula name is free. GitHub has many projects called Canopy, none of them
doing this.

Keeping it. Homebrew is the distribution channel that actually matters for a Go CLI, and it is
clear. The GitHub collisions cost nothing, because the module is namespaced under the
organisation, so `go install github.com/Walid-Idrissi-Labs/Canopy/cmd/canopy@latest` is unambiguous
no matter how many other Canopys exist. The only real cost is search noise, which a good README
and a clear one liner fix.

The module path github.com/Walid-Idrissi-Labs/Canopy is now settled rather than provisional.

If a rename ever becomes worth it, the cheapest moment is before the first tagged release: one
line in go.mod plus an import rewrite. After a release it means broken install paths, so decide
before tagging v0.1.

Alternatives that were floated and set aside: Grove, Orchard, Muster, Warren, Hangar.

---

# Items still open from the review rounds

These were raised in Canopy-Review-Round2-Confirmed-by-Codex.md and are not resolved by any
document. They are recorded here rather than silently defaulted, because each one is a decision
about what the product promises.

## D-16 Staleness granularity. OPEN, needs Codex.

Round 2 section 3.1 asked whether a coarse whole worktree RevisionKey invalidates so often that
users learn to ignore stale, and no answer exists in any document.

Current behaviour, by default, and deliberately not invented around: coarse and whole worktree,
exactly as corrections section 3.1 specifies. Editing a README marks the tests stale.

Two mitigations are in place and neither weakens the contract. STALE is styled neutrally rather
than as an alarm (D-10), and re-running is a single keystroke (P2-11).

An ignore_globs escape hatch was considered and rejected for now. It would let a user exclude a
source path and then see a green result that does not account for it, which is a false green and
directly contrary to the corrections section 12 acceptance tests. If stale fatigue turns out to be
real in the pilot, per test path relevance is the principled fix, not a blanket ignore list.

Do not add staleness scoping without an explicit joint decision recorded here.

## D-17 Trust re-approval friction. OPEN, needs Codex.

Round 2 section 3.4 observed that re-approving on every executable configuration change means a
developer iterating on their test command hits the prompt over and over, and asked for a low
friction path that does not weaken the guarantee.

Current behaviour: strict. Any change to an executable field invalidates trust and re-prompts.

No low friction mode is approved. The candidate ideas, a "developing this config" session mode or
approving a diff of what changed rather than the whole configuration, are unevaluated. Security
regressions are exactly the kind of change that should not be made unilaterally by whoever hits
the annoyance first.

## D-18 Timeout outcome. Decided, but flagged.

Corrections section 3.2 requires a timeout to resolve to either error or failing "according to a
documented project setting", without naming a default.

Default: error. A timeout is evidence that the command did not finish, not evidence that the tests
failed, and calling it failing would assert something Canopy did not observe. The setting is
configurable for projects whose suites hang on genuine failures.

Either way, a timeout is never passing. Flagged for Codex to confirm the default.

## D-19 Does v0.1 need auto run on change? OPEN, needs both supervisors and Codex.

This is the most consequential open question, and it decides whether v0.1 is compelling or merely
correct.

Corrections section 9 defers on_change, making manual triggering the v0.1 default, until debounce
and cancellation behaviour are tested.

Round 2 section 3.2 argues the opposite risk: if a user has to configure a tool and then manually
trigger every run, the honest question "why not just run the tests in a terminal myself" may not
have a good answer, and the value may not survive.

Current plan: ship manual triggering as the default. on_change exists as P4-13, is gated on
cancellation and debounce being proven, and is not to be built until this is settled.

The argument for manual being enough, which is the current recommendation: the freshness signal is
live regardless of the trigger. Canopy polls the revision every two seconds, so a green result
goes stale by itself whether or not it reruns anything. Auto run only removes the keystroke that
re-verifies. That means the sentence that keeps a user installed, "at a glance I can see which of
my branches is green for the code actually in them right now", is already true with manual
triggering. What auto run buys is convenience, not the core claim.

The counter-argument, which is why this stays open: convenience may be exactly what determines
whether anyone bothers, and a dashboard full of stale rows that only clears when you press a key
is a dashboard you stop looking at.

Still open pending Codex and both supervisors. Recommendation is manual for v0.1, opt-in on_change
in phase 4.

## D-20 Secret redaction boundary. Decided.

Canopy will not print environment values marked secret in any output it formats, meaning the trust
screen, service detail, or its own log rendering.

Canopy cannot redact a secret that a child process prints to its own stdout. That output is
captured verbatim into the log buffers.

This boundary goes in LIMITATIONS.md plainly rather than being left to be inferred. Claiming
redaction that is not enforceable would be the same category of error as a false green.

---

## Appendix: where the settled scope comes from

Three documents govern this project and they do not carry equal weight.

1. Canopy-Pre-Build-Corrections.md is the correction layer. Where it conflicts with anything else,
   it wins. It is the source of the truth contract, the trust model, the v0.1 scope cut, the phase
   plan and the acceptance tests.
2. Canopy-Review-Round2-Confirmed-by-Codex.md accepts roughly ninety percent of the corrections
   and records the positions on the array versus single command question, the probe versus manage
   boundary, and the absence of framework parsers.
3. SPEC.md and ROADMAP.md are the original plan and are substantially superseded. The original
   MVP, the "only tool that shows health" positioning, the port model and the original phase order
   are all obsolete. Read them for background, never as instructions.

FEATURES.md is a north star brainstorm and explicitly not a build list. Nothing in it is approved.

These documents are kept outside this repository, private to the maintainers.
