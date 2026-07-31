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

What Canopy does not have: a sandbox, a language server, web search, or agents that spawn agents.
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
- [Reusable prompt commands](#reusable-prompt-commands)
- [Modes, on shift+tab](#modes-on-shifttab)
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
```

No API key, and a Claude, Copilot or ChatGPT subscription instead? Use `canopy keys signin` rather
than `canopy keys add`, and read
[Sign in with a subscription instead of a key](#sign-in-with-a-subscription-instead-of-a-key) first.

Anything that is not Anthropic needs a model named explicitly. There is no default anybody could
guess for somebody else's gateway, and a credential without one cannot answer a single message.
Finishing the credential wizard both stores the key and asks the current conversation to use it.
The screen only says the switch is active after the session accepts it; a conversation mid-answer
keeps its current key and reports the newly stored one as not selected.

The keys screen offers a dated catalog where Canopy knows both the endpoint and a compatible
transport, while still accepting an unlisted model id. OpenAI's offered list is intentionally
limited to models the current Chat Completions adapter can invoke; models that require the
Responses API need a transport Canopy does not yet ship.

Now run `canopy` in a git repository. Press `?` for every key binding.

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
rather than chosen, and [LIMITATIONS.md](LIMITATIONS.md) states it route by route.

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

If another agent needs permission, a compact notice reaches the conversation you are on. The
notice cannot approve anything: `ctrl+g` opens the asking conversation, where the complete
canonical request is shown before `y` or `a` can act.

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

`shift+tab` cycles them, and it works while a turn is running: tightening takes hold on the next
tool call rather than on the next message.

The mode it stops on is the one that takes effect, a couple of seconds after the last press. Cycling
past a mode is not choosing it, and applying every rung on the way would put a working agent into
plan for a fraction of a second on its way from cruise to build. The box says both while it settles,
`cruise → plan`, so the mode in effect is never the one being claimed. Sending a message, naming a
mode with `/mode`, leaving the conversation or quitting all apply it at once, and `/mode plan` skips
the wait entirely. The key is not the emergency stop, and never was: `esc` ends the turn now.

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
- **No sandboxing claims.** Canopy runs agent-generated commands under your account. A worktree is
  file isolation, not a security boundary, and pretending otherwise would be the same class of
  error as a false green. There is a permission model. It is not a sandbox.
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
