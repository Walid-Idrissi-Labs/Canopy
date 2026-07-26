// Package trust decides whether Canopy may execute a repository's configured commands.
//
// A configuration file living inside a repository can run arbitrary commands as the user, and a
// git worktree is file isolation rather than a security boundary. So nothing from a newly
// discovered repository runs until the user has seen the fully resolved commands and approved
// them.
//
// Approvals live outside the observed repository, keyed by repository identity plus a hash of the
// executable configuration. Changing any executable field invalidates the previous approval,
// because otherwise approval would be a one time gate on a file that can change afterwards.
//
// Canopy does not sandbox what it runs and must never imply that it does.
//
// Filled in by P3-04 and P3-05.
package trust
