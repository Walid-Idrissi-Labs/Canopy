# Canopy limitations

Last reviewed: 2026-07-28

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

- A test command is an argument array by default, and a shell string only when you write
  `"allow_shell": true` next to it (D-05). The difference is not cosmetic. With an argument array a
  program that is not installed fails to start, which Canopy reports as an error rather than as a
  failing test. Through a shell it exits 127, which is indistinguishable from a suite that failed, so
  opting in costs you that distinction. Canopy will not guess it back for you by reading stderr,
  because that answer is locale and shell dependent, and it will not treat every 126 and 127 as an
  error, because a suite can legitimately exit with one.

- **Test commands written as a plain string no longer load.** `"command": "go test ./..."` becomes
  `"command": {"argv": ["go", "test", "./..."]}`. The error says so and shows both forms. This is a
  breaking change to a pre-release file format, made now because it is the cheapest moment there
  will ever be.

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

- The cost estimate shown before spawning is crude and says so. It finds priced turns from this
  project's persisted history with overlapping significant task words, takes their median, and
  multiplies it by a range of four to twenty five turns per agent. Below three similar priced turns
  it shows no number at all. This lexical match is not semantic similarity: differently worded work
  may be excluded, and two tasks sharing vocabulary may still have very different difficulty.

- Cost versus outcome is observational and local. A sample is one session at one currently verified
  revision, so repeated revisions from one long session are not statistically independent. Unknown
  provider cost and stale or unknown evidence are excluded and named. The view refuses a conclusion
  until two models have at least three exact samples each, but that threshold prevents the smallest
  anecdotes from being presented as findings; it does not turn the result into a controlled study.
  A session that switched models is excluded because its accumulated cost cannot honestly be
  attributed to either model alone.
  Project identity is derived from the cleaned startup path, so moving a repository or launching
  Canopy from a different linked worktree begins a separate history rather than joining it by remote.

- Spending caps count only what Canopy can price. A request on a profile with no known rate is
  recorded as uncosted rather than as free, and the status line says the total is a floor, but the
  cap itself cannot stop spending it cannot see.

- Steering delivers guidance at the next turn boundary, which is not the same as immediately. An
  agent that is thirty seconds into a long tool call gets your correction when that turn ends, not
  when you type it. Interrupt is the mechanism for "stop now" and it discards nothing except the
  rest of the turn.

- Git tools run with the workspace as their working directory, and the path arguments currently
  exposed by `git_add` and scoped `git_diff` are resolved against that workspace before Git runs.
  A direct agent intentionally works in the repository where Canopy was started and can commit
  there when its trust level permits it, including when it is the primary checkout. An isolated
  agent's structured tools are rooted at its Canopy-owned worktree. Neither statement makes the
  shell a containment boundary: an allowed shell command can invoke Git anywhere the user's account
  can reach (D-33).

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
  **cut from 0.1** rather than merely unbuilt (A8-01, A8-02, D-40). Sub agents need their own depth
  and fan-out limits and their own cost attribution before they can exist safely, since inheriting
  dispatch by accident turns one confirmation into an unbounded fan out. Handoff depends on them.

- Plan first mode is **cut from 0.1** (A4-09, D-40). The engine is built and tested and nothing
  reaches it: turning it on needs a profile setting and a screen that shows a plan and takes an
  approval, and neither exists. The four modes on `shift+tab` are a different feature and they do
  work.

- An agent keeps a todo list and there is no pane showing it live (A4-10, D-40). The list appears in
  what the agent says it is doing rather than in a panel of its own.

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

- Web search is **cut from 0.1** (A4-07, D-40). `fetch_url` works and ships, so an agent can read a
  page it already knows the address of, which covers checking a library version or similar. It cannot
  discover a page it has never heard of, because no search provider or account has been chosen and
  that choice is Q-11.

- There are five modes on `shift+tab`, and each is a trust level the permission layer enforces
  rather than an instruction the model is asked to follow (M-09, D-41). Plan reads and thinks;
  confined edits through structured tools in its assigned workspace but cannot use shell; build
  edits and asks before running anything; runway edits and runs freely and reverts a turn that ends
  red; cruise runs everything without asking. Network calls still ask at confined trust. Confined
  is a capability profile, not an operating-system sandbox. Runway and cruise refuse to engage
  where their safety net is missing rather than quietly behaving like the mode below. What is not
  built is the second axis:
  capability and approval are one setting, so "edit freely but review every edit" cannot be
  expressed, and there is no screen for reviewing a plan before approving it (A4-09). The separate
  plan-and-execute approval mechanism in `internal/agent/plan.go` is still called from nowhere and
  is now superseded by the mode's trust level.

