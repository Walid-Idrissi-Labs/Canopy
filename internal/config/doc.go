// Package config loads and validates the per project configuration file.
//
// The file is committed to the repository it describes, and defines agent profiles, test commands,
// permission posture and project instructions.
//
// Validation is strict on purpose. An unknown executable field is an error rather than a warning,
// because quietly ignoring a field the user believed was in effect is a way to run the wrong
// command. Templates resolve before anything executes, and a relative path may not escape the
// worktree.
//
// Filled in by A8-03.
package config
