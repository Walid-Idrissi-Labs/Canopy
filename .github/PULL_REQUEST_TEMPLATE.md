## What this changes

## Why

The diff says what. Say why, and if there was an obvious alternative, why it lost.

## How it was verified

The exact commands and what they printed, not "tests pass". `canopy report` will run this
repository's checks and print a summary suitable for pasting here.

- [ ] `gofmt -l .` is clean
- [ ] `go vet ./...`
- [ ] `golangci-lint run ./...`
- [ ] `go test -race ./...`

## Checklist

- [ ] The commit messages use a Conventional Commits prefix
- [ ] New behaviour has a test that would have failed before this change
- [ ] `README.md` and `LIMITATIONS.md` updated if this changes what Canopy promises
- [ ] `TASKS.md` and `DECISIONS.md` untouched, unless this pull request is about a decision

Anything touching trust levels, permissions, workspace isolation, worktree ownership or what a
verification state means: say so here explicitly. Those get read differently.