- The shell tool is not confined by construction the way the file tools are. It hands the permission
  model an opaque command string that can do anything your account can do; whatever stops it is the
  trust level, not a boundary the tool itself enforces. "Confined" is the trust level that denies
  shell; it is not a claim that an enabled child process is sandboxed (A4-03, A4-04, D-33).

- The git tools deliberately omit several operations. `commit` takes no `--amend` and no `-a`.
  `branch` can only create a new branch, not switch to an existing one. There is no `reset`,
  `clean`, `stash`, `push`, or `rebase` tool at all yet. Any of these is still reachable through the
  shell tool, where it appears as a plain command rather than a named, separately governed git
  action (A4-06).

- Canopy cannot redact a secret that a child process prints to its own stdout. Redaction only covers
  what Canopy itself formats: the trust screen, service detail, and its own log rendering. Anything
  a spawned command chooses to print is captured into the logs verbatim (D-20). The TUI does escape
  terminal controls before displaying tool output or diff content, but that prevents terminal
  injection rather than removing sensitive data from the stored result.

- On the permission prompt, the key that grants broad, standing approval for the rest of the session
  ('a') sits directly next to the one-time, single-use answer ('y'), with no separate confirmation
  step of its own. `y` covers only the current call; `a` remembers the displayed scope for the
  session. Every other key, including enter and escape, refuses (Q-09).

- A field can be set on an agent, stored, displayed, and never actually consulted by the code
  responsible for enforcing it. This already happened with per-agent trust. A deliberate review
  later found branch mutation hidden behind a coarse tool kind, one-time approval being stored for
  the session, confinement refusals misclassified in the audit trail, and denied tools still shown
  to restricted agents. Fixes exist on `feat/permissions-and-confinement`, but Q-15 remains open
  until another reviewer reruns them independently.

- There is no language server integration: no real go-to-definition, no find-references, no compiler
  diagnostics. Agents rely on grep and the structured file tools. This was cut deliberately rather
  than deferred, on the grounds that few terminal agents have it and it is a subsystem-sized
  commitment; it would take a measurable drop in output quality on a large codebase to revisit
  (D-27).

- There is no shareable skills format, and it is **cut from 0.1** rather than pending (A8-09, D-40):
  a distribution format is a compatibility promise, and making one before there is anybody to make it
  to is the wrong order. Custom slash commands are prompts only: they do not register tools, execute
  shell, or provide a general template language.

- Hooks run, and a failing one is only reported when Canopy exits (A8-05). A hook fires on a
  verification state that is current rather than stale, and a command bound to `tests-passed` is
  eligible again at the next revision, which is deliberate: tests passing over different code is a
  different event. It also means a hook that commits moves HEAD, and the pass at that new revision
  is a second event that would fire it again, so a revision that appears between a hook batch firing
  and its last command returning is treated as the batch's own and does not fire it a second time,
  even if the poller verifies that revision before the first command returns (D-39). The
  cost of that rule falls in one place: if you commit your own work during the few seconds a hook is
  running, that revision is claimed too and the hook does not fire for it. It fires again for the
  next thing you do.

- **A hook that fails is only visible when Canopy exits.** The failure is recorded with its command,
  its output and its error, and there is nowhere on screen to show it yet, so a long session can hide
  a broken hook for hours. That is the exact failure automation invites, since the point of it is
  that somebody stops watching. A8-05 stays claimed for this reason alone.

- MCP servers are started when a conversation opens and stopped when it closes, and their tools are
  governed exactly as Canopy's own are: every one of them counts as running a command, whatever the
  server says about itself, so read-only and confined agents get none of them and standard trust sees
  each call before it runs.

- **Isolated agents do not get MCP tools** (D-38). Servers start once, in the project directory,
  rather than once per worktree, so a server is not rooted where an isolated agent works. An isolated
  agent is confined by having its tools rooted at its worktree, and a tool reaching a program started
  elsewhere would be a way around that. The cost is real and lands on the feature that matters most:
  the agents in a fan out have strictly less available to them than the one you are talking to.
  Q-18 carries the per worktree design that would lift it.

