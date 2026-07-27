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
