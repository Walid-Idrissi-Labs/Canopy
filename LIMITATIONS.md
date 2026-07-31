# Canopy limitations

Last reviewed: 2026-07-31

Canopy is pre-alpha, version 0.1, and under active development by two people. Keys, providers,
subscription sign-in, chat and persistence, tools and permissions, multi-agent dispatch and the
verification engine are built.
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
  with, and there is no way to move a running agent to a different one. A conversation also refuses
  a credential change while its answer is in flight, so usage and attribution cannot name a key
  different from the one that actually paid for the reply. A key added during that interval remains
  stored but is reported as not selected.

- A credential keeps one selected default model, but may remember several catalog or user-added
  models and switch between them without storing the secret again. The shipped catalogs are dated
  conveniences rather than complete provider inventories; free text accepts a model that is absent
  from the list, but it cannot add a provider API that Canopy's adapter does not implement.

- Sub agents, one agent spawning helper agents for a subtask, and agent handoff with model
  escalation, handing a worktree and a summary from a cheap model to a stronger one, are both
  **cut from 0.1** rather than merely unbuilt (A8-01, A8-02, D-40). Sub agents need their own depth
  and fan-out limits and their own cost attribution before they can exist safely, since inheriting
  dispatch by accident turns one confirmation into an unbounded fan out. Handoff depends on them.

- Plan first mode is **cut from 0.1** (A4-09, D-40). The engine is built and tested and nothing
  reaches it: turning it on needs a profile setting and a screen that shows a plan and takes an
  approval, and neither exists. The five modes on `shift+tab` are a different feature and they do
  work.

- An agent keeps a todo list and there is no pane showing it live (A4-10, D-40). The list appears in
  what the agent says it is doing rather than in a panel of its own.

## Providers and cost

- Cost is only ever computed for endpoints where the endpoint itself determines the price: Anthropic
  direct, and local runtimes that are genuinely free. Any OpenAI-compatible gateway, OpenRouter,
  NVIDIA NIM, Together, and the rest, reports "cost unknown" and names itself, until you set a rate
  on that key yourself with `canopy keys rate` (D-32, A2-09). A subscription credential is the one
  exception to the second half of that: a rate set on one changes nothing, for the reason
  "Subscription sign-in" gives below.

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
  It currently speaks Chat Completions only, not the Responses API. Responses-only OpenAI models
  are therefore omitted from the picker and cannot be used successfully even if their ids are
  entered by hand.

- Tests that talk to a real provider are gated behind a manually supplied key and never run in CI.
  They found two real cancellation bugs on their first run that no scripted test caught, which means
  the same class of bug can reappear between one person's manual run and the next (Q-05).

## Subscription sign-in

Canopy can run turns on a Claude Max or Pro plan, a GitHub Copilot seat, or a ChatGPT Plus, Pro,
Team or Business plan, with no API key anywhere. Exactly three routes are permitted, each for a
stated reason rather than by the absence of an objection (D-51). Every claim below about what a
vendor permits carries the date it was true. Three of them changed in the eight weeks before this
was written, and an undated statement about somebody else's terms is the confident wrong answer this
project exists to refuse.

**Canopy does not implement claude.ai OAuth and will not, however often it is asked.** As of
2026-07-30 Anthropic's legal and compliance page states that they do not permit third-party
developers to offer Claude.ai login or to route requests through Free, Pro or Max plan credentials
on behalf of their users, and that they reserve the right to enforce that without prior notice. It
is enforced on their servers as well as written down. Other tools have shipped it anyway; that is
not a precedent, it is a list of people who can be stopped without warning. The Claude route below
is a different thing and the difference is not a technicality: implementing the login means holding
somebody's subscription credential, and that route holds none. A test walks every Go file in this
repository and fails on an Anthropic authorisation endpoint, a code challenge or a client secret, so
the next person to propose it finds the answer from the test suite rather than from Anthropic
(S-04).

**There is no Gemini route.** Google's consumer sign-in was prohibited by their terms before
2026-06-18 and switched off on that date. Recorded so it is not proposed again.

