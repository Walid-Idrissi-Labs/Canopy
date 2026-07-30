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
| passing | PASS | check | verified green for the current code |
| failing | FAIL | cross | the suite ran and did not pass |
| stale | STALE | ~ | needs a re-run, not broken and not trusted |
| running | RUN | > | in flight |
| queued | QUEUED | middle dot | waiting to start |
| error | ERROR | ! | could not run, distinct from failing |
| cancelled | CANCEL | - | stopped by the user |
| unknown | UNKNOWN | ? | evidence cannot be trusted |
| not-configured | NOT SET | blank | nothing was ever configured to run |

Service states:

| State | Label | Glyph |
|---|---|---|
| healthy | UP | check |
| unhealthy | SICK | cross |
| starting | START | > |
| stopping | STOP | - |
| stopped | DOWN | middle dot |
| crashed | CRASH | ! |
| unknown | UNKNOWN | ? |
| not-configured | NOT SET | blank |

The workspace roll-up reads YES or NO rather than a bare tick, because a tick alone invites the
eye to see a green shape and stop looking, while the word pushes the reader on to the columns
that say which evidence produced it.

Every glyph is single width so a state change can never shift a column. Implemented in
internal/tui/styles.go as of P1-07.

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

## D-21 What the finished product is. Decided 2026-07-26.

Recorded because it was previously only implied, and because it reverses a stated non-goal.

**Canopy is an agent runtime.** In the finished product it holds provider API keys as named
credentials and makes the API calls itself, in the same category as OpenCode and Claude Code. It
does not spawn someone else's agent CLI and watch the output.

An agent session is assigned a key by name, so naming an agent `claude` runs it against the key
registered for Anthropic while another agent runs on a different key, provider or account. Because
Canopy owns the request, per agent token and cost accounting is exact rather than inferred, and
budget limits are enforceable rather than advisory.

This reverses SPEC.md section 3, which said "not an agent itself, it drives existing agent CLIs".
That bullet is now marked as reversed and applies to v0.1 only.

**Nothing about v0.1 changes.** v0.1 spawns no agents, holds no keys and makes no API calls, per
D-02 and the v0.1 exclusion list. The build order is unchanged: the truth engine first. The reason
is stronger under this decision rather than weaker, since an agent runtime nobody can trust is a
worse Claude Code, while one that ranks its own agents by whether their code actually passes is
something no incumbent currently does.

Three consequences that have to be designed for before any of it is built, listed here so they are
not discovered late:

1. **The trust model does not cover agent execution.** D-04 and the trust contract govern commands
   *the user wrote* in a config file. An agent runtime executes commands a *model* generated, which
   is a different threat model and needs its own permission design: per tool approval, path
   restrictions, an allow and deny model for shell execution, and an audit trail. Shipping agent
   execution under the current trust contract would claim a protection that does not exist, which
   is the same class of error as a false green.
2. **Key custody needs a real answer** before a single key is accepted. OS keychain, never
   plaintext on disk, never in logs, never in a snapshot or an exported run report. See D-20 for
   the boundary Canopy can and cannot enforce.
3. **The positioning changes.** "Worktree manager independent, works beside your existing agent
   tool" is a v0.1 property, not a permanent one. The finished product competes with Claude Squad
   and Orca directly, rather than sitting alongside them. That is worth stating plainly, because
   avoiding that fight was the stated reason for the observe first cut in the first place.

Full write up in FEATURES.md section 10.

## D-22 Roadmap re-planned around the agent runtime. Decided 2026-07-26.

D-21 settled what Canopy is. This settles the order it gets built in, and it reverses the
observe-first sequencing that phases 2 to 6 were built around.

**What changed.** Old phases 2 to 6 built a worktree verification cockpit and left the agent
runtime as a conditional expansion after a pilot. They are replaced by A1 to A7, which build the
agent runtime first and fold verification in at A6.

**Why.** Walid observed, correctly, that the state of the app did not look like it was heading
toward the product described in D-21, and said so three times. It was not. Everything shipped so
far is Pillar 1. Recording a destination in a document does not make the code move toward it, and
a plan whose next six weeks of work are invisible in the finished product is a plan that will get
abandoned rather than followed.

**What survives.** More than it might look like. The shared contract, the sequenced snapshot store
with its coalescing rules, the TUI shell and its selection model, and the process group and
bounded output designs all carry forward. Token streaming is the highest volume event source this
project will have, which is exactly the case the store was designed for, so A3-01 is the first
real test of whether P1-01 was right.

**What is deferred rather than cancelled.** Service health, the whole of the old phase 3 section on
probes, is off the path to the agent product and moves to after A9. Per project config returns at
A8-03, when there is something worth configuring. The pilot still matters and follows A9.

**Expanded on the same day.** A full review of `SPEC.md` and `FEATURES.md` grew A1 to A7 into A1 to
A9, adding persistence, compaction, git workflow and the extensibility layer. See D-23 to D-29.

**Decisions this supersedes or changes:**

