# Open questions

## What happened overnight, 26 to 27 July

Walid asked for unsupervised work through the night on `feat/agent-runtime`, with everything
committed as it went and the decisions written down rather than explained in the morning. This is
the orientation; the reasoning for each piece is in that task's notes in TASKS.md.

**28 commits.** Phase A3 is complete and phase A4 is complete except for the interface work that
depends on A5. Phase A5's git foundations are done.

What now exists that did not last night:

- **Sessions persist.** SQLite, migrations from the first version, full text search across every
  conversation. `canopy search <words>`.
- **An agent does work.** It reads and writes files, runs commands, uses git, and fetches pages.
  Eleven tools. Proved against a real provider, not only against scripted streams.
- **Permissions decide, and ask.** Per agent trust levels, a prompt in the transcript, an audit
  trail of every call including the refused ones.
- **Nothing is silently lost.** Compaction announces itself and keeps the history. Every turn is
  checkpointed so it can be undone. A cancelled turn keeps its partial and says it is partial.
- **Markdown and syntax highlighting** in replies, written by a Sonnet agent working in parallel.
- **Several agents at once.** Named, each with its own conversation, credential and model. Three
  layouts over them: a list ordered by what needs you, a two way split, and one full width. `ctrl+d`
  from chat, `enter` to open one, `n` to start another.
- **Worktrees.** Discovered, created and removed, with the primary checkout and anything Canopy did
  not make protected at every level of confirmation.

Four things worth a second pair of eyes, in order:

1. **Q-08**, a commit with a misleading message that I chose not to rewrite unsupervised.
2. **Q-09**, whether `a` (approve everything of this shape) is too easy to reach on the prompt.
3. **Q-06**, the 148 MB SQLite dependency, given Walid is storage aware.
4. **Q-01**, whether a rate of zero should be allowed for a genuinely free endpoint.

**Ten real bugs were found by tests rather than by review**, and each is recorded where it happened.
The four most instructive:

- A cancelled turn reported itself as **failed** on both providers. Only a live test could catch it,
  because the ordering only goes wrong when a read is genuinely blocked when the cancel arrives.
- The tool loop **hung for ten minutes** because it read past the done event, which only showed up
  because a scripted stream blocks where a real one returns false.
- A compaction could report having made the conversation **larger**, because the before and after
  figures were measured two different ways.
- The marker file that says "Canopy made this worktree" could not be written at all, because in a
  linked worktree `.git` is a file rather than a directory.

And one found by rendering a screen and looking at it: the cost figure in the chat header vanished
every time somebody sent a message, because an unpriced in flight turn poisoned the total.

**The last live run of the night** had NVIDIA accept a request and then send nothing. That was worth
having: it exposed a real gap, and there is now a stall watchdog. The live suite passes.

---

Things that need a person, kept here rather than guessed at. Each one says what was decided in the
meantime so nothing is blocked waiting for an answer, and what would change if the answer is
different.

Anyone may add to this. Cross an entry out with a line and a date rather than deleting it, so the
reasoning stays findable.

---

## Q-01 The NVIDIA endpoint shows "cost unknown"

**Decided in the meantime:** the pricing table only holds rates where the endpoint determines the
price, which is Anthropic first party and local runtimes. An OpenAI compatible gateway sets its own
prices, so pricing `minimaxai/minimax-m2.7` at MiniMax's published rate when it was reached through
NVIDIA would be a guess wearing the clothes of a fact. See D-32.

**Consequence:** every turn on the NIM key reports "cost unknown" with the endpoint named.

**Now built.** `canopy keys rate nim -in 0.30 -out 1.20` makes turns on that key show a figure,
labelled "priced at your own rate for this key". **Walid needs to set the real number**, because the
one used to test it was invented and has been cleared again rather than left on the key.

**Still open:** the NVIDIA free tier genuinely bills nothing at personal volumes, and a rate of zero
is currently refused on the grounds that it would report every turn as free, which is a claim rather
than an absence. That reasoning is right for a gateway with unknown pricing and possibly wrong for
one that really is free. Options are a `-free` flag that means it deliberately, or leaving it
unpriced. Needs a decision.

---

## Q-02 Provider name in the header versus the key name

