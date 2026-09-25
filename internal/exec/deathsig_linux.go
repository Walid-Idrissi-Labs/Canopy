//go:build linux

package exec

import (
	"os/exec"
	"syscall"
)

// dieWithParent asks the kernel to kill the child if Canopy dies without running its shutdown, a
// SIGKILL or a crash. Without it a vendor agent and everything it started outlive Canopy, holding a
// session and a rate limit nobody can see.
func dieWithParent(cmd *exec.Cmd) { cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL }
