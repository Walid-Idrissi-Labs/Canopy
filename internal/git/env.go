package git

import "os"

// pathEnv and homeEnv are the two variables git genuinely needs.
//
// PATH so git can find its own subcommands, which are separate binaries. HOME because git reads
// configuration from it and refuses some operations without one. Everything else in the user's
// environment is deliberately left out: a stray GIT_DIR or GIT_INDEX_FILE would redirect Canopy's
// own bookkeeping somewhere unexpected, and the failure would be a checkpoint silently taken of the
// wrong repository.

func pathEnv() string {
	if path := os.Getenv("PATH"); path != "" {
		return path
	}
	return "/usr/bin:/bin:/usr/local/bin"
}

func homeEnv() string { return os.Getenv("HOME") }