- **D-02 is now false as written.** v0.1 is not a companion for existing worktrees. Canopy creates
  and removes worktrees for its agents at A5-03, which the old plan forbade outright. The lifecycle
  guards survive as behaviour: never manage the primary checkout and never remove a dirty worktree
  silently. D-33 later distinguishes that lifecycle guarantee from a direct agent intentionally
  operating in the workspace where Canopy started.
- **D-06 is deferred.** Observe-only service health is not on the path. When it returns, the
  question of whether Canopy starts services reopens, because an agent runtime that owns a worktree
  has a much better claim to owning its dev server than a passive monitor did.
- **D-19 stays open** but is now answered in the A6 context rather than the v0.1 one.
- **D-11 still holds.** No fixed ownership split, tasks claimed through TASKS.md.
- **D-01, D-03, D-05, D-07 to D-10, D-12 to D-18, D-20 and D-21 are unaffected.**

**The risk, recorded rather than argued away.** This is several times the scope of the plan the
corrections document approved, against incumbents who do it full time. The competitive reasoning in
that document has not become wrong, it has been overruled deliberately by the person whose project
this is. A6-05, ranking agents by whether their code actually passes, is the bet that makes the
larger scope worth attempting: Orca already fans out across agents, and nobody uses test truth to
rank the results.

## D-23 Go, not Rust. Decided 2026-07-26.

Considered and rejected a rewrite in Rust on performance grounds.

The workload is I/O bound. Ten agents streaming is ten HTTP connections waiting on model latency
measured in hundreds of milliseconds. Rust does not make a model answer faster. The only genuinely
CPU bound work in the design is hashing and diffing worktrees, which is milliseconds.

Where Rust would win: memory footprint at high agent counts, startup time, binary size. All real,
all small at this scale. Where Go wins here: goroutines make N concurrent streaming sessions
simpler than async Rust, Bubble Tea is more mature than ratatui for this shape of application, and
two part time students ship several times faster. A rewrite would also discard 4,600 tested lines.

**Revisit trigger, so this is a decision rather than an assumption:** measure at A5 with eight
agents running. If memory or render latency is actually a problem, port the hot path rather than
the product.

## D-24 SQLite for session storage. Decided 2026-07-26.

Sessions are persisted, resumable by id, and searchable across history.

One storage decision buys four features, because sessions, the tool call audit trail, cost history
and run reports are all queries over the same data. Embedded, no server, one file, and it is the
obvious fit.

Schema migrations from the first version. The schema will change, and a tool that loses your
history on upgrade is not one anyone keeps.

The alternative considered was a file per session, which is simpler and gives up search, and
would have meant building the audit trail and cost history separately anyway.

## D-25 Permissions are per agent trust levels. Decided 2026-07-26.

Trust posture is a property of the profile, not a global setting. A scratch agent in a throwaway
worktree runs loose. An agent working near `main` asks for everything.

Rejected alternatives and why:

- **Ask every time.** Unusable with several agents running. You answer prompts constantly, which
  trains you to approve without reading, which is worse than asking less.
- **One global allowlist.** Lowest friction, but a wrong allowlist is silent and you find out
  afterwards.
- **Ask once per tool per session.** Reasonable, and it was the recommendation, but it forces the
  strictest agent's friction onto every agent, and people respond to that by loosening everything.

Costs more to design and to explain. Worth it, because the whole product is about running agents
with different levels of trust at the same time, and a single posture cannot express that.

See A4-04, and note that the repository trust contract from the corrections document does **not**
cover this. That governs commands the user wrote. This governs commands a model wrote.

## D-26 MCP after A5. Decided 2026-07-26.

An MCP client is in, at A8-06, not at A4.

One protocol implementation gets an entire ecosystem of third party tools, which makes it the
highest leverage single feature on the list. It is deliberately placed after the multi agent core,
so that core is built on tools we control and can reason about completely.

Non negotiable when it lands: third party tools pass through the same permission model as built in
ones, with no exemption. A tool somebody else wrote is exactly the thing that most needs the same
scrutiny as our own.

## D-27 LSP integration is cut. Decided 2026-07-26.

Real go to definition, find references and compiler diagnostics would measurably improve agent
output on a large codebase, and few terminal agents offer it.

Cut anyway. It is one client per language server, which is a subsystem sized commitment against a
product that already has nine phases. Grep and structured file tools are adequate for most work.

Recorded with the reasoning rather than deleted, so reversing it is cheap. Revisit only if agent
quality on large repositories becomes the binding constraint, which is a measurable condition
rather than a feeling.

## D-28 Context compaction is always visible. Decided 2026-07-26.

Automatic compaction near the context limit, plus a permanently visible context meter, plus manual
compaction on demand.

Compaction always announces itself in the transcript and says what was summarised. Silently
dropping context so an agent quietly gets dumber is the same class of lie as a false green: the
output still looks confident and there is no way for the user to know why it got worse.

Compaction shortens what is sent to the model. It never destroys history, which stays complete in
storage and stays searchable.

## D-29 Features added during the 2026-07-26 review. Decided.

Beyond what Walid specified, seven additions were proposed and accepted. Recorded here with their
justification so they can be cut on purpose if the scope needs to shrink.

