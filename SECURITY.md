# Security

## Reporting a vulnerability

Email **info@lmi-64.ma**. Put "Canopy security" in the subject line so it does not get read as
ordinary mail.

Include the version (`canopy version`), your operating system, and the smallest sequence of steps
that reproduces the problem. If you have a proof of concept, send it. A report that says what you
did and what happened is worth more than one that says what you think the cause is.

Please do not open a public issue for a vulnerability until it has been fixed or until we have
agreed there is nothing to fix.

## What to expect

Canopy is maintained by two people as a side project. There is no security team and no on-call
rotation, so no response time is promised here that could not be kept. In practice you should get
an acknowledgement within a few days. If a week passes with no reply, send the mail again; it was
missed rather than ignored.

When a report is valid, the fix and the release notes will say what was wrong and what versions
were affected. Credit goes to the reporter unless the reporter asks otherwise.

There is no bug bounty.

## The threat model, stated plainly

This is the part worth reading before deciding whether something is a vulnerability.

**Canopy is not a sandbox and does not claim to be.** It runs agent-generated commands under your
own account with your own permissions. A git worktree gives an agent its own files; it is file
isolation, not a security boundary. The same statement is in README under "What it will not do"
and in LIMITATIONS.md, and it is not a caveat we intend to quietly grow out of. There is a
permission model. It decides which tools an agent may call and when it has to ask. It is not an
operating-system containment layer, and "confined" is the name of a trust level whose tool surface
excludes shell, not a claim that an enabled child process is jailed.

So the following are known and documented behaviour rather than vulnerabilities:

- A shell command an agent ran, and you approved, read or wrote files outside its workspace. The
  shell starts in the assigned workspace. That is a starting directory, not a fence.
- An agent used git through the shell to reach a repository other than its own. The structured git
  tools resolve paths inside the assigned workspace; an approved shell string does not.
- A model was persuaded by the contents of a file, a web page fetched with `fetch_url`, or a tool
  result to attempt something you did not intend. Prompt injection is real and Canopy's answer to
  it is the permission model plus a human, not a filter that claims to detect it.
- An MCP server you configured did something you did not expect. Every MCP tool call is treated as
  running a command, whatever the server says about itself, but the server is code you chose to
  run.
- A secret was printed by a child process into its own stdout and ended up in the logs. Canopy can
  only redact what it formats itself (D-20).

These are in scope, and we want to hear about them:

- A permission check that can be bypassed: a tool that runs at a trust level that should have
  denied it, an approval scope that is wider than what was displayed, or a one-time `y` that
  behaves like a standing `a`.
- A path escape in the structured file or git tools, where an isolated agent reaches outside its
  Canopy-owned worktree without the shell.
- Anything that causes Canopy to remove, reset or force-check-out a checkout or worktree it does
  not own.
- Credentials leaking: a secret written to disk unencrypted when the OS keychain backend is in
  use, a secret appearing in an argument list or in shell history, a secret in a crash dump, or a
  secret sent to an endpoint other than the one its credential names.
- A credential being used against the wrong provider or base URL.
- A false green: verification reporting a pass that the evidence does not support. That is a
  correctness bug rather than a memory-safety one, and it is treated with the same seriousness,
  because the whole product rests on it.
- Anything in Canopy's own supply chain: a dependency with a known advisory that reaches the
  shipped binary, or a release artifact that does not match its source.

Out of scope, beyond the behaviour above: findings against provider APIs rather than Canopy,
denial of service against your own machine by an agent you told to run something expensive, and
reports produced solely by a scanner with no reasoning about how the finding is reachable.

## Supported versions

Only the latest release. Canopy is pre-alpha and there are no maintained branches behind it, so a
fix ships in the next tag rather than being backported.
