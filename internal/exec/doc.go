// Package exec runs configured test commands and captures their evidence.
//
// Each command runs in its own process group inside the target worktree, so a timeout or a
// cancellation takes down the whole tree instead of orphaning children. The revision key is
// captured when the run starts, not when it finishes, because a result belongs to the code that
// was there when it began.
//
// Process exit code is the only source of pass/fail truth in v0.1. There are no framework
// specific parsers. The difference between a command that failed and a command that could not run
// is preserved, so a missing binary is an error, never a failure and never a pass.
//
// Output goes into bounded ring buffers that drop the middle and say so, never silently.
//
// Filled in by P2-05 through P2-07.
package exec
