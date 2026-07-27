# Working on Canopy

Read this before changing the repository. Canopy has multiple human-agent pairs working in the same
task ledger, and a locally plausible change can still be wrong if it contradicts a settled product
decision or another pair's claimed scope.

## Read order and authority

1. Read `DECISIONS.md`. It is authoritative for settled product and engineering choices. For agent
   execution and workspace safety, read D-21, D-22, D-25 and especially D-33.
2. Read sections 1 and 2 of `TASKS.md`, then the complete block for the task you intend to touch.
   `TASKS.md` is authoritative for state, ownership, acceptance and independent verification.
3. Read `README.md` for the user-facing promise and `LIMITATIONS.md` for what Canopy explicitly does
   not guarantee.
4. If `.local-context/THREAD-HANDOFF.md` exists, read it for local checkout context. The entire
   `.local-context` directory is local-only and must never be staged or committed.

The private planning documents explain how the project arrived here. They are provenance, not a
parallel live specification. The precedence rules are recorded at the end of `DECISIONS.md`.

If code, acceptance criteria and a decision disagree, stop and mark the task blocked. Record the
exact contradiction and ask the supervisors. Do not silently choose the sentence that best matches
the current implementation.

## Workspace and permission contract

D-33 controls this entire area:

- A direct agent works in the repository where Canopy started. That may be the primary checkout,
  and its trust level determines which agent tools may modify it.
- An isolated agent gets a Canopy-owned worktree. Structured file and path-scoped Git tools resolve
  against that worktree and refuse paths outside it.
- Fan-out and concurrent editing require isolated worktrees. They must never silently share a
  direct checkout.
- Shell is not contained. It starts in the assigned workspace but runs with the user's account
  permissions and can leave that workspace. Read-only and confined trust deny shell; standard asks
  for the exact command; broad allows it without asking.
- Canopy's worktree lifecycle code never removes, resets, force-checks-out or takes ownership of the
  primary checkout or an unowned worktree. That is separate from direct agent tool execution.
- "Confined" is a trust level whose tool surface excludes shell and destructive Git. It is not an
  operating-system sandbox.

Never turn a working directory, path-validation helper or approval prompt into a broader
containment claim than the code can enforce.

## Required change discipline

Before editing, inspect `git status`, the current branch, the task's owner and its scope. Use a short
feature-named branch such as `feat/permissions-and-confinement`, not an agent-named branch. Preserve
unrelated and uncommitted work.

A change to workspace selection, tool kinds, trust levels, shell execution, worktree ownership or
approval scope must update all of these in the same branch:

1. The controlling entry in `DECISIONS.md`, with an explicit supersession if the rule changed.
2. Every affected acceptance block in `TASKS.md`.
3. `README.md` and `LIMITATIONS.md`.
4. Direct/isolated by trust-level regression tests, including permission and audit outcomes.

Do not mark a task done because it compiles. Follow the verification protocol in `TASKS.md`: the
implementer demonstrates acceptance, the other agent independently reads the diff and reruns the
evidence, and only both dated checks make the task done.

Run targeted tests while working, then `go test ./...`, `go vet ./...`, `golangci-lint run ./...`,
`gofmt -l .` and `git diff --check` before handoff. Use race tests in proportion to concurrency and
permission risk.