A turn records `Provider` as the credential's name for OpenAI compatible keys ("nim") and the vendor
name for Anthropic ("anthropic"). That is inconsistent, and it shows up wherever a turn is
attributed.

**Decided in the meantime:** left as is. The credential name is the more useful of the two for the
compatible family, since the vendor name would be "openai-compatible" for every one of them.

**What would change it:** making the Anthropic client take a name too, so both report the
credential. Small change, but it touches the attribution shown at the A2 gate, so worth agreeing
first.

---

## Q-03 Reasoning tokens are billed as output and not distinguished

MiniMax on NIM returned 73 output tokens for a two word reply, which is reasoning being billed as
output. The usage record has no field for it and the interface shows one output figure.

**Decided in the meantime:** reported as output, because that is what the provider bills.

**What would change it:** a separate `ReasoningTokens` field on `core.Usage`. Worth it if the
difference is large enough to surprise somebody looking at a bill; not worth it if every provider
reports it differently, which they currently do.

---

## Q-04 Only one session exists so far

The engine holds a list of sessions and the chat screen opens `session-1`. Nothing creates a second
one yet, and there is no session switcher.

**Decided in the meantime:** one session, because A5 is where several agents and the views over them
belong, and building a switcher before there is anything to switch between would be guessing at the
shape of that screen.

**What would change it:** nothing, unless a session picker is wanted before A5.

---

## Q-05 Live tests need a stored credential and cost money

`internal/session/live_test.go` talks to a real provider, gated behind `CANOPY_LIVE_KEY`. It found
two real bugs on its first run that no scripted test could have found, both about cancellation.

**Decided in the meantime:** skipped by default, never run in CI, and no secret is in the repository.

**What would change it:** whether to run them on a schedule against a cheap model, and who pays.

---

## Q-06 SQLite costs 148 MB in the module cache

`modernc.org/sqlite` is the pure Go driver, chosen so `go install` works on a machine with no C
toolchain, which is most of them. It ships transpiled C for every platform, so the module is large
even though only about 9 MB reaches the binary.

**Decided in the meantime:** taken, because D-24 chose SQLite and full text search over history is
in PG-A3's acceptance line. Walid was told the same day, since he is storage aware.

**What would change it:** dropping to an append only file format. That would cost full text search,
which would have to become a linear scan or be cut. The alternative cgo driver is much smaller but
breaks `go install` for anyone without a compiler, which is the wrong trade for a tool people are
meant to try out.

---

## Q-07 History is kept forever and nothing prunes it

Every session and turn stays in `history.db` until somebody deletes it. There is no retention
policy, no size cap, and no `canopy history prune`.

**Decided in the meantime:** unbounded, because throwing away somebody's conversations without
asking is worse than a large file, and the file is text so it compresses well and grows slowly.

**What would change it:** a cap, an age based prune, or simply a `canopy history size` so people can
see it before it surprises them. Given Walid is storage aware this probably wants deciding rather
than deferring.

**Grew teeth on 2026-07-28:** the engine now loads every session and every turn of every project
into memory at startup, where the session list was deliberately written not to. So the file's
growth is felt at boot, not only on disk, and the answer to this question sets how soon.

---

## Q-08 Commit 2a83944 has a misleading message

It says "add the tool contract and the file tools" and also contains `internal/tui/chat/markdown.go`
and its tests, written by a parallel agent. Two agents were working in the same package and the
staging swept them in.

**Decided in the meantime:** left alone. The branch is shared and pushed, and rewriting pushed
history without a human present is a worse outcome than one commit whose message is incomplete. It
is recorded in A3-04's notes so the record is accurate even where the log is not.

**What would change it:** if the four of us decide the history matters more, `feat/agent-runtime` can
be rebased before merge, since nothing has been built on it outside this branch.

---

## ~~Q-09 Permission decisions have no interface yet~~ resolved 2026-07-27

**Built.** The prompt appears at the bottom of the transcript, under the reasoning that led to the
call, rather than in a dialogue over it: a modal that covers the conversation asks somebody to decide
with the context hidden. `y` allows once, `a` allows everything of that shape for the rest of the
session, and **anything else refuses, including enter and escape**. That last part is deliberate.
The reflex key on a prompt somebody has not read is enter, and enter meaning no is the difference
between a misread prompt costing a retry and costing a repository.

