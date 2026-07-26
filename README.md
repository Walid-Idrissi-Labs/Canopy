# Canopy

A terminal coding agent built for running several agents at once, isolating them on their own git
branches when they need it, and knowing which of them actually produced working code.

> **Status: pre-alpha, and early.** The shared contract, the verification state machine, the fake
> store and the dashboard exist and are tested. There is no provider connection yet. Development
> is tracked in [TASKS.md](TASKS.md), and the decisions behind it in [DECISIONS.md](DECISIONS.md).

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

Canopy resolves the names and hands each agent the task. Agents work in your repository by
default. When they would collide, or when you want to compare their results, they are isolated
into their own worktree and branch.

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

Agents get status, diff, log, branch, commit and stash as structured tools scoped to their own
worktree. Not `bash("git ...")`.

That matters for a reason that is easy to miss. A shell tool hands the permission model an opaque
string, and an opaque string cannot tell `git status` from `git push --force`. With git as its own
tool, destructive operations are approved separately from ordinary ones, and an agent cannot touch
another agent's worktree or your primary checkout.

## Know which agent was actually right

Every agent carries a verification state bound to the exact code it produced. Not "the tests passed
at some point", but "the tests pass for this revision, right now".

- A result is tied to the precise worktree state it tested: commit plus staged, unstaged and
  untracked content.
- Any later change invalidates it. Green becomes `STALE` within about two seconds of an edit.
- `error` is not `failing`. `stale` is not `failing`. "No tests configured" is never "tests
  passed".
- Missing, stale or contradictory evidence is never shown as green.

Give the same task to three agents and Canopy can rank the results by whose code actually passes,
rather than by which one sounded most confident. Fanning out across agents is not new. Using test
evidence to decide who won appears to be.

## What it will not do

- No cloud, no account, no hosted control plane. Keys never leave your machine.
- No unattended merging. A human stays in the loop on anything destructive.
- **No sandboxing claims.** Canopy runs agent-generated commands under your account. A worktree is
  file isolation, not a security boundary, and pretending otherwise would be the same class of
  error as a false green. There is a permission model. It is not a sandbox.
- Windows is deferred until process group and terminal semantics are designed for it rather than
  approximated.

## Requirements

- Go 1.26 or newer
- git
- macOS or Linux

## Development

```sh
go build ./...
go test ./...
go vet ./...
gofmt -l .
golangci-lint run ./...
```

Work is claimed and verified through [TASKS.md](TASKS.md). Read its first section before starting
anything: tasks are built in order, claims are pushed before work begins, and every task is
independently checked by someone who did not write it.

## License

MIT, see [LICENSE](LICENSE).
