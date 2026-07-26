# Canopy

A terminal coding agent that runs several agents at once, each on its own git worktree, and knows
which of them actually produced working code.

Canopy holds your provider API keys as named credentials, runs agents against them, and verifies
each agent's work against the tests rather than against how confident it sounded.

> Status: pre-alpha, and early. There is a working contract, state machine and dashboard, all
> against scripted data. There is no provider connection yet. Development is tracked in
> [TASKS.md](TASKS.md) and the decisions behind it in [DECISIONS.md](DECISIONS.md).

## The problem

If you keep several Git worktrees active, one per branch or per task or per coding agent, you end
up asking the same question about each one over and over: is this branch actually green right now?

Answering it means changing directory into a worktree, remembering whether the tests you ran an
hour ago were before or after that last edit, checking whether the dev server is still up, then
doing it again for the next worktree.

The expensive failure is not a red test. It is a green result you trust that is no longer true.
The suite passed, then you changed three files, and the memory of "that one passed" quietly
survived the edit.

## What Canopy does

Canopy watches worktrees that already exist and reports evidence about them, with every piece of
evidence bound to the exact code it came from.

**Revision aware.** Every result is tied to the precise worktree state it tested, meaning the
commit plus staged, unstaged and untracked content.

**Freshness aware.** Any later change invalidates the previous result. A green result goes stale
within about two seconds of an edit, without restarting anything.

**Structured.** Tests, service readiness, failure output and timestamps each have explicit states.
An error is not a failure. Stale is not a failure. "No tests configured" is never "tests passed".

**Worktree manager independent.** Canopy monitors worktrees made by git, a script, an editor, or
any agent tool, and in v0.1 it never modifies them.

**Terminal first and local.** No account, no hosted control plane, no desktop app. Works over SSH.

**Truthful by default.** Missing, stale, unparseable or contradictory evidence is never shown as
green.

That last point is the whole design constraint. A verification tool that is occasionally wrong is
worse than no verification tool at all, because you stop checking manually.

## What Canopy does not do

Canopy is a companion to your existing setup, not a replacement for it. In v0.1 it deliberately
does not:

- create, remove, prune or adopt worktrees, or delete branches
- spawn coding agents, attach to their terminals, or infer what they are doing
- commit, push, merge, open pull requests, stage files, or discard changes
- start your services, since it observes services you started (see below)
- run setup commands automatically, copy files between worktrees, or restart crashed processes
- persist state across restarts

There is a deliberate asymmetry worth stating plainly. Canopy executes your test commands itself,
but only observes services you started. Starting and supervising services is a later feature, and
until it exists Canopy will not pretend otherwise.

## Honest positioning

Several tools already run tests and dev servers per worktree. What Canopy is trying to add is
narrower, and as far as we can tell unclaimed: modelling freshness rigorously, so a result is
never presented as current when the code has moved underneath it.

We have not found a tool that models this rigorously. That is a statement about our search rather
than a claim that none exists. This space moves quickly, and we would rather understate the
position than claim to be the only anything.

## Safety model

A configuration file that lives in a repository can run commands as you. A worktree is file
isolation, not a security sandbox, and Canopy never claims to sandbox anything it runs.

- No command from a newly discovered repository runs until you have seen it and approved it.
- Approvals are stored outside the repository, keyed by repository identity and configuration
  hash. Changing an executable field invalidates the approval.
- Commands are argument arrays by default. Shell strings need an explicit `allow_shell: true` and
  are marked as higher risk.
- Environment values marked secret are never printed in output Canopy formats. Output your own
  processes print is captured verbatim, and Canopy cannot redact that.
- Logs stay on your machine, bounded, and are never uploaded.

## Requirements

- Go 1.26 or newer
- git
- macOS or Linux. Windows is deferred until its process and terminal semantics are properly
  designed for rather than approximated.

## Development

```sh
go build ./...
go test ./...
go vet ./...
gofmt -l .
```

Work is claimed and verified through [TASKS.md](TASKS.md). Read its first section before starting
anything: tasks are built in order, claims are pushed before work begins, and every task is
independently checked by someone who did not write it.

## License

MIT, see [LICENSE](LICENSE).