Worth a look in review: whether `a` is too easy to reach. It is one keystroke away from the safe
answer, and it is the one that stops the asking.

---

## Q-10 The trust level is hardcoded to standard

`attachTools` in `cmd/canopy/commands.go` gives every agent `TrustStandard`. There is no way to
choose, because per profile levels are configured at A5 and there are no profiles yet.

**Decided in the meantime:** standard, which reads and writes inside the workspace without asking
and shows every shell command before running it. The level that asks about the dangerous half is the
only defensible default when the user has not chosen.

**What would change it:** A5 brings profiles. Until then, somebody who wants a read-only agent
cannot have one, which is a real limitation for the "point it at a repository you do not own" case.

---

## Q-11 Web search needs a provider and an account

`fetch_url` is built. Search is not, because every usable search API wants a key and an account:
Brave Search, Tavily, Exa, Serper. Which one, and whose account pays for it, is not something to
decide unilaterally.

**Decided in the meantime:** not built. Scraping a search engine's HTML was considered and rejected:
it breaks constantly, and it is rude in a way that reflects on whoever ships an open source tool
that does it.

**What is needed:** a choice of provider, and a decision about whether search is a Canopy account
thing or a bring your own key thing. The second is more in keeping with how keys already work here.

A model can still find current documentation by fetching a URL it already knows, which covers the
common case of checking a library version. It cannot discover a page it has never heard of.

---

## Q-12 Direct and isolated workspace boundaries

Originally the Git tools only ran with the workspace as their working directory. Nothing stopped a
path argument from naming something outside it, and the documents disagreed about whether an agent
could operate on the primary checkout when that was where Canopy started.

**Original interim decision:** leave it recorded rather than claim confinement before A5-03 made
worktrees real.

**What it means today:** a direct agent started in the primary checkout can commit there when trust
permits it. Concurrent editing and fan-out require isolated worktrees. The direct creation screen
shows the exact workspace, primary-checkout risk and shell boundary, then requires a separate `y`
before creating the agent.

**Resolved 2026-07-27 by D-33:** structured path arguments exposed by `git_add` and `git_diff` pass
through `Workspace.Resolve` on `feat/permissions-and-confinement`. A direct agent deliberately uses
the repository where Canopy started, which may be the primary checkout. An isolated agent's
structured tools are rooted at its Canopy-owned worktree. Worktree lifecycle code never manages the
primary checkout or an unowned worktree. Shell remains explicitly outside this guarantee because
Canopy does not sandbox child processes.

---

## Q-13 The live tests are slow and occasionally flaky against NVIDIA NIM

The three live agent tests take between one and four minutes each against
`minimaxai/minimax-m2.7`, and one of them timed out at four minutes on a run where the other two
passed comfortably. Raising the budget to ten minutes fixed it.

**Decided in the meantime:** a generous budget, because tightening it makes the suite fail for
reasons that have nothing to do with this code, which is the fastest way to teach people to ignore a
red test.

**Worth knowing:** the slowdown got noticeably worse after the tool set grew from five tools to
eleven, which is a larger prompt on every step. That is expected and is the cost of giving an agent
more to choose from. It also means a faster provider will make the whole thing feel very different,
and judging Canopy's speed on this endpoint would be judging NVIDIA's free tier.

**Not decided:** whether these ever run anywhere other than by hand. They cost money and take ten
minutes, so a per commit CI run is out. A nightly one against a cheap model is plausible.

**Update, later the same night.** A final run had NIM returning 504s and, worse, accepting a request
and then never sending anything. That second failure exposed a real gap rather than a flaky test:
without a stall timeout the turn waited on the HTTP client's own limit, which is half an hour. There
is now a two minute watchdog, and a stalled provider is reported as a provider that stopped answering
rather than as a cancellation, because the two need entirely different words on screen. See the note
in A2-06.

So the flakiness was worth having. It is still worth deciding whether to keep paying for it.

---

## Q-14 Should a copied directory be cloned rather than copied?

**Added 2026-07-27, with A5-04.**

Worktree preparation can copy an allow listed path from the primary checkout into a fresh worktree.
For a `.env` that is trivial. For anything directory shaped it is not: a plain recursive copy of a
few hundred megabytes is real disk, and with several isolated agents it is that again per agent.