1. **A5-04, worktree environment setup.** Not an addition, a hole. Canopy now creates worktrees,
   and a fresh worktree has no `.env`, no dependencies and no build cache, so an agent spawns into
   a tree where nothing runs and A6 reports failures that have nothing to do with its code. That
   is a false red, and just as damaging as a false green. Restores the environment contract from
   corrections section 6, which the first re-plan dropped at exactly the point it became more
   necessary.
2. **A2-08, provider fallback chains.** Cheap once the error taxonomy exists, and it matters as
   soon as many agents run, which is when providers shed load. Authentication failures deliberately
   do not fall through, because a wrong key should be fixed rather than routed around, and every
   fallback is visible because being billed on a different key without being told would be
   dishonest.
3. **A3-07, session forking.** The companion to branch per agent, and it maps onto how people
   already think in git. Today "go back three turns and try it the other way" means either a fresh
   session with lost context or arguing with an agent already committed to an approach.
4. **A4-09, plan first mode.** Approval at the task level rather than the keystroke level. Per tool
   prompting on a fifty step task trains people to approve without reading, which is worse than not
   asking at all. Reviewing one plan is something a person actually does properly.
5. **A5-09, cost preview before dispatch.** Nearly free once usage history exists, and it is the
   real answer to a misparsed 20 instead of 2. The estimate carries its basis and says plainly when
   there is not enough history, because an estimate presented more confidently than the data
   supports is its own small lie.
6. **A6-06, ready to review queue.** Nearly free, since the truth engine already knows which agents
   are green for their current code. With six agents running, "which should I look at next" is the
   actual question and nothing else on the list answers it.
7. **A8-02, agent handoff and model escalation.** A cost lever that exists only because keys have
   names. Exploring a large codebase is mostly reading, which a cheap model does adequately, while
   the fix wants the strongest model available.

## D-30 Official Anthropic SDK, hand-rolled elsewhere. Decided 2026-07-26.

The Anthropic client uses `github.com/anthropics/anthropic-sdk-go`. Other providers are hand
rolled.

**This reverses an earlier call in SPEC.md**, which said hand-roll both on the grounds that a
vendor SDK would fight the streaming and cancellation shape we need. Checking the SDK rather than
assuming: it supports streaming through `Messages.NewStreaming`, takes a context for cancellation,
and ships typed errors and model constants. The objection was to a problem that does not exist,
and hand rolling would mean tracking API changes ourselves for nothing in return.

The asymmetry is deliberate rather than untidy. The OpenAI compatible surface we need is small,
and pointing a vendor SDK at arbitrary base URLs is exactly the case those SDKs handle worst. Each
provider package hides its own approach behind the `core` interface, so nothing above the provider
layer can tell which one is which.

## D-31 Provider contract rules from the current API. Decided 2026-07-26.

Checked against the current API reference rather than written from memory, because several of
these changed recently and a plausible-looking wrong value is worse than an obvious one.

- **Default model is `claude-opus-5`.** Model IDs are exact and carry no date suffix.
- **`refusal` is a stop reason, not an error.** A declined request returns HTTP 200 with
  `stop_reason: "refusal"` and possibly empty content. Code that reads the first content block
  without checking the stop reason breaks on it, so the contract requires checking first.
- **Sampling parameters are rejected.** `temperature`, `top_p` and `top_k` return 400 on the
  current models. `AgentProfile.Temperature` exists in the contract from A1-01 and must not be
  sent to those models. The provider layer enforces this rather than trusting callers.
- **Thinking depth is controlled by effort, not a token budget.** `budget_tokens` returns 400.
  Thinking is on by default on the current Opus, so an omitted setting means it thinks.
- **Streaming above roughly 16k output tokens**, because non-streaming requests hit HTTP timeouts
  there. We stream everything anyway.

Recorded here rather than only in code comments so the next person to touch the provider layer
does not have to rediscover them, and so a wrong value can be traced to a decision rather than to
a guess.

---

## D-32 Prices are recorded, never inferred. Decided 2026-07-26.

The pricing table in `internal/pricing` only holds rates for endpoints where the endpoint
determines the price: Anthropic first party, and local runtimes where nothing is billed at all.

The temptation was to price the OpenAI compatible family by model name, since a model called
`anthropic/claude-opus-5` obviously costs what Anthropic charges. It does not. The gateway sets the
price, there are many gateways, and their margins differ. A number derived that way would be a guess
wearing the clothes of a fact, and the person reading it would have no way to tell.

So an endpoint with no recorded rate reports as unpriced and names itself, and A2-09 will let the
user supply their own rate, labelled as theirs. Three states, all distinguishable on screen: a
checked price, the user's price, and no price. Canopy is a tool for telling which of several things
is actually true, and a cost figure it cannot stand behind is exactly the kind of confident wrong
answer the rest of the design is built to avoid.

Two consequences worth naming. **Free and unpriced are separate claims** and both get made:
`CostKnown` exists next to `CostUSD` precisely so a local model can say zero and mean it. And
**`Usage.Add` has no identity element**, which is why `core.Sum` exists: `Usage{}` is
indistinguishable from a turn nobody could price, so folding a list from the zero value would mark
every total unknown.

