package sandbox

import (
	"fmt"
	"os"
)

// On a platform that confines by re-running the current executable (Linux), whatever binary asked
// for a command to be wrapped is the one re-run as the trampoline. For canopy that is canopy, whose
// main knows the argument; for a test it is the test binary, which does not, and which ran its own
// suite again instead: a test that ran a project's tests in the sandbox started a copy of itself
// that did the same, until the machine ran out. This runs the trampoline before anything else in
// every binary that links this package, which is every binary that can ask for a wrap.
func init() {
	if len(os.Args) > 1 && os.Args[1] == TrampolineArg {
		if err := RunTrampoline(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "canopy sandbox: %v\n", err)
		}
		// RunTrampoline returns only when it failed to become the command.
		os.Exit(126)
	}
}