On APFS and on Btrfs a reflink copy is nearly free, and `cp -c` on macOS or `cp --reflink=auto` on
Linux would get it. The costs are platform branching, a dependency on `cp` behaving the same way
everywhere, and a silent fall back to a full copy on filesystems that cannot do it, which is the
kind of performance cliff nobody notices until a disk fills.

**Done for now:** a plain copy in Go, with the size measured and put into the confirmation before
anybody answers it. That is what actually protects somebody today, since the failure mode is
answering yes without knowing the number.

**Worth deciding:** whether to add reflink support at all, or whether the honest answer is that a
directory should be rebuilt by the setup command rather than copied, and the allow list should stay
what it is good at, which is small secret files. I lean towards the second, but it is a product
decision rather than a technical one.

---

## Q-15 What else from A4-04 is stored but never read?

**Added 2026-07-27, found while building A5-11.**

A4-04 put a `TrustLevel` on every agent. Nothing ever read it. Turns took the engine's trust level,
so an agent explicitly configured as read only ran at whatever the engine happened to be set to.
That is fixed now, and turns resolve trust per agent.

The specific bug is closed. The worrying part is the shape of it: a field that is set, stored,
displayed and never consulted, in the layer whose whole job is refusing things. It was found by
accident, because isolation needed the same lookup.

**Worth doing in review:** a deliberate sweep for the same shape elsewhere, particularly anything on
`Agent` or in `internal/permission`. A wrong answer there is a permission that was believed to be in
force and was not, which is the worst category of quiet failure in this codebase.

**Codex review 2026-07-27:** the sweep found three more instances. `git_branch` could create and
switch branches at read-only trust because its mutating field was not represented in the permission
request. A one-time `y` approval was stored as a session grant by the loop. Workspace path refusals
were recorded as allowed operations that ran and failed. Restricted agents were also shown tools
their trust level structurally denied. Proposed fixes and mutation-resistant tests are on
`feat/permissions-and-confinement`; this question stays open until those changes receive an
independent rerun.

---

## ~~Q-16 Test commands do not implement D-05~~ resolved 2026-07-28

**Added 2026-07-28 by the independent A6 pass.**

D-05 is still a settled decision and D-22 explicitly says it is unaffected by the agent-runtime
pivot. It requires an argument array as the default command form and permits a shell string only
with an explicit opt-in. The current `canopy.json` schema accepts only `"command": "..."`, and the
runner always invokes `/bin/sh -c`.

That is not only a schema mismatch. It breaks A6-03's three-outcome acceptance contract. When the
configured executable does not exist, the shell itself starts successfully and returns 127, so
Canopy records FAIL even though no test ran. The existing test notices this exact result but does
not fail on it. Guessing from stderr is locale- and shell-dependent, while treating every 126 or
127 as ERROR would misclassify a valid shell test that deliberately exits with that code.

**Resolved by implementing D-05 as written**, which both supervisors recommended independently and
which D-22 says the agent-runtime pivot left untouched. This was drift from a settled decision, not a
choice that was still open, so option 2 was never really on the table.

`command` is now an object. `{"argv": [...]}` is the default, `{"shell": "...", "allow_shell": true}`
is available for the pipes and prefixes that genuinely need one, and setting both is a validation
error. Canopy's own `canopy.json` is migrated, and a bare string is refused with a message showing
both forms rather than with Go's unmarshalling error.

The acceptance contract for A6-03 is met rather than approximated: with an argument vector the
executable either exists or `Start` fails, so a command that could not start and a test that failed
are different objects instead of the same integer. Neither prohibited shortcut was used. Nothing
reads shell stderr and nothing reinterprets 126 or 127.

What the shell form costs is now asserted in the test file rather than described in a comment. The
same missing program run through a shell is pinned as `failing`, so the trade someone accepts by
writing `allow_shell` is visible next to the case it contrasts with. A6-03 is unblocked.

---

## ~~Q-17 How should a revision a hook itself produced be recognised?~~ resolved 2026-07-28

**Added 2026-07-28 by the review of PR #29, which made the hook path reachable.**