---

## D-33 Direct and isolated agents have different workspace contracts. Decided 2026-07-27.

Canopy supports two deliberate ways for an agent to work. They are product modes, not two names for
the same safety guarantee.

**Direct mode** uses the repository where Canopy was started as the agent's workspace. That may be
the primary checkout. It is the ordinary one-person, one-agent workflow, and an agent may modify that
workspace when its trust level permits it. The interface must identify direct mode and its workspace
before a write-capable agent runs. Direct mode must never be described as isolated.

**Isolated mode** creates a Canopy-owned worktree and builds that agent's structured tool registry
against the worktree root. File tools and path-scoped Git tools refuse paths outside that root,
including the primary checkout and another agent's worktree. Fan-out and any workflow in which
several agents may edit concurrently require isolated mode; silently falling back to a shared
checkout is a refusal, not a convenience.

**The shell is not contained in either mode.** It starts in the selected workspace, but its command
is opaque and runs with the user's operating-system permissions. It can use `..`, an absolute path,
or another program to reach outside an isolated worktree. Read-only and confined trust therefore do
not expose shell. Standard trust asks for the exact command; broad trust runs it without asking.
Those levels control permission and visibility, not operating-system containment. A future sandbox
would be a new decision, not a documentation synonym for the current permission model.

**Primary-checkout lifecycle protection remains absolute.** Canopy's worktree manager never removes,
resets, force-checks-out, or otherwise takes ownership of the primary checkout or a worktree it did
not create. A direct agent editing the workspace the user selected is agent tool execution, not
worktree lifecycle management. The interface and audit trail must keep that distinction visible.

This resolves four conflicting statements:

- D-22's surviving guard, "never touch the primary checkout", is narrowed to worktree lifecycle
  operations. It does not prohibit an explicitly direct agent from editing its selected workspace.
- A1-01's statement that no trust level permits touching the primary checkout is replaced by the
  combination of workspace mode and trust level above.
- A4-06 must distinguish a direct workspace from an isolated worktree instead of claiming every Git
  tool always runs away from the primary checkout.
- A5-11 must claim structural confinement only for structured tools. It must state the shell
  exception rather than promise a sandbox Canopy does not have.

The word **confined** names a trust level with a restricted tool surface: reads and writes inside
the assigned workspace, with shell and destructive Git denied. It does not mean that Canopy places
arbitrary child processes in a sandbox.

This is a cross-cutting safety contract. A future change to workspace selection, tool kinds, trust
levels, shell execution, worktree ownership, or approval scope must update this decision, the
affected TASKS.md acceptance blocks, README.md, LIMITATIONS.md, and the direct/isolated by
trust-level tests in the same branch. If those sources disagree, the task is blocked until the
supervisors decide; an agent must not silently choose whichever sentence matches the code.

---

## D-34 Commands expand at input; cost evidence is project and revision scoped. Decided 2026-07-27.

Reusable slash commands are prompt aliases, not executable configuration. They resolve at the chat
input boundary and the existing engine receives an ordinary prompt. The only substitution is the
literal, one-pass `$ARGUMENTS` placeholder; argument text is never evaluated, recursively expanded,
or passed to a shell by the command mechanism. A project command shadows a global command of the
same name only in that project. `/commands` is reserved for the visible active catalog, and `//`
escapes a literal leading slash.

The global file is `commands.json` in Canopy's user config directory and has the same
`{"commands": [...]}` shape as the project field. Both sources are strict: malformed definitions,
unknown fields, unreachable names and duplicates are errors. A broken global file disables that
optional layer with a warning; it does not disable valid project commands or the chat.

Cost history is not machine-wide. Every new session is associated with the stable identity of the
repository where Canopy started, because the SQLite file is shared across repositories and mixing
them would make "this project's history" false. Pre-dispatch estimates use only priced turns from
that project whose significant task words overlap, name a low, medium or high confidence band, and
refuse a number below three matching turns.

Cost versus outcome records one idempotent observation per project, session and verified revision.
The sample joins the session's exact accumulated provider cost to the verifier's current required
test counts. Unranked evidence is not converted into failure, and unknown cost is not converted into
zero. A session that used more than one model is excluded rather than attributed to whichever model
it happens to use now. The comparison names all exclusions and its exact sample size, requires two
models with at least three exact samples each, and describes any result as association rather than
causation.

Any future command expansion, global configuration, session storage, cost estimate, ranking or
review-screen change must preserve these scopes or explicitly supersede this decision. In
particular, adding a general template evaluator or silently falling back to history from another
project is a product-contract change, not a refactor.

---

## D-35 An approval is scoped by who defined the arguments. Decided 2026-07-28.

An approval remembered for a session is scoped by the most specific thing in the call that a person
can read: the shell command, or the path. Those are picked out by argument name, which is a sound
reading of the tools Canopy wrote and a guess about everybody else's.

