//go:build unix

package exec

import (
	"os/exec"
	"syscall"
)

// Process group handling, which is the whole reason cancellation works here.
//
// A test runner spawns workers, a dev server spawns a bundler, a shell script spawns whatever it
// likes. Killing only the process we started leaves all of those running, holding ports and file
// handles, and the next run fails with "address already in use" for reasons nobody can see. Putting
// the command in its own group and killing the group takes the children with it.

// setProcessGroup puts the command in a new process group of its own.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killGroup terminates the command and everything it started.
//
// A negative PID addresses the group rather than the process, which is the entire mechanism. It is
// also why the group has to be set at start: without Setpgid the command shares Canopy's own group,
// and a negative kill would take Canopy down with it.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid

	// Terminate first, so a process with cleanup to do gets the chance. A test runner asked to stop
	// politely removes its temporary directories; one that is killed outright leaves them.
	_ = syscall.Kill(-pid, syscall.SIGTERM)

	// SIGKILL follows shortly after regardless, because a process that ignores SIGTERM is exactly
	// the process this exists to deal with. Sent to the group, then to the process itself as a
	// fallback for the case where the group was never established.
	go func() {
		<-afterGracePeriod()
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
	}()
}
