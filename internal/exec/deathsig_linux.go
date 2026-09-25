//go:build linux

package exec

import (
	"os/exec"
	"syscall"
)

// dieWithParent asks the kernel to kill the direct child if Canopy dies without running its
// shutdown, a SIGKILL or a crash. It reaches only that child, and fires when the thread that started
// it exits, so it narrows the orphan problem rather than closing it: grandchildren a vendor agent
// started are stopped only by the orderly shutdown, which signals the whole process group.
func dieWithParent(cmd *exec.Cmd) { cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL }
