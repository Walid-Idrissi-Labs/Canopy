# Canopy limitations

Last reviewed: 2026-07-27

Canopy is pre-alpha, version 0.1, and under active development by two people. Keys, providers, chat
and persistence, tools and permissions, multi-agent dispatch and the verification engine are built.
The extensibility layer is not, and several things that are built are not yet reachable from the
interface. This document is not a one-time disclosure. It is kept next to
DECISIONS.md and TASKS.md and updated as the same commits that close a gap here, so a limitation
stays listed until the work that removes it actually lands, not until someone remembers to delete a
line.

Every entry below traces back to a decision or a task in this repository. Where a decision names why
something is a hole rather than an oversight, that reasoning is kept, because the difference
matters: a hole that was chosen can be revisited on purpose, and a hole nobody wrote down can only
be rediscovered by getting burned by it.

## Verification and freshness

- There is no automatic test discovery. Canopy runs the test commands it is configured with and has
  no idea what your project's are until somebody tells it, so on a fresh repository the honest
  answer it gives is "nothing is configured", not a green tick. That is the intended behaviour and
  it is also the thing most likely to read as the feature being broken.

- Ranking refuses more often than it ranks, on purpose. An agent whose worktree changed after its
  tests ran is not placed at all, and with an agent that is actively working that is the normal
  state rather than the exception. Expect to re-run before a fan out can be compared.

- Staleness is coarse by design: one revision key for the whole
  worktree, not per file and not per test. Editing a README marks every test in that worktree stale,
  the same as editing the code the tests exercise. A per-test-path relevance model was considered
  and rejected, because an exclude list would let a changed source path stop counting and produce
  exactly the false green this project exists to refuse (D-16).

- The revision key has three built-in blind spots. Git-ignored files, most commonly a `.env`, never
  affect it, so a config change can alter what the tests actually do without ever marking a result
  stale. Submodules contribute only their checked-out commit; content changes inside one are not
  detected. Symlinks are hashed by their target string rather than followed, so what the link points
  to is not inspected (D-09).

- An untracked file over 25 MB (`untracked_file_hash_limit_mb`) forces the revision to unknown
  rather than being hashed or quietly skipped. That is the honest choice, since skipping it would
  let a large fixture change without invalidating a green result, but it also means a repository
  with a sizeable generated or fixture file routinely reports unknown instead of a trustworthy state
  (D-09).

- A command that times out resolves to error, not failing. That is because a hang is evidence the
  command did not finish, not evidence the tests failed, but the default itself is still flagged as
  unconfirmed rather than settled (D-18).

- Whether tests ever re-run automatically when a file changes is still an open question. The current
  plan is manual triggering only: a green result still goes stale by itself the moment the revision
  changes, but nothing re-runs the command for you until you ask (D-19).

- Canopy does not check whether your dev server, database, queue, or any other external process is
  actually running. Observing services was designed early as read-only (D-06), but the whole feature
  area, health checks, port allocation, restart policy, was deferred past phase A9 when the project
  became an agent runtime instead of a worktree monitor (D-22). Right now nothing in Canopy reports
  service health at all.

- Once test logs exist, they will be kept in a bounded ring buffer per source, 5000 lines by
  default. Past that limit the buffer keeps the head and the tail and states how many lines were
  dropped in between. A failure buried in the exact middle of a very long run will not be shown by
  default, only flagged as missing (D-08).

## Agents and isolation

