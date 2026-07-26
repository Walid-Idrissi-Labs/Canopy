# Open questions

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
