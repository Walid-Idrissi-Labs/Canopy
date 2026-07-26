// Package app wires the engine to the interface.
//
// It owns process lifetime, startup and shutdown ordering, and the plumbing that turns discovery,
// revision polling, test runs and health probes into store updates. It is the only place that
// knows about both halves of the system, which is what keeps every other package testable on its
// own.
//
// Filled in by P1-08.
package app