- A server's tool list is bounded at 50 pages and 500 tools. Hitting either is reported rather than
  absorbed, on stderr at startup and in the server's own description, because a tool missing because
  of a bound Canopy imposed is otherwise indistinguishable from one the server never offered.

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

- Compaction summarises older turns, and the model then works from that summary rather than the
  original text for anything before it. The full, original text stays in storage and stays
  searchable regardless, but the model's working context from that point on is a shortened version
  of what was actually said (D-28). Today compaction only happens when you ask for it, with ctrl+r
  or /compact; the meter warns near the limit, but nothing compacts by itself, and a conversation
  that outgrows the window fails its next turn until you compact by hand. The automatic half of
  D-28 is planned as E-02 and does not exist yet.

## Interface

- Canopy asks the terminal for mouse events, so the wheel scrolls the conversation. The cost is that
  dragging to select text no longer reaches the terminal, so copying out of Canopy means holding a
  modifier while you drag: option on macOS terminals, shift on most others. Without this the wheel
  arrives as arrow key presses, and the arrow keys walk back through what you have sent, so
  scrolling up to reread an answer would replace what you were typing.

- A process that detaches its own output and outlives the command that started it is left running
  (D-37). Canopy puts every command in its own process group and kills the group, which is what takes
  a test runner's workers with it, and it will not signal a group once that group's leader has been
  waited on, because the number naming the group can be reissued by the kernel at that point and the
  signal would land on somebody else's work. In practice the common case is still covered: waiting on
  a command does not return while a child holds its output open, so an orphaned worker keeps the
  leader unreaped and the group is signalled safely. What escapes is the child that closes or
  redirects the streams it inherited, which is to say a daemon. On Windows nothing beyond the process
  itself is killed at all, because there are no process groups in the POSIX sense there.
  On supported Unix platforms, exit is observed without reaping before the actual reap and group
  signals are serialized; this is what closes the pid-reuse window rather than a flag written after
  `Wait` returns.

- The engine half of the robustness sweep has been run and the interface half has not. Timeouts,
  bounded output, event delivery under load, paths with spaces, externally removed worktrees and
  orphaned processes on quit are covered by tests of their own now (A9-01). Resize
  handling, readability at 80 columns with several agents, every state being distinguishable without
  colour, and rapid updates not moving the selection are not: they are A9-02 and nothing has
  verified them together.

- Bounding output is done where a command produces it, which is where the memory cost is, and not
  where a reply does. A model that streams a very long answer grows the turn in the snapshot without
  limit and the interface renders all of it, so a runaway reply is still a way to make the screen
  slow. Command output, tool results and diffs are capped at 32 KB with the middle marked as dropped
  (A9-01).

- Homebrew is not wired up. The cask is configured but skipped for prerelease tags, and the tap
  repository does not exist yet, so `brew install` is not available until the first non-prerelease
  tag. Binaries are on the releases page and `go install` works.

- `go install ...@latest` builds from the default branch rather than from the latest tag, because Go
  ignores prerelease versions for `@latest` and every tag so far is one. The binary from `go install`
  and the binary from the releases page are therefore not the same code.

- The `snapshot`, `watch` and `demo` subcommands read a fake project rather than your repository.
  They exist to demonstrate the state machine and they say so. The worktree monitor inside the
  running program reads your actual repository, as does the review screen.

- `canopy report` runs this repository's checks and prints a markdown summary for a pull request
  body (A8-08). It describes the directory it is run in and the most recent conversation for that
  project, not a named agent or an arbitrary worktree, and it runs the suite rather than reading a
  result from elsewhere, so it takes as long as the tests do.

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

- A task list longer than six items collapses to a one line summary rather than scrolling in place.
  The whole list is still in the transcript. The pane competes with the conversation for the screen
  and the conversation wins.

## Platform

- macOS and Linux only. Windows is deferred until process-group and terminal semantics are actually
  designed and tested for it, rather than approximated and shipped half working (D-03).

- A Windows stub already exists in the process-handling code, and it says plainly that it is
  incomplete rather than pretending to be finished: Windows has no process-group equivalent in
  place, so a cancelled command there can leave children running behind it (A4-03).
