# Contributing to Canopy

Contributions are welcome. This file is the door for people outside the project.

`AGENTS.md` is not that door, and it is worth saying so up front so nobody reads it and concludes
they are in the wrong repository. It is written for the two human-agent pairs working through
`TASKS.md`, and it describes a claim-and-verify protocol that only makes sense if you are one of
them. You do not need to follow it, and you do not need to read `DECISIONS.md` cover to cover
before you can fix a bug.

What is worth reading first is [LIMITATIONS.md](LIMITATIONS.md). Canopy is pre-alpha and a
surprising number of things that look like bugs are entries in there, written down on purpose with
the reasoning attached. If what you found is already listed, an issue arguing that the tradeoff is
wrong is still useful; a pull request that removes the limitation without touching the reasoning is
not.

## Before you write code

For anything more than a small fix, open an issue first and say what you intend to do. Two people
are working through a task ledger and there is a real chance the area you want to change is already
being rebuilt this week. Ten minutes of asking saves an afternoon of rebasing.

Small and obvious fixes, a wrong error message, a broken link, a race in a test, do not need an
issue. Send the pull request.

## Building and testing

You need Go 1.26 or newer, `git`, and `/bin/sh`. `golangci-lint` v2.12.2 for the lint target;
anything older is a different tool and will fail differently than CI does.

```sh
make build     # CGO_ENABLED=0 go build -trimpath -ldflags ... -o canopy ./cmd/canopy
make test      # go test -race -count=1 ./...
make vet       # go vet ./...
make lint      # golangci-lint run ./...
make fmt       # gofmt -w .
make install   # same build, into $(go env GOPATH)/bin, with version stamping
```

`make test` uses `-race` because most of this project is agents running concurrently, and
`-count=1` because a green result from the test cache is a statement about code from ten minutes
ago. That is the same reason the verification engine treats a stale result as no result at all.

Run all four of `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...` and
`go test -race -count=1 ./...` before you push. The local test command deliberately disables the Go
test cache. CI checks formatting, builds, vets and runs `go test -race ./...` on both ubuntu-latest
and macos-latest; lint is a separate ubuntu-latest job. The formatting check fails rather than
rewriting files. CI does not currently pass `-count=1`, so “exactly the same as CI” would be
incorrect and the local command is intentionally stricter on cached results. See
[`.github/workflows/ci.yml`](.github/workflows/ci.yml) for the authoritative workflow.

`golangci-lint` runs with the standard linter set and, deliberately, no exclusion for unchecked
write errors. If a write genuinely cannot be acted on, drop it explicitly at the call site with
`_ =` so the intent is visible, rather than adding an exclusion.

## Tests

New behaviour needs a test. The bar is not coverage, it is that the test would have failed before
your change. Use race tests in proportion to how much concurrency and permission risk the code
carries: anything touching agent execution, trust levels, approval scope or worktree ownership
should be exercised under `-race`.

Tests never touch the real OS keychain. `keys.NewMemoryBackend()` exists for that, and a test that
leaves credentials on a maintainer's machine will be asked to change.

## Branches and commits

Branch off `main` with a short feature-named branch. The prefixes in use are:

- `feat/` for new behaviour
- `fix/` for bug fixes
- `tui/` for interface work
- `docs/` for documentation

So `fix/dispatch-hardening` or `tui/agent-mosaic`, naming the change rather than the person or the
task number.

Commits follow [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`,
`docs:`, `chore:`, `test:`, with an optional scope like `fix(tui):`. The release changelog is
generated from these prefixes, so a wrong one shows up in a release note.

Write the body for someone reading it in a year with no memory of the conversation. Say why, not
what; the diff already says what. If a change was chosen over an obvious alternative, name the
alternative and why it lost.

## Pull requests

Keep them focused. One reviewable idea per pull request, and a refactor separate from the behaviour
change it enables.

Fill in the template. Say how you verified the change: the exact commands, not "tests pass".
`canopy report` will run this repository's checks and print a markdown summary suitable for the
body if you want it.

One thing to leave alone: **do not edit `TASKS.md` or `DECISIONS.md`.** `TASKS.md` is the
maintainers' ledger, with claimed ownership and dated independent verification on every entry, and
a drive-by edit to it conflicts with whatever the two pairs are doing that day. `DECISIONS.md` is
changed by a pull request whose whole subject is the decision, with the reasoning and the affected
tasks updated together. Neither is a place to record that you fixed something. If your change means
a decision is now wrong, say so in the pull request and it will be handled on that side.

## Documentation

If your change alters what Canopy promises, `README.md` and `LIMITATIONS.md` change in the same
pull request. A limitation stays listed until the work that removes it actually lands, so closing a
gap means deleting the entry in the commit that closed it, not later.

House style for prose: plain, specific, and it explains why. No marketing language, no em dashes,
and no claim the code cannot support. That last one is not a stylistic preference. A tool whose
entire proposition is refusing to show a green tick it cannot justify does not get to overstate
anything in its own documentation.

## Security

Do not open a public issue for a vulnerability. [SECURITY.md](SECURITY.md) has the address and what
is in scope.

## License

Canopy is MIT licensed. Contributions are accepted under the same license.
