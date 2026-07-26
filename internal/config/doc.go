// Package config loads and validates the versioned project configuration.
//
// Validation is strict on purpose. schema_version is required. An unknown executable field is an
// error rather than a warning, because quietly ignoring a field the user believed was in effect
// is a way to run the wrong command. Durations, port ranges and template references all resolve
// before anything executes, and a relative working directory may not escape the worktree.
//
// Commands are argument arrays by default. A shell string needs an explicit allow_shell opt-in
// and is marked as higher risk. Defining both forms is rejected.
//
// Named ports are declared and resolved into templates. There is no port allocator in v0.1, since
// Canopy cannot allocate a port for a process it does not start.
//
// Filled in by P3-01 through P3-03 and P3-08.
package config
