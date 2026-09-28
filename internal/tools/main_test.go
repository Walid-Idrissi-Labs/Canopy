package tools

import (
	"fmt"
	"os"
	"testing"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
)

// On Linux the sandbox re-runs the current executable as a trampoline, which in a test is this
// test binary, so it has to recognise the trampoline the way canopy's main does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == sandbox.TrampolineArg {
		if err := sandbox.RunTrampoline(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(126)
		}
		return
	}
	os.Exit(m.Run())
}
