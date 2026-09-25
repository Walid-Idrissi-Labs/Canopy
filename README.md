# Canopy

[![CI](https://github.com/Walid-Idrissi-Labs/Canopy/actions/workflows/ci.yml/badge.svg)](https://github.com/Walid-Idrissi-Labs/Canopy/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/Walid-Idrissi-Labs/Canopy?include_prereleases&sort=semver)](https://github.com/Walid-Idrissi-Labs/Canopy/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/Walid-Idrissi-Labs/Canopy)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A terminal coding agent built for running several agents at once, isolating them on their own git
branches when they need it, and knowing which of them actually produced working code.

> **Status: beta.** Everything on this page is built and tested, except where
> [LIMITATIONS.md](LIMITATIONS.md) says otherwise, and that document is worth reading before this
> one. Most of the extensibility layer is not built; custom prompt commands are the first exception.
> Development is tracked in
> [TASKS.md](TASKS.md), and the decisions behind it in [DECISIONS.md](DECISIONS.md).
>
> Beta means the interface has been used, the engine has been tested, and the phase gates have not
> been signed by the second pair who are meant to sign them. Nothing here is API stable, the version
> says so, and the honest summary is that this is worth your time and not yet worth your trust with
> anything you cannot review afterwards.

## Why this and not Claude Code or aider

For a single agent editing a single checkout, use whichever of those you already like. Canopy is
not trying to win that comparison. It exists for the moment one agent stops being enough, and five
things follow from taking that seriously:

- **Credentials have names.** A key is stored under a name and carries its own provider, endpoint
  and model, so "a kimi agent" is a resolvable thing rather than a way of talking. An environment
  variable holds a secret and nothing else: no name, no model, and no second one.
- **You dispatch in a sentence.** "Use 2 claude agents for the auth refactor and a kimi agent to
  write the tests" spawns them, after a confirmation, on their own git worktrees and branches when
  they would otherwise collide.
- **Steering does not interrupt.** Guidance queues and lands at the next turn boundary, so
  correcting an agent does not throw away the turn it is halfway through. Interrupt still exists,
  as a separate key, for when you actually mean stop.
- **Git is a set of tools, not a string.** Status, diff, log, add, commit and branch are structured
  and rooted at the agent's workspace. A permission model handed `bash("git ...")` cannot reliably
  tell `git status` from `git push --force`; one handed a typed call can.
- **Verification decides who won.** Every agent's result is bound to the exact worktree state it
  tested, and three agents on one task are ranked by whose code passes rather than by which one
  sounded most confident. Fanning out is not new. Using test evidence to settle it appears to be.

What Canopy does not have yet: agents that spawn agents. In a trusted repository a language server
found on PATH (gopls, typescript-language-server, pyright, rust-analyzer, clangd) checks every file
an agent edits, the errors coming back with the edit, and answers `find_definition` and
`find_references`. Web search is
Anthropic's own server-side search, offered on Anthropic keys when `CANOPY_WEB_SEARCH=on`; other
providers have `fetch_url` only.
Those are stated plainly rather than deferred quietly, and
[LIMITATIONS.md](LIMITATIONS.md) is the honest list.

## Contents

- [Install](#install)
- [What it is](#what-it-is)
- [Named keys, so agents have names](#named-keys-so-agents-have-names)
- [Sign in with a subscription instead of a key](#sign-in-with-a-subscription-instead-of-a-key)
- [Dispatch agents from the conversation](#dispatch-agents-from-the-conversation)
- [Watch them, and steer without stopping them](#watch-them-and-steer-without-stopping-them)
- [Git as a real tool, not a shell string](#git-as-a-real-tool-not-a-shell-string)
- [Know which agent was actually right](#know-which-agent-was-actually-right)
- [Where the tokens go](#where-the-tokens-go)
- [Reusable prompt commands](#reusable-prompt-commands)
- [Modes, on shift+tab](#modes-on-shifttab)
- [Themes](#themes)
- [A report for the pull request](#a-report-for-the-pull-request)
- [What it will not do](#what-it-will-not-do)
- [Requirements](#requirements)
- [Development](#development)
- [Contributing and security](#contributing-and-security)
- [License](#license)

## Install

```sh
go install github.com/Walid-Idrissi-Labs/Canopy/cmd/canopy@latest
```

Or take a binary from the [releases page](https://github.com/Walid-Idrissi-Labs/Canopy/releases).
macOS and Linux, on both Intel and ARM. Windows is not supported, see below.

Then give it a key. A credential is stored by name and carries its own endpoint and model, which is
what lets you talk about agents by name later:

```sh
canopy keys add claude                       # anthropic, model picked for you
canopy keys add nim -provider openai-compatible \
  -base-url https://integrate.api.nvidia.com/v1 -model minimaxai/minimax-m2.7
canopy keys list                             # the MODEL column says NOT SET where one is missing
canopy keys rename nim minimax               # the value is not asked for again
```

Already have `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` set for another tool? `canopy keys import`
shows what it found, by fingerprint and never by value, and stores them as named keys once you say
yes.

No API key, and a Claude, Copilot or ChatGPT subscription instead? Use `canopy keys signin` rather
than `canopy keys add`, and read
[Sign in with a subscription instead of a key](#sign-in-with-a-subscription-instead-of-a-key) first.

A name is the one thing here you are likely to get wrong, because you choose it before the
credential has been used for anything. Renaming moves the credential and every conversation
recorded on it, since the name is what each one looks up on its next message. In the interface it
is `e` on the credential screen. The CLI reports success only after both the credential and stored
history have moved; if history cannot be updated it restores the old name before asking you to
retry. Restart any Canopy process that was already running, because another process cannot update
the conversations it holds in memory.

Anything that is not Anthropic needs a model named explicitly. There is no default anybody could
guess for somebody else's gateway, and a credential without one cannot answer a single message.
Finishing the credential wizard both stores the key and asks the current conversation to use it.
The screen only says the switch is active after the session accepts it; a conversation mid-answer
keeps its current key and reports the newly stored one as not selected.

The keys screen offers a dated catalog where Canopy knows both the endpoint and a compatible
transport, while still accepting an unlisted model id. OpenAI's offered list is intentionally
limited to models the current Chat Completions adapter can invoke; models that require the
Responses API need a transport Canopy does not yet ship.

In a project with no canopy.json, `canopy init` writes one with the tests its go.mod, Cargo.toml,
package.json or pytest setup suggests, for you to read and `canopy trust`. `canopy doctor` checks
git, the key store, the sandbox, the project's configuration, language servers, the programs the
subscription routes need, and the terminal, and says what to do about each that is missing.

Now run `canopy` in a git repository. Press `?` for every key binding.

Canopy opens directly into a conversation ready for input: the message box is centred, identity
stays in the header corner, and the campfire animates at bottom-right when the terminal has room.
There is no separate centre-logo splash that disappears after the first message.

Homebrew is not available yet and will not be until the first release without a prerelease suffix.
[INSTALL.md](INSTALL.md) has the rest, [RELEASING.md](RELEASING.md) has what publishing involves.

## What it is

Canopy does what a terminal coding agent does. You give it a credential, which is either a provider
API key you paste or a subscription you sign in to, then talk to it, and it reads and writes code
with tools. If that were all, there would be no reason to use it over the tools that already do it
well.

The reason to use it is what happens when one agent is not enough.

## Named keys, so agents have names

Credentials are stored by name, not as an ambient environment variable.

```sh
canopy keys add claude   --provider anthropic
canopy keys add kimi     --provider openai-compatible --base-url ...
canopy keys add minimax  --provider openai-compatible --base-url ...
```

Once a key has a name, so does an agent, and you can talk about agents the way you already think
about them.

## Sign in with a subscription instead of a key

If you pay for a model by the month and have never opened a billing account, there is nothing to
paste, and `canopy keys signin <name>` is the way in. Three routes are permitted, each for a reason
recorded with the date it was true (D-51, 2026-07-30). Only the Copilot route is one the vendor
unambiguously invites; the other two rest on narrower arguments, and LIMITATIONS sets out the
counter-position on the ChatGPT one rather than leaving you to find it:

- **GitHub Copilot**, `-route copilot`. Canopy runs GitHub's device flow, holds the resulting token
  in your keychain, and puts turns through GitHub's official Copilot SDK against your seat.
- **Claude**, `-route claude-code`. Canopy holds no Anthropic credential at all and never sees one.
  It drives the Claude Code you installed and signed in to yourself. Anthropic do not permit
  third-party tools to offer Claude.ai login, so Canopy does not implement it and will not.
- **ChatGPT**, `-route codex`. OpenAI's own `codex app-server` runs the sign-in, hosts the callback
  and keeps the grant afterwards, so Canopy holds no token here either. That is not what makes the
  route permitted, and it is worth not confusing the two: it rests on OpenAI publishing that app
  server under Apache-2.0 as the interface for exactly this kind of integration, and on Canopy
  identifying itself honestly to it. This is the contested one. `-route codex-device` prints a code
  to type on another device, for a machine you only reach over ssh.

There is no Gemini route. Google's consumer sign-in was prohibited by terms and then switched off on
2026-06-18.

One thing is worth knowing before you choose, because it is the opposite of what the rest of this
page describes. **On the Copilot route Canopy's own tools and permission prompts stay in the path.
On the Claude and ChatGPT routes they do not.** Those two are delegated: the vendor's agent runs the
turn under the vendor's own permission rules, its auto-approved tool calls never reach Canopy, and
so Canopy gates nothing and verifies nothing while it happens. That is set by each vendor's protocol
rather than chosen. Canopy does still start the vendor session in the exact workspace assigned to
the agent, so an isolated agent starts in its owned worktree rather than silently falling back to
the primary checkout. A starting directory is not a sandbox; [LIMITATIONS.md](LIMITATIONS.md) states
that boundary route by route.

Subscription turns report their token counts and no dollar figure, because a monthly plan is not
billed per token and a list price would be a correct number about an invoice nobody receives.
[INSTALL.md](INSTALL.md) has what each route needs on the machine.

## Dispatch agents from the conversation

```
> use 2 claude sonnet agents for the auth refactor, and a kimi agent to write the tests
```

Canopy resolves the names and hands each agent the task. Name no model at all, "use 3 agents for
this", and the new agents run on the profile your conversation is already using. Agents work in
your repository by default. When they would collide, or when you want to compare their results,
they are isolated into their own worktree and branch.

It confirms the plan before spawning anything, because spawning agents spends real money against
real keys, and a misread number should be a question rather than an invoice.

## Watch them, and steer without stopping them

Split panes show several agents working at once. Move between them by keyboard or by clicking.
Replies render as terminal-width documents: tables fit their columns where possible and become
labelled rows when the screen is too narrow to preserve every column horizontally.

The transcript shows bounded tool output and file diffs. Control characters from commands or file
content are printed as visible escapes before terminal styling, so viewed output cannot act as a
second terminal program.

If another agent needs permission, a compact notice reaches the conversation you are on, and with
your message box empty you can answer it right there: `enter` approves that one call, `backspace`
declines it. The same two keys answer for the selected pane on the agents screen. An inline answer
is always a single yes or no, never a standing approval, because the notice may summarise the
request; `a`, the answer that is remembered for the session, only works on the asking
conversation's own prompt, one `ctrl+g` away, where the complete canonical request is shown.
If the request stops waiting between being shown and the keypress, Canopy says so instead of
claiming that it was approved or declined.

Steering and interrupting are deliberately two different things:

- **Steer** queues guidance that arrives at the next turn boundary. The current turn finishes and
  the agent never loses its place.
- **Interrupt** stops the turn now and keeps the partial output, clearly marked as interrupted.

Cancelling a turn to inject a correction throws away the work in progress, and usually the
reasoning with it. Steering is the one you want almost every time, and it is the one most tools do
not have.

An agent that has stopped and cannot start again without you is counted in the header of every
screen, not only on the one that lists agents, and no screen is ever locked because a question is
waiting: leaving a conversation is not answering it, and the question is still there when you come
back. Scrolling a permission prompt to read what is above it does not answer it either. Set
`CANOPY_BELL=1` to have the terminal beep the moment an agent starts needing you, which is off
unless you ask for it.

## Git as a real tool, not a shell string

Agents get status, diff, log, add, commit and branch as structured tools rooted at their workspace.
Canopy has two workspace modes:

- A **direct agent** works in the repository where Canopy was started. That may be your primary
  checkout. The creation flow identifies the mode and exact workspace, warns about the
  primary-checkout risk, and requires a separate `y` confirmation before the agent exists.
- An **isolated agent** gets a Canopy-owned worktree. Fan-out and concurrent editing use this mode,
  and never silently fall back to a shared checkout.

That matters for a reason that is easy to miss. A shell tool hands the permission model an opaque
string, and an opaque string cannot reliably distinguish `git status` from `git push --force`.
Structured tools can classify reads and mutations separately, resolve path arguments inside the
assigned workspace and omit destructive operations entirely.

The shell is different. Read-only and confined agents do not get it. Standard agents ask before an
exact command and broad agents run it without asking, but in both cases it is a process running with
your account permissions. Its starting directory is the workspace; that is not a containment
boundary.

Canopy's worktree manager never removes or takes ownership of your primary checkout or a worktree it
did not create. That lifecycle guarantee is separate from choosing direct mode, where an agent is
intentionally allowed to edit the checkout you selected.

## Know which agent was actually right

Every agent carries a verification state bound to the exact code it produced. Not "the tests passed
at some point", but "the tests pass for this revision, right now".

- A result is tied to the precise worktree state it tested: commit plus staged, unstaged and
  untracked content.
- Any later change invalidates it. Green becomes `STALE` within about two seconds of an edit.
- `error` is not `failing`. `stale` is not `failing`. "No tests configured" is never "tests
  passed".
- Missing, stale or contradictory evidence is never shown as green.

Give the same task to three agents and Canopy ranks the results by whose code actually passes,
rather than by which one sounded most confident. Fanning out across agents is not new. Using test
evidence to decide who won appears to be.

The part that makes it honest is the refusal. An agent whose worktree changed after its tests ran is
not placed fourth, it is not placed at all, and the ranking says why. That is more often than you
might expect, because the branch that looked best is usually the one still being worked on.

Canopy runs the test commands you configure in `canopy.json` and has no idea what your project's tests are
until you tell it. On a repository it has never seen, the honest answer is "nothing is configured",
not a green tick.

```json
{
  "tests": [
    { "name": "unit", "command": { "argv": ["go", "test", "./..."] }, "required": true },
    { "name": "lint", "command": { "shell": "eslint . | tee lint.log", "allow_shell": true } }
  ]
}
```

The argument form is the default and the shell form has to be asked for, because a shell always
starts successfully. Run `go test ./...` through one and you get exit 127, which looks exactly like a
failing test suite, and you go looking for the bug in your code. Run it as an argument list and
Canopy says the program does not exist, which is what actually happened. Reach for `shell` when you
need a pipe or a redirect, and know that you are giving that distinction up.

The review screen also compares model cost with verified outcomes from this repository's own
history. It records a sample only when the evidence describes the current revision, excludes
unknown provider costs, names the sample size, and refuses a conclusion until at least two models
have three exact samples each. The result is an association in local history, not a claim that the
model caused the outcome.

On the ranking, `o` asks a model for a second look at the attempts: whether any of them passes its
tests without doing the work (a test edited, an input special-cased, a failure silenced), and which
it would choose among those that pass. It is shown under the ranking as an opinion, never in the
ranking's place, and it costs one request.

## Project instructions

Canopy sends a project's standing instructions with every request: your own
`instructions.md` in the Canopy config directory, then the repository's `AGENTS.md`, `CLAUDE.md`,
`.canopy/instructions.md` and the `instructions` field of canopy.json, in that order, later ones
winning where they conflict. They sit in the system prompt, which never changes during a
conversation, so they are cached rather than paid for again on every step. Oversized instructions
(over 48 KB together) are refused by name rather than cut. Like the rest of a repository's
configuration, they are only sent once the repository is trusted.

## Skills

Canopy reads Agent Skills: folders holding a `SKILL.md` whose frontmatter has a `name` and a
`description`. Only the name and description go into the system prompt; the model loads a skill's
full instructions, and any file beside it, with the `skill` tool when a task calls for it, so a
hundred skills cost a hundred lines. Your own skills are read from `skills/` in the Canopy config
directory, `~/.claude/skills` and `~/.agents/skills`; a repository's from `.canopy/skills`,
`.claude/skills` and `.agents/skills`, once the repository is trusted.

## Agent definitions

An agent can be defined once and dispatched by name: a markdown file with `name`, `description` and
optionally `model` in its frontmatter, and its standing instructions as the body. "Use the reviewer
agent on this branch" starts it with those instructions and that model. Definitions are read from
`agents/` in the Canopy config directory and `~/.claude/agents`, and, once a repository is trusted,
from its `.canopy/agents` and `.claude/agents`, so definitions written for Claude Code work here.

## A repository has to be trusted before it runs anything

canopy.json can name a setup command, test commands, hooks and MCP servers, and carry instructions
for the model. None of that runs, and nothing of it reaches the model, until you have seen exactly
what it asks for and said yes. Opening Canopy in a repository with such a configuration shows the list
and asks once; `canopy trust` reviews it later, `canopy trust revoke` takes it back. The answer
covers that exact configuration: change a hook, add an MCP server or add vendor agent settings such as
`.claude/settings.json`, and Canopy asks again. A repository's `"trust"` field may lower its agents to
read-only or confined; it can never raise them above standard.

An MCP server is a local program started over stdio, or a remote one reached over HTTP:

```json
{"mcp": [
  {"name": "files", "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]},
  {"name": "issues", "url": "https://mcp.example.com/mcp",
   "headers": {"Authorization": "Bearer ${ISSUES_TOKEN}"}}
]}
```

A remote server's url must be https, or http to this machine, and a redirect is never followed. A
header names its token as `${NAME}`, read from the environment Canopy starts in, so the committed
file never holds it; the trust prompt shows which variables go to which url, a server whose variable
is not set is not connected, and a general credential (a model provider's key, `GITHUB_TOKEN`, a
cloud's) is never sent to a server a repository names.

A hook runs on something that happened: `tests-passed`, `tests-failed`, `verified`, `agent-idle` and
`agent-blocked` for the project's state, and `pre-tool`, `post-tool` and `turn-end` around an agent's
work. The last three are given what happened as JSON on stdin. A `pre-tool` hook can refuse a call,
by answering `{"decision": "deny", "reason": "..."}` or exiting 2 with the reason on stderr, and the
model is told why; it can never approve one, since it runs only after the permission layer has said
yes. One that cannot answer, by crashing, timing out or saying something else, refuses the call too.
A `post-tool` hook can answer `{"note": "..."}`, which is added to what the model is told of the
result. `"tools": ["run_command"]` narrows either to some tools. Every hook runs in the sandbox, in
the directory of the agent the call belongs to, which its input names as `workspace`.

```json
{"hooks": [
  {"on": "pre-tool", "tools": ["run_command"], "run": "./scripts/guard.sh", "timeout": "10s"},
  {"on": "post-tool", "tools": ["write_file", "edit_file"], "run": "./scripts/lint-note.sh"},
  {"on": "turn-end", "run": "osascript -e 'display notification \"turn done\"'"}
]}
```

## Landing an agent's work

```sh
canopy land agent/parser-fix        # merge into the branch you are on, only if the result passes
canopy land -pr agent/parser-fix    # or push the agent's branch and open a pull request
```

The merge is made in a scratch worktree, never in your checkout, and the project's tests run on the
merged result, which is what will exist afterwards, since two branches that each pass can fail
together. Your branch moves only if every required test passed and it still points where it did when
the merge was made. A checkout with uncommitted changes is refused, and the agent's branch is kept
either way.

## Headless runs, and escalating on red

```sh
canopy run -p "fix the flaky parser test" -output stream-json
canopy run -p "..." -effort low -verify -escalate 2
```

`canopy run` is the full agent without the interface, for scripts and CI. With `-verify` the
project's own tests decide the exit code (3 when they fail). `-escalate N` retries a red result up to
N times, one effort level higher each time, with the failing output: run cheap, and pay for more
thinking only when the evidence says it was needed. `-budget 0.50` stops the run once it has spent
fifty cents, retries included; in the interface, `/budget 2` caps one agent and `/budget all 10`
caps every agent together. A cap is checked between steps, so the request in flight finishes and the
next one is not made.

### In an editor

```sh
canopy acp
```

`canopy acp` speaks the Agent Client Protocol on stdin and stdout, so an editor that runs agents
over ACP, Zed among them, can run Canopy in the project it opens. The editor sends prompts and draws
the replies, tool calls and results as they stream; the questions Canopy would ask in the interface
are asked in the editor, and its mode menu offers the Canopy modes the project allows. In Zed, add it under
`agent_servers` in settings:

```json
{ "agent_servers": { "Canopy": { "type": "custom", "command": "canopy", "args": ["acp"] } } }
```

### In the background

```sh
canopy serve &           # keeps this project's agents running
canopy attach new        # start a conversation; ctrl+d leaves it working
canopy attach            # what is running, and what is waiting on you
canopy attach 12         # pick one up: what happened, then what is happening
```

`canopy serve` keeps agents working after the client that started them has gone. It listens on a
socket only you can reach, and speaks the same protocol as `canopy acp`. A question an agent asks
while nobody is attached waits for the next `canopy attach`, and the server says which conversation
is waiting.

### When a turn fails

Under a failed turn, with nothing typed, enter tries it again as a new turn, and the question is
sent to the model once, not twice. A rate limit counts down the wait the provider asked for, a
network failure is called one, and a failure trying again cannot fix, a refused credential or a
conversation too long for the model, says what to change instead and is not retried.

## Where the tokens go

Every request resends the conversation, so what it costs is decided by how much of that is read
from the provider's cache and how much is sent fresh. Canopy keeps the conversation append-only
until it is compacted, when a summary takes the place of the older part: a turn's messages, tool
calls and the model's signed thinking are replayed exactly as they were exchanged, the system prompt and tools never change mid-conversation, and a test holds every request
to beginning, byte for byte, with the one before it. What that buys is visible:

- Under each finished turn, a quiet line gives the model, the time, the tokens read and written,
  the share that came from the cache, and the cost where the price is known.
- `/context` breaks the next request into its parts (system prompt, project instructions, tool
  definitions, summary, messages, replies, replayed thinking, tool calls and results) and says how
  much of the last turn came from the cache, with a plain warning when a later turn got nothing
  from it.
- On Anthropic's current models, once MCP servers bring more than about five thousand tokens of
  tool definitions, those are held back and the model finds the ones it needs through a tool
  search, so a request does not carry forty tools to use one.
- A read of a file already sent and unchanged is answered with a short reference instead of the
  file again, and long tool output is kept aside with its head and tail shown and the rest one
  `read_output` away.
- `canopy bench` runs built-in tasks, each a small project whose check fails until the work is
  done, and reports the pass rate, tokens, cache reads and cost per passing task; `-compare` sets
  one run against another. It calls the model, so it only runs when you type it.

## Reusable prompt commands

Project commands live in `canopy.json`:

```json
{
  "commands": [
    {
      "name": "review",
      "description": "review one subsystem against its tests",
      "prompt": "Review this subsystem carefully and run its relevant tests:\n$ARGUMENTS"
    }
  ]
}
```

Invoke that as `/review authentication`. Type `/commands` to list the definitions active in the
current repository; tab completes a unique command name and lists ambiguous matches. `//text` sends
the literal prompt `/text` instead of treating it as a command.

Global commands use the same `{"commands": [...]}` shape under the platform user config directory:
`~/Library/Application Support/canopy/commands.json` on macOS or
`$XDG_CONFIG_HOME/canopy/commands.json` on Linux. `CANOPY_COMMANDS_FILE` overrides the path. A
project definition with the same name wins only for that project. `$ARGUMENTS` is replaced literally
in one pass; there is no template evaluation or shell interpolation. When the placeholder is
absent, arguments are appended under an `Arguments:` heading.

## Modes, on shift+tab

Five postures, and each one is a trust level the permission layer enforces rather than a paragraph
asking the model to behave. An agent told it is planning and choosing to edit a file anyway is
stopped, which is the only kind of instruction worth relying on.

- **plan** reads and thinks and changes nothing.
- **confined** edits through structured tools in the assigned workspace and cannot use shell.
  Network calls still ask. This is a capability profile, not an operating-system sandbox.
- **build** edits freely and asks before running anything. The ordinary way to work.
- **runway** edits and runs freely, and the turn is put back if the workspace does not verify
  afterwards. Needs a git repository and a configured test, and refuses to engage without them.
- **cruise** runs everything without asking. Needs a git repository, so there is a way back.

One exception holds in every mode: once a conversation has read a fetched page, a web search or an
MCP tool's result, which can carry instructions aimed at the model, anything that could send data
out (a network tool, an MCP tool, a git push, a shell command such as curl, ssh or `base64`) is
asked about from then on. Reading, editing, building and testing are not.

`shift+tab` cycles them, and it works while a turn is running: tightening takes hold on the next
tool call rather than on the next message.

The mode it stops on is the one that takes effect, a couple of seconds after the last press. Cycling
past a mode is not choosing it, and applying every rung on the way would put a working agent into
plan for a fraction of a second on its way from cruise to build. The box says both while it settles,
`cruise → plan`, so the mode in effect is never the one being claimed. Sending a message, naming a
mode with `/mode`, leaving the conversation or quitting all apply it at once, and `/mode plan` skips
the wait entirely. The key is not the emergency stop, and never was: `esc` ends the turn now.

## Themes

`/theme` lists the palettes and `/theme nord` switches to one; `CANOPY_THEME=nord` starts in it.
Canopy's own palette ships with catppuccin, dracula, gruvbox, nord, solarized and tokyonight, each in
a light and a dark form that follow the terminal's background, and `mono`, which is what `NO_COLOR`
gives. Every shipped palette is checked for contrast against the background it was made for (text
4.5:1, outcomes and quiet text 3:1, code 2.5:1, borders just visible), which deepened a few of the
upstream colours, as each theme's own description says. A test fails if a colour is made outside the
theme package in any of the ways it knows to look for, so a theme reaches the whole interface.

A theme of your own is a JSON file in `canopy/themes` under your config directory
(`~/Library/Application Support` on macOS, `~/.config` on Linux, or `CANOPY_THEMES_DIR`), one colour
per role, each either
`"#rrggbb"` or `{"light": "#rrggbb", "dark": "#rrggbb"}`. The shipped ones in
`internal/tui/theme/themes` are complete examples. A file that does not load is named, with the
reason, by a bare `/theme`.

## A report for the pull request

```bash
canopy report
```

Runs this repository's own checks and prints a markdown summary of what changed, whether it
verified, and what it cost. It never claims a verification state the evidence does not support: if
the worktree moves while the suite is running, it says the result went stale rather than reporting
the pass.

## What it will not do

- No cloud of Canopy's, no Canopy account, no hosted control plane. A key you paste never leaves
  your machine. Signing in with a subscription is the one exception and it is the vendor's own: on
  the Copilot route Canopy obtains a token from github.com and hands it to GitHub's own runtime, and
  on the other two the vendor's program holds the grant. See "Sign in with a subscription".
- No unattended merging. A human stays in the loop on anything destructive.
- **No sandboxing claims beyond what the sandbox does.** Shell commands an agent runs are confined
  by the operating system where it can: on macOS they can write only in the workspace, temporary
  directories and toolchain caches, and cannot read `~/.ssh`, `~/.aws`, keychains and the like; on
  Linux, Landlock confines writes the same way but cannot hide files from reading. Network access
  is not restricted by default; `CANOPY_SANDBOX_NETWORK=registries` lets commands reach only
  package registries (and hosts added in `CANOPY_SANDBOX_ALLOW`) through a proxy Canopy runs, and
  `CANOPY_SANDBOX_NETWORK=off` cuts them off entirely. The project's test commands run in the same
  sandbox, and so do hooks; setup and MCP servers are not sandboxed yet, and a worktree on its own
  is file isolation, not a security boundary. `CANOPY_SANDBOX=off` turns it off, and every command that runs
  unconfined says so in its result.
- Windows is deferred until process group and terminal semantics are designed for it rather than
  approximated.

## Requirements

- git
- `/bin/sh`, since shell tools and test commands run through it
- macOS or Linux
- Go 1.26 or newer, only if you are building from source rather than taking a binary
- The vendor's own program for whichever subscription route you use, none of which Canopy bundles:
  Claude Code plus the ACP bridge for the Claude route, which is two programs rather than one, the
  Copilot CLI for the Copilot route, the Codex CLI for the ChatGPT route. [INSTALL.md](INSTALL.md)
  names each one and what installs it.

## Development

```sh
make build
make test    # go test -race -count=1 ./...
make lint
make vet
make fmt
```

Work is claimed and verified through [TASKS.md](TASKS.md). Read its first section before starting
anything: tasks are built in order, claims are pushed before work begins, and every task is
independently checked by someone who did not write it.

## Contributing and security

[CONTRIBUTING.md](CONTRIBUTING.md) is the door if you are not one of the maintainers: what to run
before you push, how branches and commits are named, and what to leave alone. `AGENTS.md` and the
`TASKS.md` claim-and-verify protocol are internal to the two pairs working the ledger, and you do
not need to follow either of them to send a pull request.

For a vulnerability, use the address in [SECURITY.md](SECURITY.md) rather than a public issue. That
file also states the threat model, which matters more here than it does for most tools: Canopy runs
agent-generated commands under your account, so a fair amount of alarming-looking behaviour is
documented rather than broken, and it says which is which.

## License

MIT, see [LICENSE](LICENSE).
