// Package permission decides what an agent may do.
//
// A trust level is a property of the agent's profile, not a global setting, so a scratch agent in
// a throwaway worktree and an agent working near the primary checkout can behave differently on
// the same request. One global posture would force the strictest agent's friction onto every
// agent, and people respond to that by loosening everything.
//
// This is a different problem from approving a command a user wrote in a config file. An agent
// runtime executes commands a model generated, and reusing a contract built for the former would
// claim a protection that does not exist.
//
// Canopy does not sandbox what it runs and must never imply that it does. There is a permission
// model. It is not containment.
//
// Filled in by A4-04.
package permission