**For a tool whose arguments Canopy did not define, the scope is the whole call and nothing less.**
An MCP server names its own parameters and is free to call something `path` that is not a path.
These two calls agree on that field and differ on the one that decides what happens:

	{"path": "project-1", "operation": "read"}
	{"path": "project-1", "operation": "delete"}

Scoping by the familiar-looking field lets one standing approval cover both. A tool therefore
declares whether its arguments are its own vocabulary, and anything reached over MCP declares that
they are.

**The canonical form preserves what was written.** Key order and spacing do not distinguish two
calls, so they are normalised; numbers do, so they are carried through as their literals. Decoding
into a generic value turns every JSON number into a float64, which cannot hold every integer:
9007199254740993 becomes 9007199254740992, and two calls naming different records would be
indistinguishable. Issue ids, account numbers and row ids are all exactly that shape.

**What is displayed is what is remembered.** The prompt shows the canonical arguments the approval
covers, because offering "always, this tool with exactly these arguments" while showing none of them
asks somebody to agree to something they cannot see. The stored key is a digest of that same text,
so the sentence read and the approval held cannot come apart.

This extends D-33 rather than replacing any part of it. The trust level still decides what may run
at all; this decides only how far a "yes" reaches once one has been given.

## D-36 A duplicated argument on an external call is refused. Decided 2026-07-28.

A JSON object with the same key twice does not have one meaning. Canopy keeps the last value, as most
libraries do, but the format does not require that and some keep the first.

For a call that leaves the machine, that is a confused deputy rather than an untidiness. The prompt
shows the value Canopy read, the approval is remembered against the text Canopy produced, and the
server is handed the original bytes, which it is entitled to read the other way:

	{"operation": "delete", "operation": "read"}

The person agreed to a read and a delete was performed. Nothing downstream can catch it, because by
that point every part of Canopy is looking at the same collapsed map.

**So a repeated key anywhere in an external call is refused before permission is evaluated and before
the tool runs**, and audited as a call that did not run. The refusal names the argument and says to
send it once, because a model told that can fix it and a model told "invalid input" guesses.

Canopy's own tools are not checked this way. The same map backs the prompt, the scope and the call,
so there is no second reader to disagree with, and the schema check already answers for malformed
input with a better message.

## D-37 A process group is never signalled after its leader has been reaped. Decided 2026-07-28.

A process group is named by its leader's pid. The kernel holds that number back only while the leader
is still there, so once it has been waited on the number returns to circulation and can be handed to
somebody else's job within milliseconds. A group signal sent after that point does not miss. It lands
on whatever holds the number by then, and a group signal reaches every process in it.

Canopy previously narrowed this window rather than closing it: after the reap it asked, with signal
zero, whether a group with that id still existed, and took yes to mean the group was still its own.
That cannot distinguish "still ours" from "reissued", so the one answer that permitted a signal was
also the one that could not be trusted. The comment on it said as much and kept it anyway, on the
grounds that declining to signal after the reap would leave every orphaned child of every cancelled
test run alive.

**That justification was measured and is wrong.** `Wait` does not return until the output pipes
close, and a child inherits them. So for the case the justification names, a test runner whose
workers are still running, `Wait` has not returned, the leader is unreaped, and the ordinary unreaped
path already reaches the whole group safely. Both cases, checked on darwin:

| The command leaves behind | `Wait` returns | Leader | Signalling the group |
|---|---|---|---|
| a child holding stdout | no, it blocks | unreaped | safe, and this is the common case |
| a child that redirected stdout | yes, at once | reaped | unsafe, and the id may already be reissued |

The post-reap probe therefore bought exactly one case, a child that detaches its own output, and paid
for it with a signal that can land on an unrelated process group. So the rule is now absolute: no
group is signalled once its leader has been waited on.

The check and the signal are one indivisible step, under the same lock as the actual reap. That
requires observing exit without reaping first: Linux uses `waitid(..., WNOWAIT)` and macOS uses
`EVFILT_PROC/NOTE_EXIT`. The unreaped leader still reserves its pid. After that observation,
`cmd.Wait`, the `reaped` transition and every group signal are serialized by one lock, so a signal
is wholly before the reap or is refused wholly after it. Setting a boolean after `cmd.Wait` returns
does not establish this ordering and was explicitly regression-tested.

That is why `exec.Child` is a type rather than a boolean, and why the two supported kernels have
separate exit observers.

**What this gives up**, stated plainly: a child that closes or redirects the standard streams it
inherited and outlives its parent is no longer killed. It is left running. That is the daemon case,
it is rarer than the orphaned worker case by a wide margin, and the failure it produces is a stray
process rather than a signal delivered to somebody else's work.

This supersedes the narrowing recorded against A9-01, which is closed by it rather than accepted.

## D-38 MCP servers are started once for the project, and isolated agents do not get their tools. Decided 2026-07-28.

Wiring MCP up forced a question the package could avoid while nothing called it: which agents get the
tools, and where does the server run.

Servers start once, in the project directory, when the conversation opens, and stop when it closes.
The alternative is a set of servers per worktree, which is the more obviously correct answer and is
the one this rejects for now, because a fan out of three agents would then start three of everything
and MCP servers are frequently a package manager fetching something before they answer at all.

