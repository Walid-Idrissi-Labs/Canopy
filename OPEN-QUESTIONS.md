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

**What would change it:** A2-09 lets a rate be attached to a stored credential, labelled as the
user's figure rather than a checked one. Worth building; the question is whether "cost unknown" in
the meantime is acceptable at the A2 gate, whose acceptance line says both supervisors see cost
figures on two providers.

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