A hook fires once per revision, and a pass at a new revision is a new event. That rule is right on
its own: tests passing over one piece of work and then over the next is two things happening, and
suppressing the second would silently skip the commit for the second piece of work.

It also means a committing hook re-triggers itself. Commit on green moves HEAD, the results go stale,
they run again, they pass again, and the hook is eligible again at the new revision. `git commit -am`
fails harmlessly the second time. `git commit -am ... --allow-empty` does not, and will keep
producing revisions for as long as the session runs.

TASKS previously claimed once-per-revision ended this loop. It does not, and that claim has been
removed rather than left to be discovered by somebody whose repository filled with empty commits.

**Resolved by option 1, recorded as D-39.** Option 2 puts an unenforceable rule on the person least
able to see the cycle, and its failure mode is a loop that stops only when somebody quits Canopy.

The objection to option 1 was that every cheap way of recognising a hook's own revision is wrong, and
that objection stands: the commit author is the user when the hook commits as the user, and
remembering one revision breaks the moment a hook makes two. The way through is that the recognisable
thing is not the revision, it is **the interval**. The runner already holds the revision the hook
fired at, because that is what the once-per-revision guard is keyed on. It reads the revision again
when the hook returns. Anything that moved between those two points moved while the hook was running,
and the hook was the only thing that had been asked to do anything. Neither of the failing cheap
tests is used, and a hook that commits five times is covered by one read.

Read from git directly rather than from the poller, which is up to an interval behind at exactly the
moment this matters.

What it costs is in D-39 and in LIMITATIONS: a person committing their own work during the seconds a
hook runs has that revision claimed too, so the hook does not fire for it. One missed firing against
a non-terminating loop.

A8-05's acceptance sentence, that a hook fires only on a real state transition, is now true: a
transition the hook caused itself is no longer counted as one.

## Q-18 Should MCP servers be started per worktree?

**Added 2026-07-28 by the work that made the MCP client reachable.**

D-38 starts servers once, in the project directory, and consequently withholds their tools from
isolated agents. An isolated agent is confined by having its tools rooted at its own worktree, and a
server started at the project root is not, so handing those tools across would be a route around the
boundary D-33 defines, through a capability Canopy cannot inspect.

That keeps the confinement honest and costs real capability: the fan out at A6-05 is the product's
central argument, and under D-38 the agents being fanned out are exactly the ones that cannot use a
third party tool. An agent asked to do the work in parallel has strictly less available to it than
the one being talked to.

The alternative is a set of servers per worktree, rooted where the agent actually works. It is more
correct and it is not free:

- Three fanned out agents times four configured servers is twelve processes, several of which are
  commonly a package manager fetching something before answering at all.
- `Tools func(dir string) (*core.ToolRegistry, error)` builds a registry and has no teardown hook, so
  there is nowhere for those servers to be stopped. Without one they leak for the life of the
  session, which is the failure A8-06 has just finished fixing at the level below.
- A server with expensive startup would make starting an agent slow enough to change how the fan out
  feels, which is the feature it would be serving.

**Supervisor decision required**, because it trades a headline capability against process cost and
needs a lifecycle change to the isolation contract either way:

1. **Per worktree servers.** Needs `Isolation.Tools` to return something closable, and a bound on how
   many servers a fan out may start.
2. **Keep D-38** and document that MCP is for the conversation rather than for the fan out.
3. **Per worktree, opt in per server**, with a flag in `canopy.json` for the servers that are cheap
   enough and safe enough to duplicate. More configuration, and it puts the choice with the person
   who knows what the server actually does.

## Q-19 May compaction run on a cheaper key than the conversation's?

**Added 2026-07-28 by the phase E planning.**

The named-key architecture makes it one field: summarising a long conversation is exactly the
kind of work a cheap model does adequately, and compaction is a pure cost with no product upside,
so paying the strong model's rate for it is the worst tokens in the bill.

The catch is not technical. The summary call sends the whole conversation, so a cheap-key option
sends everything the user said to a provider they chose for something else, on a credential with
its own terms. That is a data-flow decision, not an engineering one, which is why E-03 refuses to
build the option until this is answered.

**Supervisor decision required:** no, always the session's own key; or yes, opt in per profile,
with the transcript naming which key summarised.

