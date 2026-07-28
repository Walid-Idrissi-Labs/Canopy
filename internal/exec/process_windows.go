//go:build windows

package exec

import (
	"os/exec"
	"time"
)

// Windows has no process groups in the POSIX sense, and the equivalent, a job object, is a larger
// piece of work than this project needs today. Killing the process alone is what is available, and
// saying so here is better than an empty file that reads as though the problem were handled.
//
// The consequence is real: on Windows, a cancelled command may leave children running. Recorded so
// somebody porting this knows it is a gap rather than a decision. See LIMITATIONS.md.

func setProcessGroup(*exec.Cmd) {}

// stop kills the process and nothing it started.
//
// The grace period is accepted and ignored. It paces an escalation from SIGTERM to SIGKILL, and
// there is no first signal here to escalate from.
func (c *Child) stop(_ <-chan time.Time) {
	c.alive(func(int) { _ = c.cmd.Process.Kill() })
}
