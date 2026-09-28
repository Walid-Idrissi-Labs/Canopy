//go:build windows

package exec

import (
	"os/exec"
	"time"

	"golang.org/x/sys/windows"
)

// Windows has no process groups in the POSIX sense. Its equivalent is a job object: every process a
// command starts is in its job unless it asks to leave, and ending the job ends all of them, which is
// what a group kill does on unix. The command is put in a job as soon as it has started, so anything
// it starts in the moment before that is outside it; that gap is stated in LIMITATIONS.
//
// The job is not set to end its processes when its handle closes. A command that deliberately left
// something running is left alone once it has finished, as on unix (D-37); only Stop ends the tree.

// tree is the job holding a started command and what it starts.
type tree struct{ job windows.Handle }

// holdTree puts a started command in a job of its own. A command that cannot be put in one is still
// run, and Stop then ends only the process itself, which is what it did before jobs.
func holdTree(cmd *exec.Cmd) tree {
	if cmd.Process == nil {
		return tree{}
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return tree{}
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false,
		uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return tree{}
	}
	defer func() { _ = windows.CloseHandle(process) }()
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return tree{}
	}
	return tree{job: job}
}

// release closes the job's handle, leaving whatever is still in it running.
func (t tree) release() {
	if t.job != 0 {
		_ = windows.CloseHandle(t.job)
	}
}

func setProcessGroup(*exec.Cmd) {}

// stop ends the command and everything in its job, or the process alone where it has none.
//
// The grace period is accepted and ignored. It paces an escalation from SIGTERM to SIGKILL, and
// there is no first signal here to escalate from.
func (c *Child) stop(_ <-chan time.Time) {
	c.alive(func(int) {
		if c.tree.job != 0 && windows.TerminateJobObject(c.tree.job, 1) == nil {
			return
		}
		_ = c.cmd.Process.Kill()
	})
}