The consequence is that a server is rooted at the project and not at any agent's worktree, and that
decides the second half. **An isolated agent does not get MCP tools.** Its confinement is enforced by
having its file and shell tools rooted at its own worktree, and a tool that reaches a program started
somewhere else is a way around that, through a capability Canopy cannot see inside. Giving those
tools to a broad isolated agent would hand it an unaudited write outside the boundary D-33 defines.
It is not a hypothetical: a filesystem MCP server is one of the most commonly configured ones there
is.

So the conversation's own agent gets them and the isolated ones do not. That is a real reduction in
what fan out can do, it is recorded in LIMITATIONS.md rather than left to be discovered, and Q-18
carries the per-worktree design that would lift it.

## D-39 A revision that appeared while a hook was running belongs to that hook. Decided 2026-07-28.

Hooks fire once per revision for anything that is a claim about code, which is the right rule and
does not terminate on its own. A hook that commits moves HEAD, the evidence goes stale, the tests run
again, they pass again, and the guard is satisfied again because the revision is new. `git commit
-am` fails harmlessly the second time round. `git commit -am --allow-empty` does not.

The choice recorded in Q-17 was between recognising a hook's own revision and requiring hooks to be
idempotent. Requiring idempotence is a rule with no enforcement and an infinite loop as its failure
mode, and it puts the burden on the person least able to see the cycle, so this takes the first.

**What makes a revision recognisable is not anything about the revision.** Every cheap test fails:
the commit author is the user when the hook commits as the user, and remembering the one revision a
hook produced breaks the moment it produces two. What is knowable is the interval. The runner already
holds the revision the hook fired at, because that is what the once-per-revision guard is keyed on,
and it can read the revision again when the hook returns. Anything that moved between those two
points moved while the configured hook batch was running, and those hooks were the only work Canopy
asked to do. That revision is recorded as already fired.

The interval is active before the commands start, not only after they return. The poller can observe
and verify a hook's commit while that hook continues with later commands; an observation inside the
interval is claimed immediately instead of starting a second batch. When several hooks listen to
one event, the interval remains active until the last of them returns.

Read from git directly rather than from the poller's last answer. The poller is up to an interval
behind, and the whole question is what happened in the last few seconds.

**What this gives up.** A person who commits their own work in the seconds a hook is running has that
revision claimed as well, and the hook does not fire for it. One missed firing, in a window as long as
one hook, weighed against a loop that stops only when somebody quits Canopy. No rule separates the
two cases without asking the hook to declare itself, and a hook that has to be trusted to declare
itself is the thing being guarded against.

## D-40 What 0.1 does not include. Decided 2026-07-28.

Six features were built partially or not at all, and leaving them in an ambiguous state costs more
than cutting them. A half-built feature with no decision against it reads to a reader of the ledger
as work in progress, and to a user of the release as something that should be there and is broken.

Each is either finished or explicitly out. There is no third option, and the ones below are out.

| Task | State | Out of 0.1 because |
|---|---|---|
| A4-07 web **search** | not built | Needs a search provider and an account, which is Q-11 and unanswered. `fetch_url` is built, registered and ships. |
| A4-09 plan first mode | engine built, unreachable | `Loop.Plan` and `Loop.Execute` work and are tested, and nothing calls them. Reaching them needs a screen and a profile setting, and the screen is the other pair's side of the boundary. |
| A4-10 todo tracking | tool built, no pane | An agent can keep a list and nothing shows it live. The visible half went to M-03 and the rest is not worth a screen of its own for 0.1. |
| A8-01 sub agents | not built | Needs its own depth and fan-out limits and its own cost attribution. Getting those wrong turns one confirmation into an unbounded fan out. |
| A8-02 handoff and escalation | not built | Depends on A8-01. |
| A8-09 shareable skills | not built | A distribution format is a compatibility promise, and making one before there are users to make it to is the wrong order. |

**The reason for cutting rather than finishing** is the same in every case: none of them is what makes
Canopy worth using. The argument is that several agents work in parallel and are ranked on evidence
rather than on confidence, and that argument is carried by A6 and A7, which are built. Adding a sixth
half-feature does not strengthen it and does delay the point where somebody can try the first one.

**What "deferred" means here**, and it is not "abandoned": the code that exists stays, the tests that
exist keep running, and each task keeps its acceptance criteria for whoever picks it up. What changes
is that nothing is waiting on them and LIMITATIONS says plainly that they are not there.

## D-41 Confined is an explicit mode, not an invisible ceiling. Decided 2026-07-28.

The mode name shown in the interface, the prompt sent to the model and the trust level enforced on
each tool call must describe the same capability. A hidden clamp is not enough: showing `build`
while silently enforcing confined trust makes allowed structured edits look broken and invites the
model to keep requesting a shell it can never receive.

**Confined is therefore the fifth posture in the `shift+tab` cycle**, between plan and build. It may
read and edit through structured tools in the assigned workspace and may use ordinary path-scoped
Git tools. It cannot invoke shell. Network calls retain the existing approval requirement. Its
prompt states those limits and also states that this is capability confinement, not an
operating-system sandbox.