## Q-20 Should the context window table gate requests?

**Added 2026-07-28 by the phase E planning.**

`internal/core` holds a static table of context windows by model prefix, used only to colour the
meter. Nothing refuses a request that cannot fit; the provider does, and today that error is
terminal (E-02 will make it compact and retry once).

The alternative is a pre-flight check: estimate, compare against the table, compact before
sending. It saves one failed round trip per overflow. It also promotes a display table into an
enforcement table, and a stale entry then refuses requests the provider would have accepted,
which is a confident wrong answer in exactly the sense D-32 forbids. The provider knows its own
limits; our table is a rumour about them.

**Who decides:** both supervisors, after E-02 exists and the cost of the round trip can be seen
rather than argued about.

## Q-21 Do ctrl+r, ctrl+k and ctrl+d keep their meanings?

**Added 2026-07-28 by the phase U planning.**

Three chat bindings sit on keys with forty years of shell muscle memory attached: ctrl+r is
compact where fingers expect history search, ctrl+k is credentials where fingers expect kill to
end of line, ctrl+d is agents where fingers expect EOF. U-09 removes the expensive consequence,
ctrl+r will no longer spend money without a confirmation, but the bindings themselves are taste,
and rebinding after release costs every existing user their habits, which is why this wants
answering before 0.1 has many of them.

**Who decides:** both supervisors, ideally by watching one person with strong shell habits use
the product for ten minutes.

## Q-22 What happens to the Claude route when the paused credit change lands?

**Added 2026-07-30 by the phase S planning.**

Anthropic announced, and then paused on 2026-06-15, a change that would move Claude Agent SDK,
`claude -p` and third-party app usage off subscription limits and onto separately purchased
credits. S-04 is built on the current arrangement, where a delegated Claude Code turn draws on the
user's own Max or Pro limits, and that arrangement is the whole reason the route is worth having:
the user has already paid.

Paused is not cancelled, and nothing in the announcement said it would not return. If it does, S-04
still works, because Canopy is driving a binary the user signed in to themselves and none of that
changes. What changes is the cost story. Usage that looked included becomes a separate purchase,
and somebody who signed in expecting the first finds out by being billed for the second.

**Decided in the meantime:** build S-04 on the current arrangement, and say plainly in LIMITATIONS
that it meters against the subscription as of the date recorded there. **What would change:** if the
change lands, both S-04's documentation and its in-product cost surface need rewriting before the
next release, and the honest form may be a warning at sign-in rather than a line in a document.

**Who watches for it:** whoever holds the release. This one arrives from outside and nothing in the
tree will notice it happening.

## Q-23 What do Canopy's tools, permissions and verification mean in a delegated turn?

**Added 2026-07-30 by the phase S planning.**

All three routes D-51 permits work by driving the vendor's own agent over a protocol rather than by
calling a completions endpoint, so a delegated turn runs the vendor's tool loop, the vendor's
permission model and the vendor's context handling. Canopy's own tools, its per-agent trust levels
and prompts from A4, its audit trail of refused calls, and A6's verification of what an agent
actually produced were all built on the assumption that Canopy runs the loop.

Three answers are possible and they are not equivalent. Canopy could refuse to delegate any turn
that needs its own tools, and be honest that a subscription credential buys a weaker agent. It could
expose its tools to the delegated agent through the protocol, which ACP and the app server both
partly allow, and re-impose its own gating at that boundary. Or it could accept that a delegated
turn is governed by the vendor and say so on screen, which is the smallest change and the largest
honesty problem, because the trust level displayed would not be the trust level in force.

**Decided in the meantime:** nothing, deliberately. S-03 to S-05 may deliver a working conversation
on a delegated agent, but no task in phase S may claim a trust level or an audit guarantee it does
not enforce, and no screen may show a permission mode the delegated turn is not actually running
under. A screen showing `plan` while somebody else's agent edits files is worse than no screen.

**Partly answered by S-04, 2026-07-30, on the Claude Code route.** The first delegated route works
end to end, so the gap can now be looked at rather than argued about, and one of the three possible
answers turned out not to be a choice.

