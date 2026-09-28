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

	"github.com/Walid-Idrissi-Labs/Canopy/internal/childenv"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
)

// Shell runs a hook through the user's shell, in the project directory.
//
// Through a shell rather than split into arguments, because a hook is written by the person who
// owns the repository and pipes and redirection are most of why they wrote it. This is the same
// repository trust contract the test commands run under, and it is a different contract from the
// one governing commands a model generated. Shell itself confines nothing; Confined does.
func Shell(ctx context.Context, command, dir string, env []string) (string, error) {
	return run(ctx, command, dir, env, nil, nil)
}

// Confined is Shell inside the sandbox confine gives for a hook's directory. A hook usually runs a
// script in the repository, which an agent can have edited, so it gets the confinement an agent's
// own commands get. Where confine gives no sandbox, the hook runs as Shell would.
func Confined(confine func(dir string) (*sandbox.Policy, []string)) Executor {
	return func(ctx context.Context, command, dir string, env []string) (string, error) {
		policy, base := confine(dir)
		return run(ctx, command, dir, env, policy, base)
	}
}

// shellPath is the user's shell, or sh.
func shellPath() string {
	if shell := os.Getenv("SHELL"); strings.TrimSpace(shell) != "" {
		return shell
	}
	return "/bin/sh"
}

// withInherited is base, or the inherited environment when the sandbox gave none.
func withInherited(base []string) []string {
	if base == nil {
		return childenv.Inherited()
	}
	return base
}

func run(ctx context.Context, command, dir string, env []string, policy *sandbox.Policy, base []string) (string, error) {
	shell := shellPath()
	base = withInherited(base)
	result, err := exec.Run(ctx, shell, []string{"-c", command}, exec.Options{
		Dir: dir,
		// Added to the environment rather than replacing it. A hook that cannot see PATH is a hook
		// that cannot find git, and the surprise of an empty environment is worse than the risk of
		// a full one for a command the user wrote themselves.
		Env:     append(base, env...),
		Sandbox: policy,
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
