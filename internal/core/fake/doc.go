// Package fake is an in-memory implementation of the core interfaces.
//
// It emits four scripted worktrees, one passing, one failing, one that goes stale on command and
// one unconfigured, which is exactly the shape of the demo the project is aiming at. It exists so
// the interface can be built and demonstrated before any real git or process code lands, and it
// doubles as the test double for the rest of the project.
//
// Both maintainers depend on it, so a breaking change here counts as a contract change.
//
// Filled in by P1-05.
package fake
