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

- Natural language dispatch caps a single request at six agents, and the whole run at eight running
  at once. Asking for more is refused rather than trimmed. The counts are blast radius limits rather
  than measured capacity, so they may be wrong for your machine in either direction.

- A spawned agent cannot spawn agents of its own. That is deliberate for 0.1: nested dispatch is
  A8-01 and needs its own limits, and inheriting it by accident would let one confirmation multiply
  into an unbounded fan out.

- The cost estimate shown before spawning is crude and says so. It is a median cost per turn from
  turns that actually ran in this session, multiplied by a range of four to twenty five turns per
  agent. Below three priced turns it shows no number at all. It does not know anything about the
  task you are about to give, so a one line fix and a rewrite get the same range.

- Spending caps count only what Canopy can price. A request on a profile with no known rate is
  recorded as uncosted rather than as free, and the status line says the total is a floor, but the
  cap itself cannot stop spending it cannot see.

- Steering delivers guidance at the next turn boundary, which is not the same as immediately. An
  agent that is thirty seconds into a long tool call gets your correction when that turn ends, not
  when you type it. Interrupt is the mechanism for "stop now" and it discards nothing except the
  rest of the turn.

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
  credential and model currently in use; choosing a different trust posture per agent waits on a
  profile picker that does not exist yet (A5-06).

- Choosing a credential applies to the conversation you are in, not to agents that already exist.
  Agents created afterwards inherit it; ones already running keep the credential they were started
  with, and there is no way to move a running agent to a different one.

- A credential can only talk to one model. Using the same key against two models means storing it
  twice under two names, which is what naming keys is for, but it does mean pasting the secret in
  again.

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

- Canopy asks the terminal for mouse events, so the wheel scrolls the conversation. The cost is that
  dragging to select text no longer reaches the terminal, so copying out of Canopy means holding a
  modifier while you drag: option on macOS terminals, shift on most others. Without this the wheel
  arrives as arrow key presses, and the arrow keys walk back through what you have sent, so
  scrolling up to reread an answer would replace what you were typing.

- No single pass covering resize handling, huge output, rapid state changes, and orphaned processes
  on quit has been run as its own gate yet. Individual tasks exercise pieces of this already, but
  nothing has verified all of it together in one sweep (A9-01, A9-02).

- Homebrew is not wired up. The cask is configured but skipped for prerelease tags, and the tap
  repository does not exist yet, so `brew install` is not available until the first non-prerelease
  tag. Binaries are on the releases page and `go install` works.

- `go install ...@latest` builds from the default branch rather than from the latest tag, because Go
  ignores prerelease versions for `@latest` and every tag so far is one. The binary from `go install`
  and the binary from the releases page are therefore not the same code.

- The worktree monitor screen still reads from a fake project rather than from your repository. The
  real verification state lives on the review screen. So do the `snapshot`, `watch` and `demo`
  subcommands.

- There is no run report yet: no single command that produces a markdown summary of an agent's
  changes, test results, and cost suitable for a pull request body (A8-08).

- The commit helper stages everything in the worktree. There is no way to commit a subset, because
  partial staging needs a way to select hunks and that is its own screen. A half version that
  silently staged whole files would be worse, since the difference only shows up in what was
  committed.

- The conflict radar reports overlap, not conflict. It says which files more than one agent has
  changed; it does not run a merge, so two agents editing different functions in one file appear as
  an overlap even though git will merge them without complaint. It is a statement about right now,
  not a prediction about the merge.

- The commit message drafter never writes the subject line. It derives the conventional commit type
  and scope from the files and leaves the sentence to you. An edit that adds no new file is drafted
  as a chore rather than a fix, because a diff cannot tell a feature from a bug fix.

- The project configuration file is JSON, so it cannot carry comments. That is a real loss for a
  file people edit by hand, and the alternative was a dependency on a YAML or TOML parser for a file
  with about eight fields in it.

- The task list an agent keeps as it works is not shown in its own pane yet. The list exists, the
  agent maintains it through a tool, and it appears in the transcript, but the dedicated display is
  not wired (A4-10).

## Platform

- macOS and Linux only. Windows is deferred until process-group and terminal semantics are actually
  designed and tested for it, rather than approximated and shipped half working (D-03).

- A Windows stub already exists in the process-handling code, and it says plainly that it is
  incomplete rather than pretending to be finished: Windows has no process-group equivalent in
  place, so a cancelled command there can leave children running behind it (A4-03).