**What in this section rests on somebody else's word rather than on this repository.** The rest of
the page describes code you can read. These do not, and are marked once here rather than hedged in
every sentence. Which plan tiers actually work: Canopy asks no vendor whether a plan is Max or Pro,
Plus or Team, so the tiers named above are what these vendors say their own sign-ins accept and not
something Canopy checks or could check. What each vendor's terms permit: no terms page was
re-fetched for this document, so every date below is the date D-51 or the task that built the route
recorded, carried forward. That GitHub requires a client secret to renew or revoke a grant, and that
an OAuth app's user tokens do not expire: GitHub's own documented behaviour for their endpoints,
relied on rather than confirmed against a live registration. The Copilot scopes, which have their
own paragraph below and are evidence rather than fact. Anything here could have changed after
2026-07-30 without this file noticing.

### Canopy's permission gate is in the path on one of the three routes

This is the most important thing on this page for anybody signing in with a subscription, because it
is the opposite of what the rest of Canopy teaches you to expect. Which answer a route gives is set
by that vendor's protocol rather than chosen, and it is not the same answer for all three.

**On the Copilot route Canopy runs the tools and the gate applies in full.** The session is created
in the SDK's `empty` mode, which starts a session with no built-in tools, and the allowlist it is
given is `custom:*`. That is a source pattern rather than a list of tool names: it admits the source
Canopy's own tools are registered under, and it names no built-in source and no MCP source, so
`bash`, `powershell`, `edit`, `grep`, `web_fetch` and the rest are never offered to the model. The
one other thing `custom:*` admits, by the SDK's own definition of the pattern, is a tool belonging
to a custom agent. Canopy configures none, `empty` mode restricts custom agents to locally defined
ones, and the session's working and config directories are a Canopy-owned directory rather than
your project, so there is no local definition for one to be loaded from. The allowlist is not what
keeps a custom agent's tool out; the absence of a custom agent is.

Canopy's tools are declared to the vendor with no implementation behind them, so a call comes back
out to Canopy, through the agent's trust level and, where the level requires it, past a person, and
only then goes back down as a result. GitHub's agent decides what it wants done and has no way to do
any of it itself. The permission mode on screen is the one in force, and A4's audit trail and A6's
verification apply, because Canopy ran every tool call there was (S-03).

**On the Claude and ChatGPT routes the vendor's agent runs the turn and Canopy's permission gate
does not apply.** Concretely:

- **Canopy's own tools are not available, and cannot be.** ACP v1 gives a client no channel for its
  own tools except MCP servers, and Canopy's tools are not an MCP server. The Codex app server has
  no field for them at all, which is read off the protocol schema that binary generates for itself
  rather than off a published document. The tools in the room are the vendor's own, plus whatever
  MCP servers that vendor's own configuration starts: `$CODEX_HOME/config.toml` on the ChatGPT
  route, Claude Code's own configuration on the Claude route. Canopy chose neither and can see
  neither.
- **A tool call the vendor auto-approves never reaches Canopy.** Not gated, not refused, not seen.
  The permission mode shown on Canopy's screen describes what Canopy would do, and on these two
  routes Canopy is not the one doing it. Every delegated turn opens with a notice saying so before
  the first word of the reply, which is what stops the mode indicator from being a lie, but a notice
  is a statement and not a control.
- **Where the vendor does ask for approval, Canopy declines.** It will not stand in as your approver
  for a call it did not make, cannot describe in its own vocabulary and has no trust level for. Each
  refusal is reported in the conversation. Declining does not make the turn gated: it covers only
  the calls the vendor chose to ask about.
- **A4's audit trail and A6's verification see nothing**, because Canopy ran nothing. Each tool the
  delegated agent says it ran is reported as a notice, which is a record rather than a control.
- **On the ChatGPT route the thread is opened read-only**, which is the honest pairing for a client
  that refuses every approval, so a delegated Codex turn does not write files. Be exact about whose
  bound that is. Canopy asks for the read-only sandbox when it opens the thread and declines every
  approval the app server asks it for; the app server's own sandbox is what then enforces it. It is
  the vendor holding a line Canopy asked for, not Canopy's permission gate under another name. A
  subscription credential buys a visibly weaker agent there, and saying so is the point.

If you want a turn where Canopy gates the tools, use a credential where Canopy runs them: a pasted
API key, or the Copilot route. Whether a route that cannot be governed should ship beside one that
can is Q-23, and it is not settled.

### What is true of all three routes