This explicitly supersedes M-09's four-mode cycle and its statement that all four cycle modes could
be represented without adding a confined posture. It does not change D-33: direct and isolated
workspaces remain different contracts, structured path tools remain rooted at the assigned
workspace, and shell remains opaque and uncontained wherever a higher trust level enables it.

The configured trust level remains an absolute ceiling. A mode may lower the effective level and
may never raise it. Bare read-only, confined and standard configurations resolve to plan, confined
and build respectively. Broad resolves to cruise only where the undo safety net exists; otherwise
it falls back to build, just as an explicit cruise selection does. The displayed default and the
enforced default therefore agree without bypassing a mode's prerequisites.

## D-42 Every token saving is visible, and elision is compaction's sibling. Decided 2026-07-28.

Extends D-28 from compaction to every mechanism that changes what the model is sent. Phase E
implements this; the decision is recorded here so the rules survive the tasks.

1. Anything that shortens or rewrites the outgoing request, compaction, elision, ranged reads,
   instruction bounds, announces itself where the user is looking and never touches stored
   history. D-28 said this about compaction; it now covers the whole family.
2. Only deterministic reads may be elided: read_file, glob and grep results superseded by the
   same tool with the same arguments, or invalidated by a later edit to the same path. Shell and
   MCP output is never elided, because nothing can re-derive it, and shortening evidence that
   cannot be regenerated is destroying it.
3. A prefix rewrite forfeits the provider cache, so rewrites are batched to the moments the
   prefix is changing anyway, or to when the pricing table shows the saving beats the rewrite
   premium. The arithmetic is computed from recorded rates and shown, never assumed. A saving
   that costs more than it saves is a spend wearing the wrong name.
4. Cache health is on screen. Caching is the one saving that degrades silently, so its absence
   must be as visible as its presence, in the product and not only in headless output.
5. Usage a provider billed is recorded even when the turn failed. A failed turn that vanishes
   from the totals is a bill the totals are lying about.

None of this blocks 0.1. Phase E starts after PG-M, per the release instruction of 2026-07-27.

## D-43 Navigation never answers, attention is ambient, reflexes never spend. Decided 2026-07-28.

Three interface rules, recorded together because each one exists to protect the person running
several agents at once, and each was being violated by one concrete screen when written.

1. **Navigation never answers a question.** Scrolling, paging and moving between screens decide
   nothing. A pending prompt is answered only by an explicit answer; leaving the screen leaves it
   pending. The default for an unrecognised key on a prompt remains refusal, which is Q-09's
   settled reflex-safety property, but the navigation set is carved out of it: reading before
   deciding must be possible with the keyboard, or the safe default punishes exactly the person
   it exists for.
2. **Attention is ambient.** An agent needing a person is visible from every screen, and no
   screen is ever locked against leaving. The product's premise is parallel agents; an indicator
   that lives only on the agents screen is a smoke alarm installed inside the fire.
3. **No reflex spends money.** No single unconfirmed keystroke starts a paid model call.
   Confirmations for spending name what will be spent, on which key, within what bound.

Phase U implements these. A future screen that violates one of them is wrong even if every test
passes, in the same way a false green is wrong.

## D-44 Declared and unreachable is a defect, not a feature in waiting. Decided 2026-07-28.

The 2026-07-28 audits found the pattern the change log had already counted four instances of, at
larger scale: mechanisms that are built, tested, documented in doc comments as though live, and
called by nothing. The full inventory at the time of writing: the fallback chain, budgets and
their interface, `AgentProfile.SystemPrompt` with `MaxTokens` and `Temperature`, `Loop.MaxTokens`,
`config.Instructions`, `Engine.ClearSteering`, `Engine.RemoveAgent`, `Grants.Granted` and
`Grants.Revoke`, `permission.PathScope`, `permission.GrantableKinds`, `agent/plan.go`, and
auto-compaction, whose deciding decision shipped while its trigger did not.

The rule going forward has three parts:

1. A ledger status may not outrun reachability. A task whose mechanism cannot be driven by a
   person or by another live mechanism is at most partial, whatever its tests say. Four blocks
   were set back under this rule today.
2. Every item in the inventory is either wired by a named task (E-02, E-07, E-09, U-07, U-08,
   U-12, U-13) or cut by a decision that says what existed, the way D-40 cuts. No third state.
3. Review asks "what calls this?" of new exported affordances, and a doc comment describing an
   interface that does not exist is treated as the doc comment lying.

This does not supersede D-40: deferred features keep their code and their tests. It closes the
gap D-40 left, where code that was not deferred and not finished sat in between, reading as done.

## D-45 A mode is chosen where the key stops, not where it passes. Decided 2026-07-29.

`shift+tab` cycles a ladder of five, so most journeys along it cross modes nobody wanted. Applied on
the keystroke itself, walking from cruise back to build put the conversation into plan and then into
confined on the way, each for a fraction of a second. Every one of those is a real trust level, and
D-41's own rule is that a mode takes hold at the next tool call rather than at the next message, so
an agent already running commands could have a call refused by a mode that existed for one frame.
The refusal is indistinguishable from a deliberate one, and the agent then spends a turn arguing
with a boundary that has already gone.

