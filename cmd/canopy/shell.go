package main

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/childenv"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/exec"
	"github.com/Walid-Idrissi-Labs/Canopy/internal/tui/chat"
)

// shellIn runs a "!command" typed in the box, in dir, the way the person's own terminal would:
// through their shell, unconfined, since they typed it, but without the provider keys Canopy holds.
func shellIn(dir string) func(ctx context.Context, command string) chat.ShellResult {
	return func(ctx context.Context, command string) chat.ShellResult {
		shell := os.Getenv("SHELL")
		if strings.TrimSpace(shell) == "" {
			shell = "/bin/sh"
		}
		result, err := exec.Run(ctx, shell, []string{"-c", command}, exec.Options{
			Dir: dir, Env: childenv.Inherited(), Timeout: 2 * time.Minute, MaxOutput: 64 << 10,
		})
		switch {
		case err != nil:
			return chat.ShellResult{Failed: err.Error()}
		case !result.Ran:
			return chat.ShellResult{Failed: "it could not be run: " + strings.TrimSpace(result.Output)}
		case result.TimedOut:
			return chat.ShellResult{Failed: "it did not finish in two minutes"}
		}
		return chat.ShellResult{Output: result.Output, ExitCode: result.ExitCode}
	}
}