- **No cost figure is shown on a subscription turn.** The token counts are real and are reported,
  with one caveat that belongs to the Claude route: ACP v1 has no usage field, the bridge sends one
  anyway, and Canopy reads it defensively, so a bridge that stops sending it leaves a turn with no
  token count rather than a wrong one. The dollar value is not shown at all: a monthly plan is
  metered against its own limits rather than billed per
  token, so a list price would be a correct number about an invoice nobody receives, and zero would
  say the turn was free. Unpriced is the honest answer. A rate you set on the credential yourself
  does not override that, and this is the one place in Canopy where your own figure does not win,
  because a per-million-token rate cannot describe a plan billed monthly whoever supplied it.

- **Each route needs a vendor program Canopy does not ship**: Claude Code plus the ACP bridge, the
  Copilot CLI, or the Codex CLI. Every one is discovered on your machine rather than bundled, and
  that is deliberate. Bundling would multiply the size of a release that is one small static binary,
  pin a vendor version your own installation would then be stuck at, and put a proprietary vendor
  binary inside Canopy's release archives, which is a redistribution question nobody has asked. The
  cost is that the version is not Canopy's to control, so each route checks what it found at the
  handshake rather than assuming a shape, and an absent program is reported as a sentence naming
  what to install rather than as an exec error.

- **The delegated agent has whatever access to your machine you already gave it**, which is not
  something Canopy sets, sees or can bound. It runs under your account and under its own
  configuration. Canopy passes the exact agent workspace into the vendor process and protocol
  session, so an isolated agent starts in its owned worktree and not in the primary checkout. That
  is workspace selection, not containment: the vendor's own configuration and tools may still reach
  elsewhere. SECURITY.md says the same thing in threat-model terms.

- **The model is the vendor's to choose unless it offers a say**, and the three routes differ enough
  that one sentence will not cover them. On the Claude and ChatGPT routes the credential stores no
  model and has nowhere to put one, so the sign-in says the vendor chooses rather than showing an
  empty list; both ask for the model a request names where the vendor offers that exact value, and
  where something else answers, that is said on screen rather than substituted silently. The Copilot
  route does take a model, fixed when the session opens, and it is the one route where naming a
  different model mid-conversation is neither honoured nor reported. The Copilot section below says
  why.

- **Where Canopy holds a token, renewal is per process.** Two Canopy processes on one machine can
  renew the same credential at the same time. The cost is one wasted renewal, and on a vendor that
  rotates refresh tokens the loser renews again on its next turn. The fix would be a lock file, and
  a lock file held across a network call is how a crashed process leaves a credential unusable until
  somebody deletes a file they have never heard of (S-02). This reaches only the Copilot route,
  since it is the one route of the three where Canopy holds a token at all.

- **Canopy never listens on a loopback port, on any route.** The Copilot sign-in is a device flow
  and needs no callback at all. The ChatGPT sign-in does need one, and OpenAI's own app server hosts
  it on its own loopback port rather than Canopy opening a listener of its own; where no browser is
  reachable that flow becomes a code you type somewhere else instead. The Claude route signs nobody
  in, so the question does not arise there.

### The Claude route, and what it gives up

Canopy does not sign you in to Claude on this route. It discovers a Claude Code you installed and
signed in to yourself, asks it which account that is, and drives it over the Agent Client Protocol.
The credential it stores holds no token and there is nowhere in it to put one, which is the whole
reason the route is permitted at all: Anthropic contemplate and meter this category in writing,
where they prohibit the other one (D-51, S-04).

- **It needs two programs, not one.** Claude Code, signed in, and the ACP bridge, which is a
  separate package (`npm install -g @agentclientprotocol/claude-agent-acp`). Claude Code does not
  speak ACP by itself. A machine missing either is told which one and how to get it.

- **The system prompt does not replace Claude Code's own.** A Canopy agent profile's system prompt
  is sent as the first thing the delegated agent reads. ACP has no field that could do more.

- **Turns draw on your plan's usage limits, as of 2026-07-30.** Anthropic state that Claude Agent
  SDK, `claude -p` and third-party app usage draw from the subscription's usage limits. They
  announced, and then paused on 2026-06-15, a change that would move that usage onto separately
  purchased credits. Paused is not cancelled (Q-22). If it returns, usage that looked included
  becomes a separate purchase, this paragraph is wrong, and the route's cost surface needs rewriting
  before the release that follows. Nothing in Canopy will notice that happening.

- **A Claude Code signed in to a Console account is not a subscription**, and Canopy says so rather
  than describing it as one. Delegated turns on such an installation really are billed per token, to
  that account.

### The Copilot route, and where its conversation lives