The rule: **passing through a mode is not selecting it.** The key moves a selection. The selection
becomes the mode a short wait after the last press, and the wait is restarted by every press, so
walking the whole ladder applies exactly one mode, the one it stopped on.

Three properties keep the delay from becoming its own defect:

1. **The wait is never the last word.** Sending a message, naming a mode with `/mode`, leaving the
   conversation and quitting all apply the selection at once. Anybody who presses the key and then
   acts has stopped cycling. Dropping the selection in those cases would be worse than having no
   delay at all: the key would have named a mode, said what it does, and then not done it.
2. **The screen never claims the selection is enforced.** While it settles the box says both, in the
   order they happen, `cruise → plan`. The mode in effect keeps the colour. A screen that showed the
   selection alone would put "plan" over a conversation the permission layer was still letting run
   commands, which is the failure D-41 and the mode indicator exist to prevent.
3. **What the key offers is what the engine would accept.** The ladder skips modes this agent cannot
   enter, and it now asks the engine rather than finding out by attempting the change. Offering a
   mode and refusing it two seconds later is a key that appeared to work.

This does not weaken anything D-41 settled. The ceiling, the refusals for a missing safety net, and
"tightening takes hold on the next tool call" are all unchanged. It only decides which keystroke
counts as the decision.

## D-46 One key, many models, and the list is a convenience. Decided 2026-07-29.

A named credential is the unit of authentication, not of capability. What a key can run is a
list: a catalog for the providers whose lineups we ship knowledge of, plus whatever the user adds
by hand, each entry an id with an optional display name. Today the program holds this knowledge
twice, in the pricing table and the context window table, and exports it from neither, which is
why the keys screen edits a model as free text and dispatch can only match credential names.

Four rules keep the list honest:

1. **The catalog never gates.** Typing a model that is on no list must always work. The day the
   list is wrong is the day it would block the one model the person actually wants, so a miss
   costs a warning at most. This is Q-20's question answered for the catalog case, and it is
   answered on the side of the wire.
2. **Knowledge carries its date.** The catalog says when it was true, the way pricing.AsOf does,
   and a stale one says so rather than pretending. Recorded, never inferred, which is D-32
   extended from prices to lineups.
3. **Resolution forgives spelling and refuses ambiguity.** Case, spacing, hyphens and a missing
   family prefix are forgiven; a bare family name means the newest member the catalog knows;
   anything still ambiguous or unknown is refused with the real choices listed. A guess that
   spawns the wrong model spends real money politely.
4. **core stays out of it.** KeyMetadata keeps its single Model field as the selected default;
   the plural lives in the keys store beside it. The frozen contract does not grow a field for a
   feature that a layer above it can carry.

“What can this provider run” means what Canopy can invoke through the provider transport it ships,
not every model name the provider publishes. In particular, the current OpenAI-compatible client
uses Chat Completions. A model documented as requiring Responses is not offered by that catalog
until Canopy has a Responses transport for it. The free-text rule still accepts unlisted model ids
so a dated list cannot gate a newly compatible model, but accepting an id is not a claim that the
current transport implements an API the model requires.

## D-47 A question reaches you where you are, and only your hand answers it. Decided 2026-07-29.

Extends D-43. A permission prompt raised by any agent in the project may surface on whatever
screen you are on, named after who is asking, because walking to a subagent's screen to discover
it was stuck is exactly the attention failure D-43 names. But a surfaced prompt from another
conversation never owns your keyboard: an explicit focus step stands between seeing it and
answering it, so no keystroke aimed at your own conversation can spend another agent's
permission. Your own conversation's prompt outranks visitors, and a count of who else waits stays
visible either way. D-43's rule that no reflex spends money is unchanged; this is that rule
holding at one more distance.

## Appendix: where the settled scope comes from

The repository has two current authorities:

1. **DECISIONS.md governs settled product and engineering choices.** A later numbered decision must
   name what it supersedes. Code or task prose cannot silently reverse one.
2. **TASKS.md governs implementation state, ownership, acceptance and independent verification.**
   It may expand a decision into executable criteria, but it cannot contradict one.

The private planning documents are sources and history, not a second live ledger:

- Canopy-Pre-Build-Corrections.md supplied the truth, trust and safety foundations. They remain in
  force unless a later numbered repository decision explicitly changes them.
- Canopy-Review-Round2-Confirmed-by-Codex.md refined stale, test and service semantics.
- Canopy-Replan-Agent-Runtime.md deliberately pivoted the product to a credential-holding agent
  runtime.
- SPEC.md v2 and FEATURES.md v2 drove A1 through A9, after which this file and TASKS.md became the
  maintained authorities.
- The old SPEC.md and ROADMAP.md are historical. Read them for provenance, never as current
  instructions.

If a private source and a repository authority disagree, record the discrepancy and follow the
later explicit repository decision. If chronology or intent is unclear, block the task and ask the
supervisors instead of choosing silently.

These documents are kept outside this repository, private to the maintainers.