- Natural language dispatch, the exact feature the README demonstrates ("use 2 claude sonnet agents
  for the auth refactor"), is not built yet (A5-08). Today an agent is created one at a time from
  the interface; describing a task and having Canopy resolve names, counts, and worktrees from it
  does not happen yet.

- Cost preview and budget guardrails before spawning agents are not built yet (A5-09). Nothing
  currently estimates a cost range before an agent starts, and nothing pauses an agent automatically
  at a spending cap.

- Steering, queuing guidance that arrives at the next turn boundary without stopping the current
  one, is not built yet (A5-07). The only mechanism that exists today is interrupt: stopping a turn
  outright and keeping the partial output. Injecting a mid-course correction right now means
  throwing away whatever the agent was in the middle of working out.

- Nothing enforces worktree confinement on the git tools yet. They run git with the workspace as the
  working directory, which is correct, but nothing stops a path argument from naming something git
  tracks elsewhere through a submodule, and nothing stops an agent started in the primary checkout
  from committing to the primary checkout (Q-12).

- There is no sandboxing anywhere in this design and there will not be one implied. Agent-run
  commands execute under your own account with your own permissions. A worktree gives an agent its
  own files; it is not a security boundary, and claiming otherwise would be the same kind of error
  as a false green (README, "What it will not do").

- A freshly prepared worktree gets no isolated database, queue, cache, or OAuth callback. A named
  port is templated in, but a port does not isolate the service listening behind it. Only small,
  git-ignored files, in practice a `.env`, can be copied across through an explicit allow list;
  anything larger is expected to be rebuilt by a setup command rather than copied (A5-04).

- Copying a large directory into a new worktree, such as an installed dependency tree, is a plain
  byte-for-byte copy, not a reflink or copy-on-write clone, even on filesystems like APFS or Btrfs
  that could do it nearly free. The size is measured and shown before you confirm, but each isolated
  agent still pays that disk and time cost again (Q-14).

- There is no way yet to choose an agent's trust level from the interface. A new agent inherits the
  credential and model of whichever agent you were looking at when you created it; choosing a
  different trust posture per agent waits on a profile picker that does not exist yet (A5-06).

- Sub agents, one agent spawning helper agents for a subtask, and agent handoff with model
  escalation, handing a worktree and a summary from a cheap model to a stronger one, are both
  unbuilt (A8-01, A8-02).

## Providers and cost

- Cost is only ever computed for endpoints where the endpoint itself determines the price: Anthropic
  direct, and local runtimes that are genuinely free. Any OpenAI-compatible gateway, OpenRouter,
  NVIDIA NIM, Together, and the rest, reports "cost unknown" and names itself, until you set a rate
  on that key yourself with `canopy keys rate` (D-32, A2-09).

- A rate of zero is refused when setting a custom price, even for a tier that genuinely bills
  nothing at personal volumes, such as NVIDIA's free NIM tier. There is no explicit "this is really
  free" flag yet, so an endpoint like that stays priced unknown rather than free (Q-01).

- The name recorded as a turn's provider is inconsistent: the credential's own name for OpenAI-
  compatible keys, the vendor name for Anthropic. Both show up under the same "Provider" heading
  wherever a turn is attributed (Q-02).

- Reasoning tokens are billed and reported as ordinary output tokens, with no separate field for
  them. A short visible reply from a reasoning model can carry a bill considerably larger than the
  text on screen explains, and there is currently no way to tell the two apart in the usage record
  (Q-03).

- Setting a temperature on a profile has no effect against current Anthropic models. Sampling
  parameters, temperature, top_p, and top_k, are all rejected by those models with a 400, so the
  provider layer strips them before the request is sent rather than passing them through (D-31).

- Provider fallback chains, routing to a backup key on overload or a rate limit, are implemented and
  tested, but were not yet wired into a profile's own configuration as of the task that built them;
  a wrong API key is never one of the errors that falls through, since a bad key should be fixed
  rather than quietly routed around on someone else's account (A2-08).

- The OpenAI-compatible client is hand rolled against a small common surface rather than built on
  any one vendor's SDK, because it has to speak to many different gateways that each shape the same
  API slightly differently. A two-minute stall watchdog exists because at least one gateway has been
  observed accepting a request and then sending nothing back at all; without it, that turn would sit
  for the length of the underlying HTTP client's own timeout, which is thirty minutes (A2-06).

- Tests that talk to a real provider are gated behind a manually supplied key and never run in CI.
  They found two real cancellation bugs on their first run that no scripted test caught, which means
  the same class of bug can reappear between one person's manual run and the next (Q-05).

## Tools and permissions

- Web search is not built. `fetch_url` works, so an agent can read a page it already knows the
  address of, which covers checking a library version or similar, but it cannot discover a page it
  has never heard of, because no search provider or account has been chosen yet (A4-07).

- Plan-first mode's approval mechanism is built and tested end to end, but there is no profile
  setting to turn it on and no screen for reviewing a plan before approving it. Nothing calls it
  yet, so it cannot currently be reached from the running program (A4-09).

- A visible, per-agent task list, the thing that makes a long agent run and several agents at once
  followable rather than four scrolling walls of text, is not built yet (A4-10).

- The shell tool is not confined by construction the way the file tools are. It hands the permission
  model an opaque command string that can do anything your account can do; whatever stops it is the
  trust level, not a boundary the tool itself enforces (A4-03, A4-04).

- The git tools deliberately omit several operations. `commit` takes no `--amend` and no `-a`.
  `branch` can only create a new branch, not switch to an existing one. There is no `reset`,
  `clean`, `stash`, `push`, or `rebase` tool at all yet. Any of these is still reachable through the
  shell tool, where it appears as a plain command rather than a named, separately governed git
  action (A4-06).

- Canopy cannot redact a secret that a child process prints to its own stdout. Redaction only covers
  what Canopy itself formats: the trust screen, service detail, and its own log rendering. Anything
  a spawned command chooses to print is captured into the logs verbatim (D-20).

- On the permission prompt, the key that grants broad, standing approval for the rest of the session
  ('a') sits directly next to the one-time, single-use answer ('y'), with no separate confirmation
  step of its own. Every other key, including enter and escape, refuses (Q-09).

- A field can be set on an agent, stored, displayed, and never actually consulted by the code
  responsible for enforcing it. This already happened once: an agent's configured trust level was
  read from the wrong place and the bug was found by accident rather than by a deliberate check. A
  full sweep for the same shape elsewhere in the permission code has not been completed (Q-15).

- There is no language server integration: no real go-to-definition, no find-references, no compiler
  diagnostics. Agents rely on grep and the structured file tools. This was cut deliberately rather
  than deferred, on the grounds that few terminal agents have it and it is a subsystem-sized
  commitment; it would take a measurable drop in output quality on a large codebase to revisit
  (D-27).

- The whole extensibility layer is unbuilt: no MCP client, so no third-party tools; no hooks or
  automations that fire on a state change; no custom slash commands; no committed project
  configuration file for profiles or permission posture; no shareable skills format (A8-03 through
  A8-09). Everything an agent can do today comes from the eleven built-in tools.

## Storage

- Session history lives in SQLite through a pure Go driver, chosen so `go install` keeps working on
  a machine with no C toolchain. That costs roughly 148 MB in the module cache, even though only
  about 9 MB of it reaches the compiled binary (Q-06).

- History is kept forever. There is no retention policy, no size cap, and no `canopy history prune`
  or equivalent command yet. Every session, turn, and tool call stays in the database until you
  delete it by hand (Q-07).

- Opening a history file with a build older than the one that wrote it is refused outright rather
  than silently downgraded, so an older Canopy binary will not quietly drop fields a newer one
  added. In practice this means rolling back to an older build can leave your history unreadable
  until you upgrade again.

- Compaction summarises older turns once a conversation nears the model's context limit, and the
  model then works from that summary rather than the original text for anything before it. The full,
  original text stays in storage and stays searchable regardless, but the model's working context
  from that point on is a shortened version of what was actually said (D-28).

## Interface

- There is no help or keybinding overlay yet, and only one visual theme exists. Both a second theme
  and a screen listing every binding are still open work (A9-03).

- No single pass covering resize handling, huge output, rapid state changes, and orphaned processes
  on quit has been run as its own gate yet. Individual tasks exercise pieces of this already, but
  nothing has verified all of it together in one sweep (A9-01, A9-02).

- There is no run report yet: no single command that produces a markdown summary of an agent's
  changes, test results, and cost suitable for a pull request body (A8-08).

- Diff review, a commit helper that drafts a message from the diff, and a view showing which files
  several agents have all touched, are not in the interface yet (A7-01, A7-02, A7-03). Reviewing and
  committing an agent's work today means using your own git tooling outside Canopy once the agent
  has finished, even though the agent itself used Canopy's structured git tools to get there.

## Platform

- macOS and Linux only. Windows is deferred until process-group and terminal semantics are actually
  designed and tested for it, rather than approximated and shipped half working (D-03).

- A Windows stub already exists in the process-handling code, and it says plainly that it is
  incomplete rather than pretending to be finished: Windows has no process-group equivalent in
  place, so a cancelled command there can leave children running behind it (A4-03).