This is the one route of the three that the vendor documents for exactly this case: you register an
app, the user authorises it, and their token is handed to GitHub's official SDK so that requests are
made on their behalf against their own subscription (D-51, S-03). It is also the one route where
Canopy holds a token: it runs GitHub's device flow, stores the result in the OS credential store, or
in the mode 0600 file the `CANOPY_KEY_BACKEND=file` escape hatch writes, and identifies itself as its
own app rather than reusing another editor's client id or version headers.

- **Editing history, re-rolling a turn and compacting do not reach the vendor's copy.** GitHub's SDK
  owns the conversation: a session accumulates its own history and there is no call that seeds one,
  so Canopy holds one session per conversation and sends only the newest message. When its history
  and the vendor's have diverged, Canopy refuses the turn by name rather than answering from a
  conversation you can no longer see. Start a new conversation to change what has been said.

- **A conversation picked up after a restart is seeded, not resumed.** The earlier turns go into the
  next prompt as a labelled transcript. That is weaker than having had them and it is the only
  surface the SDK offers.

- **It also happens without a restart, to the conversation you have left alone the longest.** Each
  held session is a resident `copilot` process, so Canopy keeps at most eight of them and closes the
  least recently used one when a ninth conversation opens. Nothing is lost from Canopy's own
  transcript and nothing is announced; the next turn on that conversation re-seeds a new session
  from it, by the same labelled transcript as after a restart and with the same loss. Eight is one
  more than the largest ordinary arrangement, a conversation that has dispatched a full fleet of six
  agents and is still being talked to, so nothing anybody does deliberately reaches it. A
  conversation with a turn still running is never the one closed; the bound gives way instead.

- **The model and the reasoning effort belong to the session**, set when the conversation starts.
  Naming a different model mid-conversation does not restart it, because restarting would throw the
  conversation away to honour a flag, and this route says nothing on screen when that happens: it is
  the one route of the three that reports no notices at all, so the turn simply runs on the model the
  session was opened with. **`MaxTokens` is not sent** either; the SDK exposes no per-turn output cap
  for a Copilot session.

- **Canopy needs a GitHub app of its own.** Release builds require the public repository variable
  `CANOPY_GITHUB_CLIENT_ID` and compile it in; the workflow refuses to publish without it. For a
  local build, register an **OAuth app** with the device flow enabled and set that variable to its client id,
  which is not a secret. An OAuth app rather than a GitHub app for one specific reason: its user
  tokens do not expire, so Canopy never has to renew one, and renewing needs a client secret that a
  program you can download cannot keep. A GitHub app with expiring user tokens works if you supply
  `CANOPY_GITHUB_CLIENT_SECRET` as well, or accept signing in again by hand when a grant lapses.

- **The scopes are not documented by GitHub and Canopy's list is evidence rather than fact.** Canopy
  requests `copilot` and `read:user`. As of 2026-07-30 there is no published GitHub scope table
  entry containing the word Copilot, GitHub's own Copilot SDK setup page names no scope at all, and
  the SDK's Go source validates nothing about the token it is handed. `copilot` is what every
  third-party Copilot client sends; `read:user` is documented and is the smallest scope that lets a
  credential say whose subscription it is. Whether both are needed has not been confirmed against a
  live seat. `CANOPY_GITHUB_SCOPES` overrides the list so the question can be settled by experiment.

- **A seat is not checked at sign-in.** GitHub publish no endpoint Canopy can ask, and the SDK's own
  `account.getQuota` is defined in its schema and not implemented in the CLI as of v1.0.8, which
  GitHub's own end-to-end test skips over. An account with no Copilot seat is told exactly that on
  its first turn, rather than shown an authentication failure that would send somebody to replace a
  credential that is fine.

- **Canopy cannot revoke the grant for you.** `canopy keys signout` deletes the tokens and the
  record. Revoking Canopy's access at GitHub needs a client secret, for the same reason renewing
  does, so the command says plainly that it did the local half only and where to do the other.

### The ChatGPT route, and why it needs the Codex CLI

Canopy drives `codex app-server`, which OpenAI publish under Apache-2.0 as the interface for host
applications wanting a deep integration, authentication included (D-51, S-05). Canopy asks it to
sign you in and it does the rest: it builds the authorisation URL, hosts the callback on its own
loopback port, talks to OpenAI, and keeps the grant in `$CODEX_HOME` afterwards.

