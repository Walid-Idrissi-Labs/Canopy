// Package agent runs the loop.
//
// An agent is a profile, a worktree, a branch, a session and a budget. This package owns the turn:
// send, stream, execute tools under the permission model, feed results back, repeat until the
// model stops or a limit is reached.
//
// It also owns the things that only exist because there is more than one agent: the registry,
// dispatch from the conversation, steering, handoff, and ranking agents by whose code passes.
//
// Filled in from A4-05 onward.
package agent