Settled, because the protocol settles it: **Canopy's own tools are not offered to a delegated turn.**
The second option above, exposing them over the protocol and re-imposing Canopy's gating at that
boundary, is not available in ACP v1. MCP servers are the only channel a client has for handing an
agent its own tools, Canopy's tools are not an MCP server, and there is no other field. `session/new`
is therefore sent with an empty `mcpServers` list, and a test holds that no Canopy tool definition
ever reaches the wire.

Settled, and enforced rather than promised: **Canopy declines every permission request the delegated
agent makes.** It advertises no filesystem and no terminal capability, so the agent never routes work
back through Canopy, and when the agent does ask for approval Canopy answers with the protocol's
`reject_once` option and reports the refusal in the conversation. The reasoning is that approving
would be Canopy standing in as the user's approver for a call it did not make, cannot describe in its
own vocabulary and has no trust level for, which is exactly the screen-says-`plan` failure this
question forbids.

Settled, and this one was load-bearing rather than aesthetic: **a delegated tool call is a notice, not
a `core.EventToolCall`.** `internal/agent/loop.go` invokes every tool call event it is handed, so
mapping ACP's `tool_call` update onto that kind would have made Canopy run the vendor's tool a second
time, through a gate, against a tool definition it does not have.

Still open, and now visible rather than theoretical: **the honest answer for the third question, what
verification means, is that it does not apply.** Claude Code's own auto-approved tools never reach
Canopy at all, so declining permission requests does not make a delegated turn gated; it only means
Canopy grants nothing. A4's audit trail records no refused calls because Canopy refused none of its
own, and A6 verifies nothing because Canopy ran nothing. That is written down in LIMITATIONS.md in
those words. What is not settled is whether shipping a route with that property is acceptable at all
beyond a beta, or whether the answer is the first option, refusing to delegate a turn that would need
Canopy's tools, at the cost of a subscription credential buying a visibly weaker agent.

Also still open: **what the conversation screen should show for the permission mode during a
delegated turn.** Today the turn opens with a notice saying Canopy's permissions are not in the path,
which stops the mode indicator from being a lie by contradicting it in words. A mode indicator that
knew about delegation would be better than a sentence that argues with it.

**Answered differently by S-03, 2026-07-30, on the GitHub Copilot route, and the difference is the
protocol rather than the principle.** The second option, exposing Canopy's tools to the delegated
agent and re-imposing Canopy's gating at that boundary, is unavailable in ACP v1 and is available in
GitHub's SDK. So on the Copilot route it is what Canopy does, and the three answers come out as:

Canopy's own tools **are** offered, and they are the only tools in the session. The client is created
in the SDK's `empty` mode, which starts a session with no built-in tools, and the session's tool
allowlist names Canopy's tools by source and no vendor source at all, so nothing of GitHub's reaches
the model to be called.

Canopy's permission gate **is** in force, and by construction rather than by cooperation. Canopy's
tools are declared to the vendor with no implementation behind them, which is the SDK's
declaration-only form: a call arrives as an event and stays pending until somebody resolves it. That
somebody is Canopy's own loop, one layer up, after the call has been through the agent's trust level
and, where the level requires it, past a person. There is no path by which the vendor runs one of
Canopy's tools, because it was never given a way to run any of them.

A4's audit trail and A6's verification **do** apply, for the same reason: every tool call in a
Copilot turn was made by Canopy.

The trap S-04 found holds here too and was checked rather than assumed. `internal/agent/loop.go`
invokes every tool call event it is handed, so the only vendor event this route maps onto
`core.EventToolCall` is the one that means "waiting for you to run this". The events that mean a tool
is running or has run produce nothing at all, and a test holds that.

**What this leaves genuinely open, and it is narrower than before.** The question is no longer
whether a delegated turn can be governed, because one of them is. It is whether a route that cannot
be governed should ship beside one that can. The Copilot route shows that the ceiling is set by each
vendor's protocol rather than by Canopy's design, which means the answer will differ per route and
the product decision is how to say that on screen without three different explanations. It also
sharpens the first option: refusing to delegate a turn that needs Canopy's tools would now cost
nothing on Copilot and everything on Claude Code, so it is a per-route policy rather than a phase-wide
one.

**Who decides:** both supervisors. Two delegated routes now work end to end and they sit at opposite
ends of what a protocol allows, which is the comparison this question was waiting for.