- **Canopy never holds a ChatGPT credential**, and `internal/keys` refuses to put a token behind
  this credential at all. That also means **Canopy renews nothing on this route**: the grant belongs
  to the app server, Canopy never asks it to refresh one, and the five-minute refresh margin that
  applies to a pasted or Copilot credential has nothing to act on here. Keeping that grant alive is
  the app server's business rather than Canopy's, and Canopy has no way to make it happen. If the
  grant lapses beyond renewal, `canopy keys test` says so and signing in again fixes it.

- **This route may draw on a smaller allowance than the ChatGPT app does.** There is an open,
  unanswered report of third-party OAuth sign-ins hitting 429 quota errors on active Plus plans,
  which if it is real means this route is quota-segregated from what the same person sees in the
  ChatGPT client. That is said before you sign in, on both surfaces, rather than only here.
  `canopy keys test` reads the plan's actual limits from OpenAI, so it will say when one has been
  hit.

- **Canopy identifies itself as `canopy` and never as another client.** The name a client gives at
  the handshake becomes the originator the app server sends to OpenAI, and it lands in OpenAI's
  compliance logs. Canopy does not send `codex_cli_rs` or any first-party client's name, and reads
  its own name back off the handshake rather than trusting that it took. As of 2026-07-30 OpenAI's
  app-server documentation asks integrations intended for enterprise use to contact them to be added
  to a known-clients list. Canopy is not on one, and being added is a conversation somebody has to
  have rather than something the code can do.

- **The `codex` binary has to be on the machine.** `npm install -g @openai/codex` or `brew install
  codex`; `CANOPY_CODEX` overrides where it is found and is checked rather than trusted.

- **There is no fallback that runs turns without it.** If `$CODEX_HOME/auth.json` exists and the
  binary does not, Canopy names the account that login belongs to and says the program rather than
  the sign-in is what is missing. It does not lift the tokens out of that file and call
  `chatgpt.com/backend-api/codex` itself, and that is a decision rather than an omission. D-51
  permits this route through the app server, and permits the Claude route explicitly because Canopy
  holds none of your subscription credential; reading those tokens out and using them is the thing
  it does not permit. It would also break what it was rescuing, because OpenAI rotate refresh tokens
  and whichever process redeems one last wins: a Canopy that renewed a login it does not own would
  sign you out of your own Codex to keep a copy working.

- **Signing out does not sign your Codex out.** `canopy keys signout` removes Canopy's record. The
  ChatGPT login stays in `$CODEX_HOME`, where your own `codex` uses it too. Run `codex logout` if
  you want it gone from the machine.

- **Threads are opened ephemeral**, so a turn does not leave a second copy of your conversation in
  `$CODEX_HOME/sessions`. Canopy's own transcript is the one that persists and it is handed over in
  full on every turn, so unlike the Copilot route, editing history, re-rolling and compaction all
  work normally.

- **Reasonable people read OpenAI's terms the other way.** Charm's Crush deliberately refused to add
  a ChatGPT subscription provider, twice closing working implementations, with their maintainers
  citing those terms. This route exists anyway on a narrower ground than "OpenAI seem fine with it":
  OpenAI publish the app server under Apache-2.0 and document it as the interface for exactly this
  case, their documentation asks integrations to identify themselves through `clientInfo.name`, and
  the one behaviour their terms plausibly reach is impersonating another client, which this build
  refuses and holds a repository-wide test against. If that reading is wrong, the honest consequence
  is that the route goes, not that it gets quieter.

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
  ('a') sits directly next to the one-time answers, with no separate confirmation step of its own.
  `enter` and `y` cover only the current call; `a` remembers the displayed scope for the session.
  Enter approving is a deliberate reversal of the old reflex-safety default, in which enter refused
  (Q-09, superseded by D-50): a misread prompt now costs whatever the one displayed call does,
  rather than a retry. Every other key, including escape, still refuses. A compact notice about
  another agent's prompt, and a waiting pane on the agents screen, accept a once-only enter or
  backspace while nothing is being typed; both may summarise the request they answer for. The
  remembered approval can still only be given on the owning conversation's full canonical prompt.
  A compact request that stops waiting before the key arrives is reported as gone; the surface does
  not claim that the stale approval or refusal succeeded.

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

- A CLI credential rename spans the credential backend, its metadata file and SQLite; those systems
  cannot commit one shared transaction. Canopy compensates by restoring the old credential name if
  the history update fails, and it reports the exact split state if that restoration also fails.
  Backend cleanup failures can still leave an extra secret or signed-in grant under the old or
  proposed name; the error names that account so it can be deleted or revoked. A delegated
  credential holds no backend value, so only its metadata and conversation references move. A
  Canopy process already running cannot be updated by the CLI and must be restarted after a
  successful rename.

