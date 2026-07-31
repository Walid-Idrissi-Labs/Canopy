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

**A credential is one of three things now, and they are not equally exposed.** Canopy used to hold
only pasted secrets. Since subscription sign-in it holds three shapes, and it is worth knowing which
one you have before deciding what a compromise would cost you:

- **A pasted API key.** Stored in the OS credential store, or in a mode 0600 `credentials.json` when
  `CANOPY_KEY_BACKEND=file` is set, which prints a warning on every command while it is.
- **A token Canopy obtained and holds**, which today means the GitHub Copilot route only. The access
  token and the refresh token go into the same credential store, as one entry under the credential's
  name, marked so that somebody who opens Keychain Access can tell what they found. They are never
  written to `keys.json`, and two separate things hold that. The record that file is made of has no
  field a token could go in, so there is nothing to write; and if a later edit added one,
  `core.Secret.MarshalJSON` emits `[redacted]` rather than the value, so the token still would not
  reach the disk. `core.Secret.UnmarshalJSON` refuses outright, which closes the other direction: a
  secret cannot be loaded out of a file somebody committed.
- **A delegation with nothing behind it**, which is the Claude route and the ChatGPT route. Canopy
  holds no vendor token, and `internal/keys` refuses to store one against such a credential. The
  grant lives with the vendor's own program, in that program's own storage, and Canopy neither reads
  it nor renews it.

In all three cases `keys.json` holds metadata only: the credential's name, provider, endpoint,
model, and for a sign-in the kind, the route it was signed in through and the account it belongs to.
An expiry is there too on the Copilot route, and only there: a delegated credential holds no token
of its own, so `internal/keys` refuses to record an expiry against one rather than storing a date
that would describe nothing. None of those is a secret, which is what lets a list say "signed in as
this account" without unlocking anything, and say "expires then" beside it wherever Canopy is
holding the thing that expires.

**What an attacker who reached the credential store would have.** For a pasted key, that key, usable
anywhere the vendor accepts it until you rotate it. For a Copilot sign-in, an access token scoped to
what Canopy asked for, plus a refresh token where the registration issues one, usable until the
grant is revoked at GitHub. The recommended registration issues an access token that does not expire
at all, so that one is good until somebody revokes it. For a delegated credential there is nothing
in the store to take: what such an attacker would find instead is the vendor's own program already
signed in on the same machine, which is a much better target and not one Canopy put there.

**What signing out removes, per route.** `canopy keys signout` always removes Canopy's record and
whatever Canopy holds in the credential store. It never ends the vendor's grant, on any of the three
routes, and it says so rather than letting you assume otherwise. No route in this build implements
revocation: on the Copilot route revoking needs a client secret a downloadable program cannot keep,
and on the two delegated routes Canopy never held a credential to revoke. So a Copilot signout tells
you the grant was not revoked and to remove Canopy's access where you granted it, and a delegated
signout tells you that nothing was revoked anywhere and that the vendor still considers you signed
in. A command that did only the local half while calling it signing out is how somebody comes to
believe they revoked access they still have. If you want the ChatGPT login itself gone, that is
`codex logout` against your own `$CODEX_HOME`; Canopy will not touch it, because OpenAI rotate
refresh tokens and signing out a login Canopy does not own is a surprise nobody asked for.

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
- On a delegated route, the Claude Code or Codex route, the vendor's agent read files, ran a
  command, or reached the network without Canopy asking you anything, and on the Claude route wrote
  files too. On the ChatGPT route Canopy opens the thread in the app server's read-only sandbox and
  declines every approval it is asked for, so that one does not write; the sandbox is the app
  server's to enforce rather than a gate of Canopy's, and LIMITATIONS.md gives the per-route detail.
  Canopy's permission gate is not in either path. That
  agent has whatever access to your machine you gave it when you set it up, under its own
  configuration and its own permission rules, and none of that is something Canopy sets, sees or
  can bound. A report about what somebody else's
  agent did with the access you granted it belongs to that vendor. A report that Canopy claimed
  otherwise on screen is ours, and is in scope below.

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
  secret sent to an endpoint other than the one its credential names. An access or refresh token
  from a sign-in counts as a secret here in every respect, including reaching `keys.json`, an
  environment variable handed to a child process, or the terminal.
- A credential being used against the wrong provider or base URL, including a subscription
  credential resolving to a route other than the one it was signed in through.
- Canopy claiming a gate it does not have: a screen showing a permission mode as being in force
  during a turn Canopy is not gating, a delegated tool call presented as though Canopy approved it,
  or an audit trail or verification result that covers work Canopy did not run.
- Canopy reading, renewing or revoking a grant that belongs to a vendor's own program rather than to
  Canopy, or signing you out of a tool you did not ask it to touch.
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
