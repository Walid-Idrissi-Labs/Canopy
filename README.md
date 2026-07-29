# Canopy

A terminal coding agent built for running several agents at once, isolating them on their own git
branches when they need it, and knowing which of them actually produced working code.

> **Status: pre-alpha.** Everything on this page is built and tested, except where
> [LIMITATIONS.md](LIMITATIONS.md) says otherwise, and that document is worth reading before this
> one. Most of the extensibility layer is not built; custom prompt commands are the first exception.
> Development is tracked in
> [TASKS.md](TASKS.md), and the decisions behind it in [DECISIONS.md](DECISIONS.md).

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

Anything that is not Anthropic needs a model named explicitly. There is no default anybody could
guess for somebody else's gateway, and a credential without one cannot answer a single message.

Now run `canopy` in a git repository. Press `?` for every key binding.

Homebrew is not available yet and will not be until the first release without a prerelease suffix.
[INSTALL.md](INSTALL.md) has the rest, [RELEASING.md](RELEASING.md) has what publishing involves.

## What it is

Canopy does what a terminal coding agent does. You plug in provider API keys, talk to it, and it
reads and writes code with tools. If that were all, there would be no reason to use it over the
tools that already do it well.

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

Steering and interrupting are deliberately two different things:

- **Steer** queues guidance that arrives at the next turn boundary. The current turn finishes and
  the agent never loses its place.
- **Interrupt** stops the turn now and keeps the partial output, clearly marked as interrupted.

Cancelling a turn to inject a correction throws away the work in progress, and usually the
reasoning with it. Steering is the one you want almost every time, and it is the one most tools do
not have.

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

- No cloud, no account, no hosted control plane. Keys never leave your machine.
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

## License

MIT, see [LICENSE](LICENSE).
