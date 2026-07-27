package main

import "fmt"

// version, commit and date are set at build time with -ldflags, by the Makefile for a local
// build and by GoReleaser for a released one, using the same three -X targets so a binary
// reports itself the same way regardless of which of the two built it.
//
// The defaults below are what a plain `go build` or `go test` produces, since not every build
// goes through either path. "dev" says this binary was not built for a release, so a version
// number is not something to file a bug against. "none" and "unknown" say the same about the
// commit and the date without borrowing the word that already means that for the version.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionLine formats version, commit and date into the one line `canopy version` (and a future
// `canopy --version`) should print, so there is exactly one place that decides what that line
// looks like rather than each call site assembling its own.
//
// All three fields always show, even the unset ones. A line silently missing the commit is a
// harder thing to notice than a line that plainly says "none", and the second is what someone
// pasting a bug report actually needs.
func versionLine() string {
	return fmt.Sprintf("canopy %s (commit %s, built %s)", version, commit, date)
}
