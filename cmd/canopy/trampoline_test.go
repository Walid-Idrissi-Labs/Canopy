package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Walid-Idrissi-Labs/Canopy/internal/sandbox"
)

// A test binary re-run as the sandbox trampoline acts as one, and never runs its tests a second
// time: on Linux the sandbox re-runs the current executable, which under go test is this binary,
// and each run that went on to run tests started another.
func TestATestBinaryRerunAsTheTrampolineIsOne(t *testing.T) {
	// Should the binary ever run its tests again instead, this one must not start another copy,
	// or the check would itself be the runaway it is checking for.
	if os.Getenv("CANOPY_TRAMPOLINE_PROBE") != "" {
		t.Skip("inside the probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], sandbox.TrampolineArg, "not a policy")
	cmd.Env = append(os.Environ(), "CANOPY_TRAMPOLINE_PROBE=1")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 126 {
		t.Fatalf("exit %v, output:\n%s", err, out)
	}
	if strings.Contains(string(out), "PASS") || strings.Contains(string(out), "=== RUN") ||
		!strings.Contains(string(out), "canopy sandbox:") {
		t.Fatalf("the binary did something other than act as the trampoline:\n%s", out)
	}
}
