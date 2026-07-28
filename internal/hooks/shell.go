package hooks

// Actually running a hook.
//
// Separated from the deciding half so the rules about what fires can be tested without a shell, and
// so this file is the only place that knows a hook is a shell command at all. If hooks ever grow a
// second kind, a notification or an internal action, the runner does not change.

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
)

// Shell runs a hook through the user's shell, in the project directory.
//
// Through a shell rather than split into arguments, because a hook is written by the person who
// owns the repository and pipes and redirection are most of why they wrote it. This is the same
// repository trust contract the test commands run under, and it is a different contract from the
// one governing commands a model generated. Nothing here is sandboxed and this package does not
// pretend otherwise.
func Shell(ctx context.Context, command, dir string, env []string) (string, error) {
	shell := os.Getenv("SHELL")
	if strings.TrimSpace(shell) == "" {
		shell = "/bin/sh"
	}

	result, err := exec.Run(ctx, shell, []string{"-c", command}, exec.Options{
		Dir: dir,
		// Added to the environment rather than replacing it. A hook that cannot see PATH is a hook
		// that cannot find git, and the surprise of an empty environment is worse than the risk of
		// a full one for a command the user wrote themselves.
		Env: append(os.Environ(), env...),
	})
	if err != nil {
		return "", err
	}

	switch {
	case !result.Ran:
		return result.Output, fmt.Errorf("it could not be run")
	case result.TimedOut:
		// The runner turns this into a sentence naming the timeout. Reported here as well so a
		// caller using Shell directly is not left guessing.
		return result.Output, fmt.Errorf("it timed out")
	case result.ExitCode != 0:
		return result.Output, fmt.Errorf("it exited %d", result.ExitCode)
	}
	return result.Output, nil
}
