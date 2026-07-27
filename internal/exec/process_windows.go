//go:build windows

package exec

import "os/exec"

// Windows has no process groups in the POSIX sense, and the equivalent, a job object, is a larger
// piece of work than this project needs today. Killing the process alone is what is available, and
// saying so here is better than an empty file that reads as though the problem were handled.
//
// The consequence is real: on Windows, a cancelled command may leave children running. Recorded so
// somebody porting this knows it is a gap rather than a decision.

func setProcessGroup(*exec.Cmd) {}

func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