- Compaction summarises older turns, and the model then works from that summary rather than the
  original text for anything before it. The full, original text stays in storage and stays
  searchable regardless, but the model's working context from that point on is a shortened version
  of what was actually said (D-28). Today compaction only happens when you ask for it, with ctrl+r
  or /compact, and both ask before they spend: the first press offers and names what goes, on which
  key and within what bound, and the second goes ahead. The meter warns near the limit, but nothing
  compacts by itself, and a conversation that outgrows the window fails its next turn until you
  compact by hand. The automatic half of D-28 is planned as E-02 and does not exist yet.

## Interface

- The opening conversation deliberately does not reserve space for a large central wordmark. Canopy
  or the named agent stays in the header from the first frame, and the message box is centred on
  itself. On a short terminal the header falls back from the three-line wordmark to written text;
  when there is not enough room for the complete campfire scene, the scene is omitted rather than
  clipped into the message box.

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

- The engine half of the robustness sweep has been run and the interface half is half done. Timeouts,
  bounded output, event delivery under load, paths with spaces, externally removed worktrees and
  orphaned processes on quit are covered by tests of their own (A9-01). Resize is now covered by a
  test that applies seven sizes in sequence to one model, across the threshold where the header
  changes shape, which is what every earlier size test failed to do: they rebuilt the application per
  size, so nothing was ever carried across a change. Readability at 80 columns and every state being
  distinguishable without colour are covered per screen. Selection is held by identity rather than by
  row index, and there are tests for a row moving under it and for its subject disappearing entirely,
  so the mechanism that keeps a selection honest is proven. What is still not covered is quitting
  with several agents live, and a burst of updates arriving faster than the screen redraws. That is
  the rest of A9-02.

- A model that streams a very long answer is still rendered in full, and that is now the only
  unbounded rendering path left: finished turns are rendered once and cached, so the cost of a long
  conversation no longer grows with every frame, but the cost of one enormous reply is still paid
  while it arrives.

- Bounding output is done where a command produces it, which is where the memory cost is, and not
  where a reply does. A model that streams a very long answer grows the turn in the snapshot without
  limit and the interface renders all of it, so a runaway reply is still a way to make the screen
  slow. Command output, tool results and diffs are capped at 32 KB with the middle marked as dropped
  (A9-01).

- Homebrew is not wired up. The cask is configured but skipped for prerelease tags, and the tap
  repository does not exist yet, so `brew install` is not available until the first non-prerelease
  tag. Binaries are on the releases page and `go install` works.

- `go install ...@latest` gives you the newest tag, not the default branch. Go prefers a release
  version for `@latest` and falls back to the highest prerelease when a module has no release
  version at all, which is this one, so `@latest` currently resolves to `v0.1.0-alpha.3`. It is the
  same code as the archive on the releases page for that tag, built without the version stamping,
  so it reports itself as `dev`. What it does not contain is anything merged since the tag. The
  behaviour changes the day a non-prerelease tag exists: `@latest` will pin to that and stop
  following prereleases, and getting a newer alpha will mean naming it, `@v0.1.1-alpha.1`.

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

- **`canopy keys add` fails outright on a machine with no Secret Service.** Secrets go to the OS
  credential store, which on Linux means a D-Bus Secret Service such as gnome-keyring or
  KWallet. A container, a CI runner, a bare server over SSH and most WSL setups have none, and
  what you get there is the keyring library's own error wrapped in "storing in the OS keychain",
  on the first command anybody runs. There is no fallback, and the message does not mention the
  escape hatch, which is `CANOPY_KEY_BACKEND=file`. That writes secrets to a mode 0600 JSON file
  next to the metadata instead, and every command run afterwards prints a warning saying so. The
  missing hint is the actual defect here: not falling back automatically is deliberate, since
  silently downgrading to plaintext on disk because the keychain was awkward is the kind of
  shortcut that stays invisible until it is a headline, but an error that does not name the
  option leaves a first-time user with nothing to try. `INSTALL.md` documents it.

- A Windows stub already exists in the process-handling code, and it says plainly that it is
  incomplete rather than pretending to be finished: Windows has no process-group equivalent in
  place, so a cancelled command there can leave children running behind it (A4-03).
